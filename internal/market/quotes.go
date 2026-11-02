// Package market 解析并校验全球多市场标的（美股 ETF/正股、A股、港股、亚太、欧洲指数）实时行情与五态时段状态机。
package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

const (
	MaxBody       = 32768
	PrimaryHost   = "query1.finance.yahoo.com"
	FallbackHost  = "query2.finance.yahoo.com"
	TencentHost   = "qt.gtimg.cn"
	CookieHost    = "fc.yahoo.com"
	TimeoutMillis = 1500
)

const (
	GroupVOOQQQ          = "voo_qqq"
	GroupSOXXSMH         = "soxx_smh"
	GroupTop2US          = "top2_us"
	GroupGoldOil         = "gold_oil"
	GroupCNIndices       = "cn_indices"
	GroupHKIndices       = "hk_indices"
	GroupAsiaIndices     = "asia_indices"
	GroupEUIndices       = "eu_indices"
	GroupETF159632159655 = "etf_159632_159655"
	GroupUSIndices       = "us_indices"
	GroupCNComposite     = "cn_composite"
	DefaultGroup         = GroupVOOQQQ
)

// SessionState 表示五态交易时段状态信号。
type SessionState string

const (
	StatePreMarket  SessionState = "pre_market"  // ☀️ 早盘 / 盘前 (Pre-Market)
	StateRegular    SessionState = "regular"     // 🟢 盘中 (Regular Trading)
	StatePostMarket SessionState = "post_market" // 🔵 盘后 (Post-Market)
	StateOvernight  SessionState = "overnight"   // 🌙 夜盘 (Overnight)
	StateClosed     SessionState = "closed"      // ⚪ 休市 / 周末 (Closed / Weekend)
)

// Badge 返回用于文本诊断与悬停提示的时段状态符号。
func (s SessionState) Badge() string {
	switch s {
	case StatePreMarket:
		return "☀"
	case StateRegular:
		return "🟢"
	case StatePostMarket:
		return "🔵"
	case StateOvernight:
		return "🌙"
	default:
		return "⚪"
	}
}

type GroupSpec struct {
	ID        string
	MenuLabel string
	Symbols   []string
}

var Groups = []GroupSpec{
	{ID: GroupVOOQQQ, MenuLabel: "标普 / 纳指 ETF (VOO / QQQ)", Symbols: []string{"VOO", "QQQ"}},
	{ID: GroupSOXXSMH, MenuLabel: "半导体 ETF (SOXX / SMH)", Symbols: []string{"SOXX", "SMH"}},
	{ID: GroupTop2US, MenuLabel: "美股市值前二 (NVDA / AAPL) · 2026-10-29核对", Symbols: []string{"NVDA", "AAPL"}},
	{ID: GroupGoldOil, MenuLabel: "黄金 / 布伦特原油 (GC / BZ)", Symbols: []string{"GC=F", "BZ=F"}},
	{ID: GroupCNIndices, MenuLabel: "A股指数 (上证 / 创指)", Symbols: []string{"000001.SS", "399006.SZ"}},
	{ID: GroupHKIndices, MenuLabel: "港股指数 (恒指 / 恒科)", Symbols: []string{"^HSI", "^HSTECH"}},
	{ID: GroupAsiaIndices, MenuLabel: "日韩指数 (TradingView · 日经参考 / 韩国综合)", Symbols: []string{"^N225", "^KS11"}},
	{ID: GroupEUIndices, MenuLabel: "欧洲参考指数 (TradingView · CAC / DAX)", Symbols: []string{"^FCHI", "^GDAXI"}},
	{ID: GroupETF159632159655, MenuLabel: "华安纳指ETF / 华夏标普500ETF", Symbols: []string{"159632.SZ", "159655.SZ"}},
	{ID: GroupUSIndices, MenuLabel: "美股指数 (纳斯达克100 / 标普500)", Symbols: []string{"^NDX", "^GSPC"}},
	{ID: GroupCNComposite, MenuLabel: "沪深指数 (上证 / 深证成指)", Symbols: []string{"000001.SS", "399001.SZ"}},
}

var (
	groupMu     sync.RWMutex
	activeGroup = Groups[0]
	Symbols     = []string{"VOO", "QQQ"}

	Aliases = map[string]string{
		"VOO":       "VOO",
		"QQQ":       "QQQ",
		"SOXX":      "SOXX",
		"SMH":       "SMH",
		"NVDA":      "NVDA",
		"MSFT":      "MSFT",
		"GC=F":      "黄金",
		"BZ=F":      "布油",
		"000001.SS": "上证",
		"399006.SZ": "创指",
		"399001.SZ": "深证",
		"^NDX":      "纳指100",
		"^GSPC":     "标普500",
		"^HSI":      "恒指",
		"^HSTECH":   "恒科",
		"^N225":     "日经",
		"^KS11":     "韩指",
		"^FCHI":     "CAC40",
		"^GDAXI":    "DAX",
		"159632.SZ": "纳指ETF",
		"159655.SZ": "标普500ETF",
	}

	Tickers = map[string]string{
		"VOO":       "NYSEArca:VOO",
		"QQQ":       "NASDAQ:QQQ",
		"SOXX":      "NASDAQ:SOXX",
		"SMH":       "NASDAQ:SMH",
		"NVDA":      "NASDAQ:NVDA",
		"AAPL":      "NASDAQ:AAPL",
		"MSFT":      "NASDAQ:MSFT",
		"GC=F":      "COMEX:GC",
		"BZ=F":      "ICE:BZ",
		"000001.SS": "SSE:000001",
		"399006.SZ": "SZSE:399006",
		"399001.SZ": "SZSE:399001",
		"^NDX":      "NASDAQ:NDX",
		"^GSPC":     "SP:SPX",
		"^HSI":      "HKEX:HSI",
		"^HSTECH":   "HKEX:HSTECH",
		"^N225":     "OSE:N225",
		"^KS11":     "KRX:KS11",
		"^FCHI":     "Euronext:CAC40",
		"^GDAXI":    "XETRA:DAX",
		"159632.SZ": "SZSE:159632",
		"159655.SZ": "SZSE:159655",
	}

	allowedCurrencies = map[string]bool{
		"USD": true,
		"CNY": true,
		"HKD": true,
		"JPY": true,
		"EUR": true,
		"KRW": true,
		"":    true,
	}

	allowedQuoteTypes = map[string]bool{
		"ETF":    true,
		"EQUITY": true,
		"INDEX":  true,
		"FUTURE": true,
		"":       true,
	}
)

func ValidGroup(id string) bool {
	groupMu.RLock()
	defer groupMu.RUnlock()
	return groupByIDLocked(id).ID == id
}

func GroupByID(id string) GroupSpec {
	groupMu.RLock()
	defer groupMu.RUnlock()
	return groupByIDLocked(id)
}

func SetActiveGroup(id string) GroupSpec {
	groupMu.Lock()
	g := groupByIDLocked(id)
	activeGroup = cloneGroup(g)
	Symbols = append([]string(nil), g.Symbols...)
	groupMu.Unlock()
	return g
}

func ActiveGroup() GroupSpec {
	groupMu.RLock()
	g := cloneGroup(activeGroup)
	groupMu.RUnlock()
	return g
}

func ActiveSymbols() []string {
	groupMu.RLock()
	out := append([]string(nil), activeGroup.Symbols...)
	groupMu.RUnlock()
	return out
}

func AliasFor(symbol string) string {
	if a, ok := Aliases[symbol]; ok && a != "" {
		return a
	}
	return symbol
}

func quoteAlias(symbol, name string) string {
	if alias, ok := Aliases[symbol]; ok {
		return alias
	}
	return name
}

func isTrackedSZETF(symbol string) bool {
	return symbol == "159632.SZ" || symbol == "159655.SZ"
}

// PriceDecimals returns the number of decimal places used for displayed prices.
func PriceDecimals(symbol string) int {
	if isTrackedSZETF(symbol) || watchlist.IsMainlandETF(symbol) {
		return 3
	}
	return 2
}

func normalizeSymbol(sym string) string {
	s := strings.TrimSpace(sym)
	if strings.EqualFold(s, "HSTECH.HK") {
		return "^HSTECH"
	}
	return s
}

type HTTPError struct {
	StatusCode int
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

type Quote struct {
	Symbol         string          `json:"symbol"`
	Alias          string          `json:"alias"`
	Price          float64         `json:"price"`
	ChangePercent  float64         `json:"change_percent"`
	ChangeAbs      float64         `json:"change_abs"`
	RegularPrice   float64         `json:"regular_price"`
	Currency       string          `json:"currency,omitempty"`
	QuoteType      string          `json:"quote_type,omitempty"`
	MarketState    string          `json:"market_state,omitempty"`
	State          SessionState    `json:"session_state"`
	TradeTime      int64           `json:"trade_time,omitempty"`
	QuoteSource    string          `json:"quote_source,omitempty"`
	UpdateMode     string          `json:"update_mode"`
	Session        string          `json:"session"`
	Description    string          `json:"description"`
	RTTMs          float64         `json:"rtt_ms"`
	ProviderSymbol string          `json:"provider_symbol,omitempty"`
	Reference      bool            `json:"reference,omitempty"`
	DelaySeconds   int             `json:"delay_seconds,omitempty"`
	Capitalization *Capitalization `json:"capitalization,omitempty"`
	Valuation      *IndexValuation `json:"valuation,omitempty"`
	QuoteDate      string          `json:"quote_date,omitempty"`
}

func (q Quote) Previous() float64 {
	return q.Price - q.ChangeAbs
}

func (q Quote) SessionText() string {
	if q.Session != "" {
		return q.Session
	}
	return "实时行情"
}

func (q Quote) DisplayName() string {
	if isTrackedSZETF(q.Symbol) {
		return AliasFor(q.Symbol)
	}
	if q.Alias != "" {
		return q.Alias
	}
	return AliasFor(q.Symbol)
}

func ValidateQuote(q Quote) error {
	if _, ok := Tickers[q.Symbol]; !ok && !watchlist.IsSymbol(q.Symbol) {
		return fmt.Errorf("标的不匹配 (%s)", q.Symbol)
	}
	if watchlist.IsUSSymbol(q.Symbol) && (q.Currency != "USD" || (q.QuoteType != "EQUITY" && q.QuoteType != "ETF")) {
		return fmt.Errorf("美股代码必须对应美元计价的股票或ETF")
	}
	for _, v := range []float64{q.Price, q.ChangePercent, q.ChangeAbs, q.RTTMs} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("非有限数值")
		}
	}
	prev := q.Previous()
	if math.IsNaN(prev) || math.IsInf(prev, 0) || q.Price <= 0 || prev <= 0 || q.RTTMs < 0 {
		return fmt.Errorf("价格、基准或请求耗时无效")
	}
	calc := q.ChangeAbs / prev * 100.0
	if math.IsNaN(calc) || math.IsInf(calc, 0) || !isClose(calc, q.ChangePercent, 1e-5, 0.05) {
		return fmt.Errorf("最新价、涨跌额与涨跌幅不一致")
	}
	if q.UpdateMode == "" || len(q.UpdateMode) > 80 {
		return fmt.Errorf("更新模式字段无效")
	}
	return nil
}

func isClose(a, b, relTol, absTol float64) bool {
	diff := math.Abs(a - b)
	limit := math.Max(relTol*math.Max(math.Abs(a), math.Abs(b)), absTol)
	return diff <= limit
}

type SymbolResult struct {
	Quote *Quote
	Err   string
}

type yahooQuoteBatchResponse struct {
	QuoteResponse struct {
		Result []yahooQuoteItem `json:"result"`
	} `json:"quoteResponse"`
}

type yahooQuoteItem struct {
	Symbol                       string          `json:"symbol"`
	Currency                     string          `json:"currency"`
	QuoteType                    string          `json:"quoteType"`
	ShortName                    string          `json:"shortName"`
	LongName                     string          `json:"longName"`
	MarketState                  string          `json:"marketState"`
	QuoteSourceName              string          `json:"quoteSourceName"`
	GMTOffsetMilliseconds        int64           `json:"gmtOffSetMilliseconds"`
	RegularMarketPrice           *float64        `json:"regularMarketPrice"`
	RegularMarketChange          *float64        `json:"regularMarketChange"`
	RegularMarketChangePercent   *float64        `json:"regularMarketChangePercent"`
	RegularMarketTime            *int64          `json:"regularMarketTime"`
	RegularMarketPreviousClose   *float64        `json:"regularMarketPreviousClose"`
	PreMarketPrice               *float64        `json:"preMarketPrice"`
	PreMarketChange              *float64        `json:"preMarketChange"`
	PreMarketChangePercent       *float64        `json:"preMarketChangePercent"`
	PreMarketTime                *int64          `json:"preMarketTime"`
	PostMarketPrice              *float64        `json:"postMarketPrice"`
	PostMarketChange             *float64        `json:"postMarketChange"`
	PostMarketChangePercent      *float64        `json:"postMarketChangePercent"`
	PostMarketTime               *int64          `json:"postMarketTime"`
	OvernightMarketPrice         *float64        `json:"overnightMarketPrice"`
	OvernightMarketChange        *float64        `json:"overnightMarketChange"`
	OvernightMarketChangePercent *float64        `json:"overnightMarketChangePercent"`
	OvernightMarketTime          *int64          `json:"overnightMarketTime"`
	FulldayPrice                 *float64        `json:"fulldayPrice"`
	FulldayChange                *float64        `json:"fulldayChange"`
	FulldayChangePercent         *float64        `json:"fulldayChangePercent"`
	MarketCap                    json.RawMessage `json:"marketCap"`
}

type sessionCandidate struct {
	price     float64
	changeAbs float64
	changePct float64
	tradeTime int64
	state     SessionState
	session   string
	mode      string
	valid     bool
}

func makeCandidate(price, chg, pct *float64, ts *int64, state SessionState, session, mode string) sessionCandidate {
	if price == nil || chg == nil || pct == nil || *price <= 0 {
		return sessionCandidate{}
	}
	var t int64
	if ts != nil {
		t = *ts
	}
	p := *price
	c := *chg
	cp := *pct
	if prev := p - c; prev > 0 {
		calc := c / prev * 100.0
		if !math.IsNaN(calc) && !math.IsInf(calc, 0) && !isClose(calc, cp, 1e-5, 0.05) && math.Abs(calc-cp) < 0.35 {
			cp = calc
		}
	}
	return sessionCandidate{
		price:     p,
		changeAbs: c,
		changePct: cp,
		tradeTime: t,
		state:     state,
		session:   session,
		mode:      mode,
		valid:     true,
	}
}

// ResolveSessionState 根据 Yahoo API 返回的 marketState、资产类型与交易所本地时钟判定五态时段状态。
// 注意：Yahoo 在当地午夜 00:00 后会将尚未进入盘前的休市状态标记为 PREPRE，在盘后结束后标记为 POSTPOST，
// 且对无盘后交易的欧洲/亚太现金指数（INDEX）标记为 POST/PREPRE，需结合交易所本地时间过滤误报。
func ResolveSessionState(marketState string, now time.Time, gmtOffsetSeconds int64) (SessionState, string) {
	return ResolveSessionStateForType(marketState, "", now, gmtOffsetSeconds)
}

func ResolveSessionStateForType(marketState, quoteType string, now time.Time, gmtOffsetSeconds int64) (SessionState, string) {
	raw := strings.ToUpper(strings.TrimSpace(marketState))
	qt := strings.ToUpper(strings.TrimSpace(quoteType))
	isIndex := qt == "INDEX"
	isFuture := qt == "FUTURE"

	if isFuture {
		if raw == "CLOSED" {
			return StateClosed, "休市 (Closed)"
		}
		if raw == "REGULAR" {
			return StateRegular, "24h 实时交易 (Regular)"
		}
		return resolveCommoditySession(now)
	}

	switch raw {
	case "REGULAR":
		return StateRegular, "盘中 (Regular)"
	case "OVERNIGHT":
		return StateOvernight, "夜盘 (Overnight)"
	case "CLOSED", "POSTPOST":
		return StateClosed, "休市 (Closed)"
	case "POST":
		if !isIndex && gmtOffsetSeconds <= 0 {
			return StatePostMarket, "盘后 (Post-Market)"
		}
		return StateClosed, "休市 (Closed)"
	case "PRE":
		if !isIndex && gmtOffsetSeconds <= 0 {
			return StatePreMarket, "盘前 (Pre-Market)"
		}
		if isPreOpenWindow(now, gmtOffsetSeconds) {
			return StatePreMarket, "盘前 (Pre-Market)"
		}
		return StateClosed, "休市 (Closed)"
	case "PREPRE":
		// PREPRE 表示“盘前尚未开始”（如亚太午夜 00:00–09:00 休市期，或美股 00:00–04:00 夜盘期）
		if !isIndex && gmtOffsetSeconds <= 0 {
			return inferSessionByClock(now, gmtOffsetSeconds)
		}
		if isPreOpenWindow(now, gmtOffsetSeconds) {
			return StatePreMarket, "盘前 (Pre-Market)"
		}
		return StateClosed, "休市 (Closed)"
	}
	return inferSessionByClock(now, gmtOffsetSeconds)
}

func isPreOpenWindow(now time.Time, gmtOffsetSeconds int64) bool {
	local := now.UTC().Add(time.Duration(gmtOffsetSeconds) * time.Second)
	wd := local.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	mins := local.Hour()*60 + local.Minute()
	if gmtOffsetSeconds == 8*3600 {
		// A股 / 港股盘前集合竞价窗口: 09:00 - 09:30
		return mins >= 9*60 && mins < 9*60+30
	}
	// 日韩 (UTC+9) 与欧洲 (UTC+1/+2) 盘前窗口: 08:30 - 09:00
	return mins >= 8*60+30 && mins < 9*60
}

func inferSessionByClock(now time.Time, gmtOffsetSeconds int64) (SessionState, string) {
	if gmtOffsetSeconds == 0 {
		gmtOffsetSeconds = -14400
	}
	local := now.UTC().Add(time.Duration(gmtOffsetSeconds) * time.Second)
	wd := local.Weekday()
	mins := local.Hour()*60 + local.Minute()

	// 美区时区（UTC-4 / UTC-5）具备 24×5 夜盘/盘前/盘中/盘后四段结构
	if gmtOffsetSeconds < 0 {
		if wd == time.Saturday || (wd == time.Friday && mins >= 20*60) || (wd == time.Sunday && mins < 20*60) {
			return StateClosed, "周末休市"
		}
		switch {
		case mins >= 4*60 && mins < 9*60+30:
			return StatePreMarket, "盘前 (Pre-Market)"
		case mins >= 9*60+30 && mins < 16*60:
			return StateRegular, "盘中 (Regular)"
		case mins >= 16*60 && mins < 20*60:
			return StatePostMarket, "盘后 (Post-Market)"
		default:
			return StateOvernight, "夜盘 (Overnight)"
		}
	}

	// 亚太及欧洲指数时区（UTC+1 ~ UTC+9）：仅在盘前集合竞价窗口显示盘前太阳，其余非交易时间一律为休市灰点
	if wd == time.Saturday || wd == time.Sunday {
		return StateClosed, "周末休市"
	}
	if isPreOpenWindow(now, gmtOffsetSeconds) {
		return StatePreMarket, "盘前 (Pre-Market)"
	}
	if gmtOffsetSeconds == 8*3600 {
		if mins >= 9*60+30 && mins < 16*60 {
			return StateRegular, "盘中 (Regular)"
		}
		return StateClosed, "休市 (Closed)"
	}
	if mins >= 9*60 && mins < 17*60+35 {
		return StateRegular, "盘中 (Regular)"
	}
	return StateClosed, "休市 (Closed)"
}

func resolveCommoditySession(now time.Time) (SessionState, string) {
	nyZone := time.FixedZone("EDT", -4*3600)
	ny := now.In(nyZone)
	wd := ny.Weekday()
	mins := ny.Hour()*60 + ny.Minute()

	if wd == time.Saturday || (wd == time.Friday && mins >= 17*60) || (wd == time.Sunday && mins < 18*60) {
		return StateClosed, "周末休市"
	}
	if mins >= 17*60 && mins < 18*60 {
		return StateClosed, "盘间结算休市"
	}
	return StateRegular, "24h 实时交易 (Regular)"
}

func resolveCNHKSession(sym string, now time.Time, tradeTime int64) (SessionState, string) {
	cstZone := time.FixedZone("CST", 8*3600)
	local := now.In(cstZone)
	wd := local.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return StateClosed, "周末休市"
	}
	if isTrackedSZETF(sym) {
		mins := local.Hour()*60 + local.Minute()
		switch {
		case mins >= 9*60+15 && mins < 9*60+30:
			return StatePreMarket, "集合竞价 (Pre-Market)"
		case (mins >= 9*60+30 && mins < 11*60+30) || (mins >= 13*60 && mins < 15*60):
			if tradeTime > 0 {
				tLocal := time.Unix(tradeTime, 0).In(cstZone)
				if tLocal.Year() != local.Year() || tLocal.YearDay() != local.YearDay() {
					return StateClosed, "休市或行情日期未更新"
				}
			}
			return StateRegular, "盘中 (Regular)"
		case mins >= 11*60+30 && mins < 13*60:
			return StateClosed, "午间休市 (Lunch Break)"
		default:
			return StateClosed, "休市 (Closed)"
		}
	}
	if tradeTime > 0 {
		tLocal := time.Unix(tradeTime, 0).In(cstZone)
		if tLocal.Year() != local.Year() || tLocal.YearDay() != local.YearDay() {
			if local.Hour()*60+local.Minute() >= 9*60+15 {
				return StateClosed, "休市 (Closed)"
			}
		}
	}
	mins := local.Hour()*60 + local.Minute()
	if sym == "000001.SS" || sym == "399006.SZ" || sym == "399001.SZ" {
		switch {
		case mins >= 9*60+15 && mins < 9*60+30:
			return StatePreMarket, "集合竞价 (Pre-Market)"
		case (mins >= 9*60+30 && mins <= 11*60+31) || (mins >= 13*60 && mins <= 15*60+1):
			return StateRegular, "盘中实时 (Regular)"
		case mins > 11*60+31 && mins < 13*60:
			return StateClosed, "午间休市 (Lunch Break)"
		default:
			return StateClosed, "休市 (Closed)"
		}
	}
	// 港股 (^HSI, ^HSTECH)
	switch {
	case mins >= 9*60 && mins < 9*60+30:
		return StatePreMarket, "开市前时段 (Pre-Market)"
	case (mins >= 9*60+30 && mins <= 12*60+1) || (mins >= 13*60 && mins <= 16*60+10):
		return StateRegular, "盘中实时 (Regular)"
	case mins > 12*60+1 && mins < 13*60:
		return StateClosed, "午间休市 (Lunch Break)"
	default:
		return StateClosed, "休市 (Closed)"
	}
}

func currencyForSymbol(sym string) string {
	switch sym {
	case "000001.SS", "399006.SZ", "399001.SZ", "159632.SZ", "159655.SZ":
		return "CNY"
	case "^HSI", "^HSTECH":
		return "HKD"
	case "^N225":
		return "JPY"
	case "^KS11":
		return "KRW"
	case "^FCHI", "^GDAXI":
		return "EUR"
	default:
		return "USD"
	}
}

func resolveIndexSession(sym string, now time.Time, tradeTime int64) (SessionState, string) {
	switch sym {
	case "000001.SS", "399006.SZ", "399001.SZ", "159632.SZ", "159655.SZ", "^HSI", "^HSTECH":
		return resolveCNHKSession(sym, now, tradeTime)
	case "^N225":
		jstZone := time.FixedZone("JST", 9*3600)
		local := now.In(jstZone)
		wd := local.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			return StateClosed, "周末休市"
		}
		if tradeTime > 0 {
			tLocal := time.Unix(tradeTime, 0).In(jstZone)
			if (tLocal.Year() != local.Year() || tLocal.YearDay() != local.YearDay()) && local.Hour()*60+local.Minute() >= 9*60 {
				return StateClosed, "休市 (Closed)"
			}
		}
		mins := local.Hour()*60 + local.Minute()
		switch {
		case mins >= 8*60+30 && mins < 9*60:
			return StatePreMarket, "集合竞价 (Pre-Market)"
		case mins >= 9*60 && mins <= 11*60+31:
			return StateRegular, "前场交易 (Regular)"
		case mins > 11*60+31 && mins < 12*60+30:
			return StateClosed, "午间休市 (Lunch Break)"
		case mins >= 12*60+30 && mins <= 15*60+31:
			return StateRegular, "后场交易 (Regular)"
		default:
			return StateClosed, "休市 (Closed)"
		}
	case "^KS11":
		kstZone := time.FixedZone("KST", 9*3600)
		local := now.In(kstZone)
		wd := local.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			return StateClosed, "周末休市"
		}
		if tradeTime > 0 {
			tLocal := time.Unix(tradeTime, 0).In(kstZone)
			if (tLocal.Year() != local.Year() || tLocal.YearDay() != local.YearDay()) && local.Hour()*60+local.Minute() >= 9*60 {
				return StateClosed, "休市 (Closed)"
			}
		}
		mins := local.Hour()*60 + local.Minute()
		switch {
		case mins >= 8*60+30 && mins < 9*60:
			return StatePreMarket, "集合竞价 (Pre-Market)"
		case mins >= 9*60 && mins <= 15*60+31:
			return StateRegular, "盘中 (Regular)"
		default:
			return StateClosed, "休市 (Closed)"
		}
	case "^FCHI", "^GDAXI":
		cetZone := time.FixedZone("CET", 3600)
		local := now.In(cetZone)
		wd := local.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			return StateClosed, "周末休市"
		}
		mins := local.Hour()*60 + local.Minute()
		switch {
		case mins >= 8*60+30 && mins < 9*60:
			return StatePreMarket, "盘前 (Pre-Market)"
		case mins >= 9*60 && mins <= 17*60+36:
			return StateRegular, "盘中 (Regular)"
		default:
			return StateClosed, "休市 (Closed)"
		}
	default:
		return StateRegular, "实时行情 (Regular)"
	}
}

var tencentSymbolMap = map[string]string{
	"000001.SS": "sh000001",
	"399006.SZ": "sz399006",
	"399001.SZ": "sz399001",
	"159632.SZ": "sz159632",
	"159655.SZ": "sz159655",
	"^HSI":      "r_hkHSI",
	"^HSTECH":   "r_hkHSTECH",
	"GC=F":      "hf_GC",
	"BZ=F":      "hf_OIL",
	"^N225":     "gzN225",
	"^KS11":     "gzKS11",
	"^FCHI":     "gzFCHI",
	"^GDAXI":    "gzGDAXI",
}

var tencentVarToSymbol = map[string]string{
	"v_sh000001":   "000001.SS",
	"v_sz399006":   "399006.SZ",
	"v_sz399001":   "399001.SZ",
	"v_sz159632":   "159632.SZ",
	"v_sz159655":   "159655.SZ",
	"v_r_hkHSI":    "^HSI",
	"v_hkHSI":      "^HSI",
	"v_r_hkHSTECH": "^HSTECH",
	"v_hkHSTECH":   "^HSTECH",
	"v_hf_GC":      "GC=F",
	"v_hf_OIL":     "BZ=F",
	"v_gzN225":     "^N225",
	"v_N225":       "^N225",
	"v_gzKS11":     "^KS11",
	"v_KS11":       "^KS11",
	"v_gzFCHI":     "^FCHI",
	"v_FCHI":       "^FCHI",
	"v_gzGDAXI":    "^GDAXI",
	"v_GDAXI":      "^GDAXI",
}

var commodityDescriptions = map[string]string{
	"GC=F":      "COMEX 黄金期货 (Gold Futures)",
	"BZ=F":      "ICE 布伦特原油 (Brent Crude)",
	"000001.SS": "上证综合指数 (SSE Composite)",
	"399006.SZ": "创业板指数 (ChiNext)",
	"399001.SZ": "深证成份指数 (Shenzhen Component)",
	"^NDX":      "纳斯达克100指数 (Nasdaq-100)",
	"^GSPC":     "标普500指数 (S&P 500)",
	"159632.SZ": "纳斯达克ETF华安 (QDII)",
	"159655.SZ": "标普500ETF华夏 (QDII)",
	"^HSI":      "恒生指数 (Hang Seng Index)",
	"^HSTECH":   "恒生科技指数 (Hang Seng TECH Index)",
	"^N225":     "日经225指数 (Nikkei 225)",
	"^KS11":     "韩国综合股价指数 (KOSPI)",
	"^FCHI":     "法国CAC40指数 (CAC 40)",
	"^GDAXI":    "德国DAX指数 (DAX Performance)",
}

func TencentQueryForSymbols(symbols []string) (string, bool) {
	if len(symbols) == 0 {
		return "", false
	}
	var q []string
	for _, s := range symbols {
		t, ok := tencentSymbolMap[s]
		if !ok {
			t, ok = watchlist.TencentSymbol(s)
		}
		if !ok {
			return "", false
		}
		q = append(q, t)
	}
	return strings.Join(q, ","), true
}

// ParseTencentQuoteBatch 解析腾讯行情接口 (qt.gtimg.cn) 返回的行情数据。
func ParseTencentQuoteBatch(expectedSymbols []string, body []byte, rttMs float64, now time.Time) (map[string]SymbolResult, error) {
	if customSymbols(expectedSymbols) && !(len(expectedSymbols) == 2 && isTrackedSZETF(expectedSymbols[0]) && isTrackedSZETF(expectedSymbols[1])) {
		return parseCustomQuoteBatch(expectedSymbols, body, rttMs, now)
	}
	if len(body) > MaxBody {
		return nil, fmt.Errorf("响应超过 %d 字节上限", MaxBody)
	}
	text := string(body)
	lines := strings.Split(text, ";")
	out := make(map[string]SymbolResult, len(expectedSymbols))
	cstZone := time.FixedZone("CST", 8*3600)
	var anySuccess bool

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		varName := strings.TrimSpace(line[:eq])
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), "\"\r\n ")
		if val == "" {
			continue
		}

		sym, ok := tencentVarToSymbol[varName]
		if !ok {
			continue
		}

		if strings.HasPrefix(varName, "v_hf_") {
			// 期货格式 (逗号分隔): 4019.64,-0.46,4019.70,4019.90,4033.50,4016.70,11:16:58,4038.40,4032.80,0,1,1,2026-10-27,纽约黄金
			parts := strings.Split(val, ",")
			if len(parts) < 13 {
				out[sym] = SymbolResult{Err: "期货行情字段不足"}
				continue
			}
			price, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			prevClose, err2 := strconv.ParseFloat(strings.TrimSpace(parts[7]), 64)
			if err1 != nil || err2 != nil || price <= 0 || prevClose <= 0 {
				out[sym] = SymbolResult{Err: "期货价格解析错误"}
				continue
			}
			changeAbs := price - prevClose
			changePct := changeAbs / prevClose * 100.0

			var tradeTime int64
			dateStr := strings.TrimSpace(parts[12])
			timeStr := strings.TrimSpace(parts[6])
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", dateStr+" "+timeStr, cstZone); err == nil {
				tradeTime = t.Unix()
			}

			state, session := resolveCommoditySession(now)
			desc := commodityDescriptions[sym]
			if desc == "" {
				desc = AliasFor(sym)
			}

			q := Quote{
				Symbol:        sym,
				Alias:         AliasFor(sym),
				Price:         price,
				ChangePercent: changePct,
				ChangeAbs:     changeAbs,
				RegularPrice:  prevClose,
				Currency:      "USD",
				QuoteType:     "FUTURE",
				MarketState:   "REGULAR",
				State:         state,
				TradeTime:     tradeTime,
				QuoteSource:   "Tencent Real Time",
				UpdateMode:    "commodity_realtime",
				Session:       session,
				Description:   desc,
				RTTMs:         rttMs,
			}
			if err := ValidateQuote(q); err != nil {
				out[sym] = SymbolResult{Err: err.Error()}
			} else {
				anySuccess = true
				qCopy := q
				out[sym] = SymbolResult{Quote: &qCopy}
			}
		} else if strings.HasPrefix(varName, "v_gz") || strings.HasPrefix(val, "N225~") || strings.HasPrefix(val, "KS11~") || strings.HasPrefix(val, "FCHI~") || strings.HasPrefix(val, "GDAXI~") {
			// 全球/亚太/欧洲指数格式 (~分隔): N225~日经225指数~2026-10-27 14:25:26~63254.28~1064.63~1.71~AP~
			parts := strings.Split(val, "~")
			if len(parts) < 6 {
				out[sym] = SymbolResult{Err: "全球指数行情字段不足"}
				continue
			}
			if strings.TrimSpace(parts[0]) != strings.TrimPrefix(sym, "^") {
				out[sym] = SymbolResult{Err: "全球指数标识不匹配"}
				continue
			}
			price, err1 := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			changeAbs, err2 := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
			if err1 != nil || err2 != nil || price <= 0 {
				out[sym] = SymbolResult{Err: "全球指数价格解析错误"}
				continue
			}
			prevClose := price - changeAbs
			if prevClose <= 0 {
				prevClose = price
			}
			changePct := changeAbs / prevClose * 100.0
			if pctRaw, err3 := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64); err3 == nil {
				if isClose(pctRaw, changePct, 1e-3, 0.08) {
					changePct = pctRaw
				}
			}

			var tradeTime int64
			tsRaw := strings.TrimSpace(parts[2])
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", tsRaw, cstZone); err == nil {
				tradeTime = t.Unix()
			}
			if tradeTime == 0 || tradeTime > now.Add(30*time.Second).Unix() {
				out[sym] = SymbolResult{Err: "全球指数行情时间无效"}
				continue
			}

			cur := currencyForSymbol(sym)
			state, session := resolveIndexSession(sym, now, tradeTime)
			desc := commodityDescriptions[sym]
			if desc == "" {
				desc = AliasFor(sym)
			}

			q := Quote{
				Symbol:        sym,
				Alias:         AliasFor(sym),
				Price:         price,
				ChangePercent: changePct,
				ChangeAbs:     changeAbs,
				RegularPrice:  prevClose,
				Currency:      cur,
				QuoteType:     "INDEX",
				MarketState:   "REGULAR",
				State:         state,
				TradeTime:     tradeTime,
				QuoteSource:   "Tencent Finance（备用指数）",
				UpdateMode:    "global_index_quote",
				Session:       session,
				Description:   desc,
				RTTMs:         rttMs,
			}
			if err := ValidateQuote(q); err != nil {
				out[sym] = SymbolResult{Err: err.Error()}
			} else {
				anySuccess = true
				qCopy := q
				out[sym] = SymbolResult{Quote: &qCopy}
			}
		} else {
			// A股/港股格式 (~分隔): 1~上证指数~000001~3691.37~3697.52~...~20261027111742~-6.15~-0.17~...
			parts := strings.Split(val, "~")
			if len(parts) < 33 {
				out[sym] = SymbolResult{Err: "指数行情字段不足"}
				continue
			}
			price, err1 := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			prevClose, err2 := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
			if err1 != nil || err2 != nil || price <= 0 || prevClose <= 0 {
				out[sym] = SymbolResult{Err: "指数价格解析错误"}
				continue
			}
			changeAbs := price - prevClose
			changePct := changeAbs / prevClose * 100.0

			var tradeTime int64
			tsRaw := strings.TrimSpace(parts[30])
			if t, err := time.ParseInLocation("20060102150405", tsRaw, cstZone); err == nil {
				tradeTime = t.Unix()
			} else if t, err := time.ParseInLocation("2006/01/02 15:04:05", tsRaw, cstZone); err == nil {
				tradeTime = t.Unix()
			}
			if isTrackedSZETF(sym) {
				if strings.TrimSpace(parts[2]) != strings.TrimSuffix(sym, ".SZ") {
					out[sym] = SymbolResult{Err: "ETF 行情代码不匹配"}
					continue
				}
				if tradeTime == 0 || tradeTime > now.Add(30*time.Second).Unix() {
					out[sym] = SymbolResult{Err: "ETF 行情时间无效"}
					continue
				}
			}

			cur := currencyForSymbol(sym)
			state, session := resolveIndexSession(sym, now, tradeTime)
			quoteType, source, updateMode, marketState := "INDEX", "Tencent Real Time", "cnhk_realtime", "REGULAR"
			if isTrackedSZETF(sym) {
				quoteType, source, updateMode = "ETF", "Tencent Finance", "sz_etf_quote"
				switch state {
				case StatePreMarket:
					marketState = "PRE"
				case StateClosed:
					marketState = "CLOSED"
				}
			}
			desc := commodityDescriptions[sym]
			if desc == "" {
				desc = AliasFor(sym)
			}

			q := Quote{
				Symbol:        sym,
				Alias:         AliasFor(sym),
				Price:         price,
				ChangePercent: changePct,
				ChangeAbs:     changeAbs,
				RegularPrice:  prevClose,
				Currency:      cur,
				QuoteType:     quoteType,
				MarketState:   marketState,
				State:         state,
				TradeTime:     tradeTime,
				QuoteSource:   source,
				UpdateMode:    updateMode,
				Session:       session,
				Description:   desc,
				RTTMs:         rttMs,
			}
			if quoteType == "INDEX" && len(parts) > 46 {
				code := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSuffix(sym, ".SS"), ".SZ"), "^")
				if strings.TrimSpace(parts[2]) == code {
					q.setTencentIndexValuation(parts[39], parts[46])
				}
			}
			if err := ValidateQuote(q); err != nil {
				out[sym] = SymbolResult{Err: err.Error()}
			} else {
				anySuccess = true
				qCopy := q
				out[sym] = SymbolResult{Quote: &qCopy}
			}
		}
	}

	for _, sym := range expectedSymbols {
		if _, ok := out[sym]; !ok {
			out[sym] = SymbolResult{Err: "缺少标的响应"}
		}
	}
	if !anySuccess {
		return out, fmt.Errorf("全部标的解析失败")
	}
	return out, nil
}

// DetectSession 根据交易所时区偏移返回当前时段文本描述。
func DetectSession(now time.Time, gmtOffsetSeconds int64) string {
	_, label := inferSessionByClock(now, gmtOffsetSeconds)
	return label
}

// ParseYahooQuoteBatch 解析 /v7/finance/quote 返回的多市场行情批次。
func ParseYahooQuoteBatch(body []byte, rttMs float64, now time.Time) (map[string]SymbolResult, error) {
	return ParseYahooQuoteBatchForSymbols(ActiveSymbols(), body, rttMs, now)
}

// ParseYahooQuoteBatchForSymbols 解析指定标的列表的 /v7/finance/quote 行情批次。
func ParseYahooQuoteBatchForSymbols(expectedSymbols []string, body []byte, rttMs float64, now time.Time) (map[string]SymbolResult, error) {
	if len(body) > MaxBody {
		return nil, fmt.Errorf("响应超过 %d 字节上限", MaxBody)
	}
	var resp yahooQuoteBatchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if len(resp.QuoteResponse.Result) == 0 {
		return nil, fmt.Errorf("缺少行情结果")
	}

	out := make(map[string]SymbolResult, len(expectedSymbols))
	expected := make(map[string]bool, len(expectedSymbols))
	for _, sym := range expectedSymbols {
		expected[sym] = true
	}
	for _, item := range resp.QuoteResponse.Result {
		item.Symbol = normalizeSymbol(item.Symbol)
		if !expected[item.Symbol] {
			continue
		}
		if _, duplicate := out[item.Symbol]; duplicate {
			out[item.Symbol] = SymbolResult{Err: "重复的标的行情"}
			continue
		}
		q, err := parseYahooQuoteItem(item, rttMs, now)
		if err != nil {
			out[item.Symbol] = SymbolResult{Err: err.Error()}
		} else {
			qCopy := q
			out[item.Symbol] = SymbolResult{Quote: &qCopy}
		}
	}
	var anySuccess bool
	for _, sym := range expectedSymbols {
		if _, ok := out[sym]; !ok {
			out[sym] = SymbolResult{Err: "缺少标的响应"}
		}
		if out[sym].Quote != nil {
			anySuccess = true
		}
	}
	if !anySuccess {
		return out, fmt.Errorf("全部标的解析失败")
	}
	return out, nil
}

func parseYahooQuoteItem(item yahooQuoteItem, rttMs float64, now time.Time) (Quote, error) {
	cur := strings.ToUpper(strings.TrimSpace(item.Currency))
	qt := strings.ToUpper(strings.TrimSpace(item.QuoteType))
	if !allowedCurrencies[cur] || !allowedQuoteTypes[qt] {
		return Quote{}, fmt.Errorf("不支持的类型或币种 (%s/%s)", item.QuoteType, item.Currency)
	}
	desc := strings.TrimSpace(item.LongName)
	if desc == "" {
		desc = strings.TrimSpace(item.ShortName)
	}
	if desc == "" {
		desc = AliasFor(item.Symbol)
	}
	var regPrice float64
	if item.RegularMarketPrice != nil {
		regPrice = *item.RegularMarketPrice
	}

	reg := makeCandidate(item.RegularMarketPrice, item.RegularMarketChange, item.RegularMarketChangePercent, item.RegularMarketTime, StateRegular, "盘中 (Regular)", "regular_realtime")
	post := makeCandidate(item.PostMarketPrice, item.PostMarketChange, item.PostMarketChangePercent, item.PostMarketTime, StatePostMarket, "盘后 (Post-Market)", "post_market_realtime")
	ovn := makeCandidate(item.OvernightMarketPrice, item.OvernightMarketChange, item.OvernightMarketChangePercent, item.OvernightMarketTime, StateOvernight, "夜盘 (Overnight BOATS)", "overnight_boats_realtime")
	pre := makeCandidate(item.PreMarketPrice, item.PreMarketChange, item.PreMarketChangePercent, item.PreMarketTime, StatePreMarket, "盘前 (Pre-Market)", "pre_market_realtime")

	rawState := strings.ToUpper(strings.TrimSpace(item.MarketState))
	var chosen sessionCandidate

	if rawState == "REGULAR" && reg.valid {
		chosen = reg
	} else {
		for _, c := range []sessionCandidate{ovn, pre, post, reg} {
			if !c.valid {
				continue
			}
			if !chosen.valid || (c.tradeTime > 0 && c.tradeTime > chosen.tradeTime) {
				chosen = c
			}
		}
		if chosen.tradeTime == 0 {
			switch {
			case rawState == "OVERNIGHT" && ovn.valid:
				chosen = ovn
			case strings.HasPrefix(rawState, "PRE") && pre.valid:
				chosen = pre
			case strings.HasPrefix(rawState, "POST") && post.valid:
				chosen = post
			case reg.valid:
				chosen = reg
			}
		}
	}

	if !chosen.valid {
		return Quote{}, fmt.Errorf("缺少有效价格字段")
	}

	gmtSec := item.GMTOffsetMilliseconds / 1000
	resolvedState, resolvedLabel := ResolveSessionStateForType(item.MarketState, qt, now, gmtSec)
	if strings.TrimSpace(item.MarketState) == "" && chosen.state != "" {
		resolvedState = chosen.state
		resolvedLabel = chosen.session
	} else if resolvedState == StateOvernight && chosen.mode == "overnight_boats_realtime" {
		resolvedLabel = "夜盘 (Overnight BOATS)"
	}

	src := strings.TrimSpace(item.QuoteSourceName)
	if src == "" {
		src = "Yahoo Finance"
	}

	q := Quote{
		Symbol:        item.Symbol,
		Alias:         quoteAlias(item.Symbol, desc),
		Price:         chosen.price,
		ChangePercent: chosen.changePct,
		ChangeAbs:     chosen.changeAbs,
		RegularPrice:  regPrice,
		Currency:      cur,
		QuoteType:     qt,
		MarketState:   rawState,
		State:         resolvedState,
		TradeTime:     chosen.tradeTime,
		QuoteSource:   src,
		UpdateMode:    chosen.mode,
		Session:       resolvedLabel,
		Description:   desc,
		RTTMs:         rttMs,
	}
	if err := ValidateQuote(q); err != nil {
		return Quote{}, err
	}
	q.setYahooCapitalization(item.MarketCap, regPrice)
	return q, nil
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta yahooMeta `json:"meta"`
		} `json:"result"`
	} `json:"chart"`
}

type yahooMeta struct {
	Currency                   string          `json:"currency"`
	Symbol                     string          `json:"symbol"`
	InstrumentType             string          `json:"instrumentType"`
	MarketState                string          `json:"marketState"`
	ShortName                  string          `json:"shortName"`
	LongName                   string          `json:"longName"`
	GMTOffset                  int64           `json:"gmtoffset"`
	RegularMarketTime          int64           `json:"regularMarketTime"`
	RegularMarketPrice         *float64        `json:"regularMarketPrice"`
	RegularMarketChangePercent *float64        `json:"regularMarketChangePercent"`
	ChartPreviousClose         *float64        `json:"chartPreviousClose"`
	PreviousClose              *float64        `json:"previousClose"`
	FulldayPrice               *float64        `json:"fulldayPrice"`
	FulldayChange              *float64        `json:"fulldayChange"`
	FulldayChangePercent       *float64        `json:"fulldayChangePercent"`
	MarketCap                  json.RawMessage `json:"marketCap"`
}

// ParseYahooChart 解析单标的 /v8/finance/chart 备用接口行情，兼容 ETF、正股与全球指数。
func ParseYahooChart(expectedSymbol string, body []byte, rttMs float64, now time.Time) (Quote, error) {
	if len(body) > MaxBody {
		return Quote{}, fmt.Errorf("响应超过 %d 字节上限", MaxBody)
	}
	var resp yahooChartResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Quote{}, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if len(resp.Chart.Result) != 1 {
		return Quote{}, fmt.Errorf("缺少或重复行情结果")
	}
	m := resp.Chart.Result[0].Meta
	sym := normalizeSymbol(m.Symbol)
	cur := strings.ToUpper(strings.TrimSpace(m.Currency))
	inst := strings.ToUpper(strings.TrimSpace(m.InstrumentType))
	if sym != expectedSymbol || !allowedCurrencies[cur] || !allowedQuoteTypes[inst] {
		return Quote{}, fmt.Errorf("标的、类型或币种不匹配 (%s/%s/%s)", m.Symbol, m.InstrumentType, m.Currency)
	}
	desc := strings.TrimSpace(m.LongName)
	if desc == "" {
		desc = strings.TrimSpace(m.ShortName)
	}
	if desc == "" {
		desc = AliasFor(expectedSymbol)
	}
	var regPrice float64
	if m.RegularMarketPrice != nil {
		regPrice = *m.RegularMarketPrice
	}
	state, session := ResolveSessionStateForType(m.MarketState, inst, now, m.GMTOffset)

	var price, changeAbs, changePct float64
	mode := "24h_fullday_realtime"
	if m.FulldayPrice != nil && m.FulldayChange != nil && m.FulldayChangePercent != nil && *m.FulldayPrice > 0 {
		price = *m.FulldayPrice
		changeAbs = *m.FulldayChange
		changePct = *m.FulldayChangePercent
		if state != StateRegular && regPrice > 0 && m.ChartPreviousClose != nil && *m.ChartPreviousClose > 0 {
			rawPrev := price - changeAbs
			rawCalc := changeAbs / rawPrev * 100.0
			if isClose(rawCalc, changePct, 1e-5, 0.05) && math.Abs(*m.ChartPreviousClose-regPrice) > 1e-4 && math.Abs(rawPrev-*m.ChartPreviousClose) < 0.05 {
				changeAbs = price - regPrice
				changePct = changeAbs / regPrice * 100.0
			}
		}
	} else if m.RegularMarketPrice != nil && *m.RegularMarketPrice > 0 {
		price = *m.RegularMarketPrice
		prev := 0.0
		if m.ChartPreviousClose != nil && *m.ChartPreviousClose > 0 {
			prev = *m.ChartPreviousClose
		} else if m.PreviousClose != nil && *m.PreviousClose > 0 {
			prev = *m.PreviousClose
		}
		if prev <= 0 {
			return Quote{}, fmt.Errorf("缺少有效昨收基准价")
		}
		changeAbs = price - prev
		if m.RegularMarketChangePercent != nil {
			changePct = *m.RegularMarketChangePercent
		} else {
			changePct = changeAbs / prev * 100.0
		}
		mode = "regular_realtime"
	} else {
		return Quote{}, fmt.Errorf("缺少有效价格字段")
	}

	q := Quote{
		Symbol:        expectedSymbol,
		Alias:         quoteAlias(expectedSymbol, desc),
		Price:         price,
		ChangePercent: changePct,
		ChangeAbs:     changeAbs,
		RegularPrice:  regPrice,
		Currency:      cur,
		QuoteType:     inst,
		MarketState:   strings.ToUpper(strings.TrimSpace(m.MarketState)),
		State:         state,
		TradeTime:     m.RegularMarketTime,
		QuoteSource:   "Yahoo Chart Fallback",
		UpdateMode:    mode,
		Session:       session,
		Description:   desc,
		RTTMs:         rttMs,
	}
	if err := ValidateQuote(q); err != nil {
		return Quote{}, err
	}
	q.setYahooCapitalization(m.MarketCap, regPrice)
	return q, nil
}

type View struct {
	Symbol      string       `json:"symbol"`
	Alias       string       `json:"alias"`
	State       SessionState `json:"session_state"`
	Quote       *Quote       `json:"quote,omitempty"`
	Healthy     bool         `json:"healthy"`
	Reason      string       `json:"reason,omitempty"`
	ReceivedAge *float64     `json:"received_age_seconds,omitempty"`
}

func (v View) DisplayName() string {
	if watchlist.IsSymbol(v.Symbol) && v.Quote != nil {
		return v.Quote.DisplayName()
	}
	if isTrackedSZETF(v.Symbol) {
		return AliasFor(v.Symbol)
	}
	if v.Alias != "" {
		return v.Alias
	}
	if v.Quote != nil && v.Quote.Alias != "" {
		return v.Quote.Alias
	}
	return AliasFor(v.Symbol)
}

func (v View) EffectiveState() SessionState {
	if v.Quote != nil && v.Quote.State != "" {
		return v.Quote.State
	}
	if v.State != "" {
		return v.State
	}
	return StateClosed
}

func (v View) Tooltip() string {
	status := "实时数据正常"
	if isTrackedSZETF(v.Symbol) || watchlist.IsMainlandETF(v.Symbol) || isGlobalIndex(v.Symbol) {
		status = "行情数据正常"
	}
	if v.Quote != nil {
		if tag := v.Quote.FeedTag(time.Now()); tag != "" {
			status += "（" + tag + "）"
		}
	}
	if !v.Healthy {
		status = "数据异常 — " + v.Reason
	}
	session := "等待数据"
	stateBadge := v.EffectiveState().Badge()
	if v.Quote != nil {
		session = v.Quote.SessionText()
	}
	fetched := "尚未成功读取"
	if v.ReceivedAge != nil {
		fetched = fmt.Sprintf("%.1fs 前轮询", *v.ReceivedAge)
	}
	name := v.DisplayName()
	if name != v.Symbol && !isTrackedSZETF(v.Symbol) {
		name = fmt.Sprintf("%s (%s)", name, v.Symbol)
	}
	if v.Quote != nil {
		tradeInfo := ""
		if isGlobalIndex(v.Symbol) && v.Quote.TradeTime == 0 {
			tradeInfo = "；源未提供逐笔时间（轮询时间不代表行情时间）"
			if v.Quote.QuoteDate != "" {
				tradeInfo = "；源报价日期 " + v.Quote.QuoteDate + "（纽约时间，仅日期，无逐笔时间）"
			}
		}
		if v.Quote.TradeTime > 0 {
			tradeAge := time.Now().Unix() - v.Quote.TradeTime
			if tradeAge < 0 {
				tradeAge = 0
			}
			if isTrackedSZETF(v.Symbol) {
				local := time.Unix(v.Quote.TradeTime, 0).In(time.FixedZone("CST", 8*3600))
				tradeInfo = fmt.Sprintf("；行情时间 %s（北京时间）", local.Format("2006-01-02 15:04:05"))
			}
			if tradeAge < 120 {
				if isTrackedSZETF(v.Symbol) {
					tradeInfo += fmt.Sprintf("；行情时间距今 %ds", tradeAge)
				} else {
					tradeInfo = fmt.Sprintf("；最近成交 %ds 前", tradeAge)
				}
			} else if tradeAge < 7200 {
				if isTrackedSZETF(v.Symbol) {
					tradeInfo += fmt.Sprintf("；行情时间距今 %dm%ds", tradeAge/60, tradeAge%60)
				} else {
					tradeInfo = fmt.Sprintf("；最近成交 %dm%ds 前", tradeAge/60, tradeAge%60)
				}
			}
		}
		srcInfo := ""
		if v.Quote.QuoteSource != "" {
			srcInfo = "；数据源: " + v.Quote.QuoteSource
		}
		if isGlobalIndex(v.Symbol) && v.Quote.UpdateMode == "streaming" {
			srcInfo += "；源标记实时"
		}
		decimals := PriceDecimals(v.Symbol)
		description := v.Quote.Description
		if isTrackedSZETF(v.Symbol) {
			if description == "" {
				description = commodityDescriptions[v.Symbol]
			}
		} else {
			ticker := TickerFor(v.Symbol)
			if v.Quote.ProviderSymbol != "" {
				ticker = v.Quote.ProviderSymbol
			}
			description = fmt.Sprintf("%s (%s)", description, ticker)
		}
		return fmt.Sprintf("%s %s: %.*f (%+.2f%%, 涨跌 %+.*f, 常规收盘 %.*f) | %s\n  %s；时段: %s %s%s%s；%s",
			stateBadge, name, decimals, v.Quote.Price, v.Quote.ChangePercent, decimals, v.Quote.ChangeAbs, decimals, v.Quote.RegularPrice, status,
			description, stateBadge, session, srcInfo, tradeInfo, fetched) + v.Quote.capitalizationTooltip() + v.Quote.valuationTooltip()
	}
	description := TickerFor(v.Symbol)
	if isTrackedSZETF(v.Symbol) {
		description = commodityDescriptions[v.Symbol]
	}
	return fmt.Sprintf("%s %s: %s\n  %s；%s；%s",
		stateBadge, name, status, description, session, fetched)
}

type feedState struct {
	quote      *Quote
	receivedAt time.Time
	err        string
	failures   int
}

type Tracker struct {
	groupID string
	symbols []string
	feeds   map[string]*feedState
}

func NewTracker() *Tracker {
	g := ActiveGroup()
	t := &Tracker{
		groupID: g.ID,
		symbols: append([]string(nil), g.Symbols...),
		feeds:   make(map[string]*feedState, len(g.Symbols)),
	}
	t.Reset()
	return t
}

func (t *Tracker) SetGroup(groupID string) {
	g := GroupByID(groupID)
	t.groupID = g.ID
	t.symbols = append([]string(nil), g.Symbols...)
	t.feeds = make(map[string]*feedState, len(g.Symbols))
	t.Reset()
}

func (t *Tracker) Reset() {
	if len(t.symbols) == 0 {
		t.symbols = ActiveSymbols()
	}
	t.feeds = make(map[string]*feedState, len(t.symbols))
	for _, sym := range t.symbols {
		t.feeds[sym] = &feedState{err: "等待 " + AliasFor(sym) + " 实时行情"}
	}
}

func (t *Tracker) ApplyBatch(batch map[string]SymbolResult, now time.Time) {
	for _, sym := range t.symbols {
		res, ok := batch[sym]
		if !ok {
			t.Fail(sym, "缺少标的响应")
			continue
		}
		if res.Quote != nil {
			q := *res.Quote
			if q.Alias == "" {
				q.Alias = AliasFor(sym)
			}
			st := t.feeds[sym]
			if st == nil {
				st = &feedState{}
				t.feeds[sym] = st
			}
			st.quote = &q
			st.receivedAt = now
			st.err = ""
			st.failures = 0
		} else {
			t.Fail(sym, res.Err)
		}
	}
}

func (t *Tracker) Fail(sym, reason string) {
	if st, ok := t.feeds[sym]; ok {
		if len(reason) > 180 {
			reason = reason[:180]
		}
		st.err = reason
		st.failures++
	}
}

func (t *Tracker) FailAll(reason string) {
	for _, sym := range t.symbols {
		t.Fail(sym, reason)
	}
}

func (t *Tracker) Views(now time.Time, intervalSeconds int) []View {
	if intervalSeconds < 2 {
		intervalSeconds = 2
	}
	maxAge := math.Max(float64(6*intervalSeconds)+float64(TimeoutMillis)/1000.0, 30.0)
	out := make([]View, 0, len(t.symbols))
	for _, sym := range t.symbols {
		st := t.feeds[sym]
		if st == nil {
			st = &feedState{err: "等待 " + AliasFor(sym) + " 实时行情"}
		}
		var agePtr *float64
		reason := st.err
		healthy := false
		state := StateClosed
		if st.quote != nil {
			if st.quote.State != "" {
				state = st.quote.State
			} else {
				state = StateRegular
			}
		}
		if st.quote != nil && !st.receivedAt.IsZero() {
			age := math.Max(0, now.Sub(st.receivedAt).Seconds())
			agePtr = &age
			if age > maxAge {
				if reason == "" {
					reason = "超过预期刷新时间，数据源失联"
				}
				healthy = false
			} else if st.failures < 3 {
				healthy = true
				reason = ""
			}
		}
		var qCopy *Quote
		if st.quote != nil {
			c := *st.quote
			qCopy = &c
		}
		out = append(out, View{
			Symbol:      sym,
			Alias:       AliasFor(sym),
			State:       state,
			Quote:       qCopy,
			Healthy:     healthy,
			Reason:      reason,
			ReceivedAge: agePtr,
		})
	}
	return out
}
