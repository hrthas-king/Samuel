//go:build windows && (amd64 || arm64)

package ui

import (
	"math"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
)

const (
	barWhite   = 0xFFFFFFFF
	barUp      = 0xFF4ADE80
	barDown    = 0xFFF87171
	barIdle    = 0xFF94A3B8
	barLatency = 0xFFCBD5E1
	barDivider = 0xFF697483
)

type barHit struct {
	bounds rect
	symbol string
}

type barMarker struct {
	center point
	state  market.SessionState
	color  uint32
}

func markerColor(state market.SessionState) uint32 {
	switch state {
	case market.StateRegular:
		return 0xFF34D399
	case market.StatePreMarket:
		return 0xFFFFD60A
	case market.StatePostMarket:
		return 0xFF38BDF8
	case market.StateOvernight:
		return 0xFFBA55FF
	default:
		return barIdle
	}
}

func changeColor(pct float64, upIsRed bool) uint32 {
	if math.Abs(pct) < .005 {
		return barWhite
	}
	if (pct > 0) == upIsRed {
		return 0xFFFF5C6C
	}
	return 0xFF34D399
}

func (m barMarker) draw(d *dibSurface, scale float64) {
	radius := 4.0
	if m.state == market.StatePreMarket || m.state == market.StateOvernight {
		radius = 6
	}
	span := int32(math.Ceil(radius * scale))
	for y := max(0, m.center.Y-span); y < min(d.height, m.center.Y+span); y++ {
		for x := max(0, m.center.X-span); x < min(d.width, m.center.X+span); x++ {
			var samples uint32
			for sy := range 4 {
				for sx := range 4 {
					dx := (float64(x-m.center.X) + (float64(sx)+.5)/4) / scale
					dy := (float64(y-m.center.Y) + (float64(sy)+.5)/4) / scale
					if markerContains(m.state, dx, dy) {
						samples++
					}
				}
			}
			if samples != 0 {
				i := y*d.width + x
				d.pixels[i] = overCoverage(d.pixels[i], m.color, (255*samples+8)/16)
			}
		}
	}
}

func markerContains(state market.SessionState, x, y float64) bool {
	r2 := x*x + y*y
	switch state {
	case market.StatePreMarket:
		if r2 <= 3.1*3.1 {
			return true
		}
		ax, ay := math.Abs(x), math.Abs(y)
		return r2 >= 3.9*3.9 && r2 <= 5.8*5.8 &&
			(ax <= .72 || ay <= .72 || math.Abs(ax-ay)*.7071 <= .72)
	case market.StateOvernight:
		cutX, cutY := x-2.3, y+.9
		return r2 <= 5.2*5.2 && cutX*cutX+cutY*cutY > 4.2*4.2
	default:
		return r2 <= 16
	}
}

func (u *UI) releaseFonts() {
	for _, font := range []uintptr{u.font, u.stockFont, u.capFont} {
		if font != 0 {
			deleteObject.Call(font)
		}
	}
}

func (u *UI) rebuildFonts() {
	u.releaseFonts()
	u.font = newRasterFont(u.px(14), 600)
	u.stockFont = newRasterFont(u.px(12), 600)
	u.capFont = newRasterFont(u.px(9), 400)
}

func (u *UI) renderBarBitmap() error {
	f := &u.barBitmap
	if f.dc == 0 {
		if err := f.resize(u.width, u.height); err != nil {
			return err
		}
	}
	var snap app.Snapshot
	if p := u.latest.Load(); p != nil {
		snap = *p
	}
	centerY := u.height / 2
	curX := u.px(5)
	var text [20]rasterText
	runs := text[:0]
	add := func(text string, font uintptr, color uint32, left, top, bottom int32) int32 {
		run := textRun(text, font, color, rect{Left: left, Top: top, Bottom: bottom}, 0)
		run.bounds.Right = left + run.measure(f.dc).CX + max(1, u.px(1))
		runs = append(runs, run)
		return run.bounds.Right
	}
	flow := func(text string, color uint32) int32 {
		return add(text, u.font, color, curX, 0, u.height)
	}
	netColor := uint32(barIdle)
	switch snap.Status {
	case monitor.Online:
		netColor = markerColor(market.StateRegular)
	case monitor.Degraded:
		netColor = 0xFFF59E0B
	case monitor.Offline:
		netColor = barDown
	}
	var markers [3]barMarker
	markers[0] = barMarker{center: point{curX + u.px(4), centerY}, color: netColor}
	curX += u.px(13)
	curX = flow(Country(snap), 0xFFF0F6FF) + u.px(6)
	upColor, downColor := uint32(barIdle), uint32(barIdle)
	if snap.Rates.Valid && snap.Rates.Up > 0 {
		upColor = barUp
	}
	if snap.Rates.Valid && snap.Rates.Down > 0 {
		downColor = barDown
	}
	up, down := "↑", "↓"
	if u.cfg.ShowNetDetail {
		up, down = "↑--", "↓--"
		if snap.Rates.Valid {
			up, down = "↑"+CompactSpeed(snap.Rates.Up), "↓"+CompactSpeed(snap.Rates.Down)
		}
	}
	curX = flow(up, upColor) + u.px(3)
	curX = flow(down, downColor) + u.px(6)
	if u.cfg.ShowNetDetail {
		curX = flow(CompactRTT(snap), barLatency) + u.px(6)
	}
	hits := [3]barHit{{bounds: rect{Right: curX, Bottom: u.height}}}
	curX = flow("|", barDivider) + u.px(7)

	views := snap.Quotes
	if len(views) == 0 {
		group := market.GroupByID(u.cfg.MarketGroup)
		if group.ID == market.GroupTop2US && len(group.Symbols) == 0 {
			curX = flow(u.tr("等待市值排名"), barIdle)
		}
		for _, sym := range group.Symbols {
			views = append(views, market.View{Symbol: sym})
		}
	}
	views = views[:min(2, len(views))]
	rowBottom, quoteCenter, quoteFont := u.height, centerY, u.font
	for _, view := range views {
		if view.Quote != nil && view.Quote.HasSecondaryLine() {
			rowBottom, quoteCenter, quoteFont = u.px(17), u.px(17)/2, u.stockFont
			break
		}
	}
	for i, view := range views {
		left := curX
		state := view.EffectiveState()
		markers[i+1] = barMarker{point{curX + u.px(7), quoteCenter}, state, markerColor(state)}
		curX += u.px(17)
		if !view.Healthy {
			curX = add("!", quoteFont, barDown, curX, 0, rowBottom) + u.px(3)
		}
		nameLeft := curX
		curX = add(quoteName(u.Language(), view), quoteFont, barWhite, curX, 0, rowBottom) + u.px(5)
		text, color := "---.-- --.--%", uint32(barIdle)
		if q := view.Quote; q != nil {
			text, color = quotePriceChangeText(*q), changeColor(q.ChangePercent, u.cfg.UpIsRed)
		}
		curX = add(text, quoteFont, color, curX, 0, rowBottom)
		if q := view.Quote; q != nil && q.HasSecondaryLine() {
			capRight := add(u.tr(q.SecondaryText()), u.capFont, 0xFFAFB9C6, nameLeft, rowBottom, u.height)
			curX = max(curX, capRight)
		}
		hits[i+1] = barHit{rect{left, 0, curX + u.px(3), u.height}, view.Symbol}
		if i == 0 && len(views) > 1 {
			curX += u.px(7)
			curX = flow("|", barDivider) + u.px(7)
		}
	}

	width := curX + u.px(6)
	// Reserve a little bitmap space so changes to rate/price digits don't
	// constantly reallocate the DIB. Only the measured width is presented.
	unit := u.px(64)
	capacity := (width + unit - 1) / unit * unit
	if err := f.resize(capacity, u.height); err != nil {
		return err
	}
	clear(f.pixels)
	for _, m := range markers[:1+len(views)] {
		m.draw(&f.dibSurface, u.scale)
	}
	if err := f.drawText(runs); err != nil {
		return err
	}
	u.width, u.barHits = width, hits
	return nil
}

func (u *UI) renderBar() error {
	if err := u.renderBarBitmap(); err != nil {
		return err
	}
	if err := u.barBitmap.presentLayered(u.hwnd, u.width, u.height); err != nil {
		return err
	}
	u.syncBarInput()
	u.refreshChart(false)
	return nil
}

func (u *UI) paint(hwnd uintptr) {
	var ps paintStruct
	beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hwnd == u.hwnd {
		_ = u.renderBar()
	}
}
