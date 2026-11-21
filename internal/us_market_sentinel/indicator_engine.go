//go:build darwin

package sentinel

import (
	"fmt"
	"math"
	"time"
)

var priceSymbols = []string{"^GSPC", "^NDX", "^VIX", "^VIX3M", "RSP", "SPY"}
var fredSymbols = []string{"WALCL", "WTREGEN", "RRPONTSYD", "DGS2", "DFF", "DFII10", "BAMLH0A0HYM2"}

func indexMeasures(days []string, v vector) Measures {
	m := Measures{"close": profile(days, v), "chg_1d_pct": profile(days, changes(v, 1, 100, true)),
		"chg_5d_pct": profile(days, changes(v, 5, 100, true)), "rsi_14": profile(days, rsi14(v))}
	for _, n := range []int{5, 10, 50, 200} {
		m[fmt.Sprintf("dist_to_%dma_pct", n)] = profile(days, maDistance(v, n))
	}
	return m
}

func marketIndicators(data Dataset, asOf string) MarketData {
	sessions := func(keys ...string) []string {
		through := asOf
		for _, k := range keys {
			dates := datesFor(data, []string{k}, asOf)
			if len(dates) == 0 {
				return nil
			}
			through = min(through, dates[len(dates)-1])
		}
		// Cboe can publish VIX on days when the US cash equity market is
		// closed. Use actual equity sessions for all equity-market windows.
		if dates := datesFor(data, []string{"^GSPC", "^NDX"}, through); len(dates) > 0 {
			return dates
		}
		return datesFor(data, keys, through)
	}
	index := func(key string) Measures {
		days := sessions(key)
		return indexMeasures(days, aligned(data[key], days))
	}
	spx, ndx := index("^GSPC"), index("^NDX")
	days := sessions("^VIX")
	vix := aligned(data["^VIX"], days)
	volDates := sessions("^VIX", "^VIX3M")
	term := paired(aligned(data["^VIX"], volDates), aligned(data["^VIX3M"], volDates), ratio)
	breadthDates := sessions("RSP", "SPY")
	spy := aligned(data["SPY"], breadthDates)
	breadth := paired(aligned(data["RSP"], breadthDates), spy, ratio)
	m := Measures{
		"vix": profile(days, vix), "vix_vix3m_ratio": profile(volDates, term),
		"vix_1y_pct": profile(days, ranks(vix)), "vix_chg_5d_pct": profile(days, changes(vix, 5, 100, true)),
		"rsp_spy_ratio": profile(breadthDates, breadth), "rsp_spy_chg_20d_pct": profile(breadthDates, changes(breadth, 20, 100, true)),
		"rsp_spy_1y_pct": profile(breadthDates, ranks(breadth)),
	}
	out := MarketData{SPX: spx, NDX: ndx, Measures: m}
	n := len(days)
	if n >= 3 && vix[n-1] != nil && vix[n-2] != nil && vix[n-3] != nil {
		out.VIXDeclining = truth(*vix[n-1] < *vix[n-2] && *vix[n-2] < *vix[n-3])
	}
	// An observed recovery in either index is sufficient. Otherwise a missing
	// moving average leaves the combined confirmation unknown.
	known := true
	for _, idx := range []Measures{spx, ndx} {
		for _, key := range []string{"dist_to_5ma_pct", "dist_to_10ma_pct"} {
			v := idx[key].Level
			if above(v, 0) {
				out.Reclaimed = truth(true)
			}
			known = known && v != nil
		}
	}
	if out.Reclaimed == nil && known {
		out.Reclaimed = truth(false)
	}
	change := m["rsp_spy_chg_20d_pct"].Level
	n = len(breadthDates)
	if n >= 20 && change != nil {
		high := 0.0
		for _, p := range spy[n-20:] {
			if p == nil {
				return out
			}
			high = max(high, *p)
		}
		if high > 0 {
			out.Divergence = truth(*spy[n-1] >= 0.98*high && *change < -1.5)
		}
	}
	return out
}

// Align reported observation dates. Carry values forward on weekdays only,
// stopping at the last available observation of the daily leg.
func liquidity(data Dataset, asOf string) ([]string, vector) {
	all := datesFor(data, []string{"WALCL", "WTREGEN", "RRPONTSYD"}, asOf)
	rrpDays := observedDates(data, "RRPONTSYD", asOf)
	if len(all) == 0 || len(rrpDays) == 0 {
		return nil, nil
	}
	start, _ := time.Parse(dayFormat, all[0])
	end, _ := time.Parse(dayFormat, rrpDays[len(rrpDays)-1])
	var days []string
	var v vector
	var assets, tga, rrp *float64
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format(dayFormat)
		if x := data["WALCL"][key]; x != nil {
			assets = x
		}
		if x := data["WTREGEN"][key]; x != nil {
			tga = x
		}
		if x := data["RRPONTSYD"][key]; x != nil {
			rrp = x
		}
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		var x *float64
		if assets != nil && tga != nil && rrp != nil {
			x = number(*assets/1000 - *tga/1000 - *rrp)
		}
		days = append(days, key)
		v = append(v, x)
	}
	return days, v
}

func observedDates(data Dataset, key, asOf string) []string {
	days := datesFor(data, []string{key}, asOf)
	valid := days[:0]
	for _, d := range days {
		if data[key][d] != nil {
			valid = append(valid, d)
		}
	}
	return valid
}

func macroIndicators(data Dataset, asOf string) MacroData {
	m := Measures{}
	days, v := liquidity(data, asOf)
	m["net_liquidity_billions"] = profile(days, v)
	m["net_liquidity_chg_4w_billions"] = profile(days, changes(v, 20, 1, false))
	m["net_liquidity_chg_12w_billions"] = profile(days, changes(v, 60, 1, false))
	for _, item := range []struct{ key, metric string }{{"DFII10", "us10y_tips"}, {"BAMLH0A0HYM2", "hy_oas"}} {
		dates := observedDates(data, item.key, asOf)
		values := aligned(data[item.key], dates)
		m[item.metric+"_pct"] = profile(dates, values)
		m[item.metric+"_chg_20d_bps"] = profile(dates, bpsChanges(values, 100))
		m[item.metric+"_1y_pct"] = profile(dates, ranks(values))
	}
	// DFF also publishes weekend values. A spread observation requires a
	// Treasury trading day with both rates available for that same date.
	dates := observedDates(data, "DGS2", asOf)
	for len(dates) > 0 && data["DFF"][dates[len(dates)-1]] == nil {
		dates = dates[:len(dates)-1]
	}
	spread := paired(aligned(data["DGS2"], dates), aligned(data["DFF"], dates), func(a, b float64) *float64 { return basisPoints((a - b) * 100) })
	m["us2y_minus_dff_bps"] = profile(dates, spread)
	m["us2y_minus_dff_chg_20d_bps"] = profile(dates, bpsChanges(spread, 1))
	return MacroData{m, creditBreaker(m)}
}

// FRED publishes decimal rates. Discard sub-nanobasis-point floating-point
// noise so an exact 80 bp move cannot trip the strictly-greater-than breaker.
func basisPoints(v float64) *float64 { return number(math.Round(v*1e9) / 1e9) }

func bpsChanges(v vector, scale float64) vector {
	d := changes(v, 20, scale, false)
	for i, x := range d {
		if x != nil {
			d[i] = basisPoints(*x)
		}
	}
	return d
}
func creditBreaker(m Measures) bool {
	return above(m["hy_oas_pct"].Level, 5) || above(m["hy_oas_chg_20d_bps"].Level, 80)
}
