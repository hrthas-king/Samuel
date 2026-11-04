//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	setWindowText    = user32.NewProc("SetWindowTextW")
	getWindowText    = user32.NewProc("GetWindowTextW")
	enableWindow     = user32.NewProc("EnableWindow")
	setFocus         = user32.NewProc("SetFocus")
	panelForeground  = user32.NewProc("GetForegroundWindow")
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	panelSetTimer    = user32.NewProc("SetTimer")
	callWindowProc   = user32.NewProc("CallWindowProcW")
	getMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	panelWindows     sync.Map
	panelEdits       sync.Map
	panelProcedure   = syscall.NewCallback(customPanelProc)
	editProcedure    = syscall.NewCallback(customEditProc)
)

type customPanel struct {
	u                                   *UI
	hwnd, first, second, submit, status uintptr
	busy                                bool
	outsideArmed                        bool
	focusEstablished                    bool
	visual                              *fluentVisuals
	cancel                              uintptr
	statusError                         bool
	controls                            []uintptr
	titleFont                           uintptr
}
type editBinding struct {
	panel    *customPanel
	original uintptr
}

func panelText(hwnd uintptr) string {
	var buf [128]uint16
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:])
}
func (p *customPanel) setStatus(text string) {
	p.statusError = !p.busy
	s := winapi.UTF16(p.u.tr(text))
	setWindowText.Call(p.status, uintptr(unsafe.Pointer(s)))
	invalidateRect.Call(p.status, 0, 1)
}

func (u *UI) closeCustomPanel() {
	p := u.customPanel
	if p == nil {
		return
	}
	u.customPanel = nil
	u.customEpoch++
	if u.customCancel != nil {
		u.customCancel()
		u.customCancel = nil
	}
	destroyWindow.Call(p.hwnd)
}

func (u *UI) openCustomPanel(show bool) {
	u.closeCustomPanel()
	u.customEpoch++
	instance := u.instance
	if instance == 0 {
		instance, _, _ = getModule.Call(0)
	}
	cls := winapi.UTF16("PulseBar.CustomStocks")
	wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Style: 0x20000, Procedure: panelProcedure, Instance: instance, ClassName: cls}
	cursor, _, _ := loadCursor.Call(0, 32512)
	wc.Cursor = cursor
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 && err != syscall.Errno(1410) {
		u.showError(winapi.Error("RegisterClassExW", err))
		return
	}
	var pos point
	getCursor.Call(uintptr(unsafe.Pointer(&pos)))
	// Keep the wider panel inside the current monitor's working area, even at high DPI.
	work := popupWorkArea(pos)
	scale := u.dpiScale
	if scale <= 0 {
		scale = 1
	}
	maxScale := min(float64(work.Right-work.Left-16)/520, float64(work.Bottom-work.Top-16)/374)
	if maxScale > 0 && scale > maxScale {
		scale = maxScale
	}
	v := newFluentVisuals(scale, systemFluentColors())
	w, h := v.px(520), v.px(374)
	x, y := pos.X+v.px(8), pos.Y+v.px(8)
	anchor := rect{pos.X, pos.Y, pos.X + 1, pos.Y + 1}
	monitor, _, _ := monitorFromRect.Call(uintptr(unsafe.Pointer(&anchor)), 2)
	var info struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		if x+w > info.Work.Right {
			x = info.Work.Right - w
		}
		if y+h > info.Work.Bottom {
			y = info.Work.Bottom - h
		}
		if x < info.Work.Left {
			x = info.Work.Left
		}
		if y < info.Work.Top {
			y = info.Work.Top
		}
	}
	title := winapi.UTF16(u.tr("新增自定义股票 / ETF组合"))
	hwnd, _, err := createWindow.Call(0x8|0x80, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)), 0x80000000|0x02000000, uintptr(x), uintptr(y), uintptr(w), uintptr(h), u.hwnd, 0, instance, 0) // WS_POPUP | WS_CLIPCHILDREN
	if hwnd == 0 {
		v.close()
		u.showError(winapi.Error("CreateWindowExW", err))
		return
	}
	p := &customPanel{u: u, hwnd: hwnd, visual: v}
	u.customPanel = p
	panelWindows.Store(hwnd, p)
	child := func(class, text string, style uintptr, id uintptr, l, t, width, height int) uintptr {
		c, s := winapi.UTF16(class), winapi.UTF16(u.tr(text))
		control, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(s)), 0x40000000|0x10000000|style, uintptr(v.px(l)), uintptr(v.px(t)), uintptr(v.px(width)), uintptr(v.px(height)), hwnd, id, instance, 0)
		if control != 0 {
			p.controls = append(p.controls, control)
			sendMessage.Call(control, 0x30, v.font, 1)
		}
		return control
	}
	face := winapi.UTF16("Segoe UI")
	p.titleFont, _, _ = createFont.Call(uintptr(-int(v.px(23))), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	heading := child("STATIC", "新增自定义组合", 0, 0, 26, 22, 468, 32)
	if p.titleFont != 0 {
		sendMessage.Call(heading, 0x30, p.titleFont, 1)
	} else {
		sendMessage.Call(heading, 0x30, v.heading, 1)
	}
	subtitle := child("STATIC", "支持沪深北股票、场内 ETF 和美股", 0, 0, 26, 63, 468, 22)
	sendMessage.Call(subtitle, 0x30, v.font, 1)
	child("STATIC", "代码 1", 0, 0, 26, 99, 468, 22)
	p.first = child("EDIT", "", 0x10000|0x80, 1, 37, 135, 446, 22)
	child("STATIC", "代码 2", 0, 0, 26, 183, 468, 22)
	p.second = child("EDIT", "", 0x10000|0x80, 2, 37, 219, 446, 22)
	p.status = child("STATIC", "例如：513100 + 513500 / AAPL + MSFT\nEnter 添加 · Esc 或点击外部关闭", 0, 0, 26, 268, 468, 38)
	sendMessage.Call(p.status, 0x30, v.small, 1)
	p.cancel = child("BUTTON", "取消", 0x10000|0xb, 4, 265, 313, 95, 41)
	p.submit = child("BUTTON", "添加并显示", 0x10000|0xb, 3, 372, 313, 122, 41)
	if p.first == 0 || p.second == 0 || p.submit == 0 || p.status == 0 || p.cancel == 0 {
		u.closeCustomPanel()
		u.showError(fmt.Errorf("无法创建输入控件"))
		return
	}
	for _, edit := range []uintptr{p.first, p.second} {
		sendMessage.Call(edit, 0xc5, 10, 0) // EM_SETLIMITTEXT: also allow US share classes.
		cue := winapi.UTF16(u.tr("6位数字或美股代码，如 AAPL"))
		sendMessage.Call(edit, 0x1501, 0, uintptr(unsafe.Pointer(cue)))
	}
	for _, control := range []uintptr{p.first, p.second, p.cancel, p.submit} {
		original, _, _ := setWindowLongPtr.Call(control, ^uintptr(3), editProcedure)
		panelEdits.Store(control, editBinding{p, original})
	}
	applyFluentFrame(hwnd, v.palette)
	if show {
		showWindow.Call(hwnd, 5)
		setForeground.Call(hwnd)
		setWindowPos.Call(hwnd, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x40)
		setFocus.Call(p.first)
		active, _, _ := panelForeground.Call()
		p.focusEstablished = active == hwnd
		panelSetTimer.Call(hwnd, 1, 50, 0)
	}
}

func customPanelProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	if value, ok := panelWindows.Load(hwnd); ok {
		p := value.(*customPanel)
		switch msg {
		case 0x318:
			p.drawFrame(wp)
			return 0
		case 0xf:
			var paint paintStruct
			dc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
			p.drawFrame(dc)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
			return 0
		case 0x14:
			return 1
		case 0x133, 0x138:
			c := p.visual.palette
			color, bg, brush := c.text, c.background, p.visual.background
			if lp == p.first || lp == p.second {
				bg, brush = c.input, p.visual.input
				if p.busy {
					color = c.muted
				}
			}
			if lp == p.status {
				color = c.muted
				if p.statusError {
					color = c.danger
				}
			}
			setTextColor.Call(wp, color)
			fluentSetBk.Call(wp, bg)
			return brush
		case 0x2b:
			if lp != 0 {
				var item fluentDrawItem
				rtlMoveMemory.Call(uintptr(unsafe.Pointer(&item)), lp, unsafe.Sizeof(item))
				p.drawButton(&item)
				return 1
			}
		case 0x1a, 0x31a:
			p.visual.refresh(systemFluentColors())
			applyFluentFrame(hwnd, p.visual.palette)
			invalidateRect.Call(hwnd, 0, 1)
			for _, child := range p.controls {
				invalidateRect.Call(child, 0, 1)
			}
			return 0
		case 0x0113: // WM_TIMER: also catch clicks on non-activating taskbar windows.
			active, _, _ := panelForeground.Call()
			if active == hwnd {
				p.focusEstablished = true
			}
			var cursor point
			getCursor.Call(uintptr(unsafe.Pointer(&cursor)))
			left, _, _ := getAsyncKeyState.Call(1)
			right, _, _ := getAsyncKeyState.Call(2)
			p.outsideClick(cursor, (left|right)&0x8000 != 0)
			return 0
		case 0x0006: // WM_ACTIVATE
			// Initial activation may fail while the native menu/shell is releasing focus.
			// Only a genuine foreground-to-background transition is a dismissal.
			if wp&0xffff == 0 && p.focusEstablished {
				p.u.closeCustomPanel()
			}
			return 0
		case 0x0010:
			p.u.closeCustomPanel()
			return 0
		case 0x0111:
			switch wp & 0xffff {
			case 3:
				p.u.submitCustomPair()
			case 4:
				p.u.closeCustomPanel()
			}
			return 0
		case 0x0100:
			if wp == 27 {
				p.u.closeCustomPanel()
				return 0
			}
			if wp == 13 {
				p.u.submitCustomPair()
				return 0
			}
		case 0x0082:
			panelWindows.Delete(hwnd)
			if p.titleFont != 0 {
				deleteObject.Call(p.titleFont)
			}
			p.visual.close()
		}
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func (p *customPanel) outsideClick(cursor point, pressed bool) {
	if !pressed {
		p.outsideArmed = true
		return
	}
	if !p.outsideArmed {
		return
	} // Ignore the click that opened the menu/panel.
	var bounds rect
	getWindowRect.Call(p.hwnd, uintptr(unsafe.Pointer(&bounds)))
	if cursor.X < bounds.Left || cursor.X >= bounds.Right || cursor.Y < bounds.Top || cursor.Y >= bounds.Bottom {
		p.u.closeCustomPanel()
	}
}

func customEditProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	value, ok := panelEdits.Load(hwnd)
	if !ok {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
		return r
	}
	b := value.(editBinding)
	if msg == 0x0007 || msg == 0x0008 {
		invalidateRect.Call(b.panel.hwnd, 0, 0)
		active, _, _ := panelForeground.Call()
		if active == b.panel.hwnd {
			b.panel.focusEstablished = true
		}
	}
	if msg == 0x0102 && (wp == 13 || wp == 27 || wp == 9) {
		return 0
	}
	if msg == 0x0100 {
		switch wp {
		case 13:
			if hwnd == b.panel.cancel {
				b.panel.u.closeCustomPanel()
			} else {
				b.panel.u.submitCustomPair()
			}
			return 0
		case 27:
			b.panel.u.closeCustomPanel()
			return 0
		case 9:
			controls := []uintptr{b.panel.first, b.panel.second, b.panel.cancel, b.panel.submit}
			shift, _, _ := user32.NewProc("GetKeyState").Call(0x10)
			step := 1
			if shift&0x8000 != 0 {
				step = -1
			}
			if !b.panel.busy {
				for i, control := range controls {
					if control == hwnd {
						setFocus.Call(controls[(i+step+len(controls))%len(controls)])
						break
					}
				}
			}
			return 0
		}
	}
	if msg == 0x0082 {
		panelEdits.Delete(hwnd)
	}
	r, _, _ := callWindowProc.Call(b.original, hwnd, uintptr(msg), wp, lp)
	return r
}

type fluentDrawItem struct {
	Type, ID, Item, Action, State uint32
	Window, DC                    uintptr
	Bounds                        rect
	Data                          uintptr
}

func (p *customPanel) drawFrame(dc uintptr) {
	v, c := p.visual, p.visual.palette
	var bounds rect
	fluentGetClient.Call(p.hwnd, uintptr(unsafe.Pointer(&bounds)))
	fillRect.Call(dc, uintptr(unsafe.Pointer(&bounds)), v.background)
	fluentBox(dc, bounds, v.px(20), c.background, c.border)
	focus, _, _ := user32.NewProc("GetFocus").Call()
	for i, edit := range []uintptr{p.first, p.second} {
		top := 125 + i*84
		border := c.border
		if focus == edit {
			border = c.accent
		}
		r := rect{v.px(26), v.px(top), v.px(494), v.px(top + 42)}
		fluentBox(dc, r, v.px(12), c.input, border)
	}
}

func (p *customPanel) drawButton(item *fluentDrawItem) {
	v, c := p.visual, p.visual.palette
	fill, border, text := c.input, c.border, c.text
	if item.Window == p.submit {
		fill, border, text = c.accent, c.accent, c.onAccent
	}
	if item.State&4 != 0 {
		fill, border, text = c.hover, c.border, c.muted
	} else if item.State&1 != 0 {
		fill, border = c.hover, c.border
		text = c.text
	}
	// Owner-drawn button corners belong to the child window; clear them to the
	// panel background before painting the rounded button surface.
	fillRect.Call(item.DC, uintptr(unsafe.Pointer(&item.Bounds)), v.background)
	if item.State&16 != 0 && item.Window == p.cancel {
		border = c.accent
	}
	fluentBox(item.DC, item.Bounds, v.px(12), fill, border)
	fluentText(item.DC, panelText(item.Window), item.Bounds, v.font, text, 1)
	if item.State&16 != 0 && item.State&0x200 == 0 && item.Window == p.submit {
		r := item.Bounds
		r.Left += v.px(3)
		r.Top += v.px(3)
		r.Right -= v.px(3)
		r.Bottom -= v.px(3)
		fluentBox(item.DC, r, v.px(8), fill, text)
		fluentText(item.DC, panelText(item.Window), item.Bounds, v.font, text, 1)
	}
}
