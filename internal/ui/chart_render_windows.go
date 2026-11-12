//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

const (
	chartWidth      = 468
	chartCloseRow   = 22
	chartHeight     = 252 + chartCloseRow
	chartBackground = 0xFF0D0F12
	chartMuted      = 0xFF8E95A2
	chartPriceSize  = 42
	chartChangeSize = 22
)

type chartRaster struct {
	alphaFrame
	scale                                 float64
	name, price, currency, change, detail uintptr
	graphicsToken                         uintptr
	scrollMax, offset                     int32
	links                                 []chartLink
	plot                                  rect
}

type chartSeries struct {
	points           []market.IntradayPoint
	loading, sampled bool
}

func (r *chartRaster) releaseFonts() {
	for _, font := range []uintptr{r.name, r.price, r.currency, r.change, r.detail} {
		if font != 0 {
			deleteObject.Call(font)
		}
	}
}

func (r *chartRaster) close() {
	r.releaseFonts()
	r.alphaFrame.close()
	if r.graphicsToken != 0 {
		gdipShutdown.Call(r.graphicsToken)
	}
	*r = chartRaster{}
}

func (r *chartRaster) px(v int) int32 {
	return int32(math.Round(float64(v) * r.scale))
}

func (r *chartRaster) sampleAt(mouse point, points []market.IntradayPoint) int64 {
	if mouse.Y < max(0, r.plot.Top) || mouse.Y >= min(r.height, r.plot.Bottom) {
		return 0
	}
	if i := nearestChartSample(points, r.plot.Left, r.plot.Right, mouse.X); i >= 0 {
		return points[i].Time
	}
	return 0
}

func (r *chartRaster) render(w, h int32, scale float64, language string, upIsRed bool, view market.View, series chartSeries, extra *chartExtras) error {
	if r.scale != scale {
		r.releaseFonts()
		r.scale = scale
		r.name = newRasterFont(r.px(16), 600)
		r.price = newRasterFont(r.px(chartPriceSize), 700)
		r.currency = newRasterFont(r.px(15), 600)
		r.change = newRasterFont(r.px(chartChangeSize), 700)
		r.detail = newRasterFont(r.px(12), 400)
	}
	if err := r.resize(w, h); err != nil {
		return err
	}
	for i := range r.pixels {
		r.pixels[i] = chartBackground
	}
	const ellipsis = 0x8000 // DT_END_ELLIPSIS
	left, right := r.px(22), w-r.px(22)
	headerHeight := h
	var panel chartPanel
	r.scrollMax, r.offset = 0, 0
	r.links = r.links[:0]
	r.plot = rect{}
	if extra != nil {
		headerHeight = r.px(chartHeight)
		panel = r.detailPanel(w, view, extra, language)
		r.scrollMax = max(0, panel.height-h)
		r.offset = max(0, min(extra.scroll, r.scrollMax))
		r.drawPanel(panel, r.offset, extra.mouse)
	}
	var text [8]rasterText
	runs := text[:0]
	session := textRun(locale.Text(language, chartSession(view.EffectiveState())), r.detail,
		markerColor(view.EffectiveState()), rect{left, r.px(13), right, r.px(37)}, 2)
	nameEnd := right - session.measure(r.dc).CX - r.px(12)
	session.bounds.Left = nameEnd + r.px(6)
	runs = append(runs, session, textRun(quoteName(language, view), r.name, barWhite, rect{left, r.px(13), nameEnd, r.px(37)}, ellipsis))

	q := view.Quote
	price, currency, change, foot := "--", "", "", ""
	color := uint32(barWhite)
	base := 0.0
	if q != nil {
		decimals := market.PriceDecimals(q.Symbol)
		price = fmt.Sprintf("%.*f", decimals, q.Price)
		currency = q.Currency
		change = fmt.Sprintf("%+.*f  %+.2f%%", decimals, q.ChangeAbs, q.ChangePercent)
		color = changeColor(q.ChangePercent, upIsRed)
		base = q.Previous()
		if base > 0 && !math.IsInf(base, 0) {
			foot = fmt.Sprintf("%s %.*f", locale.Text(language, "涨跌基准"), decimals, base)
		}
		if tag := q.FeedTag(time.Now()); tag != "" {
			foot += "  ·  " + tag
		} else if !view.Healthy && view.Reason != "" {
			foot += "  ·  " + locale.Text(language, view.Reason)
		}
	}
	if series.sampled {
		foot = locale.Text(language, "本次打开后采样") + "  ·  " + foot
	}
	delta := textRun(change, r.change, color, rect{left, r.px(43), right, r.px(93)}, 2|ellipsis)
	unit := textRun(currency, r.currency, 0xFFCBD5E1, rect{Top: r.px(62), Bottom: r.px(90)}, 0)
	value := textRun(price, r.price, barWhite, rect{Left: left, Top: r.px(35), Bottom: r.px(95)}, ellipsis)
	width := func(run rasterText) int32 {
		if len(run.chars) == 0 {
			return 0
		}
		return run.measure(r.dc).CX + r.px(2)
	}
	unitWidth, valueWidth, deltaWidth := width(unit), width(value), width(delta)
	budget := right - left - unitWidth - r.px(14)
	if total := valueWidth + deltaWidth; total > budget {
		// Keep the full values visible for instruments with large nominal
		// prices or change amounts. Leave room for GDI's hinted advances.
		fit := max(.45, float64(budget-r.px(5))/float64(total))
		value.font = newRasterFont(max(1, int32(float64(r.px(chartPriceSize))*fit)), 700)
		delta.font = newRasterFont(max(1, int32(float64(r.px(chartChangeSize))*fit)), 700)
		defer deleteObject.Call(value.font)
		defer deleteObject.Call(delta.font)
		valueWidth, deltaWidth = width(value), width(delta)
	}
	delta.bounds.Left = right - min(deltaWidth, max(0, budget-min(valueWidth, budget/2)))
	value.bounds.Right = min(left+valueWidth, delta.bounds.Left-unitWidth-r.px(14))
	unit.bounds.Left = value.bounds.Right + r.px(6)
	unit.bounds.Right = unit.bounds.Left + unitWidth
	runs = append(runs, value, unit, delta,
		textRun(chartTimeText(language, q), r.detail, chartMuted, rect{left, r.px(96), right, r.px(115)}, ellipsis))

	priceHeight := headerHeight - r.px(chartCloseRow)
	plot := rect{left, r.px(124), right, priceHeight - r.px(34)}
	visiblePlot := scrollRect(plot, r.offset)
	inspection, inspectionPercent := "", ""
	inspectionColor := uint32(chartMuted)
	if visiblePlot.Bottom > 0 && visiblePlot.Top < h {
		line, baseline := chartCoordinates(series.points, base, float32(visiblePlot.Left), float32(visiblePlot.Top), float32(visiblePlot.Right), float32(visiblePlot.Bottom))
		// A negative coordinate after scrolling still belongs to the plot.
		// The renderer clips the baseline to the viewport below.
		if len(line) > 0 && r.graphicsToken == 0 {
			r.graphicsToken, _ = startChartGraphics()
		}
		marks := chartMarkers(panel.events, series.points, plot, r.px(52), language)
		var selected *chartPoint
		if len(line) > 0 && r.graphicsToken != 0 {
			r.plot = visiblePlot
			if extra != nil {
				stamp := extra.selected
				if stamp == 0 {
					stamp = r.sampleAt(extra.mouse, series.points)
				}
				if i := chartSampleIndex(series.points, stamp); i >= 0 {
					selected = &line[i]
					inspection = chartSampleText(view, series.points[i])
					inspectionPercent = "--%"
					if pct, ok := chartSampleChange(series.points[i].Price, base); ok {
						inspectionPercent = fmt.Sprintf("%+.2f%%", pct)
						inspectionColor = changeColor(pct, upIsRed)
					}
				} else {
					inspection = locale.Detail(language, "悬停取点 · 点击固定")
				}
			}
			dash := max(1, r.px(3))
			for _, marker := range marks {
				for y := visiblePlot.Top; y < visiblePlot.Bottom; y += 2 * dash {
					r.fillArea(rect{marker.x, y, marker.x + 1, min(y+dash, visiblePlot.Bottom)}, 0xFF3A4859)
				}
			}
		}
		if len(line) == 0 || r.graphicsToken == 0 || !drawSparkline(&r.dibSurface, visiblePlot, line, baseline, color, scale, selected) {
			r.plot, inspection = rect{}, ""
			status := "暂无可核实的分时数据"
			if series.loading {
				status = "正在读取分时数据…"
			} else if q == nil {
				status = "等待报价…"
			}
			runs = append(runs, textRun(locale.Text(language, status), r.detail, chartMuted, plot, 1|ellipsis))
		} else {
			for _, marker := range marks {
				x := max(plot.Left, min(marker.x-r.px(21), plot.Right-r.px(42)))
				label := rect{x, plot.Top - r.px(9), x + r.px(42), plot.Top + r.px(7)}
				r.fillArea(scrollRect(label, r.offset), chartBackground)
				runs = append(runs, textRun(marker.label, r.detail, 0xFFA8BED9, label, 1|ellipsis))
			}
		}
	}
	footBounds := rect{left, priceHeight - r.px(29), right, priceHeight - r.px(10)}
	if inspection != "" {
		readout := textRun(inspection, r.detail, 0xFFC7D6E7, footBounds, 2|ellipsis)
		if inspectionPercent != "" {
			pct := textRun(inspectionPercent, r.detail, inspectionColor, footBounds, 2|ellipsis)
			pct.bounds.Left = right - min(width(pct), (right-left)/3)
			readout.bounds.Right = pct.bounds.Left - r.px(7)
			runs = append(runs, pct)
		}
		readout.bounds.Left = max(left+(right-left)/3, readout.bounds.Right-width(readout))
		footBounds.Right = readout.bounds.Left - r.px(8)
		runs = append(runs, readout)
	}
	runs = append(runs, textRun(foot, r.detail, chartMuted, footBounds, ellipsis))

	closeLabel, closeText, closeColor := locale.Detail(language, "上次常规盘收盘"), "--", uint32(chartMuted)
	if extra != nil {
		close := extra.data.Regular.Value
		if close.Date.IsZero() {
			close = extra.data.Year.Value.Regular
		}
		if !close.Date.IsZero() {
			closeLabel += "  " + close.Date.Format("01-02")
			closeText, closeColor = fmt.Sprintf("%+.2f%%", close.ChangePercent), changeColor(close.ChangePercent, upIsRed)
		}
	}
	closeBounds := rect{left, headerHeight - r.px(30), right, headerHeight - r.px(8)}
	label := textRun(closeLabel, r.detail, chartMuted, closeBounds, ellipsis)
	label.bounds.Right = left + min(width(label), (right-left)*2/3)
	closeBounds.Left = label.bounds.Right + r.px(7)
	runs = append(runs, label, textRun(closeText, r.currency, closeColor, closeBounds, ellipsis))
	runs = append(runs, panel.text...)
	visible := runs[:0]
	for _, run := range runs {
		run.bounds = scrollRect(run.bounds, r.offset)
		if run.bounds.Bottom > 0 && run.bounds.Top < h {
			visible = append(visible, run)
		}
	}
	return r.drawText(visible)
}
