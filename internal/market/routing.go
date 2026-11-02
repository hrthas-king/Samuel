package market

import (
	"context"
	"fmt"
)

func yahooPreferred(symbol string) bool {
	switch symbol {
	case "^NDX", "^GSPC", "^FCHI", "^GDAXI":
		return true
	}
	_, tencent := TencentQueryForSymbols([]string{symbol})
	return !tencent
}

// Mixed custom stock pairs are already separated by fetchCustomQuoteBatch.
// A failed selected source must never trigger a different provider here.
func fetchPinnedQuotes(ctx context.Context, symbols []string, yahoo, tencent func(context.Context, []string) (map[string]SymbolResult, error)) (map[string]SymbolResult, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("空行情组合")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	useYahoo := yahooPreferred(symbols[0])
	for _, symbol := range symbols[1:] {
		if yahooPreferred(symbol) != useYahoo {
			return nil, fmt.Errorf("不同来源的标的应先分组请求")
		}
	}
	if useYahoo {
		return yahoo(ctx, symbols)
	}
	return tencent(ctx, symbols)
}
