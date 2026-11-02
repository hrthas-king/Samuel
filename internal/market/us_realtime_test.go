package market

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func cnbcFixture(t *testing.T, date string, status string) []byte {
	t.Helper()
	var rows []map[string]any
	for _, item := range []struct{ symbol, name, price, change, percent string }{{".NDX", "NASDAQ 100 Index", "28,384.845", "-1,017.663", "-3.461"}, {".SPX", "S&P 500 Index", "7,580.07", "-140.32", "-1.818"}} {
		rows = append(rows, map[string]any{"symbol": item.symbol, "name": item.name, "last": item.price, "change": item.change, "change_pct": item.percent,
			"code": "0", "assetType": "INDEX", "currencyCode": "USD", "source": "Exchange", "feedSymbol": item.symbol, "realTime": "true",
			"last_time": date, "mainmktstatus": status, "last_time_msec": "1793612043640", "responseTime": "Mon Nov 02 04:34:03 EST 2026"})
	}
	body, err := json.Marshal(map[string]any{"QuickQuoteResult": map[string]any{"QuickQuote": rows}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestUSRealtimeClosingQuotesDoNotUseResponseTimeAsTickTime(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 34, 3, 0, time.UTC)
	batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, cnbcFixture(t, "2026-10-30", "CLOSE"), 100, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"^NDX", "^GSPC"} {
		q := batch[symbol].Quote
		if q == nil || q.DelaySeconds != 0 || q.UpdateMode != "cnbc_realtime" || q.Reference || q.QuoteType != "INDEX" || q.Currency != "USD" {
			t.Fatalf("not a realtime cash index: %#v", q)
		}
		if q.TradeTime != 0 || q.QuoteDate != "2026-10-30" || q.State != StatePreMarket {
			t.Fatalf("response time became trade time or closed index became active: %#v", q)
		}
		if tip := (View{Symbol: symbol, Quote: q, Healthy: true}).Tooltip(); !strings.Contains(tip, "2026-10-30") || !strings.Contains(tip, "仅日期") {
			t.Fatal("timestamp precision hidden:", tip)
		}
	}
}

func TestUSRealtimeQuotesUseTrueTimestampsAndNewYorkDST(t *testing.T) {
	for _, tc := range []struct {
		name, stamp string
		now         time.Time
	}{
		{"winter", "2026-11-02T09:45:00-05:00", time.Date(2026, 11, 2, 14, 45, 2, 0, time.UTC)},
		{"summer", "2026-07-08T09:45:00-04:00", time.Date(2026, 7, 8, 13, 45, 2, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, cnbcFixture(t, tc.stamp, "OPEN"), 10, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range batch {
				q := result.Quote
				if q == nil || q.State != StateRegular || tc.now.Unix()-q.TradeTime != 2 {
					t.Fatalf("real tick timestamp or DST lost: %#v", q)
				}
			}
		})
	}
	if state, _ := usCashSession(time.Date(2026, 11, 2, 13, 45, 0, 0, time.UTC), "OPEN"); state != StatePreMarket {
		t.Fatal("winter pre-open treated as active due to fixed UTC-4 clock")
	}
}

func TestUSRealtimeRejectsStaleMissingOrFutureIntradayTimestamp(t *testing.T) {
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	for _, stamp := range []string{"2026-11-02T09:45:00-05:00", "2026-11-02", "2026-10-30T16:00:00-04:00", "2026-11-02T10:01:00-05:00", ""} {
		t.Run(stamp, func(t *testing.T) {
			if batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, cnbcFixture(t, stamp, "OPEN"), 10, now); err == nil || batch["^NDX"].Quote != nil {
				t.Fatal("stale or unverified tick accepted as realtime")
			}
		})
	}
}

func TestUSRealtimeRejectsDelayedWrongAssetAndKeepsHealthySide(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 34, 3, 0, time.UTC)
	valid := string(cnbcFixture(t, "2026-10-30", "CLOSE"))
	for _, tc := range []struct{ name, from, to string }{
		{"not_realtime", `"realTime":"true"`, `"realTime":"false"`},
		{"wrong_type", `"assetType":"INDEX"`, `"assetType":"CFD"`},
		{"wrong_currency", `"currencyCode":"USD"`, `"currencyCode":"CNY"`},
		{"derived_source", `"source":"Exchange"`, `"source":"Derived"`},
		{"wrong_feed", `"feedSymbol":".NDX"`, `"feedSymbol":"NQ=F"`},
		{"invalid_price", `"last":"28,384.845"`, `"last":"NaN"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(valid, tc.from, tc.to, 1))
			batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, body, 10, now)
			if err != nil || batch["^GSPC"].Quote == nil || batch["^NDX"].Quote != nil || batch["^NDX"].Err == "" {
				t.Fatalf("one invalid index contaminated the other: %v %#v", err, batch)
			}
		})
	}
	if usCashIndexSymbols([]string{"VOO", "QQQ"}) || usCashIndexSymbols([]string{"^NDX", "^GDAXI"}) {
		t.Fatal("realtime US route captured unrelated assets")
	}
}

func TestUSRealtimeSupportsNumericFlagsAndClosingSession(t *testing.T) {
	now := time.Date(2026, 11, 2, 22, 0, 0, 0, time.UTC)
	body := strings.ReplaceAll(string(cnbcFixture(t, "2026-11-02T16:00:00-05:00", "CLOSE")), `"realTime":"true"`, `"realTime":true`)
	body = strings.ReplaceAll(body, `"code":"0"`, `"code":0`)
	batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, []byte(body), 10, now)
	if err != nil || batch["^NDX"].Quote.State != StateClosed {
		t.Fatalf("valid close rejected: %v", err)
	}
	if date := batch["^NDX"].Quote.QuoteDate; date != fmt.Sprint("2026-11-02") {
		t.Fatal("wrong quote date")
	}
}

func TestUSRealtimeOnlyUsesRegularTimestampWhenPrimaryHasDateOnly(t *testing.T) {
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	body := strings.ReplaceAll(string(cnbcFixture(t, "2026-11-02", "OPEN")), `"last_time":"2026-11-02"`, `"last_time":"2026-11-02","reg_last_time":"2026-11-02T09:59:58-05:00"`)
	batch, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, []byte(body), 10, now)
	if err != nil || now.Unix()-batch["^NDX"].Quote.TradeTime != 2 {
		t.Fatal("valid regular market timestamp not used:", err)
	}
	body = strings.ReplaceAll(body, `"last_time":"2026-11-02"`, `"last_time":"2026-11-02T09:45:00-05:00"`)
	if _, err := parseCNBCIndexBatch([]string{"^NDX", "^GSPC"}, []byte(body), 10, now); err == nil {
		t.Fatal("stale primary tick hidden by a newer regular/response timestamp")
	}
}
