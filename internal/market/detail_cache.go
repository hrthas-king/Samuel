package market

import (
	"context"
	"sync"
	"time"
)

type DetailResult[T any] struct {
	Value   T
	Loading bool
	OK      bool
}

type DetailData struct {
	Metrics  DetailResult[QuoteMetrics]
	Regular  DetailResult[RegularClose]
	Year     DetailResult[YearPriceRange]
	Mainland DetailResult[MainlandFundamentals]
	Calendar DetailResult[[]MacroEvent]
	Fed      DetailResult[[]MacroEvent]
	Energy   DetailResult[[]MacroEvent]
	News     DetailResult[[]NewsItem]
}

type detailEntry[T any] struct {
	result  DetailResult[T]
	expires time.Time
}

type detailCache[K comparable, T any] struct {
	mu      sync.Mutex
	entries map[K]detailEntry[T]
}

// get is called only by an open card. previous preserves the displayed
// result if another pane completes after this pane's cache expires.
func (c *detailCache[K, T]) get(ctx context.Context, key K, start bool, ttl time.Duration, previous DetailResult[T],
	load func(context.Context) (T, bool), notify func()) DetailResult[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if entry, ok := c.entries[key]; ok && (entry.result.Loading || now.Before(entry.expires)) {
		return entry.result
	}
	if !start || ctx.Err() != nil {
		return previous
	}
	// Rapidly browsing many custom pairs must not queue unlimited WinHTTP
	// sessions. Expired entries remove themselves without a background poll.
	if _, ok := c.entries[key]; !ok && len(c.entries) >= 12 {
		var oldestKey K
		var oldest time.Time
		for k, entry := range c.entries {
			if !entry.result.Loading && (oldest.IsZero() || entry.expires.Before(oldest)) {
				oldestKey, oldest = k, entry.expires
			}
		}
		if oldest.IsZero() {
			return previous
		}
		delete(c.entries, oldestKey)
	}
	if c.entries == nil {
		c.entries = make(map[K]detailEntry[T])
	}
	loading := DetailResult[T]{Loading: true}
	c.entries[key] = detailEntry[T]{result: loading}
	go func() {
		value, ok := load(ctx)
		duration := ttl
		if !ok {
			duration = min(ttl, 45*time.Second)
		}
		expires := time.Now().Add(duration)
		c.mu.Lock()
		c.entries[key] = detailEntry[T]{result: DetailResult[T]{Value: value, OK: ok}, expires: expires}
		c.mu.Unlock()
		time.AfterFunc(duration, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.entries[key].expires == expires {
				delete(c.entries, key)
				if len(c.entries) == 0 {
					c.entries = nil
				}
			}
		})
		if notify != nil && ctx.Err() == nil {
			notify()
		}
	}()
	return loading
}
