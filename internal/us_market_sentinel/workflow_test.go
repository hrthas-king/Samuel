//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureFeed struct {
	mu       sync.Mutex
	requests map[string]int
	dates    []string
	now      time.Time
}

func (p *fixtureFeed) count(key string) int { p.mu.Lock(); defer p.mu.Unlock(); return p.requests[key] }
func (p *fixtureFeed) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Hostname()
	if strings.Contains(key, "finance.yahoo") {
		key = filepath.Base(req.URL.Path)
	}
	if key == "api.stlouisfed.org" {
		key = req.URL.Query().Get("series_id")
	}
	p.mu.Lock()
	p.requests[key]++
	p.mu.Unlock()
	if slices.Contains(priceSymbols, key) {
		closes := make([]*float64, len(p.dates))
		for i := range closes {
			closes[i] = number(100 + float64(i))
		}
		return reply(200, dailyFixture(key, p.dates, closes, 0, 0)), nil
	}
	if slices.Contains(fredSymbols, key) {
		if req.URL.Query().Get("api_key") != "mock-fred-key" {
			return reply(400, ""), nil
		}
		rows := []map[string]string{}
		for i, d := range p.dates {
			v := 3 + float64(i)/1000
			switch key {
			case "WALCL":
				v = 7000000
			case "WTREGEN":
				v = 600000
			case "RRPONTSYD":
				v = 30
			}
			rows = append(rows, map[string]string{"date": d, "value": fmt.Sprint(v)})
		}
		b, _ := json.Marshal(map[string]any{"observations": rows})
		return reply(200, string(b)), nil
	}
	switch key {
	case "production.dataviz.cnn.io":
		published, _ := time.ParseInLocation(dayFormat, p.dates[len(p.dates)-1], ny)
		return reply(200, cnnFixture(published.Add(16*time.Hour), p.dates)), nil
	case "economic-calendar.tradingview.com":
		return reply(200, `{"status":"ok","result":[]}`), nil
	case "api.nasdaq.com":
		return reply(200, `{"data":{"rows":[]},"status":{"rCode":200}}`), nil
	case "www.federalreserve.gov", "search.cnbc.com", "feeds.a.dj.com":
		body := fmt.Sprintf(`<rss><channel><item><title>Same headline</title><description>&lt;b&gt;Confirmed&lt;/b&gt; news.</description><pubDate>%s</pubDate></item></channel></rss>`, p.now.Add(-time.Hour).Format(time.RFC1123Z))
		return reply(200, body), nil
	default:
		return reply(503, "unavailable"), nil
	}
}
func TestCompleteRunCacheTTLAndStyleHistory(t *testing.T) {
	dates := tradingDates("2023-04-03", 540)
	end, _ := time.ParseInLocation(dayFormat, dates[len(dates)-1], ny)
	now := end.AddDate(0, 0, 1).Add(time.Hour)
	p := &fixtureFeed{requests: map[string]int{}, dates: dates, now: now}
	dir := t.TempDir()
	f := Fetcher{Client: &http.Client{Transport: p}, Dir: dir, Now: now, FREDKey: "mock-fred-key"}
	local := []Event{{now.Add(2 * time.Hour).Format(time.RFC3339), "本地 FOMC", "high", "offline fixture"}}
	if err := atomicJSON(filepath.Join(dir, "calendar.json"), local); err != nil {
		t.Fatal(err)
	}
	model := modelFunc(func(ctx context.Context, in Input, _ string) ([]byte, error) {
		if in.Coverage < .999 || in.Market.CNN.Level == nil || len(in.News) != 1 || len(in.Events) != 1 || in.News[0].Summary != "Confirmed news." {
			t.Fatal("bad input", in.Coverage, in.News, in.Events)
		}
		return json.Marshal(modelReport(in))
	})
	ctx := context.Background()
	r, err := runToday(ctx, f, Left, model)
	if err != nil || r.Mode != "llm" || r.Delta != nil {
		t.Fatal(r, err)
	}
	f.Now = f.Now.Add(time.Minute)
	second, err := runToday(ctx, f, Left, model)
	if err != nil || second.Delta == nil || *second.Delta != 0 {
		t.Fatal(second, err)
	}
	other, err := runToday(ctx, f, Right, model)
	if err != nil || other.Delta != nil {
		t.Fatal("strategy histories mixed", err)
	}
	if p.count("^GSPC") != 1 || p.count("DFF") != 1 || p.count("production.dataviz.cnn.io") != 1 {
		t.Fatal("cache misses", p.requests)
	}
	store := Store{dir}
	paths, _ := store.paths()
	if len(paths) != 3 {
		t.Fatal(paths)
	}
	row, err := readSnapshot(paths[0])
	if err != nil || row.SPXClose == nil || row.NDXClose == nil || row.SPX20 != nil || row.Raw.Sources["DFF"] != "FRED / DFF" {
		t.Fatal(row, err)
	}
	body, _ := os.ReadFile(paths[0])
	if row.Raw.Market.CNN.PublishedAt == nil || row.Raw.Sources["CNN_FEAR_GREED"] != cnnSource ||
		row.Final.FearIndices == nil || row.Final.FearIndices.VIX.Level == nil || row.Final.FearIndices.CNN.Percentile == nil ||
		!strings.Contains(Brief(row.Final), "100.00/100") {
		t.Fatal("sentiment missing from snapshot or report", row.Final.FearIndices)
	}
	if obs := row.Raw.Observations["WALCL"]; obs.Unit != "million USD" || obs.Date == "" || obs.Value == nil || *obs.Value != 7000000 {
		t.Fatal(obs)
	}
	if strings.Contains(string(body), "mock-fred-key") {
		t.Fatal("credential persisted")
	}
	f.Now = f.Now.Add(16 * time.Minute)
	start := dates[0]
	f.loadSeries(ctx, "^GSPC", start, false)
	f.loadSeries(ctx, "DFF", start, true)
	if p.count("^GSPC") != 2 || p.count("DFF") != 1 {
		t.Fatal("independent TTLs", p.requests)
	}
	f.Now = f.Now.Add(12 * time.Hour)
	f.loadSeries(ctx, "DFF", start, true)
	if p.count("DFF") != 2 {
		t.Fatal("FRED TTL")
	}
	f.Force = true
	f.loadSeries(ctx, "DFF", start, true)
	if p.count("DFF") != 3 {
		t.Fatal("force")
	}
	f.FREDKey = ""
	s, _, warn := f.loadSeries(ctx, "DFF", start, true)
	if s != nil || warn == "" || p.count("DFF") != 3 {
		t.Fatal("no API key")
	}
}
func TestRSSCalendarWindowsAndDuplicates(t *testing.T) {
	now := time.Date(2025, 3, 8, 12, 0, 0, 0, ny)
	rss := fmt.Sprintf(`<rdf:RDF xmlns:rdf="urn:rdf" xmlns:dc="urn:dc"><item><title>&lt;b&gt;Policy &amp;amp; rates&lt;/b&gt;</title><link>https://www.federalreserve.gov/newsevents/pressreleases/test.htm</link><dc:date>%s</dc:date></item><item><title>Unsafe link</title><link>javascript:alert(1)</link><dc:date>%s</dc:date></item><item><title>Old</title><dc:date>%s</dc:date></item><item><title>Future</title><dc:date>%s</dc:date></item></rdf:RDF>`, now.Add(-48*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339), now.Add(-49*time.Hour).Format(time.RFC3339), now.Add(time.Second).Format(time.RFC3339))
	h, err := parseRSS([]byte(rss), "Fed", now)
	if err != nil || len(h) != 2 || h[0].Title != "Policy & rates" || h[0].URL != "https://www.federalreserve.gov/newsevents/pressreleases/test.htm" || h[1].URL != "" {
		t.Fatal(h, err)
	}
	var extra []Headline
	for i := range 25 {
		extra = append(extra, Headline{Title: fmt.Sprint(i), Published: now.Add(-time.Duration(i) * time.Hour)})
	}
	fresh := latestNews(append(extra, extra...))
	if len(fresh) != 20 || fresh[0].Title != "0" || fresh[19].Title != "19" {
		t.Fatal(fresh)
	}
	raw := []byte(`{"status":{"rCode":200},"data":{"rows":[{"symbol":"GOOGL"},{"symbol":"GOOG"},{"symbol":"MSFT"},{"symbol":"OTHER"}]}}`)
	events, err := parseEarnings(raw, "2025-03-09")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, Event{"2025-03-08", "未注明时刻", "high", "local"}, Event{"2025-03-08T09:00:00-05:00", "已公布", "high", ""}, Event{"bad", "bad", "critical", ""}, Event{now.Add(72*time.Hour + time.Second).Format(time.RFC3339), "later", "low", ""})
	all, invalid := normalizeEvents(events, now)
	if len(all) != 3 || invalid != 1 {
		t.Fatal(all, invalid)
	}
	a, b, _ := all[1].window()
	if b.Sub(a)+time.Nanosecond != 23*time.Hour {
		t.Fatal("DST date-only window", a, b)
	}
}
func TestCalibrationPartialMaturityAndStatistics(t *testing.T) {
	dates := tradingDates("2025-05-01", 72)
	// Exclude a real exchange holiday from the fixture calendar.
	dates = slices.DeleteFunc(dates, func(d string) bool { return d == "2025-05-26" })
	data := Dataset{"^GSPC": {}, "^NDX": {}}
	for i, d := range dates {
		data["^GSPC"][d] = number(100 + float64(i))
		data["^NDX"][d] = number(200 - float64(i))
	}
	issue, _ := time.ParseInLocation(dayFormat, "2025-05-03", ny) // Saturday; Friday is the observed baseline.
	s := Snapshot{Timestamp: issue, SPXDate: dates[1], NDXDate: dates[1], SPXClose: number(101), NDXClose: number(199), Final: Result{Report: Report{Signal: "accumulate"}}}
	// The 5th session after Saturday has a null NDX close, but later
	// horizons must stay at their original positions on the shared calendar.
	data["^NDX"][dates[6]] = nil
	now, _ := time.ParseInLocation(dayFormat, dates[21], ny)
	if !fillReturns(&s, data, dates, now) || s.SPX60 != nil || s.NDX5 != nil || s.Calibrated == nil {
		t.Fatal(s.Outcomes)
	}
	near(t, s.SPX5, 100*5.0/101, 1e-8)
	near(t, s.SPX20, 100*20.0/101, 1e-8)
	near(t, s.NDX20, -100*20.0/199, 1e-8)
	if fillReturns(&s, data, dates, now) {
		t.Fatal("non-idempotent")
	}
	data["^NDX"][dates[6]] = number(194)
	mature, _ := time.ParseInLocation(dayFormat, dates[65], ny)
	if !fillReturns(&s, data, dates, mature) || s.pending() {
		t.Fatal("missing horizon")
	}
	near(t, s.SPX60, 100*60.0/101, 1e-8)
	near(t, s.NDX60, -100*60.0/199, 1e-8)
	second := s
	second.SPX20 = number(-2)
	second.NDX20 = nil
	group := summarize([]Snapshot{s, second})[0]
	if group.Samples != 2 || group.SPX["20d"].Samples != 2 || group.NDX["20d"].Samples != 1 {
		t.Fatal(group)
	}
	near(t, group.SPX["20d"].WinRate, 50, 0)
	near(t, group.NDX["20d"].WinRate, 0, 0)
	near(t, group.SPX["20d"].Mean, (*s.SPX20-2)/2, 1e-8)
	tooShort := s
	tooShort.SPX5 = nil
	if fillReturns(&tooShort, Dataset{"^GSPC": {dates[6]: number(106)}}, dates, mature) {
		t.Fatal("uncovered historical date")
	}
}
func TestCalibrationCommandReopensHistory(t *testing.T) {
	dates := tradingDates("2024-06-03", 85)
	first, _ := time.ParseInLocation(dayFormat, dates[0], ny)
	last, _ := time.ParseInLocation(dayFormat, dates[len(dates)-1], ny)
	p := &fixtureFeed{requests: map[string]int{}, dates: dates, now: last}
	dir := t.TempDir()
	f := Fetcher{Client: &http.Client{Transport: p}, Dir: dir, Now: last.AddDate(0, 0, 1)}
	in := scenario(0)
	in.AsOf = first.Add(20 * time.Hour)
	in.Market.SPX["close"] = Metric{Date: dates[0], Level: number(100)}
	if err := (Store{dir}).save(in, applyGuardrails(in, modelReport(in), "llm")); err != nil {
		t.Fatal(err)
	}
	out, err := calibrate(context.Background(), f)
	if err != nil || out.Total != 1 || out.Updated != 1 {
		t.Fatal(out, err)
	}
	near(t, out.Groups[0].SPX["20d"].Mean, 20, 1e-8)
	out, err = calibrate(context.Background(), f)
	if err != nil || out.Updated != 0 || p.count("^GSPC") != 1 || p.count("production.dataviz.cnn.io") != 0 || p.count("raw.githubusercontent.com") != 0 {
		t.Fatal(out, err)
	}
}
