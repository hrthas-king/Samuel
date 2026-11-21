//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The vendor labels historical sessions at 00:00 UTC and publishes its live
// headline as an offset timestamp. Fixture values span the entire 0–100 scale.
func cnnFixture(published time.Time, dates []string) string {
	rows := make([]map[string]any, 0, len(dates))
	for i, day := range dates {
		d, _ := time.Parse(dayFormat, day)
		value := 100.0
		if len(dates) > 1 {
			value = 100 * float64(i) / float64(len(dates)-1)
		}
		rows = append(rows, map[string]any{"x": json.Number(fmt.Sprintf("%d.0", d.UnixMilli())), "y": value})
	}
	raw, _ := json.Marshal(map[string]any{
		"fear_and_greed":            map[string]any{"score": 100, "rating": "extreme greed", "timestamp": published},
		"fear_and_greed_historical": map[string]any{"data": rows},
	})
	return string(raw)
}

func TestFearScoreDirectionCoverageAndFallback(t *testing.T) {
	in := scenario(0)
	before := computeQuantBaseScore(in.Market, in.Macro)
	near(t, before.D1, 8.65, 1e-8)
	near(t, &before.Coverage, .956, 1e-10)
	in.Market.CNN.Level = number(0)
	fear := computeQuantBaseScore(in.Market, in.Macro)
	near(t, fear.D1, 8.65, 1e-8)
	near(t, &fear.Coverage, 1, 1e-10)
	in.Market.CNN.Level = number(100)
	greed := computeQuantBaseScore(in.Market, in.Macro)
	near(t, greed.D1, 19.65, 1e-8)
	if fear.Base != 20 || greed.Base != 24 {
		t.Fatal(fear, greed)
	}
	m := MarketData{Measures: measures(map[string]float64{"vix_1y_pct": 95})}
	vixOnly := computeQuantBaseScore(m, MacroData{})
	near(t, vixOnly.D1, 0, 0)
	near(t, &vixOnly.Coverage, .044, 1e-10)
	m.Measures["vix_1y_pct"] = Metric{Level: number(5)}
	near(t, computeQuantBaseScore(m, MacroData{}).D1, 100, 0)
	m.Measures = nil
	m.CNN.Level = number(19)
	cnnOnly := computeQuantBaseScore(m, MacroData{})
	near(t, cnnOnly.D1, 19, 0)
	near(t, &cnnOnly.Coverage, .044, 1e-10)
	m.CNN.Level = nil
	if absent := computeQuantBaseScore(m, MacroData{}); absent.D1 != nil || absent.Coverage != 0 || absent.Base != 50 {
		t.Fatal(absent)
	}
}

func TestCNNHistoricalLabelsAndLookbacks(t *testing.T) {
	days := tradingDates("2024-03-01", 252)
	last, _ := time.ParseInLocation(dayFormat, days[len(days)-1], ny)
	published := last.Add(17 * time.Hour)
	s, err := parseCNN([]byte(cnnFixture(published, days)), published.AddDate(0, 0, 1))
	if err != nil || len(s.History) != len(days) || s.History[days[0]] == nil || s.Rating != "extreme greed" {
		t.Fatal("dates or current rating", err, len(s.History))
	}
	r, err := s.reading(published, days, days[len(days)-1])
	if err != nil || r.Date != days[len(days)-1] || r.PublishedAt == nil || !r.PublishedAt.Equal(published) {
		t.Fatal(r, err)
	}
	if r.RatingZH != "极度贪婪" {
		t.Fatal(r.RatingZH)
	}
	near(t, r.Delta, 100*20.0/251, 1e-9)
	near(t, r.Percentile, 100, 0)
	delete(s.History, days[len(days)-21])
	r, _ = s.reading(published, days, days[len(days)-1])
	if r.Delta != nil || r.Percentile != nil || r.Level == nil {
		t.Fatal("an unreported session was filled or skipped", r)
	}
	// Today's headline can arrive while the indices still report yesterday's close.
	r, _ = s.reading(published, days[:len(days)-1], days[len(days)-2])
	if r.Date != days[len(days)-1] || r.Delta != nil {
		t.Fatal("intraday headline assigned to previous close", r)
	}
}

func TestCNNRejectsMalformedFutureAndConflictingReadings(t *testing.T) {
	now := time.Date(2025, 3, 10, 11, 0, 0, 0, ny)
	day := time.Date(2025, 3, 7, 0, 0, 0, 0, time.UTC).UnixMilli()
	later := day + (20 * time.Hour).Milliseconds()
	raw := fmt.Sprintf(`{"fear_and_greed":{"score":0,"rating":"unknown","timestamp":%q},
		"fear_and_greed_historical":{"data":[{"x":%d,"y":20},{"x":%d,"y":50},{"x":%d,"y":1},
		{"x":%d,"y":null},{"x":%d,"y":"NaN"},{"x":%d,"y":101},{"x":%d,"y":75}]}}`,
		now.Format(time.RFC3339), later, later, day, later+1, later+2, later+3, now.Add(time.Hour).UnixMilli())
	s, err := parseCNN([]byte(raw), now)
	if err != nil || s.Rating != "" || len(s.History) != 1 || s.History["2025-03-07"] != nil {
		t.Fatal(s, err)
	}
	near(t, s.Score, 0, 0)
	for _, bad := range []string{"null", "-0.1", "101", `"NaN"`, `"10"`} {
		if _, err := parseCNN([]byte(strings.Replace(raw, `"score":0`, `"score":`+bad, 1)), now); err == nil {
			t.Fatal("invalid headline accepted", bad)
		}
	}
	if _, err := parseCNN([]byte(raw), now.Add(-time.Second)); err == nil {
		t.Fatal("future headline")
	}
	if _, err := parseCNN([]byte(`{"fear_and_greed":{"score":50}}`), now); err == nil {
		t.Fatal("undated headline")
	}
	s.Source = cnnArchive
	s.History[nyDate(now)] = number(1)
	if _, err := s.reading(now, nil, nyDate(now)); err == nil {
		t.Fatal("unverified archive headline")
	}
	s.History[nyDate(now)] = number(0)
	r, err := s.reading(now, nil, nyDate(now))
	if err != nil || r.Level == nil || r.Percentile != nil || r.RatingZH != "" {
		t.Fatal(r, err)
	}
}

func TestCNNSourceFreshnessCacheAndForcedRefresh(t *testing.T) {
	friday := time.Date(2025, 3, 7, 16, 0, 0, 0, ny)
	now := friday.AddDate(0, 0, 2)
	days := []string{"2025-03-07"}
	official, archive := cnnFixture(friday, days), cnnFixture(friday, days)
	status, hits, backups := 200, 0, 0
	f := Fetcher{Now: now, Dir: t.TempDir(), Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case cnnURL:
			hits++
			return reply(status, official), nil
		case cnnArchiveURL:
			backups++
			return reply(200, archive), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL)
			return nil, nil
		}
	})}}
	load := func() (FearReading, string) {
		return f.cnnFearGreed(context.Background(), days, fearDateFloor(f.Now, days))
	}
	r, w := load()
	if w != "" || r.Source != cnnSource || r.Date != "2025-03-07" {
		t.Fatal(r, w)
	}
	f.Now = f.Now.Add(14 * time.Minute)
	r, w = load()
	if w != "" || r.Level == nil || hits != 1 || backups != 0 {
		t.Fatal("warm cache", r, w, hits, backups)
	}
	f.Now = f.Now.Add(time.Minute)
	status = 418
	r, w = load()
	if w != "" || r.Source != cnnArchive || hits != 2 || backups != 1 {
		t.Fatal("archive", r, w, hits, backups)
	}
	f.Force = true
	status = 200
	r, w = load()
	if w != "" || r.Source != cnnSource || hits != 3 || backups != 1 {
		t.Fatal("force", r, w, hits, backups)
	}
	tuesday := time.Date(2025, 3, 11, 16, 5, 0, 0, ny)
	days = append(days, "2025-03-10", "2025-03-11")
	f.Now, f.Force = tuesday, false
	r, w = load()
	if r.Level != nil || !strings.Contains(w, "2025-03-11") || !strings.Contains(w, cnnArchive) {
		t.Fatal("old observations scored", r, w)
	}
	archive = cnnFixture(tuesday, days)
	r, w = load()
	if w != "" || r.Source != cnnArchive || r.Date != days[2] {
		t.Fatal("fresh archive not used", r, w)
	}
	days = nil
	f.Now = tuesday.AddDate(0, 0, 5)
	r, w = load()
	if r.Level != nil || w == "" {
		t.Fatal("unbounded staleness without equity quotes", r, w)
	}
}

func TestVIXFreshnessAndReportProvenance(t *testing.T) {
	in := scenario(0)
	in.Market.Measures["vix"] = Metric{Date: "2025-05-08", Level: number(24)}
	in.Market.Measures["vix_vix3m_ratio"] = Metric{Date: "2025-05-07", Level: number(1.1)}
	in.Market.VIXDeclining = truth(true)
	w := in.Market.discardStaleVIX("2025-05-08")
	if len(w) != 1 || in.Market.Measures["vix_vix3m_ratio"].Level != nil || !yes(in.Market.VIXDeclining) {
		t.Fatal(w, in.Market)
	}
	in.Market.CNN = FearReading{Metric: Metric{Date: "2025-05-08", Level: number(0)}, Source: cnnArchive, PublishedAt: &in.AsOf, Rating: "extreme fear"}
	out := runModel(t, in, modelReport(in))
	if out.FearIndices.CNN.Source != cnnArchive || !strings.Contains(Brief(out), "0.00/100") || !strings.Contains(Brief(out), "24.00 点") {
		t.Fatal(out.FearIndices)
	}
	out = applyGuardrails(in, quantReport(in), "quant_only")
	near(t, out.FearIndices.CNN.Level, 0, 0)
	w = in.Market.discardStaleVIX("2025-05-09")
	if len(w) != 1 || in.Market.Measures["vix_1y_pct"].Level != nil || in.Market.VIXDeclining != nil {
		t.Fatal(w, in.Market)
	}
}
