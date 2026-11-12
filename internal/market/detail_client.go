//go:build (windows || darwin) && (amd64 || arm64)

package market

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

type detailMetricKey struct {
	symbol, kind, currency, provider string
}

type detailNewsKey struct {
	symbol, language, kind string
}

type detailYearKey struct {
	symbol, date string
}

type detailRegularKey struct {
	symbol, date string
	state        SessionState
}

// DetailClient is allocated on first opening a chart. Each pane uses an
// independent short-lived HTTPS session and an independent cache.
type DetailClient struct {
	ctx      context.Context
	notify   func()
	metrics  detailCache[detailMetricKey, QuoteMetrics]
	regular  detailCache[detailRegularKey, RegularClose]
	year     detailCache[detailYearKey, YearPriceRange]
	mainland detailCache[string, MainlandFundamentals]
	calendar detailCache[struct{}, []MacroEvent]
	fed      detailCache[struct{}, []MacroEvent]
	energy   detailCache[struct{}, []MacroEvent]
	news     detailCache[detailNewsKey, []NewsItem]
}

func NewDetailClient(ctx context.Context, notify func()) *DetailClient {
	return &DetailClient{ctx: ctx, notify: notify}
}

func (c *DetailClient) Read(view View, name, language string, start bool, data DetailData) DetailData {
	symbol, kind := view.Symbol, ""
	if view.Quote != nil {
		q := *view.Quote
		kind = q.QuoteType
		provider := ""
		if strings.HasPrefix(q.UpdateMode, "yahoo_") {
			provider = "Yahoo"
		} else if strings.HasPrefix(q.QuoteSource, "Tencent ") {
			provider = "Tencent"
		}
		if provider != "" {
			key := detailMetricKey{symbol, kind, q.Currency, provider}
			data.Metrics = c.metrics.get(c.ctx, key, start, time.Minute, data.Metrics, func(ctx context.Context) (QuoteMetrics, bool) {
				return fetchDetailMetrics(ctx, q, provider)
			}, c.notify)
		}
		data = c.ReadRegularHistory(q, start, data)
		if q.IsEquity() && watchlist.IsMainlandSymbol(symbol) {
			data.Mainland = c.mainland.get(c.ctx, symbol, start, 15*time.Minute, data.Mainland, func(ctx context.Context) (MainlandFundamentals, bool) {
				return fetchMainlandFundamentals(ctx, symbol)
			}, c.notify)
		}
	}
	if kind == "" {
		kind = detailQuoteType(symbol)
	}
	data.Calendar = c.calendar.get(c.ctx, struct{}{}, start, 12*time.Minute, data.Calendar, fetchLiveCalendar, c.notify)
	if kind != "EQUITY" {
		data.Fed = c.fed.get(c.ctx, struct{}{}, start, 12*time.Hour, data.Fed, fetchFedCalendar, c.notify)
	}
	if symbol == "BZ=F" {
		data.Energy = c.energy.get(c.ctx, struct{}{}, start, 12*time.Minute, data.Energy, fetchEnergyCalendar, c.notify)
	}
	// Wait for the verified company name when constructing a stock search.
	if kind != "EQUITY" || view.Quote != nil {
		key := detailNewsKey{symbol, language, kind}
		data.News = c.news.get(c.ctx, key, start, 4*time.Minute, data.News, func(ctx context.Context) ([]NewsItem, bool) {
			return fetchDetailNews(ctx, symbol, name, kind, language)
		}, c.notify)
	}
	return data
}

// ReadRegularHistory refreshes the completed close separately when an open
// chart crosses sessions. Other detail panes keep their open-on-demand policy.
func (c *DetailClient) ReadRegularHistory(q Quote, start bool, data DetailData) DetailData {
	at := QuoteMarketTime(q)
	if at.IsZero() {
		return data
	}
	switch {
	case strings.HasPrefix(q.UpdateMode, "yahoo_"):
		key := detailRegularKey{q.Symbol, at.Format("2006-01-02"), q.State}
		data.Regular = c.regular.get(c.ctx, key, start, time.Minute, data.Regular, func(ctx context.Context) (RegularClose, bool) {
			return fetchYahooRegularClose(ctx, q)
		}, c.notify)
	case strings.HasPrefix(q.QuoteSource, "Tencent ") && (q.Currency == "CNY" || q.Currency == "HKD"):
		to := marketDate(at)
		through := tencentCloseThrough(time.Now(), q.Currency == "HKD")
		key := detailYearKey{q.Symbol, to.Format("2006-01-02") + "/" + through.Format("2006-01-02")}
		data.Year = c.year.get(c.ctx, key, start, 15*time.Minute, data.Year, func(ctx context.Context) (YearPriceRange, bool) {
			return fetchTencentYearRange(ctx, q.Symbol, to)
		}, c.notify)
	}
	return data
}

func fetchYahooRegularClose(parent context.Context, q Quote) (RegularClose, bool) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return RegularClose{}, false
	}
	defer session.close()
	path := "/v8/finance/chart/" + url.PathEscape(q.Symbol) + "?range=5d&interval=1d&includePrePost=false"
	for _, host := range []string{FallbackHost, PrimaryHost} {
		status, body, err := doHTTPGet(ctx, session, host, path, false)
		if err == nil && status == 200 {
			if close, ok := parseYahooRegularClose(q, body, time.Now()); ok {
				return close, true
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return RegularClose{}, false
}

func detailQuoteType(symbol string) string {
	switch symbol {
	case "GC=F", "BZ=F":
		return "FUTURE"
	case "VOO", "QQQ", "SOXX", "SMH", "159632.SZ", "159655.SZ":
		return "ETF"
	case "000001.SS", "399001.SZ", "399006.SZ":
		return "INDEX"
	}
	if strings.HasPrefix(symbol, "^") {
		return "INDEX"
	}
	if watchlist.IsMainlandETF(symbol) {
		return "ETF"
	}
	return "EQUITY"
}

const detailQuoteFields = "symbol,currency,quoteType,regularMarketTime,regularMarketOpen,regularMarketDayHigh,regularMarketDayLow," +
	"regularMarketPreviousClose,regularMarketVolume,fiftyTwoWeekHigh,fiftyTwoWeekLow,trailingPE,forwardPE,epsTrailingTwelveMonths," +
	"earningsTimestamp,earningsTimestampStart,earningsTimestampEnd,isEarningsDateEstimate,earningsReleaseTime"

func fetchDetailMetrics(parent context.Context, q Quote, provider string) (QuoteMetrics, bool) {
	ctx, cancel := context.WithTimeout(parent, 7*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return QuoteMetrics{}, false
	}
	defer session.close()
	if provider == "Tencent" {
		code, ok := TencentQueryForSymbols([]string{q.Symbol})
		if !ok {
			return QuoteMetrics{}, false
		}
		status, body, err := doHTTPGet(ctx, session, TencentHost, "/q="+code, false)
		if err != nil || status != 200 {
			return QuoteMetrics{}, false
		}
		return parseTencentMetrics(q, code, body, time.Now())
	}
	if crumb, err := requestCrumb(ctx, session); err == nil {
		path := "/v7/finance/quote?symbols=" + url.QueryEscape(q.Symbol) + "&fields=" + detailQuoteFields + "&crumb=" + url.QueryEscape(crumb)
		for _, host := range []string{FallbackHost, PrimaryHost} {
			status, body, err := doHTTPGet(ctx, session, host, path, true)
			if err == nil && status == 200 {
				if metrics, ok := parseYahooMetrics(q, body, false, time.Now()); ok {
					return metrics, true
				}
			}
		}
	}
	path := "/v8/finance/chart/" + url.PathEscape(q.Symbol) + "?range=1d&interval=1d"
	status, body, err := doHTTPGet(ctx, session, FallbackHost, path, false)
	if err != nil || status != 200 {
		return QuoteMetrics{}, false
	}
	return parseYahooMetrics(q, body, true, time.Now())
}

func fetchMainlandFundamentals(parent context.Context, symbol string) (MainlandFundamentals, bool) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return MainlandFundamentals{}, false
	}
	defer session.close()
	base := "/api/data/v1/get?filter=" + url.QueryEscape(`(SECUCODE="`+eastMoneySymbol(symbol)+`")`) + "&sortTypes=-1&pageSize=4&pageNumber=1"
	const host = "datacenter-web.eastmoney.com"
	var info MainlandFundamentals
	status, body, err := doHTTPGet(ctx, session, host, base+"&reportName=RPT_PUBLIC_BS_APPOIN&sortColumns=REPORT_DATE&columns=SECUCODE,ACTUAL_PUBLISH_DATE,APPOINT_PUBLISH_DATE,IS_PUBLISH", false)
	if err == nil && status == 200 {
		info.Earnings = parseMainlandEarnings(symbol, body, time.Now())
	}
	status, body, err = doHTTPGet(ctx, session, host, base+"&reportName=RPT_LICO_FN_CPD&sortColumns=REPORTDATE&columns=SECUCODE,BASIC_EPS,REPORTDATE,NOTICE_DATE", false)
	if err == nil && status == 200 {
		info.EPS, info.EPSAsOf = parseMainlandEPS(symbol, body, time.Now())
	}
	return info, !info.Earnings.Start.IsZero() || info.EPS != nil
}

func fetchTencentYearRange(parent context.Context, symbol string, to time.Time) (YearPriceRange, bool) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return YearPriceRange{}, false
	}
	defer session.close()
	code, ok := TencentQueryForSymbols([]string{symbol})
	if !ok {
		return YearPriceRange{}, false
	}
	code = strings.TrimPrefix(code, "r_")
	from := to.AddDate(0, 0, -52*7)
	path := "/appstock/app/kline/kline?param=" + code + ",day," + from.Format("2006-01-02") + "," + to.Format("2006-01-02") + ",365"
	status, body, err := doHTTPRequestLimited(ctx, session, "web.ifzq.gtimg.cn", path, false, "GET", nil, 128<<10)
	if err != nil || status != 200 {
		return YearPriceRange{}, false
	}
	return parseTencentYearRange(code, body, from, to, time.Now())
}

func fetchLiveCalendar(parent context.Context) ([]MacroEvent, bool) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return nil, false
	}
	defer session.close()
	from := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	to := from.AddDate(0, 0, 9)
	const headers = "Origin: https://www.tradingview.com\r\nReferer: https://www.tradingview.com/\r\n"
	status, body, err := doHTTPRequestLimited(ctx, session, "economic-calendar.tradingview.com", macroCalendarPath(from, to),
		false, "GET", nil, 256<<10, headers)
	if err == nil && status == 200 {
		if events, ok := parseMacroCalendar(body, from, to); ok {
			return events, true
		}
	}
	status, body, err = doHTTPRequestLimited(ctx, session, "nfs.faireconomy.media", "/ff_calendar_thisweek.json", false, "GET", nil, 128<<10)
	if err != nil || status != 200 {
		return nil, false
	}
	return parseWeeklyCalendar(body, from, to)
}

func fetchFedCalendar(parent context.Context) ([]MacroEvent, bool) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return nil, false
	}
	defer session.close()
	status, body, err := doHTTPRequestLimited(ctx, session, "www.federalreserve.gov", "/monetarypolicy/fomccalendars.htm", false, "GET", nil, 512<<10)
	if err != nil || status != 200 {
		return nil, false
	}
	now := time.Now()
	var events []MacroEvent
	for _, event := range parseFedCalendar(body) {
		if event.At.After(now.AddDate(0, 0, -2)) {
			events = append(events, event)
		}
	}
	return events, len(events) != 0
}

func fetchEnergyCalendar(parent context.Context) ([]MacroEvent, bool) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return nil, false
	}
	defer session.close()
	status, body, err := doHTTPRequestLimited(ctx, session, "www.eia.gov", "/petroleum/supply/weekly/schedule.php", false, "GET", nil, 160<<10)
	if err != nil || status != 200 {
		return nil, false
	}
	from := marketDate(time.Now().In(newYorkZone))
	return parseEIASchedule(body, from, from.AddDate(0, 0, 8))
}

func fetchDetailNews(parent context.Context, symbol, name, kind, language string) ([]NewsItem, bool) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return nil, false
	}
	defer session.close()
	now, equity := time.Now(), kind == "EQUITY"
	var yahoo []NewsItem
	if equity && language == "en" && watchlist.IsUSSymbol(symbol) {
		path := "/v1/finance/search?q=" + url.QueryEscape(symbol) + "&quotesCount=0&newsCount=12"
		status, body, err := doHTTPRequestLimited(ctx, session, FallbackHost, path, false, "GET", nil, 128<<10)
		if err == nil && status == 200 {
			yahoo, _ = parseYahooNews(body, symbol, name, now)
			if len(yahoo) >= 3 {
				return yahoo, true
			}
		}
	}
	query, simple := newsTerms(symbol, name, kind, language)
	market := "en-US"
	if language != "en" {
		market = "zh-HK&setlang=zh-Hant"
	}
	read := func(terms string) ([]NewsItem, bool) {
		path := "/news/search?q=" + url.QueryEscape(terms) + "&format=rss&mkt=" + market + "&qft=interval%3D%227%22"
		status, body, err := doHTTPRequestLimited(ctx, session, "www.bing.com", path, false, "GET", nil, 128<<10)
		if err != nil || status != 200 {
			return nil, false
		}
		return parseRSSNews(body, now, equity)
	}
	items, ok := read(query)
	if len(items) == 0 && simple != query {
		items, ok = read(simple)
	}
	items = recentNews(append(items, yahoo...), now, equity)
	return items, ok || len(items) != 0
}
