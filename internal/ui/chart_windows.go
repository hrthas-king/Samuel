//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"math"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

const (
	wmChartReady     = 0x8007
	chartScrollTimer = 32
)

var (
	createRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	setWindowRgn       = user32.NewProc("SetWindowRgn")
	chartSetCursor     = user32.NewProc("SetCursor")
	chartShellExecute  = shell32.NewProc("ShellExecuteW")
)

type chartActivation struct {
	pinned       bool
	screenAnchor *point
}

type chartFlyout struct {
	chartActivation
	hwnd                                 uintptr
	symbol                               string
	bounds                               rect
	leaveAt                              time.Time
	raster                               chartRaster
	client                               *market.IntradayClient
	query                                market.IntradayQuery
	queryValid                           bool
	series                               chartSeries
	samples                              market.IntradaySamples
	dwmCorners                           bool
	detailClient                         *market.DetailClient
	detail                               market.DetailData
	detailAsked                          bool
	view                                 market.View
	scale                                float64
	scroll, scrollTarget, wheelRemainder int32
	scrollTimer, mouseTracking           bool
	mouse                                point
	pressedLink                          string
	pressedPlot                          bool
	selected                             int64
	openLink                             func(uintptr, string)
	anchorBar                            rect
	anchorX                              int32
	anchorScale                          float64
	clickArmed, pointerPressed           bool
}

func chartPlacement(bar, work rect, anchor int32, scale float64) (rect, float64) {
	margin := min(int32(math.Round(8*scale)), (work.Right-work.Left)/8, (work.Bottom-work.Top)/8)
	scale = min(scale, float64(work.Right-work.Left-2*margin)/chartWidth, float64(work.Bottom-work.Top-2*margin)/chartHeight)
	w := int32(math.Round(chartWidth * scale))
	baseHeight := int32(math.Round(chartHeight * scale))
	h := min(int32(math.Round(chartMaxHeight*scale)), work.Bottom-work.Top-2*margin)
	x := max(work.Left+margin, min(anchor-w/2, work.Right-margin-w))
	above, below := bar.Top-work.Top, work.Bottom-bar.Bottom
	if above >= baseHeight+2*margin {
		h = min(h, above-2*margin)
	} else if below >= baseHeight+2*margin {
		h = min(h, below-2*margin)
	}
	y := bar.Top - h - margin
	if above < baseHeight+2*margin && below > above {
		y = bar.Bottom + margin
	}
	y = max(work.Top+margin, min(y, work.Bottom-margin-h))
	return rect{x, y, x + w, y + h}, scale
}

func (u *UI) chartOpen() bool {
	return u.chart != nil && u.chart.hwnd != 0
}

func (u *UI) showChart(symbol string, activation chartActivation) {
	if symbol == "" || (u.cfg.Locked && !activation.pinned) || u.flyout != nil || u.customPanel != nil {
		return
	}
	u.hideTooltip()
	u.hover.due = time.Time{}
	if u.chart == nil {
		ctx := u.chartContext
		if ctx == nil {
			ctx = context.Background()
		}
		hwnd := u.hwnd
		ready := func() {
			postMessage.Call(hwnd, wmChartReady, 0, 0)
		}
		u.chart = &chartFlyout{client: market.NewIntradayClient(ctx, ready), detailClient: market.NewDetailClient(ctx, ready)}
	}
	c := u.chart
	if c.hwnd == 0 {
		cls, title := winapi.UTF16(overlayAuxClass), winapi.UTF16("Samuel")
		c.hwnd, _, _ = createWindow.Call(0x8|0x80|0x08000000, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
			0x80000000|0x00800000, 0, 0, 0, 0, u.hwnd, 0, u.instance, 0) // WS_POPUP | WS_BORDER
		if c.hwnd == 0 {
			return
		}
		c.dwmCorners = applyFluentFrame(c.hwnd, fluentPalette{dark: true, border: colorRef(32, 37, 45)})
	}
	if c.symbol != symbol {
		c.clearDetail()
	}
	c.chartActivation = activation
	c.clickArmed, c.pointerPressed = false, false
	c.anchorScale = 0
	c.symbol, c.leaveAt = symbol, time.Time{}
	u.refreshChart(true)
	if c.hwnd != 0 {
		showWindow.Call(c.hwnd, 4) // SW_SHOWNOACTIVATE
	}
	u.syncHoverTimer()
}

func (c *chartFlyout) outsideClick(outside, pressed bool) bool {
	fresh := c.clickArmed && pressed && !c.pointerPressed
	c.pointerPressed = pressed
	if !pressed {
		c.clickArmed = true
	}
	return outside && fresh
}

func (u *UI) hideChart() {
	if u.chartOpen() {
		c := u.chart
		hwnd := c.hwnd
		c.hwnd = 0
		destroyWindow.Call(hwnd)
		c.raster.close()
		c.symbol, c.bounds, c.leaveAt = "", rect{}, time.Time{}
		c.queryValid, c.series, c.samples = false, chartSeries{}, market.IntradaySamples{}
		c.clearDetail()
	}
	u.syncHoverTimer()
}

func (c *chartFlyout) clearDetail() {
	if c.scrollTimer && c.hwnd != 0 {
		killTimer.Call(c.hwnd, chartScrollTimer)
	}
	c.detail, c.detailAsked, c.view = market.DetailData{}, false, market.View{}
	c.chartActivation = chartActivation{}
	c.clickArmed, c.pointerPressed = false, false
	c.scroll, c.scrollTarget, c.wheelRemainder, c.scrollTimer = 0, 0, 0, false
	c.mouse, c.mouseTracking, c.pressedLink = point{-1, -1}, false, ""
	c.pressedPlot, c.selected = false, 0
}

func (u *UI) chartAnchor(symbol string) (rect, rect, bool) {
	var bar rect
	if ok, _, _ := getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&bar))); ok != 0 {
		for i := 1; i < len(u.barHits); i++ {
			if u.barHits[i].symbol == symbol {
				r := u.barHitBounds(i)
				return bar, rect{bar.Left + r.Left, bar.Top + r.Top, bar.Left + r.Right, bar.Top + r.Bottom}, true
			}
		}
	}
	return bar, rect{}, false
}

func (u *UI) chartSlot(index int) string {
	if symbol := u.barHits[index+1].symbol; symbol != "" {
		return symbol
	}
	symbols := u.menuGroup(u.cfg.MarketGroup).Symbols
	if index < len(symbols) {
		return symbols[index]
	}
	return ""
}

func (u *UI) chartView(symbol string) market.View {
	if snap := u.latest.Load(); snap != nil {
		for _, quote := range snap.Quotes {
			if quote.Symbol == symbol {
				return quote
			}
		}
	}
	return market.View{Symbol: symbol}
}

func (u *UI) openChartSlot(index int) {
	u.closeFluentMenu()
	var at point
	getCursor.Call(uintptr(unsafe.Pointer(&at)))
	u.showChart(u.chartSlot(index), chartActivation{pinned: true, screenAnchor: &at})
}

func (u *UI) refreshChart(activate bool) {
	if !u.chartOpen() {
		return
	}
	c := u.chart
	bar, hit, found := u.chartAnchor(c.symbol)
	if !found && c.screenAnchor == nil {
		u.hideChart()
		return
	}
	view := u.chartView(c.symbol)
	previous := c.view.Quote
	c.view = view
	if activate || (!c.detailAsked && view.Quote != nil) {
		c.detailAsked = view.Quote != nil
		c.detail = c.detailClient.Read(view, quoteName(u.Language(), view), u.Language(), true, c.detail)
	} else if q := view.Quote; previous != nil && q != nil && (previous.State != q.State ||
		market.QuoteMarketTime(*previous).Format("2006-01-02") != market.QuoteMarketTime(*q).Format("2006-01-02")) {
		c.detail = c.detailClient.ReadRegularHistory(*q, true, c.detail)
	}
	var query market.IntradayQuery
	var remote bool
	if view.Quote != nil {
		query, remote = market.IntradayFor(*view.Quote)
	}
	if remote {
		if activate || !c.queryValid || query != c.query {
			if c.queryValid && query != c.query {
				c.selected = 0
			}
			c.query, c.queryValid = query, true
			c.series = chartSeries{}
			c.series.points, c.series.loading = c.client.Request(query)
			c.samples = market.IntradaySamples{}
		}
	} else {
		c.queryValid = false
		points := c.samples.Add(view, time.Now())
		c.series = chartSeries{points: points, sampled: len(points) >= 2}
	}

	if c.screenAnchor != nil {
		at := *c.screenAnchor
		bar = rect{at.X, at.Y, at.X + 1, at.Y + 1}
		c.anchorX = at.X
	} else {
		// Text and rate widths can change on each tick. Hold the card's
		// horizontal position until the bar moves or its DPI changes.
		barOrigin := rect{Left: bar.Left, Top: bar.Top, Bottom: bar.Bottom}
		if c.anchorBar != barOrigin || c.anchorScale != u.scale {
			c.anchorBar, c.anchorScale = barOrigin, u.scale
			c.anchorX = hit.Left + (hit.Right-hit.Left)/2
		}
	}
	anchor := c.anchorX
	work := popupWorkArea(point{anchor, bar.Top + (bar.Bottom-bar.Top)/2})
	bounds, scale := chartPlacement(bar, work, anchor, u.scale)
	c.scale = scale
	if bounds != c.bounds {
		w, h := bounds.Right-bounds.Left, bounds.Bottom-bounds.Top
		resized := w != c.bounds.Right-c.bounds.Left || h != c.bounds.Bottom-c.bounds.Top
		c.bounds = bounds
		setWindowPos.Call(c.hwnd, ^uintptr(0), uintptr(bounds.Left), uintptr(bounds.Top), uintptr(w), uintptr(h), 0x10|0x0200)
		if !c.dwmCorners && resized {
			radius := uintptr(math.Round(16 * scale))
			rgn, _, _ := createRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), radius, radius)
			if rgn != 0 {
				if ok, _, _ := setWindowRgn.Call(c.hwnd, rgn, 1); ok == 0 {
					deleteObject.Call(rgn)
				}
			}
		}
	}
	u.paintChartFrame()
}

func (u *UI) paintChartFrame() {
	c := u.chart
	var client rect
	fluentGetClient.Call(c.hwnd, uintptr(unsafe.Pointer(&client)))
	if c.selected != 0 && chartSampleIndex(c.series.points, c.selected) < 0 {
		c.selected = 0
	}
	extra := chartExtras{data: c.detail, now: time.Now(), scroll: c.scroll, mouse: c.mouse, selected: c.selected}
	if client.Right > 0 && client.Bottom > 0 &&
		c.raster.render(client.Right, client.Bottom, c.scale, u.Language(), u.cfg.UpIsRed, c.view, c.series, &extra) == nil {
		c.scroll = c.raster.offset
		c.scrollTarget = min(c.scrollTarget, c.raster.scrollMax)
		invalidateRect.Call(c.hwnd, 0, 0)
	}
}

func (u *UI) chartReady() {
	if !u.chartOpen() {
		return
	}
	c := u.chart
	if c.queryValid && c.series.loading {
		c.series.points, c.series.loading = c.client.Result(c.query)
	}
	c.detail = c.detailClient.Read(c.view, quoteName(u.Language(), c.view), u.Language(), false, c.detail)
	u.refreshChart(false)
}

func (u *UI) scrollChart(delta int16) {
	c := u.chart
	step := int32(delta)*c.raster.px(64) + c.wheelRemainder
	c.wheelRemainder = step % 120
	c.scrollTarget = max(0, min(c.scrollTarget-step/120, c.raster.scrollMax))
	if c.scroll != c.scrollTarget && !c.scrollTimer {
		id, _, _ := panelSetTimer.Call(c.hwnd, chartScrollTimer, 16, 0)
		c.scrollTimer = id != 0
		if !c.scrollTimer {
			c.scroll = c.scrollTarget
			u.paintChartFrame()
		}
	}
}

func (u *UI) tickChartScroll() {
	c := u.chart
	diff := c.scrollTarget - c.scroll
	if diff >= -3 && diff <= 3 {
		c.scroll = c.scrollTarget
		killTimer.Call(c.hwnd, chartScrollTimer)
		c.scrollTimer = false
	} else {
		c.scroll += diff / 3
	}
	u.paintChartFrame()
	u.setChartCursor()
}

func (u *UI) setChartCursor() {
	c := u.chart
	if !within(rect{Right: c.raster.width, Bottom: c.raster.height}, c.mouse) {
		return
	}
	id := uintptr(32512) // IDC_ARROW
	if c.raster.linkAt(c.mouse) != "" {
		id = 32649 // IDC_HAND
	} else if c.raster.sampleAt(c.mouse, c.series.points) != 0 {
		id = 32515 // IDC_CROSS
	}
	cursor, _, _ := loadCursor.Call(0, id)
	chartSetCursor.Call(cursor)
}

func openChartArticle(hwnd uintptr, link string) {
	action, address := winapi.UTF16("open"), winapi.UTF16(link)
	chartShellExecute.Call(hwnd, uintptr(unsafe.Pointer(action)), uintptr(unsafe.Pointer(address)), 0, 0, 1)
}

func (u *UI) chartWindowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	c := u.chart
	switch msg {
	case 0x000F: // WM_PAINT
		var ps paintStruct
		dc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		r := &c.raster
		if dc != 0 && r.dc != 0 {
			bitBlt.Call(dc, 0, 0, uintptr(r.width), uintptr(r.height), r.dc, 0, 0, 0x00CC0020) // SRCCOPY
		}
		endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case 0x0014: // WM_ERASEBKGND
		return 1
	case 0x0021: // WM_MOUSEACTIVATE
		return 3 // MA_NOACTIVATE
	case 0x020A: // WM_MOUSEWHEEL
		u.scrollChart(int16(wparam >> 16))
		return 0
	case 0x0113: // WM_TIMER
		if wparam == chartScrollTimer && c.scrollTimer {
			u.tickChartScroll()
			return 0
		}
	case 0x0200: // WM_MOUSEMOVE
		before := c.raster.linkAt(c.mouse)
		sample := c.raster.sampleAt(c.mouse, c.series.points)
		c.mouse = pointerFromMessage(lparam)
		if !c.mouseTracking {
			tm := trackMouse{Size: uint32(unsafe.Sizeof(trackMouse{})), Flags: 2, HwndTrack: hwnd}
			ok, _, _ := trackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
			c.mouseTracking = ok != 0
		}
		if before != c.raster.linkAt(c.mouse) || (c.selected == 0 && sample != c.raster.sampleAt(c.mouse, c.series.points)) {
			u.paintChartFrame()
		}
		u.setChartCursor()
		return 0
	case 0x02A3: // WM_MOUSELEAVE
		highlighted := c.raster.linkAt(c.mouse) != "" || (c.selected == 0 && c.raster.sampleAt(c.mouse, c.series.points) != 0)
		c.mouse, c.mouseTracking, c.pressedLink = point{-1, -1}, false, ""
		c.pressedPlot = false
		if highlighted {
			u.paintChartFrame()
		}
		return 0
	case 0x0020: // WM_SETCURSOR
		if lparam&0xffff == 1 && (c.raster.linkAt(c.mouse) != "" || c.raster.sampleAt(c.mouse, c.series.points) != 0) {
			u.setChartCursor()
			return 1
		}
	case 0x0201: // WM_LBUTTONDOWN
		p := pointerFromMessage(lparam)
		c.pressedLink = c.raster.linkAt(p)
		c.pressedPlot = c.raster.sampleAt(p, c.series.points) != 0
		return 0
	case 0x0202: // WM_LBUTTONUP
		p := pointerFromMessage(lparam)
		if stamp := c.raster.sampleAt(p, c.series.points); c.pressedPlot && stamp != 0 {
			if c.selected == stamp {
				c.selected = 0
			} else {
				c.selected = stamp
				c.pinned, c.leaveAt = true, time.Time{}
			}
			c.mouse = p
			u.paintChartFrame()
			u.setChartCursor()
		} else if link := market.SecureNewsURL(c.raster.linkAt(p)); link != "" && link == c.pressedLink {
			open := c.openLink
			if open == nil {
				open = openChartArticle
			}
			open(hwnd, link)
		}
		c.pressedLink, c.pressedPlot = "", false
		return 0
	case 0x0010: // WM_CLOSE
		u.dismissChart()
		return 0
	case 0x0100: // WM_KEYDOWN
		if wparam == 27 {
			u.dismissChart()
			return 0
		}
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}
