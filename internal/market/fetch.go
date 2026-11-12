//go:build (windows || darwin) && (amd64 || arm64)

package market

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	requestHeaders = "Accept: application/json, text/plain, */*\r\n" +
		"Accept-Encoding: identity\r\n" +
		"Cache-Control: no-cache\r\n" +
		"Pragma: no-cache\r\n"

	quoteFields = "symbol,shortName,longName,currency,quoteType,marketState,quoteSourceName,gmtOffSetMilliseconds,marketCap," +
		"regularMarketPrice,regularMarketChange,regularMarketChangePercent,regularMarketTime,regularMarketPreviousClose," +
		"preMarketPrice,preMarketChange,preMarketChangePercent,preMarketTime," +
		"postMarketPrice,postMarketChange,postMarketChangePercent,postMarketTime," +
		"overnightMarketPrice,overnightMarketChange,overnightMarketChangePercent,overnightMarketTime," +
		"fulldayPrice,fulldayChange,fulldayChangePercent"
)

var (
	clientMu    sync.Mutex
	sessHandle  *httpSession
	cachedCrumb string
	yahooQuotes yahooQuoteRecovery
	cachedCNY   fxQuote
	fxFetchedAt time.Time
	fxNextTry   time.Time
)

func ensureSessionLocked() (*httpSession, error) {
	if sessHandle != nil {
		return sessHandle, nil
	}
	session, err := newHTTPSession()
	if err != nil {
		return nil, err
	}
	sessHandle = session
	return session, nil
}

func resetSessionLocked() {
	if sessHandle != nil {
		sessHandle.close()
		sessHandle = nil
	}
	cachedCrumb = ""
}

func refreshCrumbLocked(ctx context.Context, session *httpSession) (string, error) {
	crumb, err := requestCrumb(ctx, session)
	if err == nil {
		cachedCrumb = crumb
	}
	return crumb, err
}

func requestCrumb(ctx context.Context, session *httpSession) (string, error) {
	_, _, lastErr := doHTTPGet(ctx, session, CookieHost, "/", true)
	for _, host := range []string{PrimaryHost, FallbackHost} {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && lastErr != nil {
				return "", lastErr
			}
			return "", err
		}
		status, body, err := doHTTPGet(ctx, session, host, "/v1/test/getcrumb", true)
		if err != nil {
			lastErr = err
			continue
		}
		if status != 200 {
			lastErr = HTTPError{StatusCode: status}
			continue
		}
		c := strings.TrimSpace(string(body))
		if c != "" && len(c) <= 64 && !strings.ContainsAny(c, "<>{}\r\n") {
			return c, nil
		}
		lastErr = fmt.Errorf("无效 crumb 响应")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无法获取 crumb")
	}
	return "", lastErr
}

// Fetch retrieves quotes for the active market group using the platform transport.
func Fetch(ctx context.Context) (map[string]SymbolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	group := ActiveGroup()

	clientMu.Lock()
	defer clientMu.Unlock()
	var batch map[string]SymbolResult
	var err error
	if strings.HasPrefix(group.ID, "custom_") {
		batch, err = fetchCustomQuoteBatch(ctx, group.Symbols, fetchSymbolsLocked)
	} else {
		batch, err = fetchSymbolsLocked(ctx, group.Symbols)
	}
	enrichCapitalizationLocked(ctx, batch)
	indexValuations.attach(batch, time.Now())
	return batch, err
}

var indexValuations = valuationStore{fetch: fetchIndexValuation}

func fetchIndexValuation(ctx context.Context, symbol string) (*IndexValuation, error) {
	code := valuationCodes[symbol]
	if code == "" {
		return nil, fmt.Errorf("暂无可核实的同指数历史来源")
	}
	session, err := newHTTPSession()
	if err != nil {
		return nil, err
	}
	defer session.close()
	if err := session.setTimeouts(800, TimeoutMillis, 3000, 3000); err != nil {
		return nil, err
	}
	status, body, err := doHTTPRequestLimited(ctx, session, "guchacha.com", "/api/index/valuation/"+code, false, "GET", nil, maxValuationBody)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, HTTPError{StatusCode: status}
	}
	return parseIndexValuation(symbol, body, time.Now())
}

func enrichCapitalizationLocked(ctx context.Context, batch map[string]SymbolResult) {
	needsFX := false
	for _, result := range batch {
		if q := result.Quote; q != nil && q.IsEquity() && q.Currency == "CNY" && q.Capitalization != nil {
			needsFX = true
		}
	}
	if !needsFX {
		return
	}
	now := time.Now()
	if now.Sub(fxFetchedAt) >= time.Minute && !now.Before(fxNextTry) {
		fxNextTry = now.Add(15 * time.Second)
		if session, err := ensureSessionLocked(); err == nil {
			fxCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			for _, host := range []string{PrimaryHost, FallbackHost} {
				status, body, err := doHTTPGet(fxCtx, session, host, "/v8/finance/chart/CNY%3DX?interval=1d&range=1d", false)
				if err == nil && status == 200 {
					if rate, err := parseCNYRate(body, now); err == nil {
						cachedCNY, fxFetchedAt = rate, now
						break
					}
				}
			}
			cancel()
		}
	}
	// A failed FX refresh must neither block stock prices nor retain an old
	// conversion indefinitely. The provider timestamp remains visible.
	if now.Sub(fxFetchedAt) <= 5*time.Minute && cachedCNY.at >= now.Add(-72*time.Hour).Unix() {
		convertCapitalizationUSD(batch, cachedCNY)
	}
}

func fetchSymbolsLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	return fetchPinnedQuotes(ctx, syms, fetchYahooSymbolsLocked, fetchTencentSymbolsLocked)
}

func fetchTencentSymbolsLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	session, err := ensureSessionLocked()
	if err != nil {
		return nil, err
	}
	query, ok := TencentQueryForSymbols(syms)
	if !ok {
		return nil, fmt.Errorf("腾讯源不支持该组合")
	}
	return fetchTencentRealtimeLocked(ctx, session, syms, query)
}

func fetchYahooSymbolsLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	return yahooQuotes.fetch(ctx, func(ctx context.Context) (map[string]SymbolResult, error) {
		session, err := ensureSessionLocked()
		if err != nil {
			return nil, err
		}
		batch, err := fetchV7OvernightLocked(ctx, session, syms)
		if err != nil {
			resetSessionLocked()
		}
		return batch, err
	}, func(ctx context.Context) (map[string]SymbolResult, error) {
		session, err := ensureSessionLocked()
		if err != nil {
			return nil, err
		}
		batch, err := fetchChartBatch(ctx, syms, func(ctx context.Context, symbol string) (Quote, error) {
			return fetchChartSymbol(ctx, session, symbol)
		})
		if err != nil {
			resetSessionLocked()
		}
		return batch, err
	})
}

func fetchTencentRealtimeLocked(ctx context.Context, session *httpSession, syms []string, query string) (map[string]SymbolResult, error) {
	path := "/q=" + query
	start := time.Now()
	status, body, err := doHTTPGet(ctx, session, TencentHost, path, false)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, HTTPError{StatusCode: status}
	}
	rttMs := float64(time.Since(start).Microseconds()) / 1000.0
	return ParseTencentQuoteBatch(syms, body, rttMs, time.Now())
}

func fetchV7OvernightLocked(ctx context.Context, session *httpSession, syms []string) (map[string]SymbolResult, error) {
	crumb := cachedCrumb
	if crumb == "" {
		var err error
		crumb, err = refreshCrumbLocked(ctx, session)
		if err != nil {
			return nil, err
		}
	}

	batch, err := tryV7Batch(ctx, session, syms, crumb)
	if err == nil {
		return batch, nil
	}

	var httpErr HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403) {
		resetSessionLocked()
		newSession, sErr := ensureSessionLocked()
		if sErr != nil {
			return nil, sErr
		}
		newCrumb, cErr := refreshCrumbLocked(ctx, newSession)
		if cErr != nil {
			return nil, cErr
		}
		return tryV7Batch(ctx, newSession, syms, newCrumb)
	}
	return nil, err
}

func wireSymbolList(syms []string) string {
	var wire []string
	for _, s := range syms {
		wire = append(wire, s)
		if s == "^HSTECH" {
			wire = append(wire, "HSTECH.HK")
		}
	}
	return url.QueryEscape(strings.Join(wire, ","))
}

func tryV7Batch(ctx context.Context, session *httpSession, syms []string, crumb string) (map[string]SymbolResult, error) {
	path := "/v7/finance/quote?symbols=" + wireSymbolList(syms) +
		"&overnightPrice=true&fields=" + quoteFields +
		"&crumb=" + url.QueryEscape(crumb)

	var lastErr error
	for _, host := range []string{PrimaryHost, FallbackHost} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := time.Now()
		status, body, err := doHTTPGet(ctx, session, host, path, true)
		if err != nil {
			lastErr = err
			continue
		}
		if status != 200 {
			lastErr = HTTPError{StatusCode: status}
			if status == 401 || status == 403 {
				return nil, lastErr
			}
			continue
		}
		rttMs := float64(time.Since(start).Microseconds()) / 1000.0
		batch, parseErr := ParseYahooQuoteBatchForSymbols(syms, body, rttMs, time.Now())
		if parseErr == nil {
			return batch, nil
		}
		lastErr = parseErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("行情请求失败")
	}
	return nil, lastErr
}

func fetchChartSymbol(ctx context.Context, session *httpSession, sym string) (Quote, error) {
	candidates := []string{sym}
	if sym == "^HSTECH" {
		candidates = []string{"HSTECH.HK", "^HSTECH"}
	}
	var lastErr error
	for _, wireSym := range candidates {
		path := "/v8/finance/chart/" + url.PathEscape(wireSym) + "?interval=1d&range=1d&includePrePost=true"
		for _, host := range []string{FallbackHost, PrimaryHost} {
			if err := ctx.Err(); err != nil {
				return Quote{}, err
			}
			start := time.Now()
			status, body, err := doHTTPGet(ctx, session, host, path, false)
			if err == nil && status != 200 {
				err = HTTPError{StatusCode: status}
			}
			if err == nil {
				rttMs := float64(time.Since(start).Microseconds()) / 1000.0
				q, parseErr := ParseYahooChart(sym, body, rttMs, time.Now())
				if parseErr == nil {
					return q, nil
				}
				err = parseErr
			}
			lastErr = err
		}
	}
	return Quote{}, lastErr
}

func doHTTPGet(ctx context.Context, session *httpSession, host, path string, enableCookies bool) (int, []byte, error) {
	return doHTTPRequest(ctx, session, host, path, enableCookies, "GET", nil)
}

func doHTTPRequest(ctx context.Context, session *httpSession, host, path string, enableCookies bool, method string, requestBody []byte) (int, []byte, error) {
	return doHTTPRequestLimited(ctx, session, host, path, enableCookies, method, requestBody, MaxBody)
}
