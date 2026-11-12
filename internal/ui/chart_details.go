package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

type chartMetric struct{ label, value string }

func chartMetrics(view market.View, data market.DetailData, lang string) ([]chartMetric, string) {
	q := view.Quote
	m := data.Metrics.Value
	day := m
	if q == nil || !m.SameQuoteDay(*q) {
		day = market.QuoteMetrics{}
	}
	label := func(key string) string { return locale.Detail(lang, key) }
	price := func(n *float64) string { return metricDecimal(n, market.PriceDecimals(view.Symbol)) }
	volume := metricQuantity(day.Volume)
	if volume != "--" && day.VolumeLots {
		volume += " " + label("手")
	}
	turnover := metricQuantity(day.Turnover)
	if turnover != "--" && q != nil {
		turnover += " " + q.Currency
	}
	previousLabel, previous := "前收", price(day.PrevClose)
	if previous == "--" && q != nil && q.Previous() > 0 && !math.IsInf(q.Previous(), 0) {
		base := q.Previous()
		previousLabel, previous = "涨跌基准", price(&base)
	}
	span := "--"
	tencentRange := q != nil && data.Year.OK && data.Year.Value.AsOf == market.QuoteMarketTime(*q).Format("2006-01-02")
	if m.YearLow != nil && m.YearHigh != nil && *m.YearLow <= *m.YearHigh {
		span = price(m.YearLow) + "–" + price(m.YearHigh)
	} else if tencentRange {
		r := data.Year.Value
		span = price(&r.Low) + "–" + price(&r.High)
	}
	amp := metricDecimal(day.Amplitude(), 2)
	if amp != "--" {
		amp += "%"
	}
	stats := []chartMetric{{"今开", price(day.Open)}, {"最高", price(day.High)}, {"最低", price(day.Low)},
		{previousLabel, previous}, {"成交量", volume}, {"成交额", turnover},
		{"52 周区间", span}, {"日内振幅", amp}}
	var notes []string
	if m.Source != "" {
		notes = append(notes, m.Source)
		if m.Source == "Yahoo Finance" {
			notes = append(notes, label("常规盘盘口"))
		}
	}
	if tencentRange {
		notes = append(notes, label("52 周区间取自腾讯日线"))
	}
	if q != nil {
		switch q.QuoteType {
		case "EQUITY":
			epsLabel, eps := "EPS-TTM", m.EPS
			if data.Mainland.Value.EPS != nil {
				epsLabel, eps = "基本 EPS", data.Mainland.Value.EPS
				notes = append(notes, label("EPS 报告期")+" "+data.Mainland.Value.EPSAsOf.Format("2006-01-02")+" · "+label("东方财富"))
			}
			stats = append(stats, chartMetric{"美元总市值", strings.TrimPrefix(q.CapitalizationText(), "市值 ")},
				chartMetric{"PE-TTM", metricDecimal(m.TrailingPE, 2)}, chartMetric{"预期市盈率", metricDecimal(m.ForwardPE, 2)},
				chartMetric{epsLabel, metricDecimal(eps, 2)})
		case "INDEX":
			var pe, pb *market.ValuationMetric
			if q.Valuation != nil {
				pe, pb = q.Valuation.PE, q.Valuation.PB
				notes = append(notes, label("括号内为历史分位（最长近 10 年）")+" · "+label(q.Valuation.Source))
			}
			stats = append(stats, chartMetric{"指数 PE", valuationCell(pe)}, chartMetric{"指数 PB", valuationCell(pb)})
		case "ETF", "FUTURE":
			if exposure := chartExposure(view.Symbol); exposure != "" {
				notes = append(notes, label(exposure))
			}
		}
	}
	for i := range stats {
		stats[i].label = label(stats[i].label)
	}
	return stats, strings.Join(notes, "  ·  ")
}

func metricDecimal(value *float64, decimals int) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "--"
	}
	return fmt.Sprintf("%.*f", decimals, *value)
}

func metricQuantity(value *float64) string {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "--"
	}
	n := *value
	units := [...]string{"", "K", "M", "B", "T"}
	i := 0
	for n >= 1000 && i < len(units)-1 {
		n /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", n)
	}
	return fmt.Sprintf("%.2f%s", n, units[i])
}

func valuationCell(m *market.ValuationMetric) string {
	if m == nil {
		return "--"
	}
	value := metricDecimal(&m.Value, 2)
	if m.Percentile != nil {
		value += " (" + metricDecimal(m.Percentile, 1) + "%)"
	}
	return value
}

func chartExposure(symbol string) string {
	switch symbol {
	case "VOO", "159655.SZ":
		return "跟踪标普 500 指数"
	case "QQQ", "159632.SZ":
		return "跟踪纳斯达克 100 指数"
	case "SOXX":
		return "跟踪 NYSE Semiconductor Index"
	case "SMH":
		return "跟踪 MVIS US Listed Semiconductor 25 Index"
	case "GC=F":
		return "COMEX 黄金期货（腾讯报价）"
	case "BZ=F":
		return "ICE 布伦特原油期货（腾讯报价）"
	}
	return ""
}

func calendarDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func chartEarningsText(e market.EarningsDate, lang string, now time.Time) (string, string) {
	tr := func(s string) string { return locale.Detail(lang, s) }
	if e.Start.IsZero() {
		return tr("财报日期"), tr("暂无可核实的财报日期")
	}
	today := calendarDay(now.In(e.Start.Location()))
	days := int(calendarDay(e.Start).Sub(today) / (24 * time.Hour))
	title := "最近财报日期"
	if days > 0 {
		title = "下一期财报"
	} else if e.Reported {
		title = "最近实际披露"
	}
	stamp := e.Start.Format("2006-01-02")
	if e.End.After(e.Start) {
		stamp += "–" + e.End.Format("2006-01-02")
	} else if e.Session != "" {
		stamp += " " + tr(chartSession(e.Session))
	} else {
		stamp += " " + tr("仅日期")
	}
	if e.Estimated {
		status := "预计"
		if e.Source == "东方财富" {
			status = "预约披露"
		}
		stamp += " · " + tr(status)
	}
	if days > 0 {
		stamp += " · " + fmt.Sprintf(tr("剩余 %d 天"), days)
	} else if days == 0 {
		stamp += " · " + tr("今日")
	}
	return tr(title), stamp + " · " + tr(e.Source)
}

func chartMacroTitle(event market.MacroEvent, lang string) string {
	tr := func(s string) string { return locale.Detail(lang, s) }
	title := tr(string(event.Kind))
	switch event.Kind {
	case market.MacroNFP, market.MacroUnemployment, market.MacroCPI, market.MacroCoreCPI, market.MacroPCE,
		market.MacroGDP, market.MacroClaims, market.MacroRetail, market.MacroChinaPMI, market.MacroChinaServices:
		country := map[string]string{"US": "美国", "CN": "中国", "EU": "欧元区", "JP": "日本", "KR": "韩国", "DE": "德国", "FR": "法国"}[event.Country]
		if country != "" {
			title = tr(country) + " " + title
		}
	}
	raw := strings.ToLower(event.Title)
	switch {
	case strings.Contains(raw, "yoy") || strings.Contains(raw, "y/y"):
		title += " " + tr("同比")
	case strings.Contains(raw, "mom") || strings.Contains(raw, "m/m"):
		title += " " + tr("环比")
	case strings.Contains(raw, "qoq") || strings.Contains(raw, "q/q"):
		title += " " + tr("较上季")
	}
	return title
}

func chartCountdown(remaining time.Duration, lang string) string {
	minutes := max(1, int(math.Ceil(remaining.Minutes())))
	hours, days := minutes/60, minutes/(24*60)
	value := fmt.Sprintf("%dm", minutes)
	switch {
	case days > 0:
		value = fmt.Sprintf("%dd %dh", days, hours%24)
	case hours > 0:
		value = fmt.Sprintf("%dh %dm", hours, minutes%60)
	}
	return fmt.Sprintf(locale.Detail(lang, "剩余 %s"), value)
}

func chartMacroText(event market.MacroEvent, lang string, now time.Time) (string, string, bool) {
	tr := func(s string) string { return locale.Detail(lang, s) }
	at := event.At.In(market.MacroZone(event.Country))
	localNow := now.In(at.Location())
	date := at.Format("01-02")
	if calendarDay(at).Equal(calendarDay(localNow)) {
		date = tr("今日")
	} else if at.Year() != localNow.Year() {
		date = at.Format("2006-01-02")
	}
	clock := marketClock(at)
	if event.Country == "US" {
		clock = at.Format("15:04") + " ET"
	}
	when := date + " " + clock
	upcoming := event.At.After(now)
	if upcoming {
		when += " · " + chartCountdown(event.At.Sub(now), lang)
	}
	var values []string
	published := !upcoming && event.Actual != ""
	if published {
		actual := tr("实际") + " " + event.Actual
		if change := market.RateDirection(event); change != "" {
			actual += " (" + tr(change) + ")"
		}
		values = append(values, actual)
	}
	if event.Forecast != "" {
		values = append(values, tr("预期")+" "+event.Forecast)
	}
	if event.Previous != "" {
		values = append(values, tr("前值")+" "+event.Previous)
	}
	if !upcoming && !published {
		values = append(values, tr("实际值未提供"))
	}
	values = append(values, event.Source)
	return when, strings.Join(values, "  ·  "), published
}

func chartEventX(at time.Time, points []market.IntradayPoint, left, right float32) (float32, bool) {
	if len(points) < 2 || right <= left {
		return 0, false
	}
	start, end := points[0].Time, points[len(points)-1].Time
	stamp := at.Unix()
	if start >= end || stamp < start || stamp > end {
		return 0, false
	}
	return left + (right-left)*float32(float64(stamp-start)/float64(end-start)), true
}
