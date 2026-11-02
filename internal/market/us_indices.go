package market

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// newyork.tzif is the America/New_York entry from Go's IANA zoneinfo.zip.
// Embedding only this zone keeps standalone Windows builds DST-aware.
//
//go:embed newyork.tzif
var newYorkTZ []byte

var newYorkZone = func() *time.Location {
	zone, err := time.LoadLocationFromTZData("America/New_York", newYorkTZ)
	if err != nil {
		panic("invalid embedded New York time zone: " + err.Error())
	}
	return zone
}()

var usCashTickers = map[string]string{"^NDX": ".NDX", "^GSPC": ".SPX"}

func usCashIndexSymbols(symbols []string) bool {
	if len(symbols) == 0 {
		return false
	}
	for _, symbol := range symbols {
		if usCashTickers[symbol] == "" {
			return false
		}
	}
	return true
}

type cnbcIndexItem struct {
	Symbol      string          `json:"symbol"`
	Code        json.RawMessage `json:"code"`
	Name        string          `json:"name"`
	Price       json.RawMessage `json:"last"`
	Change      json.RawMessage `json:"change"`
	Percent     json.RawMessage `json:"change_pct"`
	LastTime    string          `json:"last_time"`
	RegularTime string          `json:"reg_last_time"`
	Kind        string          `json:"assetType"`
	Currency    string          `json:"currencyCode"`
	Source      string          `json:"source"`
	Realtime    json.RawMessage `json:"realTime"`
	FeedSymbol  string          `json:"feedSymbol"`
	MainStatus  string          `json:"mainmktstatus"`
}

func cnbcScalar(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(string(raw))
}

func cnbcNumber(raw json.RawMessage) (float64, error) {
	text := strings.ReplaceAll(strings.ReplaceAll(cnbcScalar(raw), ",", ""), "−", "-")
	value, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("实时指数数值无效")
	}
	return value, nil
}

// last_time is the quote timestamp. last_time_msec, cachedTime and responseTime
// can change on every HTTP request even while the market is closed; never use
// those fields to make a closing quote appear newly traded.
func cnbcTradeTime(raw string, now time.Time) (int64, string, error) {
	text := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if at, err := time.Parse(layout, text); err == nil {
			if at.After(now.Add(30 * time.Second)) {
				return 0, "", fmt.Errorf("指数报价时间在未来")
			}
			return at.Unix(), at.In(newYorkZone).Format("2006-01-02"), nil
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05"} {
		if at, err := time.ParseInLocation(layout, text, newYorkZone); err == nil {
			if at.After(now.Add(30 * time.Second)) {
				return 0, "", fmt.Errorf("指数报价时间在未来")
			}
			return at.Unix(), at.Format("2006-01-02"), nil
		}
	}
	at, err := time.ParseInLocation("2006-01-02", text, newYorkZone)
	if err != nil || at.Format("2006-01-02") > now.In(newYorkZone).Format("2006-01-02") {
		return 0, "", fmt.Errorf("指数源报价日期无效")
	}
	return 0, at.Format("2006-01-02"), nil
}

func usCashSession(now time.Time, mainStatus string) (SessionState, string) {
	local := now.In(newYorkZone)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return StateClosed, "周末休市（保留指数收盘值）"
	}
	minutes := local.Hour()*60 + local.Minute()
	if minutes >= 4*60 && minutes < 9*60+30 {
		return StatePreMarket, "盘前（指数未开盘，保留收盘值）"
	}
	if minutes < 9*60+30 || minutes >= 16*60 || strings.EqualFold(mainStatus, "CLOSE") || strings.EqualFold(mainStatus, "CLOSED") || strings.EqualFold(mainStatus, "HALTED") {
		return StateClosed, "休市（保留指数收盘值）"
	}
	return StateRegular, "盘中 (Regular)"
}

func parseCNBCIndex(symbol string, item cnbcIndexItem, rtt float64, now time.Time) (Quote, error) {
	if item.Symbol != usCashTickers[symbol] || cnbcScalar(item.Code) != "0" || !strings.EqualFold(item.Kind, "INDEX") || item.Currency != "USD" || !strings.EqualFold(item.Source, "Exchange") ||
		(item.FeedSymbol != "" && item.FeedSymbol != item.Symbol) {
		return Quote{}, fmt.Errorf("实时指数身份、品类或币种不匹配")
	}
	if !strings.EqualFold(cnbcScalar(item.Realtime), "true") {
		return Quote{}, fmt.Errorf("指数源未确认实时权限，不使用分钟延迟报价")
	}
	price, err := cnbcNumber(item.Price)
	if err != nil {
		return Quote{}, err
	}
	change, err := cnbcNumber(item.Change)
	if err != nil {
		return Quote{}, err
	}
	percent, err := cnbcNumber(item.Percent)
	if err != nil {
		return Quote{}, err
	}
	tradeTime, date, err := cnbcTradeTime(item.LastTime, now)
	if err != nil {
		return Quote{}, err
	}
	if date < now.In(newYorkZone).AddDate(0, 0, -7).Format("2006-01-02") {
		return Quote{}, fmt.Errorf("指数源报价日期已过期")
	}
	state, label := usCashSession(now, item.MainStatus)
	if state == StateRegular {
		if tradeTime == 0 && item.RegularTime != "" {
			regularTime, regularDate, err := cnbcTradeTime(item.RegularTime, now)
			local := time.Unix(regularTime, 0).In(newYorkZone)
			minutes := local.Hour()*60 + local.Minute()
			if err == nil && regularTime > 0 && regularDate == date && minutes >= 9*60+30 && minutes <= 16*60 {
				tradeTime = regularTime
			}
		}
		if date != now.In(newYorkZone).Format("2006-01-02") || tradeTime == 0 {
			return Quote{}, fmt.Errorf("实时指数缺少可核实的当日盘中报价时间")
		}
		if now.Unix()-tradeTime > 90 {
			return Quote{}, fmt.Errorf("实时指数报价已超过90秒，未切换到分钟延迟源")
		}
	}
	marketState := "REGULAR"
	if state == StateClosed {
		marketState = "CLOSED"
	} else if state == StatePreMarket {
		marketState = "PRE"
	}
	q := Quote{Symbol: symbol, Alias: AliasFor(symbol), Price: price, ChangeAbs: change, ChangePercent: percent, RegularPrice: price - change,
		Currency: "USD", QuoteType: "INDEX", MarketState: marketState, State: state, Session: label, TradeTime: tradeTime, QuoteDate: date,
		QuoteSource: "CNBC（交易所指数，源标记实时）", ProviderSymbol: "CNBC:" + item.Symbol, UpdateMode: "cnbc_realtime", Description: item.Name, RTTMs: rtt}
	return q, ValidateQuote(q)
}

func parseCNBCIndexBatch(symbols []string, body []byte, rtt float64, now time.Time) (map[string]SymbolResult, error) {
	if !usCashIndexSymbols(symbols) || len(body) > MaxBody {
		return nil, fmt.Errorf("实时指数请求或响应大小无效")
	}
	var response struct {
		Result struct {
			Items []cnbcIndexItem `json:"QuickQuote"`
		} `json:"QuickQuoteResult"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("实时指数响应解析失败")
	}
	wanted := make(map[string]string, len(symbols))
	out := make(map[string]SymbolResult, len(symbols))
	seen := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[usCashTickers[symbol]] = symbol
		out[symbol] = SymbolResult{Err: "缺少实时指数响应"}
	}
	for _, item := range response.Result.Items {
		symbol, ok := wanted[item.Symbol]
		if !ok {
			continue
		}
		if seen[symbol] {
			out[symbol] = SymbolResult{Err: "重复实时指数响应"}
			continue
		}
		seen[symbol] = true
		q, err := parseCNBCIndex(symbol, item, rtt, now)
		if err != nil {
			out[symbol] = SymbolResult{Err: err.Error()}
		} else {
			out[symbol] = SymbolResult{Quote: &q}
		}
	}
	for _, result := range out {
		if result.Quote != nil {
			return out, nil
		}
	}
	return out, fmt.Errorf("实时指数源不可用；未回退到10/15分钟延迟源")
}
