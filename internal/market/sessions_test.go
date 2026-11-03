package market

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestAllUSStocksAndETFsUseTheActiveYahooSession(t *testing.T) {
	for _, symbol := range []string{"VOO", "QQQ", "SOXX", "SMH", "NVDA", "AAPL", "TSLA", "AMZN", "BRK-B", "JPM"} {
		for _, tc := range []struct {
			name, state, at, regularAt, field, mode string
			price, change, percent, base            float64
		}{
			{"pre", "PRE", "2026-11-03T12:00:00Z", "2026-11-02T21:00:00Z", "preMarket", "yahoo_pre", 113, 3, 3.0 / 110 * 100, 110},
			{"regular", "REGULAR", "2026-11-03T17:00:00Z", "2026-11-03T16:59:55Z", "regularMarket", "yahoo_regular", 110, 10, 10, 100},
			{"post", "POST", "2026-11-03T23:00:00Z", "2026-11-03T21:00:00Z", "postMarket", "yahoo_post", 108, -2, -2.0 / 110 * 100, 110},
			{"overnight", "OVERNIGHT", "2026-11-03T06:00:00Z", "2026-11-02T21:00:00Z", "overnightMarket", "yahoo_overnight", 107, -3, -3.0 / 110 * 100, 110},
		} {
			t.Run(symbol+"/"+tc.name, func(t *testing.T) {
				now, _ := time.Parse(time.RFC3339, tc.at)
				regularAt, _ := time.Parse(time.RFC3339, tc.regularAt)
				kind := "EQUITY"
				if symbol == "VOO" || symbol == "QQQ" || symbol == "SOXX" || symbol == "SMH" {
					kind = "ETF"
				}
				item := map[string]any{
					"symbol": symbol, "currency": "USD", "quoteType": kind, "marketState": tc.state, "gmtOffSetMilliseconds": -18000000,
					"regularMarketPrice": 110, "regularMarketPreviousClose": 100, "regularMarketChange": 10, "regularMarketChangePercent": 10, "regularMarketTime": regularAt.Unix(),
					// The full-day series has another base and must never replace a session tuple.
					"fulldayPrice": 999, "fulldayChange": 899, "fulldayChangePercent": 899,
				}
				item[tc.field+"Price"], item[tc.field+"Change"], item[tc.field+"ChangePercent"], item[tc.field+"Time"] = tc.price, tc.change, tc.percent, now.Add(-5*time.Second).Unix()
				if tc.field != "postMarket" {
					item["postMarketPrice"], item["postMarketChange"], item["postMarketChangePercent"], item["postMarketTime"] = 999, 889, 889.0/110*100, now.Add(-time.Second).Unix()
				}
				body, _ := json.Marshal(map[string]any{"quoteResponse": map[string]any{"result": []any{item}}})
				batch, err := ParseYahooQuoteBatchForSymbols([]string{symbol}, body, 1, now)
				q := batch[symbol].Quote
				if err != nil || q == nil || q.Price != tc.price || math.Abs(q.ChangePercent-tc.percent) > 1e-7 || q.Previous() != tc.base || q.UpdateMode != tc.mode || q.TradeTime != now.Add(-5*time.Second).Unix() {
					t.Fatalf("active session price/change/time must be selected together: %v %+v", err, q)
				}
			})
		}
	}
}

func TestExtendedSessionsRejectIncompleteCrossPeriodOrMixedFields(t *testing.T) {
	for _, phase := range []struct{ name, raw, at, closeAt, field string }{
		{"pre", "PRE", "2026-11-03T12:00:00Z", "2026-11-02T21:00:00Z", "preMarket"},
		{"post", "POST", "2026-11-03T23:00:00Z", "2026-11-03T21:00:00Z", "postMarket"},
		{"night", "OVERNIGHT", "2026-11-03T06:00:00Z", "2026-11-02T21:00:00Z", "overnightMarket"},
	} {
		now, _ := time.Parse(time.RFC3339, phase.at)
		closeAt, _ := time.Parse(time.RFC3339, phase.closeAt)
		for _, issue := range []string{"missing_time", "old_period", "future_tick", "mixed_delta", "wrong_percent", "missing_close_time"} {
			t.Run(phase.name+"/"+issue, func(t *testing.T) {
				data := map[string]any{"marketState": phase.raw, "quoteType": "EQUITY", "regularMarketTime": closeAt.Unix(), phase.field + "Price": 108, phase.field + "Change": -2, phase.field + "ChangePercent": -2.0 / 110 * 100, phase.field + "Time": now.Add(-5 * time.Second).Unix()}
				switch issue {
				case "missing_time":
					data[phase.field+"Time"] = nil
				case "old_period":
					data[phase.field+"Time"] = now.Add(-24 * time.Hour).Unix()
				case "future_tick":
					data[phase.field+"Time"] = now.Add(time.Minute).Unix()
				case "mixed_delta":
					data[phase.field+"Change"] = 10
				case "wrong_percent":
					data[phase.field+"ChangePercent"] = -2
				case "missing_close_time":
					data["regularMarketTime"] = nil
				}
				batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
				q := batch["AAPL"].Quote
				if err != nil || q == nil || q.Price != 110 || q.ChangePercent != 10 || q.State != StateClosed || q.UpdateMode != "yahoo_regular" || !strings.Contains(q.Session, "数据不可用") {
					t.Fatalf("invalid session data was presented as current: %v %+v", err, q)
				}
			})
		}
	}
}

func TestYahooShortenedDayUsesSourceClosingTime(t *testing.T) {
	now := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC) // 14:00 EST, after 13:00 close.
	data := map[string]any{"quoteType": "EQUITY", "marketState": "POST", "regularMarketTime": time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC).Unix(), "postMarketPrice": 108, "postMarketChange": -2, "postMarketChangePercent": -2.0 / 110 * 100, "postMarketTime": now.Add(-time.Second).Unix()}
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
	q := batch["AAPL"].Quote
	if err != nil || q == nil || q.Price != 108 || q.UpdateMode != "yahoo_post" || q.State != StatePostMarket || q.Previous() != 110 {
		t.Fatalf("early close incorrectly kept regular-session values: %v %+v", err, q)
	}
}

func TestCachedSessionExpiresAtTheBoundary(t *testing.T) {
	before := time.Date(2026, 11, 3, 14, 29, 59, 0, time.UTC)
	data := map[string]any{"quoteType": "EQUITY", "marketState": "PRE", "regularMarketTime": time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC).Unix(), "preMarketPrice": 108, "preMarketChange": -2, "preMarketChangePercent": -2.0 / 110 * 100, "preMarketTime": before.Add(-time.Second).Unix()}
	batch, _ := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", before, data), 1, before)
	q := batch["AAPL"].Quote
	if q == nil || yahooCachedSessionExpired(*q, before) || !yahooCachedSessionExpired(*q, before.Add(time.Second)) {
		t.Fatal("cached premarket quote survived the regular-session boundary")
	}
	tracker := NewTracker()
	tracker.SetGroup(GroupTop2US)
	tracker.ApplyBatch(batch, before)
	for _, view := range tracker.Views(before.Add(time.Second), 1) {
		if view.Symbol == "AAPL" && (view.Healthy || view.State != StateClosed || view.Quote.State != StateClosed || !strings.Contains(view.Reason, "时段已切换")) {
			t.Fatal("view still presents its cached premarket quote as current:", view)
		}
	}
}

func TestFirstPostmarketTickMayShareTheClosingSecond(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)
	data := map[string]any{"quoteType": "EQUITY", "marketState": "POST", "regularMarketTime": now.Unix(), "postMarketPrice": 110, "postMarketChange": 0, "postMarketChangePercent": 0, "postMarketTime": now.Unix()}
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
	q := batch["AAPL"].Quote
	if err != nil || q == nil || q.State != StatePostMarket || q.Price != 110 || q.ChangePercent != 0 {
		t.Fatalf("valid first postmarket tick was confused with the regular daily change: %v %+v", err, q)
	}
}

func TestSourceClosedStateDoesNotReuseAnEarlierPostmarketTick(t *testing.T) {
	now := time.Date(2026, 11, 27, 23, 0, 0, 0, time.UTC)
	data := map[string]any{"quoteType": "EQUITY", "marketState": "POSTPOST", "regularMarketTime": time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC).Unix(), "postMarketPrice": 108, "postMarketChange": -2, "postMarketChangePercent": -2.0 / 110 * 100, "postMarketTime": time.Date(2026, 11, 27, 21, 59, 59, 0, time.UTC).Unix()}
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
	q := batch["AAPL"].Quote
	if err != nil || q == nil || q.State != StateClosed || q.UpdateMode != "yahoo_regular" {
		t.Fatalf("a source-declared close was ignored: %v %+v", err, q)
	}
}

func TestShortenedDayPostmarketStopsAt1700(t *testing.T) {
	now := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC) // 17:00 EST.
	data := map[string]any{"quoteType": "EQUITY", "marketState": "POST", "regularMarketTime": time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC).Unix(), "postMarketPrice": 108, "postMarketChange": -2, "postMarketChangePercent": -2.0 / 110 * 100, "postMarketTime": now.Add(-time.Second).Unix()}
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
	q := batch["AAPL"].Quote
	if err != nil || q == nil || q.State != StateClosed {
		t.Fatalf("early-close day's postmarket continued past 17:00: %v %+v", err, q)
	}
}

func TestYahooFutureRegularTimeRejectedByBothEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 3, 17, 0, 0, 0, time.UTC)
	data := map[string]any{"quoteType": "EQUITY", "marketState": "REGULAR", "regularMarketTime": now.Add(time.Minute).Unix()}
	batch, _ := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, nightFixture("AAPL", now, data), 1, now)
	if batch["AAPL"].Quote != nil {
		t.Fatal("future regular timestamp accepted by v7")
	}
	body, _ := json.Marshal(map[string]any{"chart": map[string]any{"result": []any{map[string]any{"meta": map[string]any{"symbol": "AAPL", "instrumentType": "EQUITY", "currency": "USD", "regularMarketPrice": 110, "previousClose": 100, "regularMarketTime": now.Add(time.Minute).Unix()}}}}})
	if _, err := ParseYahooChart("AAPL", body, 1, now); err == nil {
		t.Fatal("future regular timestamp accepted by chart fallback")
	}
}
