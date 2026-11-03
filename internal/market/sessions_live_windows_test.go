//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLiveYahooUSSessions(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_YAHOO_SESSIONS") != "1" && os.Getenv("SAMUEL_TEST_YAHOO_OVERNIGHT") != "1" {
		t.Skip("opt-in Yahoo session network check")
	}
	syms := []string{"VOO", "QQQ", "SOXX", "SMH", "NVDA", "AAPL", "^NDX", "^GSPC"}
	for _, symbol := range strings.Split(os.Getenv("SAMUEL_TEST_QUOTE_SYMBOLS"), ",") {
		symbol = normalizeSymbol(strings.TrimSpace(symbol))
		if symbol != "" && !slices.Contains(syms, symbol) {
			syms = append(syms, symbol)
		}
	}
	if len(syms) > 32 {
		t.Fatal("network check limited to 32 requested symbols")
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
	path := "/v7/finance/quote?symbols=" + wireSymbolList(syms) + "&overnightPrice=true&fields=" + quoteFields + "&crumb=" + url.QueryEscape(crumb)
	status, body, err := doHTTPGet(ctx, session, PrimaryHost, path, true)
	if err != nil || status != 200 {
		t.Fatalf("Yahoo request failed: status=%d err=%v", status, err)
	}
	now := time.Now()
	if dir := os.Getenv("SAMUEL_TEST_QUOTE_OUTPUT"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "yahoo-sessions.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var raw yahooQuoteBatchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	batch, err := ParseYahooQuoteBatchForSymbols(syms, body, 1, now)
	if err != nil || len(raw.QuoteResponse.Result) != len(syms) {
		t.Fatalf("incomplete requested quote batch: %v", err)
	}
	for _, item := range raw.QuoteResponse.Result {
		t.Run(item.Symbol, func(t *testing.T) {
			q := batch[item.Symbol].Quote
			if q == nil {
				t.Fatal(batch[item.Symbol].Err)
			}
			if yahooUSAsset(*q) && q.UpdateMode == "yahoo_regular" {
				w := yahooUSWindow(now, item.MarketState, q.RegularTradeTime)
				var p, c, pc *float64
				var at *int64
				switch w.state {
				case StatePreMarket:
					p, c, pc, at = item.PreMarketPrice, item.PreMarketChange, item.PreMarketChangePercent, item.PreMarketTime
				case StatePostMarket:
					p, c, pc, at = item.PostMarketPrice, item.PostMarketChange, item.PostMarketChangePercent, item.PostMarketTime
				case StateOvernight:
					p, c, pc, at = item.OvernightMarketPrice, item.OvernightMarketChange, item.OvernightMarketChangePercent, item.OvernightMarketTime
				}
				if p != nil && c != nil && pc != nil && at != nil && *p > 0 && *p-*c > 0 && q.RegularTradeTime > 0 && *at > q.RegularTradeTime && w.contains(*at, now) && math.Abs(*c/(*p-*c)*100-*pc) < .05 {
					t.Fatal("a complete coherent current-session tuple was discarded")
				}
			}
			t.Logf("displayed: price=%.5f percent=%+.7f base=%.5f time=%d mode=%s state=%s", q.Price, q.ChangePercent, q.Previous(), q.TradeTime, q.UpdateMode, q.State)
			var price, change, percent *float64
			var stamp *int64
			switch q.UpdateMode {
			case "yahoo_pre":
				price, change, percent, stamp = item.PreMarketPrice, item.PreMarketChange, item.PreMarketChangePercent, item.PreMarketTime
			case "yahoo_post":
				price, change, percent, stamp = item.PostMarketPrice, item.PostMarketChange, item.PostMarketChangePercent, item.PostMarketTime
			case "yahoo_overnight":
				price, change, percent, stamp = item.OvernightMarketPrice, item.OvernightMarketChange, item.OvernightMarketChangePercent, item.OvernightMarketTime
			default:
				price, change, percent, stamp = item.RegularMarketPrice, item.RegularMarketChange, item.RegularMarketChangePercent, item.RegularMarketTime
				if yahooUSAsset(*q) {
					w := yahooUSWindow(now, item.MarketState, q.RegularTradeTime)
					if w.state != StateRegular && q.State != StateClosed {
						t.Fatal("regular-price fallback pretends to be an extended-session quote")
					}
				}
			}
			if price == nil || change == nil || percent == nil || stamp == nil || q.Price != *price || q.ChangeAbs != *change || math.Abs(q.ChangePercent-*percent) > 1e-7 || q.TradeTime != *stamp {
				t.Fatal("display does not match a complete Yahoo session tuple")
			}
			if q.QuoteType == "INDEX" && q.State != StateRegular && q.State != StateClosed {
				t.Fatal("cash index uses an unsupported extended session")
			}
		})
	}
}
