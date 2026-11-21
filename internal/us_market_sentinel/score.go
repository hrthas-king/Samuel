//go:build darwin

package sentinel

import "math"

type anchor struct{ x, score float64 }
type weighted struct {
	value  *float64
	weight float64
}
type Scores struct {
	D1         *float64 `json:"D1"`
	D2         *float64 `json:"D2"`
	D3         *float64 `json:"D3"`
	Base       int      `json:"quant_base_score"`
	Coverage   float64  `json:"coverage_ratio"`
	Confidence string   `json:"confidence"`
}

func piecewiseLinear(x *float64, points ...anchor) *float64 {
	if x == nil || len(points) == 0 {
		return nil
	}
	if *x <= points[0].x {
		return number(points[0].score)
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if *x <= b.x {
			return number(a.score + (*x-a.x)/(b.x-a.x)*(b.score-a.score))
		}
	}
	return number(points[len(points)-1].score)
}
func weights(items ...weighted) (*float64, float64) {
	var total, w float64
	for _, item := range items {
		if item.value != nil {
			total += *item.value * item.weight
			w += item.weight
		}
	}
	if w == 0 {
		return nil, 0
	}
	return number(total / w), w
}
func indexMean(m MarketData, key string) *float64 {
	v, _ := weights(weighted{m.SPX[key].Level, .5}, weighted{m.NDX[key].Level, .5})
	return v
}
func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

func computeQuantBaseScore(m MarketData, f MacroData) Scores {
	market := func(k string) *float64 { return m.Measures[k].Level }
	macro := func(k string) *float64 { return f.Measures[k].Level }
	dist50 := indexMean(m, "dist_to_50ma_pct")
	vixSentiment := piecewiseLinear(market("vix_1y_pct"), anchor{5, 100}, anchor{20, 75}, anchor{50, 50}, anchor{80, 20}, anchor{95, 0})
	// The CNN headline already runs from fear to greed. Split the original
	// sentiment bucket and use the available member when one source is missing.
	sentiment, sentimentCoverage := weights(weighted{vixSentiment, .5}, weighted{m.CNN.Level, .5})
	d1, w1 := weights(
		weighted{piecewiseLinear(market("vix_vix3m_ratio"), anchor{.75, 100}, anchor{.80, 85}, anchor{.88, 60}, anchor{.95, 45}, anchor{1.05, 15}, anchor{1.15, 0}), .38},
		weighted{sentiment, .22},
		weighted{piecewiseLinear(indexMean(m, "rsi_14"), anchor{25, 0}, anchor{35, 20}, anchor{50, 50}, anchor{65, 75}, anchor{75, 100}), .22},
		weighted{piecewiseLinear(dist50, anchor{-10, 0}, anchor{-5, 20}, anchor{0, 50}, anchor{4, 75}, anchor{8, 100}), .18},
	)
	if sentiment != nil {
		w1 -= .22 * (1 - sentimentCoverage)
	}
	if d1 != nil {
		modifier := 0.0
		if yes(m.Divergence) {
			modifier = 10
		} else if below(dist50, -3) && above(market("rsp_spy_chg_20d_pct"), 1) {
			modifier = -5
		}
		d1 = number(clamp(*d1+modifier, 0, 100))
	}
	var liq *float64
	short, long := macro("net_liquidity_chg_4w_billions"), macro("net_liquidity_chg_12w_billions")
	if short != nil && long != nil {
		liq = number(.6**short + .4*(*long/3))
	}
	liq = piecewiseLinear(liq, anchor{-200, 95}, anchor{-100, 75}, anchor{0, 50}, anchor{100, 25}, anchor{200, 5})
	tips, tipsWeight := weights(
		weighted{piecewiseLinear(macro("us10y_tips_chg_20d_bps"), anchor{-35, 10}, anchor{-15, 30}, anchor{0, 50}, anchor{20, 75}, anchor{40, 100}), .7},
		weighted{piecewiseLinear(macro("us10y_tips_1y_pct"), anchor{10, 15}, anchor{50, 50}, anchor{90, 85}), .3},
	)
	fed, fedWeight := weights(
		weighted{piecewiseLinear(macro("us2y_minus_dff_chg_20d_bps"), anchor{-40, 15}, anchor{-15, 35}, anchor{0, 50}, anchor{20, 75}, anchor{45, 95}), .7},
		weighted{piecewiseLinear(macro("us2y_minus_dff_bps"), anchor{-120, 35}, anchor{-50, 40}, anchor{0, 50}, anchor{50, 70}, anchor{100, 85}), .3},
	)
	d2, _ := weights(weighted{liq, .4}, weighted{tips, .35}, weighted{fed, .25})
	w2 := .35*tipsWeight + .25*fedWeight
	if liq != nil {
		w2 += .4
	}
	d3, w3 := weights(
		weighted{piecewiseLinear(indexMean(m, "dist_to_200ma_pct"), anchor{-12, 0}, anchor{-5, 15}, anchor{0, 35}, anchor{6, 55}, anchor{12, 80}, anchor{18, 100}), .45},
		weighted{piecewiseLinear(macro("hy_oas_pct"), anchor{2.8, 35}, anchor{3.5, 25}, anchor{4.2, 50}, anchor{5, 85}, anchor{6, 100}), .30},
		weighted{piecewiseLinear(macro("hy_oas_chg_20d_bps"), anchor{-40, 20}, anchor{0, 35}, anchor{25, 55}, anchor{50, 80}, anchor{80, 100}), .25},
	)
	if f.Breaker && (d3 == nil || *d3 < 90) {
		d3 = number(90)
	}
	raw, _ := weights(weighted{d1, .4}, weighted{d2, .3}, weighted{d3, .3})
	out := Scores{D1: d1, D2: d2, D3: d3, Base: 50, Coverage: min(1, .4*w1+.3*w2+.3*w3), Confidence: "low"}
	if raw != nil {
		out.Base = int(math.RoundToEven(clamp(*raw, 0, 100)))
	}
	switch {
	case out.Coverage >= .85-1e-12:
		out.Confidence = "high"
	case out.Coverage >= .6-1e-12:
		out.Confidence = "medium"
	}
	return out
}

type Preference struct {
	Index  string `json:"index"`
	Reason string `json:"reason"`
}

func indexPreference(m MarketData, f MacroData) Preference {
	tips, oas := f.Measures["us10y_tips_chg_20d_bps"].Level, f.Measures["hy_oas_pct"].Level
	hot := m.NDX["dist_to_200ma_pct"].Level
	if (tips != nil && *tips >= 25) || above(f.Measures["hy_oas_chg_20d_bps"].Level, 0) || (hot != nil && *hot >= 12) {
		return Preference{"SPX", "实际利率明显上升、信用利差走阔或纳指远离 200 日均线，偏好标普。"}
	}
	rsi, ndx, spx := m.NDX["rsi_14"].Level, m.NDX["chg_5d_pct"].Level, m.SPX["chg_5d_pct"].Level
	if tips != nil && *tips <= 0 && below(oas, 4.5) && ((rsi != nil && *rsi <= 35) || (ndx != nil && spx != nil && *ndx > *spx)) {
		return Preference{"NDX", "实际利率未上行、信用利差低于 4.5%，纳指超卖或五日相对表现较强。"}
	}
	return Preference{"balanced", "可用数据未形成一致的指数轮动信号。"}
}
