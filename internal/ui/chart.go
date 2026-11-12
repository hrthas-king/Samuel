package ui

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

func marketClock(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	_, offset := at.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	zone := fmt.Sprintf("GMT%s%d", sign, offset/3600)
	if minutes := offset / 60 % 60; minutes != 0 {
		zone += fmt.Sprintf(":%02d", minutes)
	}
	return zone + " " + at.Format("15:04")
}

func chartTimeText(language string, quote *market.Quote) string {
	if quote != nil {
		if clock := marketClock(market.QuoteMarketTime(*quote)); clock != "" {
			return fmt.Sprintf(locale.Text(language, "最后更新于%s"), clock)
		}
	}
	return locale.Text(language, "源未提供行情时间")
}

func chartSession(state market.SessionState) string {
	switch state {
	case market.StateRegular:
		return "盘中"
	case market.StatePreMarket:
		return "盘前"
	case market.StatePostMarket:
		return "盘后"
	case market.StateOvernight:
		return "夜盘"
	default:
		return "休市"
	}
}

type chartPoint struct{ X, Y float32 }

func nearestChartSample(points []market.IntradayPoint, left, right, x int32) int {
	if len(points) < 2 || left >= right || x < left || x > right || points[0].Time >= points[len(points)-1].Time {
		return -1
	}
	target := float64(points[0].Time) + float64(points[len(points)-1].Time-points[0].Time)*float64(x-left)/float64(right-left)
	i := sort.Search(len(points), func(i int) bool { return float64(points[i].Time) >= target })
	if i == len(points) || (i > 0 && target-float64(points[i-1].Time) <= float64(points[i].Time)-target) {
		i--
	}
	return i
}

func chartSampleIndex(points []market.IntradayPoint, stamp int64) int {
	if stamp <= 0 {
		return -1
	}
	i := sort.Search(len(points), func(i int) bool { return points[i].Time >= stamp })
	if i < len(points) && points[i].Time == stamp {
		return i
	}
	return -1
}

func chartSampleText(view market.View, sample market.IntradayPoint) string {
	q := market.Quote{Symbol: view.Symbol}
	if view.Quote != nil {
		q = *view.Quote
	}
	q.TradeTime = sample.Time
	label := fmt.Sprintf("%s · %.*f", market.QuoteMarketTime(q).Format("01-02 15:04"), market.PriceDecimals(q.Symbol), sample.Price)
	if q.Currency != "" {
		label += " " + q.Currency
	}
	return label
}

func chartSampleChange(price, base float64) (float64, bool) {
	if !(price > 0 && base > 0) || math.IsInf(price, 0) || math.IsInf(base, 0) {
		return 0, false
	}
	change := (price - base) / base * 100
	if math.IsInf(change, 0) {
		return 0, false
	}
	if math.Abs(change) < .005 {
		change = 0 // Avoid displaying a negative zero after rounding.
	}
	return change, true
}

// The time axis uses actual elapsed time, so missing minutes and breaks
// occupy their original width. Include the change reference in the Y domain.
func chartCoordinates(points []market.IntradayPoint, base float64, left, top, right, bottom float32) ([]chartPoint, float32) {
	if len(points) < 2 || points[0].Time >= points[len(points)-1].Time || left >= right || top >= bottom {
		return nil, -1
	}
	lo, hi := math.Inf(1), 0.0
	for _, point := range points {
		lo, hi = min(lo, point.Price), max(hi, point.Price)
	}
	hasBase := base > 0 && !math.IsInf(base, 0)
	if hasBase {
		lo, hi = min(lo, base), max(hi, base)
	}
	y := func(price float64) float32 {
		fraction := .5
		if hi > lo {
			fraction = (hi - price) / (hi - lo)
		}
		return top + (bottom-top)*float32(.08+.84*fraction)
	}
	first := points[0].Time
	duration := float64(points[len(points)-1].Time - first)
	line := make([]chartPoint, len(points))
	for i, p := range points {
		line[i] = chartPoint{left + (right-left)*float32(float64(p.Time-first)/duration), y(p.Price)}
	}
	baseline := float32(-1)
	if hasBase {
		baseline = y(base)
	}
	return line, baseline
}
