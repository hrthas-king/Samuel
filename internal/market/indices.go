package market

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const TradingViewHost = "scanner.tradingview.com"

type indexSpec struct {
	ticker    string
	currency  string
	reference bool
}

var indexSpecs = map[string]indexSpec{
	"^N225":  {"TVC:NI225", "JPY", true},
	"^KS11":  {"KRX:KOSPI", "KRW", false},
	"^FCHI":  {"TVC:CAC40", "EUR", true},
	"^GDAXI": {"TVC:DEU40", "EUR", true},
	"^NDX":   {"NASDAQ:NDX", "USD", false},
	"^GSPC":  {"SP:SPX", "USD", false},
}

func isGlobalIndex(symbol string) bool {
	_, ok := indexSpecs[symbol]
	return ok
}

func indexRequest(symbols []string) ([]byte, error) {
	tickers := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		spec, ok := indexSpecs[symbol]
		if !ok {
			return nil, fmt.Errorf("不支持的指数: %s", symbol)
		}
		tickers = append(tickers, spec.ticker)
	}
	return json.Marshal(map[string]any{
		"symbols": map[string][]string{"tickers": tickers},
		"columns": []string{"close", "change", "change_abs", "type", "currency", "update_mode", "description", "current_session"},
	})
}

func streamingDelay(mode string) (int, error) {
	if mode == "streaming" {
		return 0, nil
	}
	if strings.HasPrefix(mode, "delayed_streaming_") {
		seconds, err := strconv.Atoi(strings.TrimPrefix(mode, "delayed_streaming_"))
		if err == nil && seconds > 0 && seconds <= 86400 {
			return seconds, nil
		}
	}
	return 0, fmt.Errorf("无法确认指数更新模式: %s", mode)
}

func parseIndexQuotes(symbols []string, body []byte, rtt float64, now time.Time) (map[string]SymbolResult, error) {
	if len(body) > MaxBody {
		return nil, fmt.Errorf("指数响应超过大小上限")
	}
	var response struct {
		Data []struct {
			Ticker string            `json:"s"`
			Values []json.RawMessage `json:"d"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("指数响应解析失败: %w", err)
	}
	requested := make(map[string]string, len(symbols))
	out := make(map[string]SymbolResult, len(symbols))
	for _, symbol := range symbols {
		requested[indexSpecs[symbol].ticker] = symbol
		out[symbol] = SymbolResult{Err: "缺少指数响应"}
	}
	seen := make(map[string]bool, len(symbols))
	for _, item := range response.Data {
		symbol, ok := requested[item.Ticker]
		if !ok {
			continue
		}
		if seen[symbol] {
			out[symbol] = SymbolResult{Err: "重复指数响应"}
			continue
		}
		seen[symbol] = true
		q, err := parseIndexQuote(symbol, item.Values, rtt, now)
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
	return out, fmt.Errorf("参考指数均不可用")
}

func parseIndexQuote(symbol string, values []json.RawMessage, rtt float64, now time.Time) (Quote, error) {
	spec, ok := indexSpecs[symbol]
	if !ok || len(values) != 8 {
		return Quote{}, fmt.Errorf("指数标识或字段数量无效")
	}
	var price, percent, change *float64
	var kind, currency, mode, description, session string
	for i, target := range []any{&price, &percent, &change, &kind, &currency, &mode, &description, &session} {
		if err := json.Unmarshal(values[i], target); err != nil {
			return Quote{}, fmt.Errorf("指数行情字段无效")
		}
	}
	// These official index series omit currency; exact ticker identity binds it.
	if currency == "" && (spec.ticker == "KRX:KOSPI" || spec.ticker == "NASDAQ:NDX" || spec.ticker == "SP:SPX") {
		currency = spec.currency
	}
	if price == nil || percent == nil || change == nil || kind != "index" || currency != spec.currency {
		return Quote{}, fmt.Errorf("指数价格、品类或币种无效")
	}
	delay, err := streamingDelay(mode)
	if err != nil {
		return Quote{}, err
	}
	if session != "market" && session != "out_of_session" && session != "pre_market" && session != "post_market" {
		return Quote{}, fmt.Errorf("无法确认指数交易时段: %s", session)
	}
	state, sessionText := resolveIndexSession(symbol, now, 0)
	if session == "market" {
		state, sessionText = StateRegular, "盘中 (Regular)"
	} else if session == "pre_market" {
		state, sessionText = StatePreMarket, "盘前 (Pre-Market)"
	} else if session == "out_of_session" || session == "post_market" {
		state, sessionText = StateClosed, "休市 (Closed)"
	}
	marketState := "REGULAR"
	if state == StateClosed {
		marketState = "CLOSED"
	} else if state == StatePreMarket {
		marketState = "PRE"
	}
	source := "TradingView (" + spec.ticker + ")"
	if spec.reference {
		source = "TradingView 参考指数 (" + spec.ticker + ")"
	}
	q := Quote{
		Symbol: symbol, Alias: AliasFor(symbol), Price: *price,
		ChangePercent: *percent, ChangeAbs: *change, RegularPrice: *price - *change,
		Currency: currency, QuoteType: "INDEX", MarketState: marketState,
		State: state, Session: sessionText, QuoteSource: source, UpdateMode: mode,
		Description: description, RTTMs: rtt, ProviderSymbol: spec.ticker,
		Reference: spec.reference, DelaySeconds: delay,
		// Scanner's `time` is a bar's opening time, not the quote timestamp.
		// Leave TradeTime unset rather than fabricating a recent trade time.
	}
	return q, ValidateQuote(q)
}

type indexFetcher func(context.Context, []string) (map[string]SymbolResult, error)

func fetchIndexQuotes(ctx context.Context, symbols []string, primary, fallback indexFetcher) (map[string]SymbolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, primaryErr := primary(ctx, symbols)
	if out == nil {
		out = make(map[string]SymbolResult, len(symbols))
	}
	var missing []string
	for _, symbol := range symbols {
		if q := out[symbol].Quote; q == nil || q.Symbol != symbol || ValidateQuote(*q) != nil {
			out[symbol] = SymbolResult{Err: "参考指数暂不可用"}
			missing = append(missing, symbol)
		}
	}
	if len(missing) > 0 && ctx.Err() == nil {
		backup, err := fallback(ctx, missing)
		for _, symbol := range missing {
			result := backup[symbol]
			if result.Quote != nil && result.Quote.Symbol == symbol && ValidateQuote(*result.Quote) == nil {
				out[symbol] = result
			} else if result.Err != "" {
				out[symbol] = SymbolResult{Err: result.Err}
			} else if err != nil {
				out[symbol] = SymbolResult{Err: err.Error()}
			}
		}
	}
	for _, symbol := range symbols {
		if out[symbol].Quote != nil {
			return out, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if primaryErr != nil {
		return out, fmt.Errorf("指数主备源均不可用: %w", primaryErr)
	}
	return out, fmt.Errorf("指数主备源均不可用")
}

// FeedTag labels reference series and upstream delay independently of whether
// the most recent HTTP request succeeded. Closed-market quote age is not delay.
func (q Quote) FeedTag(now time.Time) string {
	var tags []string
	if q.Reference {
		tags = append(tags, "参考")
	}
	delay := q.DelaySeconds
	if isGlobalIndex(q.Symbol) && q.TradeTime > 0 && q.State == StateRegular {
		if age := now.Unix() - q.TradeTime; age > 120 && age > int64(delay) {
			delay = int(age)
		}
	}
	if delay > 0 {
		if delay >= 60 {
			tags = append(tags, fmt.Sprintf("延迟约%dm", delay/60))
		} else {
			tags = append(tags, fmt.Sprintf("延迟约%ds", delay))
		}
	}
	return strings.Join(tags, "·")
}
