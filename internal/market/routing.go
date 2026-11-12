package market

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	yahooPrimaryTimeout = 5 * time.Second
	yahooAuthRetry      = 30 * time.Second
)

// A cold Yahoo authentication attempt can consume its entire time budget.
// Keep subsequent polls on the public chart endpoint until the next auth retry.
type yahooQuoteRecovery struct {
	retryV7At time.Time
}

func (r *yahooQuoteRecovery) fetch(ctx context.Context, primary, chart func(context.Context) (map[string]SymbolResult, error)) (map[string]SymbolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if time.Now().Before(r.retryV7At) {
		return chart(ctx)
	}

	primaryCtx, cancel := context.WithTimeout(ctx, yahooPrimaryTimeout)
	batch, primaryErr := primary(primaryCtx)
	cancel()
	if primaryErr == nil {
		r.retryV7At = time.Time{}
		return batch, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	r.retryV7At = time.Now().Add(yahooAuthRetry)
	if ctx.Err() != nil {
		return nil, primaryErr
	}
	batch, chartErr := chart(ctx)
	if chartErr != nil {
		return batch, errors.Join(chartErr, primaryErr)
	}
	return batch, nil
}

func fetchChartBatch(ctx context.Context, symbols []string, fetch func(context.Context, string) (Quote, error)) (map[string]SymbolResult, error) {
	results := make(map[string]SymbolResult, len(symbols))
	var anySuccess bool
	var lastErr error
	for _, symbol := range symbols {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			if lastErr == nil {
				lastErr = err
			}
			results[symbol] = SymbolResult{Err: "行情请求超时"}
			continue
		}
		q, err := fetch(ctx, symbol)
		if err != nil {
			lastErr = err
			results[symbol] = SymbolResult{Err: fmt.Sprintf("行情读取失败: %v", err)}
			continue
		}
		anySuccess = true
		results[symbol] = SymbolResult{Quote: &q}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	if !anySuccess {
		return nil, lastErr
	}
	return results, nil
}

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
