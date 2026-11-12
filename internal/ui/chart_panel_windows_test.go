//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

func chartDetailFixture(t *testing.T) (market.View, chartSeries, chartExtras) {
	t.Helper()
	now := detailTime(t, "2026-11-06T16:00:00Z")
	q := &market.Quote{Symbol: "QQQ", Alias: "Invesco QQQ", Currency: "USD", Price: 647.05, ChangeAbs: -44.90, ChangePercent: -6.49,
		QuoteType: "ETF", State: market.StateRegular, UpdateMode: "yahoo_regular", TradeTime: now.Unix(), RegularTradeTime: now.Unix(), ChangeBase: 691.95}
	view := market.View{Symbol: q.Symbol, Quote: q, Healthy: true}
	series := chartSeries{points: []market.IntradayPoint{
		{Time: now.Add(-3 * time.Hour).Unix(), Price: 693.04}, {Time: now.Add(-150 * time.Minute).Unix(), Price: 683.02},
		{Time: now.Add(-2 * time.Hour).Unix(), Price: 673.52}, {Time: now.Add(-time.Hour).Unix(), Price: 677.15},
		{Time: now.Unix(), Price: 647.05},
	}}
	open, high, low, prev, volume, yearHigh, yearLow := 693.04, 697.89, 646.25, 691.95, 41852000.0, 762.86, 555.60
	metrics := market.QuoteMetrics{At: now.Unix(), Source: "Yahoo Finance", Open: &open, High: &high, Low: &low, PrevClose: &prev, Volume: &volume,
		YearHigh: &yearHigh, YearLow: &yearLow}
	release := detailTime(t, "2026-11-06T13:30:00Z")
	cal := []market.MacroEvent{
		{Kind: market.MacroNFP, Country: "US", At: release, Actual: "165K", Forecast: "65K", Previous: "60K", Source: "TradingView"},
		{Kind: market.MacroUnemployment, Country: "US", At: release, Actual: "4.1%", Forecast: "4.2%", Previous: "4.2%", Source: "TradingView"},
	}
	fed := []market.MacroEvent{{Kind: market.MacroFOMC, Country: "US", At: detailTime(t, "2026-12-09T19:00:00Z"), Source: "Federal Reserve"}}
	var news []market.NewsItem
	for i, item := range []struct{ category, title string }{
		{"议息", "Fed officials weigh next move after jobs report"},
		{"宏观", "Markets assess the latest US employment figures"},
		{"时政", "Trade policy talks and semiconductor export controls"},
		{"财报", "Chipmakers publish quarterly earnings updates"},
		{"市场", "Technology ETFs track a volatile session"},
	} {
		news = append(news, market.NewsItem{Title: item.title, Category: item.category, Source: "Example Wire",
			URL: fmt.Sprintf("https://example.org/news/%d", i), Published: now.Add(-time.Duration(i+1) * time.Hour)})
	}
	data := market.DetailData{
		Metrics:  market.DetailResult[market.QuoteMetrics]{Value: metrics, OK: true},
		Regular:  market.DetailResult[market.RegularClose]{Value: market.RegularClose{Date: now.AddDate(0, 0, -1), ChangePercent: .30}, OK: true},
		Calendar: market.DetailResult[[]market.MacroEvent]{Value: cal, OK: true},
		Fed:      market.DetailResult[[]market.MacroEvent]{Value: fed, OK: true},
		News:     market.DetailResult[[]market.NewsItem]{Value: news, OK: true},
	}
	return view, series, chartExtras{data: data, now: now, mouse: point{-1, -1}}
}

func TestChartDetailsRenderAndScrollAtDPI(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	view, series, fixture := chartDetailFixture(t)
	for _, dpi := range []float64{1, 1.25, 1.5, 2} {
		for _, language := range []string{locale.Chinese, locale.English} {
			t.Run(fmt.Sprintf("%g/%s", dpi, language), func(t *testing.T) {
				var r chartRaster
				defer r.close()
				w, h := int32(math.Round(chartWidth*dpi)), int32(math.Round(520*dpi))
				extra := fixture
				if err := r.render(w, h, dpi, language, false, view, series, &extra); err != nil {
					t.Fatal(err)
				}
				if r.scrollMax <= 0 || len(r.links) != 0 {
					t.Fatalf("expected a clipped scrollable content area: max=%d links=%v", r.scrollMax, r.links)
				}
				left, top := r.px(22), r.px(chartHeight)
				if c := r.pixels[(top+r.px(7)+r.px(29))*r.width+left]; c != 0xFF12171E {
					t.Fatalf("metric grid did not render, got %#x", c)
				}
				plot := rect{left, r.px(124), w - left, r.px(chartHeight) - r.px(chartCloseRow) - r.px(34)}
				marks := chartMarkers(fixture.data.Calendar.Value, series.points, plot, r.px(52), language)
				if len(marks) != 1 {
					t.Fatalf("expected a nonfarm anchor: %+v", marks)
				}
				dashes := 0
				for y := plot.Top; y < plot.Bottom; y++ {
					if r.pixels[y*r.width+marks[0].x] == 0xFF3A4859 {
						dashes++
					}
				}
				if dashes == 0 {
					t.Fatal("macro anchor did not render")
				}
				coloredClose := false
				for y := top - r.px(30); y < top-r.px(8); y++ {
					for x := left; x < w-left; x++ {
						coloredClose = coloredClose || r.pixels[y*r.width+x] == changeColor(.30, false)
					}
				}
				if !coloredClose {
					t.Fatal("last regular close change did not render below the plot")
				}
				extra.mouse = point{plot.Left + (plot.Right-plot.Left)/3, (plot.Top + plot.Bottom) / 2}
				pct, _ := chartSampleChange(series.points[2].Price, view.Quote.Previous())
				for _, pinned := range []bool{false, true} {
					if pinned {
						extra.selected = series.points[2].Time
						extra.mouse = point{-1, -1}
					}
					if err := r.render(w, h, dpi, language, false, view, series, &extra); err != nil {
						t.Fatal(err)
					}
					coloredSample := false
					priceHeight := top - r.px(chartCloseRow)
					for y := priceHeight - r.px(29); y < priceHeight-r.px(10); y++ {
						for x := plot.Right - r.px(60); x < plot.Right; x++ {
							coloredSample = coloredSample || r.pixels[y*r.width+x] == changeColor(pct, false)
						}
					}
					if !coloredSample {
						t.Fatalf("point percentage missing from the readout, pinned=%v", pinned)
					}
				}
				if dpi == 1 {
					saveChartPreview(t, "details-selection-"+language, &r.dibSurface, w)
				}

				extra.scroll = r.scrollMax + 1000
				if err := r.render(w, h, dpi, language, false, view, series, &extra); err != nil {
					t.Fatal(err)
				}
				if r.offset != r.scrollMax || len(r.links) != 5 {
					t.Fatalf("end of scrolled view: offset=%d/%d links=%v", r.offset, r.scrollMax, r.links)
				}
				hit := r.links[0]
				extra.mouse = point{hit.bounds.Left + 1, hit.bounds.Top + r.px(6)}
				if err := r.render(w, h, dpi, language, false, view, series, &extra); err != nil {
					t.Fatal(err)
				}
				if r.linkAt(extra.mouse) != fixture.data.News.Value[0].URL ||
					r.pixels[extra.mouse.Y*r.width+extra.mouse.X] != 0xFF1D2835 || r.linkAt(point{1, extra.mouse.Y}) != "" {
					t.Fatalf("wrong news hit or hover background: %+v", r.links)
				}
				if dpi == 1 {
					saveChartPreview(t, "details-scroll-"+language, &r.dibSurface, w)
				}
			})
		}
	}
}
