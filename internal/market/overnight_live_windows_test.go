//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveYahooVOOQQQOvernight(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_YAHOO_OVERNIGHT") != "1" {
		t.Skip("opt-in Yahoo overnight network check")
	}
	if _, _, active := yahooNightWindow(time.Now()); !active {
		t.Skip("outside the Yahoo overnight trading window")
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	defer resetSessionLocked()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := ensureSessionLocked()
	if err != nil {
		t.Fatal(err)
	}
	crumb, err := refreshCrumbLocked(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v7/finance/quote?symbols=VOO,QQQ&overnightPrice=true&fields=" + quoteFields + "&crumb=" + url.QueryEscape(crumb)
	status, body, err := doHTTPGet(ctx, session, PrimaryHost, path, true)
	if err != nil || status != 200 {
		t.Fatalf("Yahoo request failed: status=%d err=%v", status, err)
	}
	now := time.Now()
	if dir := os.Getenv("SAMUEL_TEST_QUOTE_OUTPUT"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "yahoo-overnight.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var raw yahooQuoteBatchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"VOO", "QQQ"}, body, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.QuoteResponse.Result) != 2 {
		t.Fatal("expected both public ETF symbols")
	}
	for _, item := range raw.QuoteResponse.Result {
		t.Run(item.Symbol, func(t *testing.T) {
			q := batch[item.Symbol].Quote
			if q == nil {
				t.Fatal(batch[item.Symbol].Err)
			}
			t.Logf("displayed: price=%.5f percent=%+.7f base=%.5f time=%d mode=%s", q.Price, q.ChangePercent, q.Previous(), q.TradeTime, q.UpdateMode)
			if item.OvernightMarketPrice == nil || item.OvernightMarketTime == nil || item.OvernightMarketChange == nil || item.OvernightMarketChangePercent == nil {
				t.Skip("source currently has no complete overnight tuple")
			}
			t.Logf("Yahoo night: price=%.5f percent=%+.7f change=%+.5f time=%d state=%s", *item.OvernightMarketPrice, *item.OvernightMarketChangePercent, *item.OvernightMarketChange, *item.OvernightMarketTime, item.MarketState)
			if q.Price != *item.OvernightMarketPrice || q.ChangeAbs != *item.OvernightMarketChange || math.Abs(q.ChangePercent-*item.OvernightMarketChangePercent) > 1e-7 || q.TradeTime != *item.OvernightMarketTime || q.UpdateMode != "yahoo_overnight" {
				t.Fatal("display does not match Yahoo overnight tuple")
			}
		})
	}
}
