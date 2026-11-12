package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

func TestNearestChartSampleUsesTimeSpacingAndEndpoints(t *testing.T) {
	points := []market.IntradayPoint{{Time: 100, Price: 1}, {Time: 110, Price: 2}, {Time: 140, Price: 3}}
	for _, tt := range []struct {
		x    int32
		want int
	}{{9, -1}, {10, 0}, {60, 1}, {135, 1}, {136, 2}, {210, 2}, {211, -1}} {
		if got := nearestChartSample(points, 10, 210, tt.x); got != tt.want {
			t.Errorf("sample at x=%d was %d, want %d", tt.x, got, tt.want)
		}
	}
	if i := nearestChartSample(points[:1], 10, 210, 10); i != -1 {
		t.Fatal("nonplottable series returned a point")
	}
}

func TestChartSampleUsesExchangeTimeAndQuotePrecision(t *testing.T) {
	at := detailTime(t, "2026-11-06T21:20:00Z")
	view := market.View{Symbol: "VOO", Quote: &market.Quote{Symbol: "VOO", Currency: "USD"}}
	if got := chartSampleText(view, market.IntradayPoint{Time: at.Unix(), Price: 676.179}); got != "11-06 16:20 · 676.18 USD" {
		t.Fatalf("unexpected quote-point caption: %s", got)
	}
}

func TestChartSampleChangeUsesChartReference(t *testing.T) {
	for _, tt := range []struct {
		price, base float64
		want        string
	}{
		{676.50, 675.97, "+0.08%"},
		{675.42, 675.97, "-0.08%"},
		{675.97, 675.97, "+0.00%"},
		{675.969, 675.97, "+0.00%"},
	} {
		pct, ok := chartSampleChange(tt.price, tt.base)
		if got := fmt.Sprintf("%+.2f%%", pct); !ok || got != tt.want {
			t.Errorf("price %g and chart reference %g gave %s (%v), want %s", tt.price, tt.base, got, ok, tt.want)
		}
	}
	for _, base := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, ok := chartSampleChange(676.50, base); ok {
			t.Errorf("invalid reference %v produced a percentage", base)
		}
	}
}

func TestChartUsesOnlyExchangeTime(t *testing.T) {
	cases := []struct {
		symbol, currency, at, language, want string
	}{
		{"QQQ", "USD", "2026-11-06T00:59:00Z", locale.Chinese, "最后更新于GMT-5 19:59"},
		{"QQQ", "USD", "2026-07-01T23:59:00Z", locale.English, "Last updated GMT-4 19:59"},
		{"159632.SZ", "CNY", "2026-11-06T02:10:00Z", locale.Chinese, "最后更新于GMT+8 10:10"},
		{"^GDAXI", "EUR", "2026-07-01T14:30:00Z", locale.English, "Last updated GMT+2 16:30"},
		{"^FCHI", "EUR", "2026-11-06T15:30:00Z", locale.English, "Last updated GMT+1 16:30"},
		{"^N225", "JPY", "2026-11-06T02:15:00Z", locale.English, "Last updated GMT+9 11:15"},
	}
	for _, c := range cases {
		at, err := time.Parse(time.RFC3339, c.at)
		if err != nil {
			t.Fatal(err)
		}
		q := &market.Quote{Symbol: c.symbol, Currency: c.currency, TradeTime: at.Unix()}
		if got := chartTimeText(c.language, q); got != c.want {
			t.Errorf("%s at %s: got %q want %q", c.symbol, c.at, got, c.want)
		}
	}
	q := &market.Quote{Symbol: "VOO", Currency: "USD", QuoteDate: "2026-11-05", RegularTradeTime: 1793912400}
	if got := chartTimeText(locale.English, q); got != "Quote time unavailable from source" {
		t.Fatalf("fabricated a time for the displayed quote: %q", got)
	}
	if got := marketClock(time.Date(2026, 1, 1, 3, 4, 0, 0, time.FixedZone("TEST", -3*3600-30*60))); got != "GMT-3:30 03:04" {
		t.Fatal(got)
	}
}

func TestChartCoordinatesPreserveTimeAndReference(t *testing.T) {
	points := []market.IntradayPoint{{Time: 10, Price: 100}, {Time: 20, Price: 105}, {Time: 50, Price: 102}}
	line, base := chartCoordinates(points, 90, 10, 20, 210, 120)
	if len(line) != 3 || line[0].X != 10 || line[1].X != 60 || line[2].X != 210 {
		t.Fatalf("chart X should be proportional to elapsed time: %+v", line)
	}
	if !(20 < line[1].Y && line[1].Y < line[2].Y && line[2].Y < line[0].Y && line[0].Y < base && base < 120) {
		t.Fatalf("price normalization or baseline invalid: %v %v", line, base)
	}
	points[1].Price, points[2].Price = 100, 100
	line, base = chartCoordinates(points, 100, 10, 20, 210, 120)
	if line[0].Y != 70 || line[0].Y != line[2].Y || base != 70 {
		t.Fatal("flat series should be centered", line, base)
	}
	if line, base := chartCoordinates(points[:1], 0, 10, 20, 210, 120); line != nil || base != -1 {
		t.Fatal("short series is not drawable")
	}
}
