package market

import (
	"fmt"
	"testing"
	"time"
)

func TestNightToPremarketDoesNotOscillateWithFallbackAndLatePackets(t *testing.T) {
	boundary := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	closeAt := time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)
	tracker := NewTracker()
	tracker.SetGroup(GroupVOOQQQ)
	apply := func(now time.Time, data map[string]any) Quote {
		data["regularMarketTime"] = closeAt.Unix()
		batch, err := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, nightFixture("VOO", now, data), 1, now)
		if err != nil || batch["VOO"].Quote == nil {
			t.Fatalf("invalid replay fixture: %v %+v", err, batch)
		}
		tracker.ApplyBatch(batch, now)
		for _, view := range tracker.Views(now, 1) {
			if view.Symbol == "VOO" && view.Quote != nil {
				return *view.Quote
			}
		}
		t.Fatal("missing visible quote")
		return Quote{}
	}
	apply(boundary.Add(-time.Second), map[string]any{"marketState": "OVERNIGHT", "overnightMarketPrice": 109, "overnightMarketChange": -1, "overnightMarketChangePercent": -1.0 / 110 * 100, "overnightMarketTime": boundary.Add(-2 * time.Second).Unix()})
	waiting := apply(boundary, map[string]any{"marketState": "PRE"})
	if waiting.Price != 109 || waiting.State != StateClosed {
		t.Errorf("first missing premarket tick must retain the explicitly stale night price: %+v", waiting)
	}
	want := 109.0
	var bad []string
	for i := 0; i < 60; i++ {
		now := boundary.Add(time.Duration(i+1) * time.Second)
		data := map[string]any{"marketState": "PRE"}
		switch i % 4 {
		case 0:
			want = 110.5 + float64(i)/100
			data["preMarketPrice"], data["preMarketChange"], data["preMarketChangePercent"], data["preMarketTime"] = want, want-110, (want-110)/110*100, now.Unix()
		case 2:
			data["preMarketPrice"], data["preMarketChange"], data["preMarketChangePercent"], data["preMarketTime"] = 110.2, .2, .2/110*100, now.Add(-4*time.Second).Unix()
		case 3:
			data["marketState"] = "OVERNIGHT"
		}
		got := apply(now, data)
		if got.Price != want || got.UpdateMode != "yahoo_pre" {
			bad = append(bad, fmt.Sprintf("%02ds %.2f/%+.2f%% (%s), want %.2f", i+1, got.Price, got.ChangePercent, got.UpdateMode, want))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d false reversions/late packets were shown; first samples: %v", len(bad), bad[:min(6, len(bad))])
	}
}

func TestHandoverAllowsNewMarketMovesAndSameSecondUpdates(t *testing.T) {
	now := time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)
	tracker := NewTrackerForSymbols([]string{"VOO"})
	for i, price := range []float64{111, 111.2, 80} {
		at := now
		if i == 2 {
			at = at.Add(time.Second)
		}
		body := nightFixture("VOO", at, map[string]any{"marketState": "PRE", "regularMarketTime": now.Add(-14 * time.Hour).Unix(), "preMarketPrice": price, "preMarketChange": price - 110, "preMarketChangePercent": (price - 110) / 110 * 100, "preMarketTime": at.Unix()})
		batch, err := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, body, 1, at)
		if err != nil {
			t.Fatal(err)
		}
		tracker.ApplyBatch(batch, at)
		if q := tracker.Views(at, 1)[0].Quote; q == nil || q.Price != price {
			t.Fatal("a real same-second update or newer sharp move was suppressed:", q)
		}
	}
}

func TestHandoverDoesNotRenewFreshnessWithRejectedFallback(t *testing.T) {
	now := time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)
	tracker := NewTrackerForSymbols([]string{"VOO"})
	valid, _ := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, nightFixture("VOO", now, map[string]any{"marketState": "PRE", "preMarketPrice": 111, "preMarketChange": 1, "preMarketChangePercent": 1.0 / 110 * 100, "preMarketTime": now.Unix()}), 1, now)
	tracker.ApplyBatch(valid, now)
	for i := 1; i <= 40; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		fallback, _ := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, nightFixture("VOO", at, map[string]any{"marketState": "PRE"}), 1, at)
		tracker.ApplyBatch(fallback, at)
	}
	view := tracker.Views(now.Add(40*time.Second), 1)[0]
	if view.Healthy || view.Quote.Price != 111 || view.ReceivedAge == nil || *view.ReceivedAge < 40 {
		t.Fatal("discarded fallbacks hid a stale source:", view)
	}
}

func TestClosedWeekendCanReturnToTheRegularHeadline(t *testing.T) {
	now := time.Date(2026, 11, 7, 1, 0, 0, 0, time.UTC) // Friday 20:00 EST.
	tracker := NewTrackerForSymbols([]string{"VOO"})
	previous := Quote{Symbol: "VOO", Currency: "USD", QuoteType: "ETF", Price: 111, ChangeAbs: 1, ChangePercent: 1.0 / 110 * 100, ChangeBase: 110, RegularPrice: 110, RegularTradeTime: now.Add(-4 * time.Hour).Unix(), TradeTime: now.Add(-time.Second).Unix(), MarketState: "POST", State: StatePostMarket, UpdateMode: "yahoo_post"}
	tracker.ApplyBatch(map[string]SymbolResult{"VOO": {Quote: &previous}}, now.Add(-time.Second))
	closed := previous
	closed.Price, closed.ChangeAbs, closed.ChangePercent, closed.ChangeBase = 110, 10, 10, 100
	closed.TradeTime, closed.State, closed.UpdateMode, closed.MarketState = previous.RegularTradeTime, StateClosed, "yahoo_regular", "CLOSED"
	tracker.ApplyBatch(map[string]SymbolResult{"VOO": {Quote: &closed}}, now)
	if q := tracker.Views(now, 1)[0].Quote; q == nil || q.Price != 110 || q.UpdateMode != "yahoo_regular" {
		t.Fatal("weekend close incorrectly retained an active postmarket quote:", q)
	}
}
