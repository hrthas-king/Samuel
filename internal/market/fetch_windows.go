//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	winhttp         = winapi.DLL("winhttp.dll")
	open            = winhttp.NewProc("WinHttpOpen")
	connect         = winhttp.NewProc("WinHttpConnect")
	openRequest     = winhttp.NewProc("WinHttpOpenRequest")
	setTimeouts     = winhttp.NewProc("WinHttpSetTimeouts")
	setOption       = winhttp.NewProc("WinHttpSetOption")
	sendRequest     = winhttp.NewProc("WinHttpSendRequest")
	receiveResponse = winhttp.NewProc("WinHttpReceiveResponse")
	queryHeaders    = winhttp.NewProc("WinHttpQueryHeaders")
	readData        = winhttp.NewProc("WinHttpReadData")
	closeHandle     = winhttp.NewProc("WinHttpCloseHandle")
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
	sessHandle  uintptr
	cachedCrumb string
	cachedCNY   fxQuote
	fxFetchedAt time.Time
	fxNextTry   time.Time
)

func ensureSessionLocked() (uintptr, error) {
	if sessHandle != 0 {
		return sessHandle, nil
	}
	session, err := newHTTPSession()
	if err != nil {
		return 0, err
	}
	sessHandle = session
	return session, nil
}

func newHTTPSession() (uintptr, error) {
	agent := winapi.UTF16("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	s, _, err := open.Call(uintptr(unsafe.Pointer(agent)), 4, 0, 0, 0)
	runtime.KeepAlive(agent)
	if s == 0 {
		return 0, winapi.Error("WinHttpOpen", err)
	}
	if ok, _, err := setTimeouts.Call(s, 800, TimeoutMillis, TimeoutMillis, TimeoutMillis); ok == 0 {
		closeHandle.Call(s)
		return 0, winapi.Error("WinHttpSetTimeouts", err)
	}
	return s, nil
}

func resetSessionLocked() {
	if sessHandle != 0 {
		closeHandle.Call(sessHandle)
		sessHandle = 0
	}
	cachedCrumb = ""
}

func refreshCrumbLocked(ctx context.Context, session uintptr) (string, error) {
	_, _, _ = doHTTPGet(ctx, session, CookieHost, "/", true)

	var lastErr error
	for _, host := range []string{PrimaryHost, FallbackHost} {
		if err := ctx.Err(); err != nil {
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
			cachedCrumb = c
			return c, nil
		}
		lastErr = fmt.Errorf("无效 crumb 响应")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无法获取 crumb")
	}
	return "", lastErr
}

// Fetch 通过 WinHTTP 获取当前激活标的分组的行情。
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
	defer closeHandle.Call(session)
	if ok, _, err := setTimeouts.Call(session, 800, TimeoutMillis, 3000, 3000); ok == 0 {
		return nil, winapi.Error("WinHttpSetTimeouts(valuation)", err)
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
	if usCashIndexSymbols(syms) {
		return fetchUSCashRealtimeLocked(ctx, syms)
	}
	indices := len(syms) > 0
	for _, symbol := range syms {
		indices = indices && isGlobalIndex(symbol)
	}
	if indices {
		return fetchIndexQuotes(ctx, syms, fetchTradingViewIndicesLocked, fetchStandardSymbolsLocked)
	}
	return fetchStandardSymbolsLocked(ctx, syms)
}

func fetchUSCashRealtimeLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	session, err := ensureSessionLocked()
	if err != nil {
		return nil, err
	}
	var wire []string
	for _, symbol := range syms {
		wire = append(wire, usCashTickers[symbol])
	}
	path := "/quote-html-webservice/quote.htm?symbols=" + url.QueryEscape(strings.Join(wire, "|")) + "&requestMethod=quick&noform=1&fund=0&exthrs=0&output=json"
	start := time.Now()
	status, body, err := doHTTPGet(ctx, session, "quote.cnbc.com", path, false)
	if err != nil {
		return nil, fmt.Errorf("实时指数读取失败: %w", err)
	}
	if status != 200 {
		return nil, HTTPError{StatusCode: status}
	}
	return parseCNBCIndexBatch(syms, body, float64(time.Since(start).Microseconds())/1000, time.Now())
}

func fetchTradingViewIndicesLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	session, err := ensureSessionLocked()
	if err != nil {
		return nil, err
	}
	requestBody, err := indexRequest(syms)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	status, body, err := doHTTPRequest(ctx, session, TradingViewHost, "/global/scan", false, "POST", requestBody)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, HTTPError{StatusCode: status}
	}
	rtt := float64(time.Since(start).Microseconds()) / 1000
	return parseIndexQuotes(syms, body, rtt, time.Now())
}

func fetchStandardSymbolsLocked(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
	session, err := ensureSessionLocked()
	if err != nil {
		return nil, err
	}

	// 优先使用腾讯行情；每个标的的行情时间由数据源提供。
	if tQuery, ok := TencentQueryForSymbols(syms); ok {
		tBatch, tErr := fetchTencentRealtimeLocked(ctx, session, syms, tQuery)
		if tErr == nil {
			return tBatch, nil
		}
		// 中国市场股票和ETF保留腾讯数据源；失败时不混入时效未经核实的Yahoo报价。
		if customSymbols(syms) || (len(syms) == 2 && isTrackedSZETF(syms[0]) && isTrackedSZETF(syms[1])) {
			return tBatch, fmt.Errorf("腾讯中国市场行情请求失败: %w", tErr)
		}
	}

	// 2. 国际市场或腾讯备用时，调用 Yahoo Finance v7
	batch, err := fetchV7OvernightLocked(ctx, session, syms)
	if err == nil {
		return batch, nil
	}

	resetSessionLocked()
	fbSession, sErr := ensureSessionLocked()
	if sErr != nil {
		return nil, err
	}
	fbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return fetchV8FallbackLocked(fbCtx, fbSession, syms, err)
}

func fetchTencentRealtimeLocked(ctx context.Context, session uintptr, syms []string, query string) (map[string]SymbolResult, error) {
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

func fetchV7OvernightLocked(ctx context.Context, session uintptr, syms []string) (map[string]SymbolResult, error) {
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

func tryV7Batch(ctx context.Context, session uintptr, syms []string, crumb string) (map[string]SymbolResult, error) {
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

func fetchV8FallbackLocked(ctx context.Context, session uintptr, syms []string, primaryErr error) (map[string]SymbolResult, error) {
	results := make(map[string]SymbolResult, len(syms))
	var anySuccess bool
	lastErr := primaryErr

	for _, sym := range syms {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q, fetchErr := fetchChartSymbol(ctx, session, sym)
		if fetchErr != nil {
			lastErr = fetchErr
			results[sym] = SymbolResult{Err: fmt.Sprintf("行情读取失败: %v", fetchErr)}
		} else {
			anySuccess = true
			qCopy := q
			results[sym] = SymbolResult{Quote: &qCopy}
		}
	}
	if !anySuccess && lastErr != nil {
		return nil, lastErr
	}
	return results, nil
}

func fetchChartSymbol(ctx context.Context, session uintptr, sym string) (Quote, error) {
	candidates := []string{sym}
	if sym == "^HSTECH" {
		candidates = []string{"HSTECH.HK", "^HSTECH"}
	}
	var lastErr error
	for _, wireSym := range candidates {
		path := "/v8/finance/chart/" + url.PathEscape(wireSym) + "?interval=1d&range=1d&includePrePost=true"
		for _, host := range []string{PrimaryHost, FallbackHost} {
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

func doHTTPGet(ctx context.Context, session uintptr, host, path string, enableCookies bool) (int, []byte, error) {
	return doHTTPRequest(ctx, session, host, path, enableCookies, "GET", nil)
}

func doHTTPRequest(ctx context.Context, session uintptr, host, path string, enableCookies bool, method string, requestBody []byte) (int, []byte, error) {
	return doHTTPRequestLimited(ctx, session, host, path, enableCookies, method, requestBody, MaxBody)
}

func doHTTPRequestLimited(ctx context.Context, session uintptr, host, path string, enableCookies bool, method string, requestBody []byte, maxBytes int) (int, []byte, error) {
	defer runtime.KeepAlive(requestBody)
	hostPtr := winapi.UTF16(host)
	conn, _, err := connect.Call(session, uintptr(unsafe.Pointer(hostPtr)), 443, 0)
	runtime.KeepAlive(hostPtr)
	if conn == 0 {
		return 0, nil, winapi.Error("WinHttpConnect", err)
	}
	defer closeHandle.Call(conn)

	verb := winapi.UTF16(method)
	pathPtr := winapi.UTF16(path)
	request, _, err := openRequest.Call(conn, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(pathPtr)), 0, 0, 0, 0x00800000)
	if request == 0 {
		return 0, nil, winapi.Error("WinHttpOpenRequest", err)
	}
	defer closeHandle.Call(request)

	if !enableCookies {
		var disable uint32 = 2 | 1
		if ok, _, err := setOption.Call(request, 63, uintptr(unsafe.Pointer(&disable)), 4); ok == 0 {
			return 0, nil, winapi.Error("WinHttpSetOption", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	headers := requestHeaders
	var payload uintptr
	if len(requestBody) > 0 {
		headers += "Content-Type: application/json\r\n"
		payload = uintptr(unsafe.Pointer(&requestBody[0]))
	}
	hdrPtr := winapi.UTF16(headers)
	ok, _, sendErr := sendRequest.Call(request, uintptr(unsafe.Pointer(hdrPtr)), ^uintptr(0), payload, uintptr(len(requestBody)), uintptr(len(requestBody)), 0)
	if ok == 0 {
		return 0, nil, winapi.Error("WinHttpSendRequest", sendErr)
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if ok, _, err := receiveResponse.Call(request, 0); ok == 0 {
		return 0, nil, winapi.Error("WinHttpReceiveResponse", err)
	}
	var status uint32
	size := uint32(4)
	if ok, _, err := queryHeaders.Call(request, 19|0x20000000, 0, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&size)), 0); ok == 0 {
		return 0, nil, winapi.Error("WinHttpQueryHeaders", err)
	}
	var encBuf [64]uint16
	encSize := uint32(unsafe.Sizeof(encBuf))
	if ok, _, _ := queryHeaders.Call(request, 29, 0, uintptr(unsafe.Pointer(&encBuf[0])), uintptr(unsafe.Pointer(&encSize)), 0); ok != 0 {
		enc := strings.ToLower(strings.TrimSpace(utf16ToString(encBuf[:])))
		if enc != "" && enc != "identity" {
			return int(status), nil, fmt.Errorf("拒绝压缩响应 (%s)", enc)
		}
	}

	body := make([]byte, 0, 2048)
	var chunk [1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return int(status), nil, err
		}
		var n uint32
		ok, _, err := readData.Call(request, uintptr(unsafe.Pointer(&chunk[0])), uintptr(len(chunk)), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			return int(status), nil, winapi.Error("WinHttpReadData", err)
		}
		if n == 0 {
			break
		}
		if n > uint32(len(chunk)) || len(body)+int(n) > maxBytes {
			return int(status), nil, fmt.Errorf("行情响应超过 %d 字节上限", maxBytes)
		}
		body = append(body, chunk[:n]...)
	}
	runtime.KeepAlive(verb)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(hdrPtr)
	return int(status), body, nil
}

func utf16ToString(u []uint16) string {
	for i, v := range u {
		if v == 0 {
			u = u[:i]
			break
		}
	}
	var sb strings.Builder
	for _, r := range u {
		sb.WriteRune(rune(r))
	}
	return sb.String()
}
