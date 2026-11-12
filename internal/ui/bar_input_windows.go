//go:build windows && (amd64 || arm64)

package ui

import (
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

const (
	barHoverTimer    = 31
	chartHover       = 150 * time.Millisecond
	chartSwitchHover = 220 * time.Millisecond
	chartGrace       = 520 * time.Millisecond
)

var (
	killTimer         = user32.NewProc("KillTimer")
	barClientToScreen = user32.NewProc("ClientToScreen")
)

type barHover struct {
	hit      int // zero outside; one for network; two and three for quotes
	due      time.Time
	tracking bool
	timer    bool
}

type barPress struct {
	down, dragging bool
	start, offset  point
	symbol         string
}

// An alpha-zero pixel in a layered HWND is inherently click-through. Use an
// input-only popup with no DWM redirection surface when the bar is unlocked,
// so gaps between glyphs work just like the glyphs for clicks and dragging.
func (u *UI) enableBarInput() {
	if u.barInput == 0 {
		const inputStyle = 0x00200000 | 0x08000000 | 0x80 | 0x8 // NOREDIRECTIONBITMAP | NOACTIVATE | TOOLWINDOW | TOPMOST
		cls := winapi.UTF16(overlayAuxClass)
		u.barInput, _, _ = createWindow.Call(inputStyle, uintptr(unsafe.Pointer(cls)), 0, 0x80000000,
			0, 0, 0, 0, u.hwnd, 0, u.instance, 0)
	}
	if u.barInput != 0 {
		u.syncBarInput()
		showWindow.Call(u.barInput, 4)
	}
}

func (u *UI) syncBarInput() {
	if u.barInput == 0 || u.cfg.Locked {
		return
	}
	var bounds rect
	if ok, _, _ := getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&bounds))); ok != 0 && bounds != u.inputBounds {
		u.inputBounds = bounds
		setWindowPos.Call(u.barInput, 0, uintptr(bounds.Left), uintptr(bounds.Top),
			uintptr(bounds.Right-bounds.Left), uintptr(bounds.Bottom-bounds.Top), 0x4|0x10|0x0200)
	}
}

// Each divider's gap belongs to its two neighboring regions. The visible
// bounds stay unchanged, so extending the target doesn't paint the bar.
func (u *UI) barHitBounds(i int) rect {
	bounds := u.barHits[i].bounds
	if i > 0 {
		if u.barHits[i].symbol == "" {
			return rect{}
		}
		prev := u.barHits[i-1].bounds
		bounds.Left = prev.Right + (bounds.Left-prev.Right)/2
	}
	if i+1 < len(u.barHits) && u.barHits[i+1].symbol != "" {
		next := u.barHits[i+1].bounds
		bounds.Right += (next.Left - bounds.Right) / 2
	} else {
		bounds.Right = u.width
	}
	return bounds
}

func (u *UI) hitAt(p point) int {
	for i := range u.barHits {
		if within(u.barHitBounds(i), p) {
			return i + 1
		}
	}
	return 0
}

func (u *UI) symbolAt(p point) string {
	if hit := u.hitAt(p); hit > 1 {
		return u.barHits[hit-1].symbol
	}
	return ""
}

func pointerFromMessage(lparam uintptr) point {
	return point{int32(int16(lparam)), int32(int16(lparam >> 16))}
}

func barScreenPoint(hwnd uintptr, p point) point {
	barClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return p
}

func (u *UI) syncHoverTimer() {
	active := u.chartOpen() || (!u.cfg.Locked && !u.hover.due.IsZero())
	if active && !u.hover.timer {
		id, _, _ := panelSetTimer.Call(u.hwnd, barHoverTimer, 25, 0)
		u.hover.timer = id != 0
	} else if !active && u.hover.timer {
		killTimer.Call(u.hwnd, barHoverTimer)
		u.hover.timer = false
	}
}

func (u *UI) dismissChart() {
	u.hover.due = time.Time{}
	u.hideChart()
}

func (u *UI) barMouseMove(hwnd uintptr, p point) {
	if u.cfg.Locked || u.flyout != nil || u.customPanel != nil {
		return
	}
	if u.press.down {
		screen := barScreenPoint(hwnd, p)
		if !u.press.dragging && u.barPressMoved(u.press, screen) {
			u.press.dragging = true
			u.dismissChart()
		}
		if u.press.dragging {
			setWindowPos.Call(u.hwnd, ^uintptr(0), uintptr(screen.X-u.press.offset.X),
				uintptr(screen.Y-u.press.offset.Y), 0, 0, 0x1|0x10)
		}
		return
	}
	if !u.hover.tracking {
		tm := trackMouse{Size: uint32(unsafe.Sizeof(trackMouse{})), Flags: 2, HwndTrack: hwnd} // TME_LEAVE
		ok, _, _ := trackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
		u.hover.tracking = ok != 0
	}
	u.updateBarHover(u.hitAt(p), time.Now())
}

func (u *UI) updateBarHover(hit int, now time.Time) {
	if hit == u.hover.hit {
		return
	}
	u.hover.hit, u.hover.due = hit, time.Time{}
	u.hideTooltip()
	if u.chartOpen() && u.chart.pinned {
		return
	}
	if hit == 1 {
		u.hover.due = now.Add(400 * time.Millisecond)
	} else if hit > 1 {
		if !u.chartOpen() {
			u.hover.due = now.Add(chartHover)
		} else if u.chart.symbol != u.barHits[hit-1].symbol {
			u.hover.due = now.Add(chartSwitchHover)
		}
	}
	u.syncHoverTimer()
}

func (u *UI) barMouseLeave() {
	u.hover.tracking, u.hover.hit, u.hover.due = false, 0, time.Time{}
	u.hideTooltip()
	u.syncHoverTimer()
}

func (u *UI) barHoverTick(now time.Time) {
	if !u.hover.timer {
		return
	}
	var screen point
	getCursor.Call(uintptr(unsafe.Pointer(&screen)))
	if u.chartOpen() {
		c := u.chart
		_, anchor, _ := u.chartAnchor(c.symbol)
		inside := within(anchor, screen) || within(c.bounds, screen)
		esc, _, _ := getAsyncKeyState.Call(27)
		left, _, _ := getAsyncKeyState.Call(1)
		right, _, _ := getAsyncKeyState.Call(2)
		outsideClick := c.outsideClick(!inside, (left|right)&0x8000 != 0)
		if esc&0x8000 != 0 || outsideClick {
			u.dismissChart()
			return
		}
		if !c.pinned {
			if inside || within(chartBridge(anchor, c.bounds), screen) {
				c.leaveAt = time.Time{}
			} else if c.leaveAt.IsZero() {
				c.leaveAt = now.Add(chartGrace)
			} else if !now.Before(c.leaveAt) {
				u.hideChart()
			}
		}
	}
	if !u.hover.due.IsZero() && !now.Before(u.hover.due) {
		var bar rect
		getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&bar)))
		hit := u.hitAt(point{screen.X - bar.Left, screen.Y - bar.Top})
		if hit != u.hover.hit {
			// The network rates and quote widths may change while the pointer
			// is still. Start the debounce for the new target.
			u.updateBarHover(hit, now)
		} else if !u.press.down {
			u.hover.due = time.Time{}
			if hit == 1 {
				u.hideChart()
				u.showTooltip()
			} else if hit > 1 {
				u.showChart(u.barHits[hit-1].symbol, chartActivation{})
			}
		}
	}
	u.syncHoverTimer()
}

func chartBridge(anchor, card rect) rect {
	left, right := max(anchor.Left, card.Left), min(anchor.Right, card.Right)
	if anchor.Top >= card.Bottom {
		return rect{left, card.Bottom, right, anchor.Top}
	}
	return rect{left, anchor.Bottom, right, card.Top}
}

func (u *UI) barPressMoved(press barPress, screen point) bool {
	logical := 4
	if press.symbol != "" {
		logical = 9
	}
	threshold := max(2, u.px(logical))
	dx, dy := screen.X-press.start.X, screen.Y-press.start.Y
	return dx >= threshold || dx <= -threshold || dy >= threshold || dy <= -threshold
}

func (u *UI) pressBar(hwnd uintptr, p point) {
	if u.cfg.Locked {
		return
	}
	u.hideTooltip()
	u.hover.due = time.Time{}
	screen := barScreenPoint(hwnd, p)
	var bar rect
	getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&bar)))
	u.press = barPress{down: true, start: screen, offset: point{screen.X - bar.Left, screen.Y - bar.Top}, symbol: u.symbolAt(p)}
	setCapture.Call(hwnd)
	u.syncHoverTimer()
}

func (u *UI) releaseBar(hwnd uintptr, p point) {
	if !u.press.down {
		return
	}
	press := u.press
	u.press = barPress{}
	releaseCapture.Call()
	if press.dragging {
		var rc rect
		getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&rc)))
		x, y := int(rc.Left), int(rc.Top)
		u.cfg.Placement, u.cfg.X, u.cfg.Y = "manual", &x, &y
		_ = config.Save(u.cfg)
	} else if press.symbol != "" {
		if !u.barPressMoved(press, barScreenPoint(hwnd, p)) {
			u.showChart(press.symbol, chartActivation{pinned: true})
		}
	}
}
