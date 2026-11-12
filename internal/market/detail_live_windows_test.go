//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveDetailFeeds(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_DETAIL_FEEDS") != "1" {
		t.Skip("opt-in checks for public WinHTTP feeds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	t.Run("economic calendar", func(t *testing.T) {
		data, ok := fetchLiveCalendar(ctx)
		if !ok || len(data) == 0 {
			t.Fatal("no matched high-impact events")
		}
		actual := 0
		for _, event := range data {
			if event.Actual != "" {
				actual++
			}
		}
		t.Logf("%s: %d matched events, %d actual values", data[0].Source, len(data), actual)
	})
	t.Run("official Fed", func(t *testing.T) {
		data, ok := fetchFedCalendar(ctx)
		picks := MacroMilestones("QQQ", "ETF", data, time.Now())
		if !ok || len(picks) == 0 || picks[len(picks)-1].Kind != MacroFOMC {
			t.Fatal("official calendar has no next decision")
		}
		t.Log(picks[len(picks)-1].At.In(newYorkZone))
	})
	t.Run("official EIA", func(t *testing.T) {
		data, ok := fetchEnergyCalendar(ctx)
		if !ok || len(data) == 0 {
			t.Fatal("official EIA schedule unavailable")
		}
		t.Log(data[0].At.In(newYorkZone))
	})
	t.Run("VOO regular close", func(t *testing.T) {
		q := Quote{Symbol: "VOO", Currency: "USD", QuoteType: "ETF"}
		close, ok := fetchYahooRegularClose(ctx, q)
		if !ok || close.Date.IsZero() {
			t.Fatal("verified completed regular close unavailable")
		}
		t.Logf("VOO completed regular session %s: %+.2f%%", close.Date.Format("2006-01-02"), close.ChangePercent)
	})
	for _, q := range []Quote{
		{Symbol: "NVDA", Currency: "USD", QuoteType: "EQUITY"},
		{Symbol: "159632.SZ", Currency: "CNY", QuoteType: "ETF"},
	} {
		t.Run(q.Symbol, func(t *testing.T) {
			provider := "Tencent"
			if q.Currency == "USD" {
				provider = "Yahoo"
			}
			data, ok := fetchDetailMetrics(ctx, q, provider)
			if !ok || data.High == nil || data.Open == nil {
				t.Fatalf("verified opening or high price unavailable: %+v", data)
			}
			t.Logf("%s open/high confirmed; PE=%t EPS=%t 52W=%t earnings=%s",
				data.Source, data.TrailingPE != nil, data.EPS != nil, data.YearHigh != nil, data.Earnings.Start.Format("2006-01-02"))
			if provider == "Tencent" {
				q.TradeTime = data.At
				r, ok := fetchTencentYearRange(ctx, q.Symbol, marketDate(QuoteMarketTime(q)))
				if !ok || r.Low <= 0 || r.High < r.Low {
					t.Fatalf("unadjusted 52-week series unavailable: %+v", r)
				}
				t.Logf("unadjusted 52-week range through %s", r.AsOf)
			}
		})
	}
	t.Run("mainland disclosure", func(t *testing.T) {
		data, ok := fetchMainlandFundamentals(ctx, "600519.SS")
		if !ok || data.EPS == nil || data.Earnings.Start.IsZero() {
			t.Fatalf("EPS or earnings date unavailable: %+v", data)
		}
		t.Logf("EPS period %s, earnings date %s", data.EPSAsOf.Format("2006-01-02"), data.Earnings.Start.Format("2006-01-02"))
	})
	for _, language := range []string{"en", "zh-CN"} {
		t.Run("news/"+language, func(t *testing.T) {
			data, ok := fetchDetailNews(ctx, "QQQ", "QQQ", "ETF", language)
			if !ok || len(data) == 0 {
				t.Fatal("no safe articles")
			}
			for _, item := range data {
				if SecureNewsURL(item.URL) == "" {
					t.Fatal("unsafe article link")
				}
			}
			t.Logf("%d verified HTTPS articles", len(data))
		})
	}
}
