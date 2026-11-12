package market

import (
	"context"
	"fmt"
	"time"
)

func MenuSymbols() []string {
	groupMu.RLock()
	defer groupMu.RUnlock()
	seen := make(map[string]bool)
	var symbols []string
	for _, groups := range [][]GroupSpec{Groups, customGroups} {
		for _, group := range groups {
			pair := group.Symbols
			if group.ID == GroupTop2US && topUSCompanies[0] != "" {
				pair = topUSCompanies[:]
			}
			for _, symbol := range pair {
				if !seen[symbol] {
					seen[symbol] = true
					symbols = append(symbols, symbol)
				}
			}
		}
	}
	return symbols
}

func (v View) SessionAvailable(now time.Time) bool {
	if !v.Healthy || v.Quote == nil {
		return false
	}
	q := *v.Quote
	if yahooUSAsset(q) {
		w := yahooUSWindow(now, q.MarketState, q.RegularTradeTime)
		return w.state == StateClosed || (q.State != StateClosed && !yahooCachedSessionExpired(q, now))
	}
	return true
}

// Small batches stay within the existing response limit. No valuation, FX or
// market-cap requests are needed for a percentage-only menu.
func fetchMenuBatches(ctx context.Context, symbols []string, yahoo, tencent func(context.Context, []string) (map[string]SymbolResult, error)) (map[string]SymbolResult, error) {
	var ys, ts []string
	seen := make(map[string]bool)
	for _, sym := range symbols {
		if seen[sym] {
			continue
		}
		seen[sym] = true
		if yahooPreferred(sym) {
			ys = append(ys, sym)
		} else {
			ts = append(ts, sym)
		}
	}
	result := make(map[string]SymbolResult, len(seen))
	any := false
	var last error
	for i, list := range [][]string{ys, ts} {
		fetch := yahoo
		if i == 1 {
			fetch = tencent
		}
		for start := 0; start < len(list); start += 8 {
			chunk := list[start:min(start+8, len(list))]
			var batch map[string]SymbolResult
			err := ctx.Err()
			if err == nil {
				batch, err = fetch(ctx, chunk)
			}
			if err != nil {
				last = err
			}
			for _, sym := range chunk {
				value := batch[sym]
				if value.Quote != nil && value.Quote.Symbol == sym && ValidateQuote(*value.Quote) == nil {
					result[sym], any = value, true
				} else if value.Err != "" {
					result[sym] = value
				} else if err != nil {
					result[sym] = SymbolResult{Err: err.Error()}
				} else {
					result[sym] = SymbolResult{Err: "菜单行情缺少有效标的响应"}
				}
			}
		}
	}
	if any {
		return result, nil
	}
	if last == nil {
		last = fmt.Errorf("菜单行情暂不可用")
	}
	return result, last
}
