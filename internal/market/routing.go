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
	yahooRateLimitDelay = 30 * time.Second
)

type quoteBatchFetch func(context.Context, []string) (map[string]SymbolResult, error)

// Back off failed authentication independently of the public chart endpoint.
type yahooQuoteRecovery struct {
	retryV7At    time.Time
	rateLimitEnd time.Time
}

func yahooRateLimited(err error) bool {
	var status HTTPError
	return errors.As(err, &status) && status.StatusCode == 429
}

func (r *yahooQuoteRecovery) fetch(ctx context.Context, symbols []string, primary, chart quoteBatchFetch) (map[string]SymbolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Before(r.rateLimitEnd) {
		return nil, HTTPError{StatusCode: 429}
	}

	var batch map[string]SymbolResult
	var primaryErr error
	if !now.Before(r.retryV7At) {
		primaryCtx, cancel := context.WithTimeout(ctx, yahooPrimaryTimeout)
		batch, primaryErr = primary(primaryCtx, symbols)
		cancel()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		switch {
		case yahooRateLimited(primaryErr):
			r.rateLimitEnd = time.Now().Add(yahooRateLimitDelay)
			return batch, primaryErr
		case primaryErr != nil:
			r.retryV7At = time.Now().Add(yahooAuthRetry)
		default:
			r.retryV7At = time.Time{}
		}
	}

	var missing []string
	for _, symbol := range symbols {
		if batch[symbol].Quote == nil {
			missing = append(missing, symbol)
		}
	}
	if len(missing) == 0 {
		return batch, nil
	}
	anySuccess := len(missing) < len(symbols)
	if err := ctx.Err(); err != nil {
		if anySuccess {
			return batch, nil
		}
		return batch, errors.Join(err, primaryErr)
	}

	fallback, chartErr := chart(ctx, missing)
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	if yahooRateLimited(chartErr) {
		r.rateLimitEnd = time.Now().Add(yahooRateLimitDelay)
	}
	if batch == nil {
		batch = make(map[string]SymbolResult, len(symbols))
	}
	for _, symbol := range missing {
		result := fallback[symbol]
		if result.Quote != nil {
			anySuccess = true
		} else if result.Err == "" && chartErr != nil {
			result.Err = chartErr.Error()
		}
		if result.Quote != nil || result.Err != "" {
			batch[symbol] = result
		}
	}
	if anySuccess {
		return batch, nil
	}
	return batch, errors.Join(chartErr, primaryErr)
}

func fetchChartBatch(ctx context.Context, symbols []string, fetch func(context.Context, string) (Quote, error)) (map[string]SymbolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		symbol string
		quote  Quote
		err    error
	}
	// Each symbol gets the same deadline. A slow symbol must not spend the
	// entire chart budget before the next symbol can even start.
	done := make(chan result, len(symbols))
	for _, symbol := range symbols {
		go func() {
			q, err := fetch(ctx, symbol)
			done <- result{symbol, q, err}
		}()
	}

	results := make(map[string]SymbolResult, len(symbols))
	var anySuccess bool
	var generalErr, rateLimitErr error
	for range symbols {
		r := <-done
		if r.err != nil {
			if yahooRateLimited(r.err) {
				rateLimitErr = r.err
			}
			generalErr = r.err
			results[r.symbol] = SymbolResult{Err: fmt.Sprintf("行情读取失败: %v", r.err)}
			continue
		}
		anySuccess = true
		results[r.symbol] = SymbolResult{Quote: &r.quote}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	if rateLimitErr != nil {
		return results, rateLimitErr
	}
	if !anySuccess {
		return results, generalErr
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
