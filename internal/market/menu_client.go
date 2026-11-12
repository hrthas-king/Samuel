//go:build (windows || darwin) && (amd64 || arm64)

package market

import (
	"context"
	"sync"
)

// MenuClient owns an independent session so a slow menu fetch cannot block the
// selected pair's quote session mutex or reset its cookies and connection.
type MenuClient struct {
	mu      sync.Mutex
	session *httpSession
	crumb   string
}

func NewMenuClient() *MenuClient { return &MenuClient{} }

func (c *MenuClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		c.session.close()
		c.session = nil
	}
	c.crumb = ""
}

func (c *MenuClient) Fetch(ctx context.Context, symbols []string) (map[string]SymbolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.session == nil {
		var err error
		c.session, err = newHTTPSession()
		if err != nil {
			return nil, err
		}
	}
	yahoo := func(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
		if c.crumb == "" {
			var err error
			c.crumb, err = requestCrumb(ctx, c.session)
			if err != nil {
				return nil, err
			}
		}
		batch, err := tryV7Batch(ctx, c.session, syms, c.crumb)
		if err != nil {
			c.crumb = ""
		}
		return batch, err
	}
	tencent := func(ctx context.Context, syms []string) (map[string]SymbolResult, error) {
		query, _ := TencentQueryForSymbols(syms)
		return fetchTencentRealtimeLocked(ctx, c.session, syms, query)
	}
	return fetchMenuBatches(ctx, symbols, yahoo, tencent)
}
