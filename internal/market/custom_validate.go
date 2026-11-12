//go:build (windows || darwin) && (amd64 || arm64)

package market

import (
	"context"
	"fmt"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

func ValidateCustomPair(ctx context.Context, a, b string) (watchlist.Pair, error) {
	p, err := watchlist.NewPair(a, b)
	if err != nil {
		return p, err
	}
	syms := p.Symbols()
	clientMu.Lock()
	defer clientMu.Unlock()
	if err := ctx.Err(); err != nil {
		return watchlist.Pair{}, err
	}
	batch, err := fetchCustomQuoteBatch(ctx, syms, fetchSymbolsLocked)
	for i, s := range syms {
		r := batch[s]
		if r.Quote == nil {
			detail := r.Err
			if detail == "" && err != nil {
				detail = err.Error()
			}
			return watchlist.Pair{}, fmt.Errorf("股票%d：%s", i+1, detail)
		}
	}
	if err := ctx.Err(); err != nil {
		return watchlist.Pair{}, err
	}
	return p, nil
}
