package market

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func yahooCloseFixture(start, end, lastQuote time.Time, todayClose string) []byte {
	return fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"VOO","currency":"USD","instrumentType":"ETF","dataGranularity":"1d","regularMarketTime":%d,
		"currentTradingPeriod":{"regular":{"start":%d,"end":%d}}},"timestamp":[%d,%d,%d],"indicators":{"quote":[{"close":[704.78,673.97,%s]}]}}]}}`,
		lastQuote.Unix(), start.Unix(), end.Unix(), start.AddDate(0, 0, -2).Unix(), start.AddDate(0, 0, -1).Unix(), start.Unix(), todayClose)
}

func TestYahooRegularCloseExcludesActiveAndUnconfirmedSessions(t *testing.T) {
	open := fixtureTime(t, "2026-11-06T14:30:00Z")
	closeAt := fixtureTime(t, "2026-11-06T21:00:00Z")
	q := Quote{Symbol: "VOO", Currency: "USD", QuoteType: "ETF", ChangePercent: .08, Price: 676.50, ChangeBase: 675.97}
	tests := []struct {
		name   string
		last   time.Time
		now    time.Time
		date   string
		change float64
	}{
		{"during regular trading", closeAt.Add(-time.Hour), closeAt.Add(-time.Hour), "2026-11-05", (673.97 - 704.78) / 704.78 * 100},
		{"after close with delayed quote", closeAt.Add(-time.Minute), closeAt.Add(time.Minute), "2026-11-05", (673.97 - 704.78) / 704.78 * 100},
		{"after close with confirmed closing quote", closeAt, closeAt.Add(25 * time.Minute), "2026-11-06", (675.97 - 673.97) / 673.97 * 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := yahooCloseFixture(open, closeAt, tt.last, "675.97")
			r, ok := parseYahooRegularClose(q, data, tt.now)
			if !ok || r.Date.Format("2006-01-02") != tt.date || math.Abs(r.ChangePercent-tt.change) > 1e-8 {
				t.Fatalf("completed close: %+v (%v), want %s %.8f%%", r, ok, tt.date, tt.change)
			}
		})
	}
	if r, ok := parseYahooRegularClose(q, yahooCloseFixture(open, closeAt, closeAt, "null"), closeAt.Add(time.Minute)); ok {
		t.Fatalf("missing closing price yielded %+v", r)
	}
}

func TestYahooRegularCloseFollowsProviderEarlyClose(t *testing.T) {
	open := fixtureTime(t, "2026-11-27T14:30:00Z")
	closeAt := fixtureTime(t, "2026-11-27T18:00:00Z") // 13:00 ET early close.
	q := Quote{Symbol: "VOO", Currency: "USD", QuoteType: "ETF"}
	r, ok := parseYahooRegularClose(q, yahooCloseFixture(open, closeAt, closeAt, "673.97"), closeAt.Add(time.Minute))
	if !ok || r.Date.Day() != 27 || r.ChangePercent != 0 {
		t.Fatalf("early-close zero change was not retained: %+v (%v)", r, ok)
	}
	q.Symbol = "QQQ"
	if _, ok := parseYahooRegularClose(q, yahooCloseFixture(open, closeAt, closeAt, "673.97"), closeAt.Add(time.Minute)); ok {
		t.Fatal("accepted another symbol's close")
	}
}

func TestTencentRegularCloseWaitsForHKClosingAuction(t *testing.T) {
	body := []byte(`{"code":0,"data":{"hkHSI":{"day":[["2026-11-04","100","100","100","100"],["2026-11-05","110","110","110","110"],["2026-11-06","121","121","121","121"]]}}}`)
	today := time.Date(2026, 11, 6, 0, 0, 0, 0, chinaMarketZone)
	from := today.AddDate(0, 0, -7)
	for _, tt := range []struct {
		elapsed time.Duration
		day     int
	}{{12 * time.Hour, 5}, {16*time.Hour + 9*time.Minute, 5}, {16*time.Hour + 11*time.Minute, 6}} {
		year, ok := parseTencentYearRange("hkHSI", body, from, today, today.Add(tt.elapsed))
		if ok || year.Regular.Date.Day() != tt.day || math.Abs(year.Regular.ChangePercent-10) > 1e-8 {
			t.Fatalf("limited daily data at %s: %+v (%v)", tt.elapsed, year, ok)
		}
	}
}
