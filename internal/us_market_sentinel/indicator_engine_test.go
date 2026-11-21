//go:build darwin

package sentinel

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func tradingDates(first string, n int) []string {
	day, _ := time.ParseInLocation(dayFormat, first, ny)
	dates := make([]string, 0, n)
	for len(dates) < n {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			dates = append(dates, day.Format(dayFormat))
		}
		day = day.AddDate(0, 0, 1)
	}
	return dates
}
func TestForwardFillAndUnits(t *testing.T) {
	data := Dataset{"WALCL": {"2025-01-02": number(7000000), "2025-01-09": number(7100000)}, "WTREGEN": {"2025-01-03": number(700000)}, "RRPONTSYD": {"2025-01-02": number(300), "2025-01-06": number(310), "2025-01-10": number(320)}}
	days, v := liquidity(data, "2025-01-10")
	if len(v) != 7 || days[0] != "2025-01-02" || v[0] != nil || days[2] != "2025-01-06" {
		t.Fatal(days, v)
	}
	for i, w := range []float64{6000, 5990, 5990, 5990, 6090, 6080} {
		near(t, v[i+1], w, 1e-9)
	}
	_, before := liquidity(data, "2025-01-07")
	near(t, last(before), 5990, 1e-9)
	delete(data, "WTREGEN")
	_, v = liquidity(data, "2025-01-10")
	if last(v) != nil {
		t.Fatal("missing component treated as zero")
	}
}
func TestMarketAndMacroProfiles(t *testing.T) {
	dates := tradingDates("2023-01-02", 540)
	data := Dataset{}
	for _, k := range append(append([]string{}, priceSymbols...), fredSymbols...) {
		data[k] = Series{}
	}
	for i, d := range dates {
		x := float64(i)
		for _, k := range []string{"^GSPC", "^NDX", "RSP", "SPY"} {
			data[k][d] = number(100 + x)
		}
		data["^VIX"][d], data["^VIX3M"][d] = number(1000-x), number(700)
		data["DGS2"][d], data["DFF"][d] = number(3+x/1000), number(4)
		data["DFII10"][d], data["BAMLH0A0HYM2"][d] = number(2+x/1000), number(3+x/1000)
		data["WALCL"][d], data["WTREGEN"][d], data["RRPONTSYD"][d] = number(7000000+x*1000), number(700000), number(300)
	}
	end := dates[len(dates)-1]
	m := marketIndicators(data, end)
	if !yes(m.VIXDeclining) || !yes(m.Reclaimed) || yes(m.Divergence) {
		t.Fatal("confirmation", m)
	}
	near(t, m.SPX["close"].Level, 639, 0)
	near(t, m.SPX["close"].Delta, 20, 0)
	near(t, m.SPX["close"].Percentile, 100, 0)
	near(t, m.SPX["rsi_14"].Level, 100, 1e-9)
	near(t, m.NDX["dist_to_200ma_pct"].Level, (639/539.5-1)*100, 1e-9)
	near(t, m.Measures["vix_1y_pct"].Level, 0, 0)
	near(t, m.Measures["rsp_spy_ratio"].Percentile, 50, 0)
	near(t, m.Measures["vix_chg_5d_pct"].Level, (461.0/466-1)*100, 1e-9)
	near(t, m.Measures["vix_vix3m_ratio"].Level, 461.0/700, 1e-9)
	f := macroIndicators(data, end)
	for k, w := range map[string]float64{"us2y_minus_dff_bps": -46.1, "us2y_minus_dff_chg_20d_bps": 2, "us10y_tips_chg_20d_bps": 2, "hy_oas_chg_20d_bps": 2, "net_liquidity_chg_4w_billions": 20, "net_liquidity_chg_12w_billions": 60} {
		near(t, f.Measures[k].Level, w, 1e-8)
	}
	if f.Breaker {
		t.Fatal("unexpected breaker")
	}
	data["RSP"][end] = number(600)
	if !yes(marketIndicators(data, end).Divergence) {
		t.Fatal("top divergence")
	}
	data["^GSPC"][end] = nil
	if m := marketIndicators(data, end); m.SPX["close"].Level != nil || m.NDX["close"].Level == nil {
		t.Fatal("missing one index")
	}
	// A delayed ETF must use its last pair of synchronous observations.
	delete(data["RSP"], end)
	if m := marketIndicators(data, end); m.Measures["rsp_spy_ratio"].Date != dates[len(dates)-2] || m.NDX["close"].Date != end {
		t.Fatal("as-of date mismatch")
	}
}
func TestRsiTiesAndGaps(t *testing.T) {
	flat := vector{}
	for range 260 {
		flat = append(flat, number(100))
	}
	near(t, last(rsi14(flat)), 50, 0)
	near(t, last(ranks(flat)), 50, 0)
	flat[254] = nil
	if last(rsi14(flat)) != nil || last(ranks(flat)) != nil || last(maDistance(flat, 10)) != nil {
		t.Fatal("gap filled")
	}
}
func dailyFixture(symbol string, dates []string, closes []*float64, periodEnd, trade int64) string {
	var times []string
	var values []string
	for i, d := range dates {
		day, _ := time.ParseInLocation(dayFormat, d, ny)
		times = append(times, fmt.Sprint(day.Add(9*time.Hour+30*time.Minute).Unix()))
		p := closes[i]
		if p == nil {
			values = append(values, "null")
		} else {
			values = append(values, fmt.Sprint(*p))
		}
	}
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{"symbol":%q,"dataGranularity":"1d","regularMarketTime":%d,"currentTradingPeriod":{"regular":{"start":%d,"end":%d}}},"timestamp":[%s],"indicators":{"quote":[{"close":[%s]}]}}]}}`, symbol, trade, periodEnd-12600, periodEnd, strings.Join(times, ","), strings.Join(values, ","))
}
func TestDailyCandlesAndOfficialFallback(t *testing.T) {
	now := time.Date(2025, 11, 28, 13, 5, 0, 0, ny)
	close := now.Add(-5 * time.Minute).Unix()
	dates := []string{"2025-11-24", "2025-11-25", "2025-11-26", "2025-11-28"}
	quotes := []*float64{number(98), nil, number(100), number(101)}
	fetch := func(trade int64) Series {
		s, err := parseDaily([]byte(dailyFixture("^GSPC", dates, quotes, close, trade)), "^GSPC", now, "2025-01-01")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fetch(close - 1)
	if len(before) != 3 || before[dates[1]] != nil {
		t.Fatal(before)
	}
	after := fetch(close)
	near(t, after[dates[3]], 101, 0)
	fred, err := publishedCloses([]byte("DATE,SPX\n11/26/2025,123\n11/28/2025,124\n"), "SPX", dates[0], dates[3])
	if err != nil || len(fred) != 2 {
		t.Fatal(fred, err)
	}
	if _, err = publishedCloses([]byte("date,NASDAQ100\n2025-11-26,1"), "SP500", dates[0], dates[3]); err == nil {
		t.Fatal("wrong series")
	}
	etf, err := nasdaqCloses([]byte(`{"data":{"symbol":"SPY","tradesTable":{"rows":[{"date":"11/26/2025","close":"$1,100.50"},{"date":"11/28/2025","close":"$5.50"}]}}}`), "SPY", dates[0], dates[3])
	if err != nil || len(etf) != 1 {
		t.Fatal(etf, err)
	}
	near(t, etf[dates[2]], 1100.5, 0)
	vals, err := parseFRED([]byte(`{"observations":[{"date":"2025-11-25","value":"."},{"date":"2025-11-26","value":"NaN"},{"date":"2025-11-28","value":"-0.25"},{"date":"2025-11-29","value":"45"}]}`), dates[0], dates[3])
	if err != nil || len(vals) != 1 {
		t.Fatal(vals, err)
	}
	near(t, vals[dates[3]], -.25, 0)
	if !sort.StringsAreSorted(datesFor(Dataset{"k": after}, []string{"k"}, dates[3])) {
		t.Fatal("date order")
	}
}
