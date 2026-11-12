package market

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

var customGroups []GroupSpec // protected by groupMu; replacement never mutates shared maps

func SetCustomPairs(pairs []watchlist.Pair) error {
	groups := make([]GroupSpec, 0, len(pairs))
	seen := make(map[string]bool)
	for _, pair := range pairs {
		p, err := watchlist.NewPair(pair.Code1, pair.Code2)
		if err != nil {
			return err
		}
		if seen[p.ID()] {
			return fmt.Errorf("重复的自定义组合")
		}
		seen[p.ID()] = true
		groups = append(groups, GroupSpec{ID: p.ID(), MenuLabel: p.Label(), Symbols: p.Symbols()})
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	customGroups = groups
	if strings.HasPrefix(activeGroup.ID, "custom_") {
		activeGroup = groupByIDLocked(activeGroup.ID)
		Symbols = append([]string(nil), activeGroup.Symbols...)
	}
	return nil
}

func cloneGroup(g GroupSpec) GroupSpec { g.Symbols = append([]string(nil), g.Symbols...); return g }
func groupByIDLocked(id string) GroupSpec {
	for _, g := range Groups {
		if g.ID == id {
			if id == GroupTop2US {
				if topUSCompanies[0] == "" {
					g.MenuLabel += " · 等待市值排名"
					return g
				}
				g.Symbols = topUSCompanies[:]
				g.MenuLabel += " (" + strings.Join(g.Symbols, " / ") + ")"
			}
			return cloneGroup(g)
		}
	}
	for _, g := range customGroups {
		if g.ID == id {
			return cloneGroup(g)
		}
	}
	return cloneGroup(Groups[0])
}
func customSymbols(s []string) bool {
	if len(s) == 0 {
		return false
	}
	for _, symbol := range s {
		if !watchlist.IsMainlandSymbol(symbol) {
			return false
		}
		if _, builtin := tencentSymbolMap[symbol]; builtin {
			return false
		}
	}
	return true
}
func TickerFor(s string) string {
	if t, ok := Tickers[s]; ok {
		return t
	}
	if watchlist.IsMainlandSymbol(s) {
		return map[string]string{".SS": "SSE:", ".SZ": "SZSE:", ".BJ": "BSE:"}[s[6:]] + s[:6]
	}
	return s
}

// Each market uses its existing provider; a failure in one market leaves the other result usable.
func fetchCustomQuoteBatch(ctx context.Context, symbols []string, fetch func(context.Context, []string) (map[string]SymbolResult, error)) (map[string]SymbolResult, error) {
	var mainland, us []string
	for _, s := range symbols {
		if watchlist.IsMainlandSymbol(s) {
			mainland = append(mainland, s)
		} else {
			us = append(us, s)
		}
	}
	out := make(map[string]SymbolResult, len(symbols))
	good := false
	for _, subset := range [][]string{mainland, us} {
		if len(subset) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		batch, err := fetch(ctx, subset)
		for _, s := range subset {
			r := batch[s]
			if r.Quote != nil {
				good = true
			} else if r.Err == "" {
				r.Err = "未找到该标的，请核对代码"
				if err != nil {
					r.Err = err.Error()
				}
			}
			out[s] = r
		}
	}
	if !good {
		return out, fmt.Errorf("未获取到有效股票或ETF行情")
	}
	return out, nil
}

func parseCustomQuoteBatch(symbols []string, body []byte, rtt float64, now time.Time) (map[string]SymbolResult, error) {
	if len(body) > MaxBody {
		return nil, fmt.Errorf("行情响应超过限制")
	}
	vars := make(map[string]string)
	for _, s := range symbols {
		if t, ok := watchlist.TencentSymbol(s); ok {
			vars["v_"+t] = s
		}
	}
	out := make(map[string]SymbolResult, len(symbols))
	zone := time.FixedZone("CST", 8*3600)
	for _, line := range strings.Split(string(body), ";") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		sym, expected := vars[strings.TrimSpace(key)]
		if !ok || !expected {
			continue
		}
		if _, duplicate := out[sym]; duplicate {
			out[sym] = SymbolResult{Err: "重复的股票行情"}
			continue
		}
		parts := strings.Split(strings.Trim(strings.TrimSpace(val), "\""), "~")
		result := func() SymbolResult {
			if len(parts) < 62 || parts[2] != sym[:6] {
				return SymbolResult{Err: "不存在该股票或行情代码不匹配"}
			}
			kind := strings.TrimSpace(parts[61])
			quoteType, mode := "EQUITY", "cn_stock_quote"
			if watchlist.IsMainlandETF(sym) && kind == "ETF" {
				quoteType, mode = "ETF", "cn_etf_quote"
			} else if watchlist.IsMainlandETF(sym) || !(strings.HasPrefix(kind, "GP-A") || (strings.HasSuffix(sym, ".BJ") && kind == "GP")) {
				return SymbolResult{Err: "请输入有效的A股或场内ETF代码"}
			}
			name, err := decodeQuoteName(parts[1])
			if err != nil || strings.TrimSpace(name) == "" || len([]rune(name)) > 64 || strings.ContainsAny(name, "\x00\r\n\t") {
				return SymbolResult{Err: "股票名称无效"}
			}
			price, e1 := strconv.ParseFloat(parts[3], 64)
			prev, e2 := strconv.ParseFloat(parts[4], 64)
			at, e3 := time.ParseInLocation("20060102150405", parts[30], zone)
			if e1 != nil || e2 != nil || e3 != nil || at.After(now.Add(30*time.Second)) {
				return SymbolResult{Err: "股票价格或行情时间无效"}
			}
			noTradePrice := price == 0
			if noTradePrice && prev > 0 {
				price = prev
			} // suspended stock: retain the previous close
			state, label := resolveMainlandSession(now, at.Unix())
			if noTradePrice {
				state, label = StateClosed, "尚无有效成交价；显示前收"
			}
			change := price - prev
			q := Quote{Symbol: sym, Alias: strings.TrimSpace(name), Description: strings.TrimSpace(name), Price: price, RegularPrice: prev, ChangeAbs: change, ChangePercent: change / prev * 100, Currency: "CNY", QuoteType: quoteType, State: state, Session: label, TradeTime: at.Unix(), QuoteSource: "Tencent Finance", UpdateMode: mode, RTTMs: rtt, MarketState: "CLOSED"}
			q.ChangeBase, q.ChangeBasis = prev, "腾讯前收"
			if state == StateRegular {
				q.MarketState = "REGULAR"
			} else if state == StatePreMarket {
				q.MarketState = "PRE"
			}
			if err := ValidateQuote(q); err != nil {
				return SymbolResult{Err: err.Error()}
			}
			if total, err := strconv.ParseFloat(strings.TrimSpace(parts[45]), 64); err == nil {
				q.setCapitalization(total*1e8, price, "Tencent Finance 总市值（亿元人民币）")
			}
			return SymbolResult{Quote: &q}
		}()
		out[sym] = result
	}
	good := false
	for _, s := range symbols {
		r, ok := out[s]
		if !ok {
			out[s] = SymbolResult{Err: "未找到该股票，请核对代码或新旧代码"}
		} else if r.Quote != nil {
			good = true
		}
	}
	if !good {
		return out, fmt.Errorf("未获取到有效A股或ETF行情")
	}
	return out, nil
}
