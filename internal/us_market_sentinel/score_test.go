//go:build darwin

package sentinel

import (
	"math"
	"testing"
	"time"
)

func measures(values map[string]float64) Measures {
	m := Measures{}
	for k, v := range values {
		m[k] = Metric{Level: number(v)}
	}
	return m
}
func scenario(n int) Input {
	inputs := [][]float64{
		{1.10, 95, 30, -5, 0, 100, 300, -15, 50, -15, -50, -5, 3.5, 0},
		{1.15, 95, 25, -10, 0, -100, -300, 0, 50, -40, -120, -12, 5.5, 90},
		{.80, 5, 75, 8, -2, -100, -300, 20, 90, 20, 50, 18, 2.8, 0},
	}
	a := inputs[n]
	idx := measures(map[string]float64{"rsi_14": a[2], "dist_to_50ma_pct": a[3], "dist_to_200ma_pct": a[11]})
	m := MarketData{SPX: idx, NDX: idx, Divergence: truth(n == 2), Measures: measures(map[string]float64{"vix_vix3m_ratio": a[0], "vix_1y_pct": a[1], "rsp_spy_chg_20d_pct": a[4]})}
	f := MacroData{Measures: measures(map[string]float64{"net_liquidity_chg_4w_billions": a[5], "net_liquidity_chg_12w_billions": a[6], "us10y_tips_chg_20d_bps": a[7], "us10y_tips_1y_pct": a[8], "us2y_minus_dff_chg_20d_bps": a[9], "us2y_minus_dff_bps": a[10], "hy_oas_pct": a[12], "hy_oas_chg_20d_bps": a[13]})}
	f.Breaker = creditBreaker(f.Measures)
	return Input{AsOf: time.Date(2025, 5, 8, 18, 0, 0, 0, time.UTC), Style: Left, Market: m, Macro: f, Scores: computeQuantBaseScore(m, f), Baseline: indexPreference(m, f), News: []Headline{}, Events: []Event{}, Warnings: []string{}}
}
func near(t *testing.T, v *float64, want, tolerance float64) {
	t.Helper()
	if v == nil || math.Abs(*v-want) > tolerance {
		t.Fatalf("got %s, want %v ± %v", val(v), want, tolerance)
	}
}
func TestAcceptanceVectors(t *testing.T) {
	for i, want := range []struct {
		d1, d2, d3 float64
		base       int
		breaker    bool
	}{{8.65, 31.73, 23, 20, false}, {0, 52.75, 90, 43, true}, {100, 75.68, 64.25, 82, false}} {
		in := scenario(i)
		near(t, in.D1, want.d1, .01)
		near(t, in.D2, want.d2, .01)
		near(t, in.D3, want.d3, .01)
		if in.Base != want.base || in.Macro.Breaker != want.breaker || in.Confidence != "high" {
			t.Fatal(in.Scores, in.Macro.Breaker)
		}
		t.Logf("%c: D1 %.2f D2 %.3f D3 %.2f base %d breaker %v", 'A'+i, *in.D1, *in.D2, *in.D3, in.Base, in.Macro.Breaker)
	}
}
func TestMissingWeights(t *testing.T) {
	all := computeQuantBaseScore(MarketData{}, MacroData{})
	if all.D1 != nil || all.Base != 50 || all.Confidence != "low" || all.Coverage != 0 {
		t.Fatal(all)
	}
	in := scenario(0)
	in.Market.NDX = nil
	single := computeQuantBaseScore(in.Market, in.Macro)
	if single.Base != 20 {
		t.Fatal(single)
	}
	partial := computeQuantBaseScore(in.Market, MacroData{})
	near(t, number(partial.Coverage), .4*(1-.11)+.3*.45, 1e-10)
	if partial.Confidence != "low" || partial.D2 != nil {
		t.Fatal(partial)
	}
	delete(in.Macro.Measures, "us10y_tips_chg_20d_bps")
	s := computeQuantBaseScore(in.Market, in.Macro)
	near(t, number(s.Coverage), 1-.4*.11-.3*.35*.7, 1e-10)
	if piecewiseLinear(nil, anchor{1, 100}) != nil {
		t.Fatal("null interpolation")
	}
	near(t, piecewiseLinear(number(-1), anchor{0, 10}, anchor{1, 20}), 10, 0)
	near(t, piecewiseLinear(number(2), anchor{0, 10}, anchor{1, 20}), 20, 0)
}
func TestCreditAndRotationBoundaries(t *testing.T) {
	m := measures(map[string]float64{"hy_oas_pct": 5, "hy_oas_chg_20d_bps": 80})
	if creditBreaker(m) {
		t.Fatal("strict thresholds")
	}
	for _, k := range []string{"hy_oas_pct", "hy_oas_chg_20d_bps"} {
		x := measures(map[string]float64{k: *m[k].Level + .01})
		if !creditBreaker(x) {
			t.Fatal(k)
		}
	}
	in := scenario(0)
	if indexPreference(in.Market, in.Macro).Index != "NDX" {
		t.Fatal("oversold")
	}
	in.Macro.Measures["us10y_tips_chg_20d_bps"] = Metric{Level: number(25)}
	if indexPreference(in.Market, in.Macro).Index != "SPX" || indexPreference(MarketData{}, MacroData{}).Index != "balanced" {
		t.Fatal("rotation")
	}
}
