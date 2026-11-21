package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

func detailTime(t *testing.T, raw string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestChartMacroReleaseStatesAndCountdown(t *testing.T) {
	at := detailTime(t, "2026-11-06T13:30:00Z")
	e := market.MacroEvent{Kind: market.MacroNFP, Country: "US", At: at, Actual: "165K", Forecast: "65K", Previous: "60K", Source: "TradingView"}
	when, values, published := chartMacroText(e, locale.Chinese, at.Add(time.Hour))
	if when != "今日 08:30 ET" || !published || !strings.Contains(values, "实际 165K  ·  预期 65K  ·  前值 60K") {
		t.Fatalf("today's reported release: %q / %q (%v)", when, values, published)
	}
	when, values, published = chartMacroText(e, locale.English, at.Add(-21*time.Hour))
	if !strings.Contains(when, "08:30 ET · in 21h 0m") || published || strings.Contains(values, "Actual") {
		t.Fatalf("future event exposed an actual: %q / %q (%v)", when, values, published)
	}
	e = market.MacroEvent{Kind: market.MacroFOMC, Country: "US", At: detailTime(t, "2026-12-09T19:00:00Z"), Source: "Federal Reserve"}
	when, values, published = chartMacroText(e, locale.English, detailTime(t, "2026-11-06T16:00:00Z"))
	if when != "12-09 14:00 ET · in 33d 3h" || published || values != "Federal Reserve" {
		t.Fatalf("cross-week decision timing: %q / %q", when, values)
	}
}

func TestChartEventOnFullBeijingAxis(t *testing.T) {
	at := detailTime(t, "2026-12-07T02:00:00Z")
	window := market.BeijingIntradayWindow(at)
	// 10 pixels per hour, including a premarket event and next-day 09:00.
	for _, c := range []struct {
		offset time.Duration
		x      float32
		in     bool
	}{
		{-time.Second, 0, false}, {0, 10, true}, {2 * time.Hour, 30, true},
		{14 * time.Hour, 150, true}, {23 * time.Hour, 240, true}, {23*time.Hour + time.Second, 0, false},
	} {
		if x, ok := chartEventX(at.Add(c.offset), window, 10, 240); ok != c.in || x != c.x {
			t.Errorf("at %s: (%v,%v), want (%v,%v)", c.offset, x, ok, c.x, c.in)
		}
	}
}

func TestChartEarningsDatesDontInferSessionOrLoseDSTDay(t *testing.T) {
	now := detailTime(t, "2026-10-31T16:00:00Z")
	q := market.Quote{Symbol: "NVDA", Currency: "USD", TradeTime: detailTime(t, "2026-11-02T16:00:00Z").Unix()}
	start := market.QuoteMarketTime(q)
	e := market.EarningsDate{Start: start, End: start.AddDate(0, 0, 2), Estimated: true, Source: "Yahoo Finance"}
	title, date := chartEarningsText(e, locale.English, now)
	if title != "Next earnings" || !strings.Contains(date, "2026-11-02–2026-11-04") || !strings.Contains(date, "in 2 days") || strings.Contains(date, "After-hours") {
		t.Fatalf("earnings presentation: %q / %q", title, date)
	}
}
