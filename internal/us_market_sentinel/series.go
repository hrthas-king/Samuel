//go:build darwin

// Package sentinel evaluates US market snapshots strictly from dated observations.
package sentinel

import (
	"math"
	"sort"
	"time"

	"github.com/hrthas-king/Samuel/internal/market"
)

var ny = market.MacroZone("US")

const dayFormat = "2006-01-02"

func nyDate(t time.Time) string { return t.In(ny).Format(dayFormat) }

type Series map[string]*float64 // A dated null candle still counts as a trading session.
type Dataset map[string]Series
type ObservedValue struct {
	Date  string   `json:"date,omitempty"`
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}

func observedInputs(data Dataset, asOf string) map[string]ObservedValue {
	out := make(map[string]ObservedValue, len(data))
	for symbol, series := range data {
		value := ObservedValue{}
		switch symbol {
		case "WALCL", "WTREGEN":
			value.Unit = "million USD"
		case "RRPONTSYD":
			value.Unit = "billion USD"
		case "DGS2", "DFF", "DFII10", "BAMLH0A0HYM2":
			value.Unit = "percent"
		case "SPY", "RSP":
			value.Unit = "USD per share"
		default:
			value.Unit = "index points"
		}
		for date, v := range series {
			if v != nil && date <= asOf && date > value.Date {
				value.Date = date
				value.Value = v
			}
		}
		out[symbol] = value
	}
	return out
}

type vector []*float64
type Metric struct {
	Date       string   `json:"date,omitempty"`
	Level      *float64 `json:"level"`
	Delta      *float64 `json:"delta_20d"`
	Percentile *float64 `json:"percentile_1y"`
}
type Measures map[string]Metric
type MarketData struct {
	SPX          Measures    `json:"spx"`
	NDX          Measures    `json:"ndx"`
	Measures     Measures    `json:"indicators"`
	CNN          FearReading `json:"cnn_fear_greed"`
	VIXDeclining *bool       `json:"vix_2d_declining"`
	Reclaimed    *bool       `json:"reclaimed_short_ma"`
	Divergence   *bool       `json:"breadth_top_divergence"`
}
type MacroData struct {
	Measures Measures `json:"indicators"`
	Breaker  bool     `json:"recession_circuit_breaker"`
}

func number(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
func truth(b bool) *bool                       { return &b }
func yes(b *bool) bool                         { return b != nil && *b }
func above(v *float64, threshold float64) bool { return v != nil && *v > threshold }
func below(v *float64, threshold float64) bool { return v != nil && *v < threshold }
func last(v vector) *float64 {
	if len(v) == 0 {
		return nil
	}
	return v[len(v)-1]
}
func delta(a, b *float64, scale float64, relative bool) *float64 {
	if a == nil || b == nil {
		return nil
	}
	if relative {
		if *b == 0 {
			return nil
		}
		// Preserve strict percentage boundaries (e.g. an exact -1.5%).
		return number(math.Round((*a-*b) / *b * scale * 1e12) / 1e12)
	}
	return number((*a - *b) * scale)
}

func datesFor(data Dataset, keys []string, through string) []string {
	set := map[string]bool{}
	for _, key := range keys {
		for day := range data[key] {
			if day <= through {
				set[day] = true
			}
		}
	}
	days := make([]string, 0, len(set))
	for day := range set {
		days = append(days, day)
	}
	sort.Strings(days)
	return days
}
func aligned(s Series, dates []string) vector {
	v := make(vector, len(dates))
	for i, d := range dates {
		v[i] = s[d]
	}
	return v
}
func changes(v vector, n int, scale float64, relative bool) vector {
	out := make(vector, len(v))
	for i := n; i < len(v); i++ {
		out[i] = delta(v[i], v[i-n], scale, relative)
	}
	return out
}
func paired(a, b vector, f func(float64, float64) *float64) vector {
	v := make(vector, len(a))
	for i := range a {
		if a[i] != nil && b[i] != nil {
			v[i] = f(*a[i], *b[i])
		}
	}
	return v
}
func ratio(a, b float64) *float64 {
	if b == 0 {
		return nil
	}
	return number(a / b)
}
func ranks(v vector) vector {
	out := make(vector, len(v))
	for i := 251; i < len(v); i++ {
		if v[i] == nil {
			continue
		}
		less, equal := 0, 0
		valid := true
		for _, p := range v[i-251 : i+1] {
			if p == nil {
				valid = false
				break
			}
			if *p < *v[i] {
				less++
			} else if *p == *v[i] {
				equal++
			}
		}
		if valid {
			out[i] = number(100 * (float64(less) + float64(equal-1)/2) / 251)
		}
	}
	return out
}
func profile(days []string, v vector) Metric {
	m := Metric{Level: last(v)}
	if len(v) > 0 {
		m.Date = days[len(v)-1]
	}
	if len(v) > 20 {
		m.Delta = delta(m.Level, v[len(v)-21], 1, false)
	}
	if len(v) >= 252 {
		m.Percentile = last(ranks(v[len(v)-252:]))
	}
	return m
}
func maDistance(v vector, n int) vector {
	out := make(vector, len(v))
	var sum float64
	count := 0
	for i, x := range v {
		if x != nil {
			sum += *x
			count++
		}
		if i >= n && v[i-n] != nil {
			sum -= *v[i-n]
			count--
		}
		if count == n && sum > 0 {
			out[i] = number(100 * (*x/(sum/float64(n)) - 1))
		}
	}
	return out
}
func rsi14(v vector) vector {
	out := make(vector, len(v))
	var gain, loss float64
	samples := 0
	for i := 1; i < len(v); i++ {
		if v[i-1] == nil || v[i] == nil {
			gain, loss, samples = 0, 0, 0
			continue
		}
		step := *v[i] - *v[i-1]
		up, down := max(0, step), max(0, -step)
		samples++
		if samples <= 14 {
			gain += up
			loss += down
			if samples < 14 {
				continue
			}
			gain /= 14
			loss /= 14
		} else {
			gain = (13*gain + up) / 14
			loss = (13*loss + down) / 14
		}
		if gain+loss == 0 {
			out[i] = number(50)
		} else {
			out[i] = number(100 * gain / (gain + loss))
		}
	}
	return out
}
