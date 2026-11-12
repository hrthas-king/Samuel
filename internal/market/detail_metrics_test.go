package market

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestYahooCardMetricsAndEarningsWindow(t *testing.T) {
	now := fixtureTime(t, "2026-11-06T16:00:00Z")
	stamp := now.Add(-time.Minute).Unix()
	start := fixtureTime(t, "2026-11-18T21:00:00Z")
	end := start.AddDate(0, 0, 4)
	q := Quote{Symbol: "NVDA", Currency: "USD", QuoteType: "EQUITY", UpdateMode: "yahoo_regular", TradeTime: stamp}
	body := fmt.Appendf(nil, `{"quoteResponse":{"result":[{"symbol":"NVDA","currency":"USD","quoteType":"EQUITY","regularMarketTime":%d,
		"regularMarketOpen":192.3,"regularMarketDayHigh":200,"regularMarketDayLow":190,"regularMarketPreviousClose":191.2,"regularMarketVolume":0,
		"fiftyTwoWeekHigh":210,"fiftyTwoWeekLow":155,"trailingPE":36,"forwardPE":23.3,"epsTrailingTwelveMonths":-1.23,
		"earningsTimestampStart":%d,"earningsTimestampEnd":%d,"isEarningsDateEstimate":true}]}}`, stamp, start.Unix(), end.Unix())
	m, ok := parseYahooMetrics(q, body, false, now)
	if !ok || m.Open == nil || *m.Open != 192.3 || m.Volume == nil || *m.Volume != 0 ||
		m.EPS == nil || *m.EPS != -1.23 || m.ForwardPE == nil || *m.ForwardPE != 23.3 || !m.SameQuoteDay(q) {
		t.Fatalf("metrics: %+v (valid %v)", m, ok)
	}
	if !m.Earnings.Estimated || m.Earnings.Session != "" || m.Earnings.Start.Day() != 18 || m.Earnings.End.Day() != 22 {
		t.Fatalf("earnings window: %+v", m.Earnings)
	}
	if date := yahooEarnings(yahooMetricFields{EarningsTimestamp: start.Unix(), EarningsReleaseTime: "AMC"}, now); date.Session != StatePostMarket {
		t.Fatal("explicit release session was dropped")
	}
	q.Symbol = "AAPL"
	if _, ok := parseYahooMetrics(q, body, false, now); ok {
		t.Fatal("accepted an unrelated instrument")
	}
	q.Symbol, q.TradeTime = "NVDA", now.AddDate(0, 0, 1).Unix()
	if m.SameQuoteDay(q) {
		t.Fatal("prior day metrics would appear as current")
	}
}

func TestTencentCardMetricsUnitsAndIdentity(t *testing.T) {
	now := fixtureTime(t, "2026-11-06T08:15:00Z")
	var parts [69]string
	parts[2], parts[4], parts[5], parts[30] = "600519", "1452", "1450", "20261106161458"
	parts[33], parts[34], parts[35], parts[36], parts[37], parts[39] = "1457.72", "1436", "1453/42848/6199528289", "42848", "619953", "22.11"
	q := Quote{Symbol: "600519.SS", Currency: "CNY", QuoteType: "EQUITY", TradeTime: now.Unix()}
	m, ok := parseTencentMetrics(q, "sh600519", []byte(`v_sh600519="`+strings.Join(parts[:], "~")+`";`), now)
	if !ok || !m.VolumeLots || m.Volume == nil || *m.Volume != 42848 || m.Turnover == nil || *m.Turnover != 6199528289 ||
		m.Amplitude() == nil || *m.Amplitude() < 1.49 || *m.Amplitude() > 1.50 || m.ForwardPE != nil {
		t.Fatalf("Tencent metric units: %+v (%v)", m, ok)
	}
	parts[2] = "600520"
	if _, ok := parseTencentMetrics(q, "sh600519", []byte(`v_sh600519="`+strings.Join(parts[:], "~")+`";`), now); ok {
		t.Fatal("accepted mismatched security code")
	}
}

func TestMainlandEarningsAndEPSUseReportDates(t *testing.T) {
	now := fixtureTime(t, "2026-11-06T15:00:00Z")
	body := []byte(`{"result":{"data":[
		{"SECUCODE":"600519.SH","IS_PUBLISH":"1","ACTUAL_PUBLISH_DATE":"2026-10-31 00:00:00"},
		{"SECUCODE":"600519.SH","IS_PUBLISH":"0","APPOINT_PUBLISH_DATE":"2026-11-20 00:00:00"},
		{"SECUCODE":"600519.SH","IS_PUBLISH":"0","APPOINT_PUBLISH_DATE":"2026-10-15 00:00:00"},
		{"SECUCODE":"600519.SZ","IS_PUBLISH":"0","APPOINT_PUBLISH_DATE":"2026-11-08 00:00:00"}]}}`)
	e := parseMainlandEarnings("600519.SS", body, now)
	if e.Reported || !e.Estimated || e.Start.Format("2006-01-02") != "2026-11-20" || e.Session != "" {
		t.Fatalf("incorrect latest appointment: %+v", e)
	}
	e = parseMainlandEarnings("600519.SS", body, now.AddDate(0, 0, 30))
	if !e.Reported || e.Start.Format("2006-01-02") != "2026-10-31" {
		t.Fatalf("past appointment was treated as publication: %+v", e)
	}
	eps, period := parseMainlandEPS("600519.SS", []byte(`{"result":{"data":[
		{"SECUCODE":"600519.SH","BASIC_EPS":80,"REPORTDATE":"2026-12-31","NOTICE_DATE":"2027-02-05"},
		{"SECUCODE":"600519.SH","BASIC_EPS":51.53,"REPORTDATE":"2026-09-30","NOTICE_DATE":"2026-10-31"}]}}`), now)
	if eps == nil || *eps != 51.53 || period.Format("2006-01-02") != "2026-09-30" {
		t.Fatalf("invalid EPS/report date: %v %v", eps, period)
	}
}

func TestTencentYearRangeUsesOnlyCompleteUnadjustedDays(t *testing.T) {
	to := marketDate(fixtureTime(t, "2026-11-06T08:00:00Z").In(chinaMarketZone))
	from := to.AddDate(0, 0, -52*7)
	var rows [][]string
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Sunday && day.Weekday() != time.Saturday {
			rows = append(rows, []string{day.Format("2006-01-02"), "10", "11", "12", "9", "100"})
		}
	}
	rows = append(rows,
		[]string{from.AddDate(0, 0, -1).Format("2006-01-02"), "10", "11", "1000", "1"},
		[]string{to.AddDate(0, 0, 1).Format("2006-01-02"), "10", "11", "2000", "1"})
	wrap := func(key string, rows [][]string) []byte {
		body, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{key: map[string]any{"day": rows}}})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	got, ok := parseTencentYearRange("sz159632", wrap("sz159632", rows), from, to, to.Add(16*time.Hour))
	if !ok || got.High != 12 || got.Low != 9 || got.AsOf != "2026-11-06" {
		t.Fatalf("incorrect 52-week range: %+v", got)
	}
	if _, ok := parseTencentYearRange("sz159632", wrap("sz159655", rows), from, to, to.Add(16*time.Hour)); ok {
		t.Fatal("accepted another fund's range")
	}
	if _, ok := parseTencentYearRange("sz159632", wrap("sz159632", rows[:50]), from, to, to.Add(16*time.Hour)); ok {
		t.Fatal("incomplete history was presented as 52 weeks")
	}
}
