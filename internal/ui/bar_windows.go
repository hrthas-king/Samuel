//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

type Action uint32

const (
	ActionRefresh Action = 1001 + iota
	ActionToggleNetDetail
	ActionToggleLock
	ActionDockLeft
	ActionToggleColor
	ActionGroupVOOQQQ
	ActionGroupSOXXSMH
	ActionGroupTop2US
	ActionGroupGoldOil
	ActionGroupCNIndices
	ActionGroupHKIndices
	ActionGroupAsiaIndices
	ActionGroupEUIndices
	ActionGroupETF159632159655
	ActionGroupUSIndices
	ActionGroupCNComposite
	ActionScale75
	ActionScale85
	ActionScale90
	ActionScale100
	ActionScale115
	ActionScale130
	ActionInterval1
	ActionInterval2
	ActionInterval5
	ActionDiagnostics
	ActionAutostart
	ActionExit
	ActionTrayIconApplication
	ActionTrayIconNetwork
)

func GroupForAction(act Action) (string, bool) {
	if act < ActionGroupVOOQQQ || act > ActionGroupCNComposite {
		return "", false
	}
	idx := int(act - ActionGroupVOOQQQ)
	if idx < 0 || idx >= len(market.Groups) {
		return "", false
	}
	return market.Groups[idx].ID, true
}

func ScaleForAction(act Action) (int, bool) {
	if act < ActionScale75 || act > ActionScale130 {
		return 0, false
	}
	idx := int(act - ActionScale75)
	if idx < 0 || idx >= len(config.UIScalePresets) {
		return 0, false
	}
	return config.UIScalePresets[idx], true
}

var scaleMenuLabels = map[int]string{
	75:  "75% · 迷你",
	85:  "85% · 紧凑",
	90:  "90% · 适中",
	100: "100% · 标准",
	115: "115% · 放大",
	130: "130% · 特大",
}

const (
	wmTray   = 0x8001
	wmUpdate = 0x8002
)

const (
	slotWidthLogical    = 176
	compactWidthLogical = 86 + 2*slotWidthLogical
	detailWidthLogical  = 196 + 2*slotWidthLogical
	barHeightLogical    = 30
)

var (
	user32               = winapi.DLL("user32.dll")
	gdi32                = winapi.DLL("gdi32.dll")
	shell32              = winapi.DLL("shell32.dll")
	kernel32             = winapi.DLL("kernel32.dll")
	comctl32             = winapi.DLL("comctl32.dll")
	registerClass        = user32.NewProc("RegisterClassExW")
	unregisterClass      = user32.NewProc("UnregisterClassW")
	createWindow         = user32.NewProc("CreateWindowExW")
	defWindowProc        = user32.NewProc("DefWindowProcW")
	destroyWindow        = user32.NewProc("DestroyWindow")
	showWindow           = user32.NewProc("ShowWindow")
	setWindowPos         = user32.NewProc("SetWindowPos")
	setLayeredAttrs      = user32.NewProc("SetLayeredWindowAttributes")
	getWindowLongPtr     = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtr     = user32.NewProc("SetWindowLongPtrW")
	getMessage           = user32.NewProc("GetMessageW")
	translateMessage     = user32.NewProc("TranslateMessage")
	dispatchMessage      = user32.NewProc("DispatchMessageW")
	postMessage          = user32.NewProc("PostMessageW")
	sendMessage          = user32.NewProc("SendMessageW")
	postQuit             = user32.NewProc("PostQuitMessage")
	registerMessage      = user32.NewProc("RegisterWindowMessageW")
	findWindow           = user32.NewProc("FindWindowW")
	getWindowRect        = user32.NewProc("GetWindowRect")
	getCursor            = user32.NewProc("GetCursorPos")
	setCapture           = user32.NewProc("SetCapture")
	releaseCapture       = user32.NewProc("ReleaseCapture")
	trackMouseEvent      = user32.NewProc("TrackMouseEvent")
	getSystemMetrics     = user32.NewProc("GetSystemMetrics")
	getDpiForSystem      = user32.NewProc("GetDpiForSystem")
	monitorFromRect      = user32.NewProc("MonitorFromRect")
	invalidateRect       = user32.NewProc("InvalidateRect")
	beginPaint           = user32.NewProc("BeginPaint")
	endPaint             = user32.NewProc("EndPaint")
	fillRect             = user32.NewProc("FillRect")
	drawText             = user32.NewProc("DrawTextW")
	loadCursor           = user32.NewProc("LoadCursorW")
	loadImage            = user32.NewProc("LoadImageW")
	createIcon           = user32.NewProc("CreateIcon")
	destroyIcon          = user32.NewProc("DestroyIcon")
	createMenu           = user32.NewProc("CreatePopupMenu")
	appendMenu           = user32.NewProc("AppendMenuW")
	destroyMenu          = user32.NewProc("DestroyMenu")
	setForeground        = user32.NewProc("SetForegroundWindow")
	trackMenu            = user32.NewProc("TrackPopupMenu")
	setDPI               = user32.NewProc("SetProcessDpiAwarenessContext")
	messageBox           = user32.NewProc("MessageBoxW")
	getModule            = kernel32.NewProc("GetModuleHandleW")
	rtlMoveMemory        = kernel32.NewProc("RtlMoveMemory")
	shellNotify          = shell32.NewProc("Shell_NotifyIconW")
	createSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	createFont           = gdi32.NewProc("CreateFontW")
	selectObject         = gdi32.NewProc("SelectObject")
	deleteObject         = gdi32.NewProc("DeleteObject")
	setTextColor         = gdi32.NewProc("SetTextColor")
	setBkMode            = gdi32.NewProc("SetBkMode")
	ellipse              = gdi32.NewProc("Ellipse")
	setPixel             = gdi32.NewProc("SetPixel")
	getTextExtentPoint32 = gdi32.NewProc("GetTextExtentPoint32W")
	initCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
)

type point struct{ X, Y int32 }
type textSize struct{ CX, CY int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type message struct {
	Window         uintptr
	Message        uint32
	_              uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	_              uint32
}

type windowClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}

type paintStruct struct {
	HDC         uintptr
	Erase       int32
	Paint       rect
	Restore     int32
	IncUpdate   int32
	RGBReserved [32]byte
	_           [4]byte
}

// notifyIcon 对应 64 位 Windows NOTIFYICONDATAW（976 字节）。
type notifyIcon struct {
	Size                uint32
	_                   uint32
	Window              uintptr
	ID, Flags, Callback uint32
	_                   uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

// toolInfo 对应 64 位 Windows TTTOOLINFOW（72 字节）。
type toolInfo struct {
	Size     uint32
	Flags    uint32
	Hwnd     uintptr
	ID       uintptr
	Rect     rect
	Hinst    uintptr
	Text     *uint16
	LParam   uintptr
	Reserved uintptr
}

type trackMouse struct {
	Size      uint32
	Flags     uint32
	HwndTrack uintptr
	HoverTime uint32
	_         uint32
}

func colorRef(r, g, b byte) uintptr {
	return uintptr(uint32(r) | (uint32(g) << 8) | (uint32(b) << 16))
}

type UI struct {
	version        string
	hwnd           uintptr
	tooltipHwnd    uintptr
	tool           toolInfo
	tooltipBuf     [2048]uint16
	instance       uintptr
	dpiScale       float64
	scale          float64
	width, height  int32
	bgBrush        uintptr
	brushGreen     uintptr
	brushBlue      uintptr
	brushYellow    uintptr
	brushRed       uintptr
	brushGray      uintptr
	font           uintptr
	stockFont      uintptr
	capFont        uintptr
	data           notifyIcon
	icons          map[string]uintptr
	appIcon        uintptr
	iconCountry    string
	trayAdded      bool
	taskbarMessage uint32
	latest         atomic.Pointer[app.Snapshot]
	desiredGroup   atomic.Value
	updatePending  atomic.Bool
	startup        atomic.Bool
	cfg            config.Config
	dragging       bool
	dragOffsetX    int32
	dragOffsetY    int32
	hoverTracking  bool
	tooltipVisible bool
	Actions        chan Action
	GroupChanges   chan string
	customPanel    *customPanel
	flyout         *fluentMenu
	menuContext    context.Context
	menuFetch      func(context.Context, []string) (map[string]market.SymbolResult, error)
	menuBusy       atomic.Bool
	menuQuotes     atomic.Pointer[menuQuoteSnapshot]
	menuTracker    *market.Tracker
	customEpoch    uint64
	customCancel   context.CancelFunc
	customResults  chan pairValidation
	saveSettings   func(config.Config) error
	validatePair   pairValidator
	callback       uintptr
}

func NewUI(version string, cfg config.Config) *UI {
	if !config.ValidUIScalePercent(cfg.UIScalePercent) {
		cfg.UIScalePercent = config.DefaultUIScalePercent
	}
	if !config.ValidTrayIcon(cfg.TrayIcon) {
		cfg.TrayIcon = config.TrayIconApplication
	}
	u := &UI{
		version:       version,
		cfg:           cfg,
		dpiScale:      1.0,
		scale:         float64(cfg.UIScalePercent) / 100.0,
		icons:         make(map[string]uintptr),
		Actions:       make(chan Action, 16),
		GroupChanges:  make(chan string, 1),
		customResults: make(chan pairValidation, 8),
		saveSettings:  config.Save,
		validatePair:  market.ValidateCustomPair,
	}
	u.desiredGroup.Store(cfg.MarketGroup)
	return u
}

func (u *UI) recalcScale() {
	pct := u.cfg.UIScalePercent
	if !config.ValidUIScalePercent(pct) {
		pct = config.DefaultUIScalePercent
	}
	if u.dpiScale <= 0 {
		u.dpiScale = 1.0
	}
	u.scale = u.dpiScale * float64(pct) / 100.0
}

func (u *UI) rebuildFonts() {
	if u.font != 0 {
		deleteObject.Call(u.font)
	}
	if u.capFont != 0 {
		deleteObject.Call(u.capFont)
	}
	if u.stockFont != 0 {
		deleteObject.Call(u.stockFont)
	}
	fontName := winapi.UTF16("Segoe UI")
	u.font, _, _ = createFont.Call(uintptr(int(-u.px(14))), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 3, 0, uintptr(unsafe.Pointer(fontName)))
	u.stockFont, _, _ = createFont.Call(uintptr(int(-u.px(12))), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 3, 0, uintptr(unsafe.Pointer(fontName)))
	u.capFont, _, _ = createFont.Call(uintptr(int(-u.px(9))), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 3, 0, uintptr(unsafe.Pointer(fontName)))
	runtime.KeepAlive(fontName)
}

func (u *UI) SetAutostart(enabled bool) { u.startup.Store(enabled) }
func (u *UI) Config() config.Config     { return u.cfg }

func (u *UI) Publish(s app.Snapshot) {
	if desired, ok := u.desiredGroup.Load().(string); ok && desired != "" && s.MarketGroup != desired {
		return
	}
	u.latest.Store(&s)
	if u.hwnd == 0 {
		return
	}
	if !u.updatePending.Swap(true) {
		ok, _, _ := postMessage.Call(u.hwnd, wmUpdate, 0, 0)
		if ok == 0 {
			u.updatePending.Store(false)
		}
	}
}

func (u *UI) px(v int) int32 {
	return int32(math.Round(float64(v) * u.scale))
}

func (u *UI) updateDimensions() {
	w := compactWidthLogical
	if u.cfg.ShowNetDetail {
		w = detailWidthLogical
	}
	u.width = u.px(w)
	u.height = u.px(barHeightLogical)
}

func (u *UI) initialPosition() (int32, int32) {
	if u.cfg.Placement == "manual" && u.cfg.X != nil && u.cfg.Y != nil {
		x, y := int32(*u.cfg.X), int32(*u.cfg.Y)
		rc := rect{Left: x, Top: y, Right: x + u.width, Bottom: y + u.height}
		if mon, _, _ := monitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), 0); mon != 0 {
			return x, y
		}
	}
	shellTray := winapi.UTF16("Shell_TrayWnd")
	taskbar, _, _ := findWindow.Call(uintptr(unsafe.Pointer(shellTray)), 0)
	var rc rect
	if taskbar != 0 {
		if ok, _, _ := getWindowRect.Call(taskbar, uintptr(unsafe.Pointer(&rc))); ok != 0 {
			top := rc.Top + max(0, (rc.Bottom-rc.Top-u.height)/2)
			return rc.Left + u.px(8), top
		}
	}
	screenH, _, _ := getSystemMetrics.Call(1)
	return u.px(8), max(0, int32(screenH)-u.height-u.px(12))
}

func copyText(dst []uint16, text string) {
	clear(dst)
	enc := utf16.Encode([]rune(text))
	if len(enc) >= len(dst) {
		enc = enc[:len(dst)-1]
		if len(enc) > 0 && enc[len(enc)-1] >= 0xd800 && enc[len(enc)-1] <= 0xdbff {
			enc = enc[:len(enc)-1]
		}
	}
	copy(dst, enc)
}

func (u *UI) callNotify(op uintptr) bool {
	ok, _, _ := shellNotify.Call(op, uintptr(unsafe.Pointer(&u.data)))
	return ok != 0
}

func (u *UI) addTrayIcon() bool {
	u.data.Flags = 1 | 2 | 4
	if !u.callNotify(0) {
		u.trayAdded = false
		return false
	}
	u.data.Version = 4
	if u.callNotify(4) {
		u.data.Flags = 1 | 2 | 4 | 0x80
		u.callNotify(1)
	}
	u.trayAdded = true
	return true
}

func (u *UI) initTooltip() {
	var icc struct {
		Size, Classes uint32
	}
	icc.Size = 8
	icc.Classes = 8
	initCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	cls := winapi.UTF16("tooltips_class32")
	u.tooltipHwnd, _, _ = createWindow.Call(
		0x8|0x80|0x08000000,
		uintptr(unsafe.Pointer(cls)),
		0,
		0x80000000|3,
		0, 0, 0, 0,
		u.hwnd, 0, u.instance, 0,
	)
	if u.tooltipHwnd == 0 {
		return
	}
	u.tool = toolInfo{
		Size:  uint32(unsafe.Sizeof(toolInfo{})),
		Flags: 0x20 | 0x80,
		Hwnd:  u.hwnd,
		ID:    1,
		Text:  &u.tooltipBuf[0],
	}
	sendMessage.Call(u.tooltipHwnd, 0x432, 0, uintptr(unsafe.Pointer(&u.tool))) // TTM_ADDTOOLW
	sendMessage.Call(u.tooltipHwnd, 0x418, 0, uintptr(u.px(680)))               // TTM_SETMAXTIPWIDTH
}

func (u *UI) showTooltip() {
	if u.tooltipHwnd == 0 {
		return
	}
	s := u.latest.Load()
	if s == nil {
		return
	}
	copyText(u.tooltipBuf[:], OverlayTooltip(*s))
	sendMessage.Call(u.tooltipHwnd, 0x439, 0, uintptr(unsafe.Pointer(&u.tool))) // TTM_UPDATETIPTEXTW
	var pt point
	getCursor.Call(uintptr(unsafe.Pointer(&pt)))
	x := int(pt.X)
	y := int(pt.Y - u.px(130))
	lparam := uintptr(uint32(uint16(x)) | (uint32(uint16(y)) << 16))
	sendMessage.Call(u.tooltipHwnd, 0x412, 0, lparam)                           // TTM_TRACKPOSITION
	sendMessage.Call(u.tooltipHwnd, 0x411, 1, uintptr(unsafe.Pointer(&u.tool))) // TTM_TRACKACTIVATE
	u.tooltipVisible = true
}

func (u *UI) hideTooltip() {
	if u.tooltipHwnd != 0 && u.tooltipVisible {
		sendMessage.Call(u.tooltipHwnd, 0x411, 0, uintptr(unsafe.Pointer(&u.tool)))
	}
	u.tooltipVisible = false
}

func (u *UI) update(s app.Snapshot) error {
	var icon uintptr
	var err error
	if u.cfg.TrayIcon == config.TrayIconApplication {
		icon, err = u.applicationTrayIcon()
	} else {
		country := Country(s)
		if u.iconCountry != country {
			old := u.icons
			u.icons = make(map[string]uintptr)
			u.iconCountry = country
			defer func() {
				for _, h := range old {
					destroyIcon.Call(h)
				}
			}()
		}
		icon, err = u.networkTrayIcon(s)
	}
	if err != nil {
		return err
	}
	u.data.Icon = icon
	copyText(u.data.Tip[:], TrayTooltip(s))
	if u.trayAdded {
		if !u.callNotify(1) {
			u.addTrayIcon()
		}
	} else {
		u.addTrayIcon()
	}
	if u.hwnd != 0 {
		invalidateRect.Call(u.hwnd, 0, 0)
	}
	if u.tooltipVisible {
		u.showTooltip()
	}
	return nil
}

func (u *UI) applicationTrayIcon() (uintptr, error) {
	if u.appIcon == 0 {
		name := winapi.UTF16("APP")
		icon, _, err := loadImage.Call(u.instance, uintptr(unsafe.Pointer(name)), 1, 32, 32, 0)
		runtime.KeepAlive(name)
		if icon == 0 {
			return 0, winapi.Error("LoadImageW(APP)", err)
		}
		u.appIcon = icon
	}
	return u.appIcon, nil
}

func (u *UI) networkTrayIcon(s app.Snapshot) (uintptr, error) {
	key := IconKey(s)
	icon := u.icons[key]
	if icon == 0 {
		pixels := Pixels(Country(s), s.Status, s.Rates.Up > 0, s.Rates.Down > 0, s.At.Unix()%2 == 0)
		var xor [4096]byte
		var and [128]byte
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				src := (y*32 + x) * 4
				dst := (y*32 + x) * 4
				xor[dst], xor[dst+1], xor[dst+2], xor[dst+3] = pixels[src+2], pixels[src+1], pixels[src], pixels[src+3]
				if pixels[src+3] == 0 {
					and[y*4+x/8] |= 1 << uint(7-x%8)
				}
			}
		}
		var err error
		icon, _, err = createIcon.Call(u.instance, 32, 32, 1, 32, uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&xor[0])))
		if icon == 0 {
			return 0, winapi.Error("CreateIcon", err)
		}
		u.icons[key] = icon
	}
	return icon, nil
}

func (u *UI) releaseTrayIcons() {
	for _, icon := range u.icons {
		destroyIcon.Call(icon)
	}
	u.icons = make(map[string]uintptr)
	if u.appIcon != 0 {
		destroyIcon.Call(u.appIcon)
		u.appIcon = 0
	}
}

func (u *UI) setTrayIcon(style string) error {
	if !config.ValidTrayIcon(style) {
		return fmt.Errorf("无效的托盘图标样式: %s", style)
	}
	next := u.cfg
	next.TrayIcon = style
	if style == config.TrayIconApplication && u.latest.Load() != nil {
		if _, err := u.applicationTrayIcon(); err != nil {
			return err
		}
	}
	if err := u.saveSettings(next); err != nil {
		return fmt.Errorf("保存托盘图标设置失败: %w", err)
	}
	u.cfg = next
	if snapshot := u.latest.Load(); snapshot != nil {
		return u.update(*snapshot)
	}
	return nil
}

func (u *UI) drawFlowText(dc uintptr, text string, leftPx int32, color uintptr) int32 {
	return u.drawFlowTextRow(dc, text, leftPx, color, 0, u.height)
}

func (u *UI) drawFlowTextRow(dc uintptr, text string, leftPx int32, color uintptr, top, bottom int32) int32 {
	if text == "" {
		return leftPx
	}
	setTextColor.Call(dc, color)
	enc := utf16.Encode([]rune(text))
	var sz textSize
	getTextExtentPoint32.Call(dc, uintptr(unsafe.Pointer(&enc[0])), uintptr(len(enc)), uintptr(unsafe.Pointer(&sz)))
	rightPx := leftPx + sz.CX + max(2, u.px(2))
	rc := rect{Left: leftPx, Top: top, Right: rightPx, Bottom: bottom}
	ptr := winapi.UTF16(text)
	drawText.Call(dc, uintptr(unsafe.Pointer(ptr)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), 0x20|0x4|0x8000)
	runtime.KeepAlive(ptr)
	runtime.KeepAlive(enc)
	return leftPx + sz.CX
}

func (u *UI) drawCirclePx(dc uintptr, brush uintptr, centerXPx, centerYPx, radiusPx int32) {
	old, _, _ := selectObject.Call(dc, brush)
	ellipse.Call(
		dc,
		uintptr(centerXPx-radiusPx),
		uintptr(centerYPx-radiusPx),
		uintptr(centerXPx+radiusPx),
		uintptr(centerYPx+radiusPx),
	)
	selectObject.Call(dc, old)
}

// drawPurpleMoonPx 使用 GDI 像素几何在像素坐标 (centerXPx, centerYPx) 绘制无字体依赖的亮紫色实心月牙。
func (u *UI) drawPurpleMoonPx(dc uintptr, centerXPx, centerYPx int32) {
	cx := float64(centerXPx)
	cy := float64(centerYPx)
	s := u.scale
	outerR := 5.2 * s
	outerR2 := outerR * outerR
	cutX := cx + 2.3*s
	cutY := cy - 0.9*s
	cutR := 4.2 * s
	cutR2 := cutR * cutR
	purple := colorRef(186, 85, 255)
	span := int32(math.Ceil(outerR + 1))
	for dy := -span; dy <= span; dy++ {
		for dx := -span; dx <= span; dx++ {
			x := cx + float64(dx)
			y := cy + float64(dy)
			d1 := float64(dx*dx + dy*dy)
			cdx := x - cutX
			cdy := y - cutY
			d2 := cdx*cdx + cdy*cdy
			if d1 <= outerR2 && d2 > cutR2 {
				setPixel.Call(dc, uintptr(centerXPx+dx), uintptr(centerYPx+dy), purple)
			}
		}
	}
}

// drawYellowSunPx 使用 GDI 像素几何在像素坐标 (centerXPx, centerYPx) 绘制无字体依赖的明黄色实心太阳与八向光芒。
func (u *UI) drawYellowSunPx(dc uintptr, centerXPx, centerYPx int32) {
	s := u.scale
	yellow := colorRef(255, 214, 10)
	span := int32(math.Ceil(6.2*s + 1))
	for dy := -span; dy <= span; dy++ {
		for dx := -span; dx <= span; dx++ {
			nx := float64(dx) / s
			ny := float64(dy) / s
			d2 := nx*nx + ny*ny
			hit := d2 <= 3.1*3.1
			if !hit && d2 >= 3.9*3.9 && d2 <= 5.8*5.8 {
				ax, ay := math.Abs(nx), math.Abs(ny)
				diag := math.Abs(ax-ay) * 0.7071
				if ax <= 0.72 || ay <= 0.72 || diag <= 0.72 {
					hit = true
				}
			}
			if hit {
				setPixel.Call(dc, uintptr(centerXPx+dx), uintptr(centerYPx+dy), yellow)
			}
		}
	}
}

func (u *UI) paint(hwnd uintptr) {
	var ps paintStruct
	dc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	defer endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	u.drawBar(dc, hwnd)
}

func (u *UI) drawBar(dc, hwnd uintptr) {

	rc := rect{Left: 0, Top: 0, Right: u.width, Bottom: u.height}
	fillRect.Call(dc, uintptr(unsafe.Pointer(&rc)), u.bgBrush)

	oldFont, _, _ := selectObject.Call(dc, u.font)
	defer selectObject.Call(dc, oldFont)
	setBkMode.Call(dc, 1) // TRANSPARENT

	var snap app.Snapshot
	if p := u.latest.Load(); p != nil {
		snap = *p
	} else {
		snap = app.Snapshot{Status: monitor.Unknown}
	}

	centerY := u.height / 2
	dotRadius := max(3, u.px(4))
	curX := u.px(5)

	// 1. 流式紧凑绘制 GeoPulse 段（消除网速与 RTT 之间的多余空白）
	netBrush := u.brushGray
	switch snap.Status {
	case monitor.Online:
		netBrush = u.brushGreen
	case monitor.Degraded:
		netBrush = u.brushYellow
	case monitor.Offline:
		netBrush = u.brushRed
	}
	u.drawCirclePx(dc, netBrush, curX+dotRadius, centerY, dotRadius)
	curX += dotRadius*2 + u.px(5)
	curX = u.drawFlowText(dc, Country(snap), curX, colorRef(240, 246, 255)) + u.px(6)

	if !u.cfg.ShowNetDetail {
		upColor := colorRef(100, 116, 139)
		if snap.Rates.Up > 0 {
			upColor = colorRef(0, 229, 255)
		}
		downColor := colorRef(100, 116, 139)
		if snap.Rates.Down > 0 {
			downColor = colorRef(56, 189, 248)
		}
		curX = u.drawFlowText(dc, "↑", curX, upColor) + u.px(1)
		curX = u.drawFlowText(dc, "↓", curX, downColor) + u.px(6)
		curX = u.drawFlowText(dc, "|", curX, colorRef(120, 130, 145)) + u.px(7)
	} else {
		upStr, downStr := "--", "--"
		if snap.Rates.Valid {
			upStr, downStr = CompactSpeed(snap.Rates.Up), CompactSpeed(snap.Rates.Down)
		}
		rateColor := colorRef(148, 163, 184)
		if snap.Rates.Up > 0 || snap.Rates.Down > 0 {
			rateColor = colorRef(125, 211, 252)
		}
		curX = u.drawFlowText(dc, fmt.Sprintf("↑%s ↓%s", upStr, downStr), curX, rateColor) + u.px(7)
		rttColor := colorRef(148, 163, 184)
		if CompactRTT(snap) != "--" {
			rttColor = colorRef(167, 243, 208)
		}
		curX = u.drawFlowText(dc, CompactRTT(snap), curX, rttColor) + u.px(6)
		curX = u.drawFlowText(dc, "|", curX, colorRef(120, 130, 145)) + u.px(7)
	}

	// 2. 流式紧凑绘制多市场行情段（五态时段信号 + 异常感叹号 + 简洁别名 + 自适应价格与涨跌幅）
	views := snap.Quotes
	if len(views) == 0 {
		g := market.GroupByID(u.cfg.MarketGroup)
		for _, sym := range g.Symbols {
			views = append(views, market.View{
				Symbol:  sym,
				Alias:   market.AliasFor(sym),
				State:   market.StateClosed,
				Healthy: false,
			})
		}
	}
	twoRows := false
	for _, view := range views {
		if view.Quote != nil && view.Quote.HasSecondaryLine() {
			twoRows = true
			break
		}
	}
	rowBottom, quoteCenter := u.height, centerY
	if twoRows {
		rowBottom = u.px(17)
		quoteCenter = rowBottom / 2
		selectObject.Call(dc, u.stockFont)
	}
	for i, v := range views {
		iconCenterX := curX + dotRadius + u.px(1)
		switch v.EffectiveState() {
		case market.StatePreMarket:
			u.drawYellowSunPx(dc, iconCenterX, quoteCenter)
		case market.StateOvernight:
			u.drawPurpleMoonPx(dc, iconCenterX, quoteCenter)
		case market.StateRegular:
			u.drawCirclePx(dc, u.brushGreen, iconCenterX, quoteCenter, dotRadius)
		case market.StatePostMarket:
			u.drawCirclePx(dc, u.brushBlue, iconCenterX, quoteCenter, dotRadius)
		default:
			u.drawCirclePx(dc, u.brushGray, iconCenterX, quoteCenter, dotRadius)
		}
		curX += dotRadius*2 + u.px(6)

		if !v.Healthy {
			curX = u.drawFlowTextRow(dc, "!", curX, colorRef(255, 82, 82), 0, rowBottom) + u.px(3)
		}

		nameLeft := curX
		curX = u.drawFlowTextRow(dc, v.DisplayName(), curX, colorRef(240, 240, 240), 0, rowBottom) + u.px(5)

		text := "---.-- --.--%"
		pctColor := colorRef(170, 170, 170)
		if v.Quote != nil {
			pct := v.Quote.ChangePercent
			text = quotePriceChangeText(*v.Quote)
			if math.Abs(pct) < 0.005 {
				pctColor = colorRef(240, 240, 240)
			} else if (pct > 0) == u.cfg.UpIsRed {
				pctColor = colorRef(255, 119, 119)
			} else {
				pctColor = colorRef(119, 204, 153)
			}
		}
		curX = u.drawFlowTextRow(dc, text, curX, pctColor, 0, rowBottom)
		if v.Quote != nil && v.Quote.HasSecondaryLine() {
			prior, _, _ := selectObject.Call(dc, u.capFont)
			capRight := u.drawFlowTextRow(dc, v.Quote.SecondaryText(), nameLeft, colorRef(175, 185, 198), rowBottom, u.height)
			selectObject.Call(dc, prior)
			curX = max(curX, capRight)
		}
		if i == 0 && len(views) > 1 {
			curX += u.px(7)
			curX = u.drawFlowText(dc, "|", curX, colorRef(120, 130, 145)) + u.px(7)
		}
	}

	neededW := curX + u.px(6)
	diff := neededW - u.width
	if diff < 0 {
		diff = -diff
	}
	if neededW > u.width || diff > u.px(8) {
		u.width = neededW
		if hwnd != 0 {
			setWindowPos.Call(hwnd, ^uintptr(0), 0, 0, uintptr(u.width), uintptr(u.height), 0x0002|0x0010)
		}
	}
}

func (u *UI) applyLockStyle() {
	style, _, _ := getWindowLongPtr.Call(u.hwnd, ^uintptr(19)) // GWL_EXSTYLE = -20
	if u.cfg.Locked {
		style |= 0x20 // WS_EX_TRANSPARENT
	} else {
		style &^= 0x20
	}
	setWindowLongPtr.Call(u.hwnd, ^uintptr(19), style)
	setWindowPos.Call(u.hwnd, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x10|0x20)
}

func (u *UI) dockLeft() {
	u.cfg.Placement = "left"
	u.cfg.X = nil
	u.cfg.Y = nil
	x, y := u.initialPosition()
	setWindowPos.Call(u.hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(u.width), uintptr(u.height), 0x10)
	_ = config.Save(u.cfg)
}

func (u *UI) showDiagnostics() {
	var snap app.Snapshot
	if p := u.latest.Load(); p != nil {
		snap = *p
	}
	report := DiagnosticsText(u.version, snap)
	text, title := winapi.UTF16(report), winapi.UTF16("Samuel — 网络与多市场行情诊断")
	messageBox.Call(u.hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40)
}

func (u *UI) buildMenu() (uintptr, map[Action]customMenuCommand) {
	u.requestMenuQuotes()
	menu, _, _ := createMenu.Call()
	if menu == 0 {
		return 0, nil
	}

	add := func(id Action, label string, flags uintptr) {
		p := winapi.UTF16(strings.ReplaceAll(label, "&", "&&"))
		appendMenu.Call(menu, flags, uintptr(id), uintptr(unsafe.Pointer(p)))
	}

	add(0, "Samuel  "+u.version, 2)
	s := u.latest.Load()
	if s != nil {
		ip := s.Geo.IP
		if ip == "" {
			ip = "查询中/未知"
		}
		add(0, fmt.Sprintf("出口 %s · %s · %s", Country(*s), StatusText(s.Status), ip), 2)
		up, down := "--", "--"
		if s.Rates.Valid {
			up, down = Speed(s.Rates.Up), Speed(s.Rates.Down)
		}
		add(0, fmt.Sprintf("网络: ↑ %s  ↓ %s | HTTPS: %s", up, down, RTT(*s)), 2)
		add(0, "行情: "+QuoteSummary(*s), 2)
		if s.EgressActive {
			add(0, "TUN/代理活跃：地区每 30 秒自动复核", 2)
		}
	}
	add(0, "", 0x800)
	add(ActionRefresh, "立即刷新 IP 与行情", 0)

	detailFlags := uintptr(0)
	if u.cfg.ShowNetDetail {
		detailFlags = 8
	}
	add(ActionToggleNetDetail, "任务栏显示实时网速与延迟", detailFlags)

	iconMenu, _, _ := createMenu.Call()
	if iconMenu != 0 {
		styleName := "应用图标"
		for _, option := range []struct {
			action       Action
			style, label string
		}{
			{ActionTrayIconApplication, config.TrayIconApplication, "Samuel 应用图标"},
			{ActionTrayIconNetwork, config.TrayIconNetwork, "网络状态图标（地区 / 上下行）"},
		} {
			flags := uintptr(0)
			if u.cfg.TrayIcon == option.style {
				flags = 8
				if option.style == config.TrayIconNetwork {
					styleName = "网络状态"
				}
			}
			label := winapi.UTF16(option.label)
			appendMenu.Call(iconMenu, flags, uintptr(option.action), uintptr(unsafe.Pointer(label)))
		}
		title := winapi.UTF16("托盘图标（当前：" + styleName + "）")
		appendMenu.Call(menu, 0x10, iconMenu, uintptr(unsafe.Pointer(title)))
	}

	lockFlags := uintptr(0)
	if u.cfg.Locked {
		lockFlags = 8
	}
	add(ActionToggleLock, "锁定位置（鼠标穿透）", lockFlags)
	add(ActionDockLeft, "移到任务栏最左侧", 0)

	colorLabel := "涨跌配色：绿涨红跌"
	if u.cfg.UpIsRed {
		colorLabel = "涨跌配色：红涨绿跌"
	}
	add(ActionToggleColor, colorLabel, 0)

	add(0, "", 0x800)
	currentGroup := u.cfg.MarketGroup
	if s != nil && market.ValidGroup(s.MarketGroup) {
		currentGroup = s.MarketGroup
	}
	if !market.ValidGroup(currentGroup) {
		currentGroup = market.DefaultGroup
	}
	activeIdx := 0
	for i, g := range market.Groups {
		if g.ID == currentGroup {
			activeIdx = i
			break
		}
	}
	// 第一行直接显示当前选中的标的对（如：✔ 标普 / 纳指 (VOO / QQQ)）
	currentSpec := market.GroupByID(currentGroup)
	if currentSpec.ID == market.Groups[activeIdx].ID {
		add(Action(uint32(ActionGroupVOOQQQ)+uint32(activeIdx)), u.groupMenuLabel(currentSpec), 8)
	} else {
		add(0, u.groupMenuLabel(currentSpec), 8|2)
	}
	customCommands := make(map[Action]customMenuCommand)
	// 第二行显示可折叠的 More 子菜单，点开后展示其余所有指数/标的
	moreMenu, _, _ := createMenu.Call()
	if moreMenu != 0 {
		for i, g := range market.Groups {
			if g.ID == currentGroup {
				continue
			}
			p := winapi.UTF16(u.groupMenuLabel(g))
			appendMenu.Call(moreMenu, 0, uintptr(uint32(ActionGroupVOOQQQ)+uint32(i)), uintptr(unsafe.Pointer(p)))
		}
		u.appendCustomMenu(moreMenu, customCommands)
		moreTitle := winapi.UTF16("More（更多行情）")
		appendMenu.Call(menu, 0x10, moreMenu, uintptr(unsafe.Pointer(moreTitle)))
	}

	add(0, "", 0x800)
	// 显示大小调节子菜单 (75% ~ 130%)
	scalePct := u.cfg.UIScalePercent
	if !config.ValidUIScalePercent(scalePct) {
		scalePct = config.DefaultUIScalePercent
	}
	scaleMenu, _, _ := createMenu.Call()
	if scaleMenu != 0 {
		for i, pct := range config.UIScalePresets {
			flags := uintptr(0)
			if pct == scalePct {
				flags = 8
			}
			label := scaleMenuLabels[pct]
			if label == "" {
				label = fmt.Sprintf("%d%%", pct)
			}
			p := winapi.UTF16(label)
			appendMenu.Call(scaleMenu, flags, uintptr(uint32(ActionScale75)+uint32(i)), uintptr(unsafe.Pointer(p)))
		}
		scaleTitle := winapi.UTF16(fmt.Sprintf("显示大小 (当前: %d%%)", scalePct))
		appendMenu.Call(menu, 0x10, scaleMenu, uintptr(unsafe.Pointer(scaleTitle)))
	}

	interval := u.cfg.IntervalSeconds
	if s != nil && app.ValidInterval(s.IntervalSeconds) {
		interval = s.IntervalSeconds
	}
	intervalMenu, _, _ := createMenu.Call()
	if intervalMenu != 0 {
		for i, n := range []int{1, 2, 5} {
			flags := uintptr(0)
			if interval == n {
				flags = 8
			}
			p := winapi.UTF16(fmt.Sprintf("%d 秒", n))
			appendMenu.Call(intervalMenu, flags, uintptr(uint32(ActionInterval1)+uint32(i)), uintptr(unsafe.Pointer(p)))
		}
		intTitle := winapi.UTF16(fmt.Sprintf("刷新间隔（当前 %d 秒）", interval))
		appendMenu.Call(menu, 0x10, intervalMenu, uintptr(unsafe.Pointer(intTitle)))
	}
	add(0, "", 0x800)
	add(ActionDiagnostics, "查看详细诊断信息", 0)

	autoFlags := uintptr(0)
	if u.startup.Load() {
		autoFlags = 8
	}
	add(ActionAutostart, "开机自启", autoFlags)
	add(0, "", 0x800)
	add(ActionExit, "退出", 0)
	return menu, customCommands
}

func (u *UI) menu() {
	u.hideTooltip()
	menu, customCommands := u.buildMenu()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	var p point
	getCursor.Call(uintptr(unsafe.Pointer(&p)))
	if !systemFluentColors().nativeMenu && u.showFluentMenu(menu, p, customCommands) {
		return
	}
	setForeground.Call(u.hwnd)
	cmd, _, _ := trackMenu.Call(menu, 0x100|2, uintptr(p.X), uintptr(p.Y), 0, u.hwnd, 0)
	postMessage.Call(u.hwnd, 0, 0, 0)
	u.handleMenuSelection(cmd, customCommands)
}

func (u *UI) handleMenuSelection(cmd uintptr, customCommands map[Action]customMenuCommand) {
	if Action(cmd) == actionAddCustom {
		// Let the native menu finish before opening a window that needs keyboard focus.
		postMessage.Call(u.hwnd, wmShowCustom, 0, 0)
		return
	}
	u.callNotify(3)
	if cmd != 0 {
		if command, ok := customCommands[Action(cmd)]; ok {
			var err error
			if command.remove {
				err = u.deleteCustomPair(command.id)
			} else {
				err = u.selectCustomPair(command.id)
			}
			if err != nil {
				ShowError(err)
			}
		} else {
			u.handleAction(Action(cmd))
		}
	}
}

func (u *UI) handleAction(act Action) {
	switch act {
	case ActionTrayIconApplication, ActionTrayIconNetwork:
		style := config.TrayIconApplication
		if act == ActionTrayIconNetwork {
			style = config.TrayIconNetwork
		}
		if err := u.setTrayIcon(style); err != nil {
			ShowError(err)
		}
	case ActionToggleNetDetail:
		u.cfg.ShowNetDetail = !u.cfg.ShowNetDetail
		u.updateDimensions()
		var rc rect
		getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&rc)))
		setWindowPos.Call(u.hwnd, ^uintptr(0), uintptr(rc.Left), uintptr(rc.Top), uintptr(u.width), uintptr(u.height), 0x10)
		invalidateRect.Call(u.hwnd, 0, 0)
		_ = config.Save(u.cfg)
	case ActionToggleLock:
		u.hideTooltip()
		u.cfg.Locked = !u.cfg.Locked
		u.applyLockStyle()
		_ = config.Save(u.cfg)
	case ActionDockLeft:
		u.dockLeft()
	case ActionToggleColor:
		u.cfg.UpIsRed = !u.cfg.UpIsRed
		invalidateRect.Call(u.hwnd, 0, 0)
		_ = config.Save(u.cfg)
	case ActionDiagnostics:
		u.showDiagnostics()
	default:
		if pct, ok := ScaleForAction(act); ok {
			u.cfg.UIScalePercent = pct
			_ = config.Save(u.cfg)
			u.recalcScale()
			u.updateDimensions()
			u.rebuildFonts()
			x, y := u.initialPosition()
			if u.cfg.Placement == "manual" {
				var rc rect
				getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&rc)))
				x, y = rc.Left, rc.Top
			}
			setWindowPos.Call(u.hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(u.width), uintptr(u.height), 0x10)
			invalidateRect.Call(u.hwnd, 0, 0)
			return
		}
		if grp, ok := GroupForAction(act); ok {
			u.cfg.MarketGroup = grp
			u.desiredGroup.Store(grp)
			_ = config.Save(u.cfg)
			spec := market.SetActiveGroup(grp)
			if s := u.latest.Load(); s != nil {
				snap := *s
				snap.MarketGroup = spec.ID
				snap.Quotes = make([]market.View, 0, len(spec.Symbols))
				for _, sym := range spec.Symbols {
					snap.Quotes = append(snap.Quotes, market.View{
						Symbol:  sym,
						Alias:   market.AliasFor(sym),
						State:   market.StateClosed,
						Healthy: false,
						Reason:  "等待 " + market.AliasFor(sym) + " 实时行情",
					})
				}
				u.latest.Store(&snap)
				invalidateRect.Call(u.hwnd, 0, 0)
			}
		} else if act == ActionInterval1 || act == ActionInterval2 || act == ActionInterval5 {
			n := map[Action]int{ActionInterval1: 1, ActionInterval2: 2, ActionInterval5: 5}[act]
			u.cfg.IntervalSeconds = n
			_ = config.Save(u.cfg)
		}
		select {
		case u.Actions <- act:
		default:
		}
	}
}

func (u *UI) windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	if msg == u.taskbarMessage && u.taskbarMessage != 0 {
		u.addTrayIcon()
		return 0
	}
	switch msg {
	case wmShowCustom:
		u.openCustomPanel(true)
		return 0
	case wmCustomResult:
		u.handleCustomResults()
		return 0
	case 0x000F: // WM_PAINT
		u.paint(hwnd)
		return 0
	case 0x0014: // WM_ERASEBKGND
		return 1
	case wmUpdate:
		u.updatePending.Store(false)
		if s := u.latest.Load(); s != nil {
			_ = u.update(*s)
		}
		return 0
	case wmTray:
		event := uint32(lparam & 0xffff)
		if event == 0x7b || event == 0x205 || event == 0x400 || event == 0x401 {
			u.menu()
		} else if event == 0x203 {
			u.showDiagnostics()
		}
		return 0
	case winapi.WMDockLeft:
		u.dockLeft()
		return 0
	case winapi.WMDiag:
		u.showDiagnostics()
		return 0
	case 0x0205: // WM_RBUTTONUP on taskbar bar
		u.menu()
		return 0
	case 0x0201: // WM_LBUTTONDOWN
		if !u.cfg.Locked {
			u.hideTooltip()
			var pt point
			var rc rect
			getCursor.Call(uintptr(unsafe.Pointer(&pt)))
			getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			u.dragOffsetX = pt.X - rc.Left
			u.dragOffsetY = pt.Y - rc.Top
			u.dragging = true
			setCapture.Call(hwnd)
		}
		return 0
	case 0x0200: // WM_MOUSEMOVE
		if u.dragging {
			var pt point
			getCursor.Call(uintptr(unsafe.Pointer(&pt)))
			setWindowPos.Call(hwnd, ^uintptr(0), uintptr(pt.X-u.dragOffsetX), uintptr(pt.Y-u.dragOffsetY), 0, 0, 0x1|0x10)
			return 0
		}
		if !u.hoverTracking && !u.cfg.Locked {
			tm := trackMouse{
				Size:      uint32(unsafe.Sizeof(trackMouse{})),
				Flags:     1 | 2, // TME_HOVER | TME_LEAVE
				HwndTrack: hwnd,
				HoverTime: 400,
			}
			trackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
			u.hoverTracking = true
		}
		return 0
	case 0x02A1: // WM_MOUSEHOVER
		if !u.dragging {
			u.showTooltip()
		}
		return 0
	case 0x02A3: // WM_MOUSELEAVE
		u.hoverTracking = false
		u.hideTooltip()
		return 0
	case 0x0202: // WM_LBUTTONUP
		if u.dragging {
			u.dragging = false
			releaseCapture.Call()
			var rc rect
			getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			x, y := int(rc.Left), int(rc.Top)
			u.cfg.Placement = "manual"
			u.cfg.X = &x
			u.cfg.Y = &y
			_ = config.Save(u.cfg)
		}
		return 0
	case 0x0215: // WM_CAPTURECHANGED
		u.dragging = false
		return 0
	case 0x007E: // WM_DISPLAYCHANGE
		x, y := u.initialPosition()
		setWindowPos.Call(hwnd, ^uintptr(0), uintptr(x), uintptr(y), 0, 0, 0x1|0x10)
		return 0
	case 0x02E0: // WM_DPICHANGED
		dpi := uint32(wparam & 0xffff)
		if dpi > 0 {
			u.dpiScale = float64(dpi) / 96.0
			u.recalcScale()
			u.updateDimensions()
			u.rebuildFonts()
			if lparam != 0 {
				var suggested rect
				rtlMoveMemory.Call(uintptr(unsafe.Pointer(&suggested)), lparam, unsafe.Sizeof(suggested))
				setWindowPos.Call(hwnd, ^uintptr(0), uintptr(suggested.Left), uintptr(suggested.Top), uintptr(u.width), uintptr(u.height), 0x10)
			}
			invalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case 0x0010: // WM_CLOSE
		destroyWindow.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		postQuit.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

func (u *UI) Run(ctx context.Context, ready func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if setDPI.Find() == nil {
		setDPI.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	}
	if getDpiForSystem.Find() == nil {
		if dpi, _, _ := getDpiForSystem.Call(); dpi > 0 {
			u.dpiScale = float64(dpi) / 96.0
		}
	}
	u.recalcScale()
	u.updateDimensions()

	u.instance, _, _ = getModule.Call(0)
	u.bgBrush, _, _ = createSolidBrush.Call(colorRef(1, 1, 1))
	u.brushGreen, _, _ = createSolidBrush.Call(colorRef(0, 230, 118))
	u.brushBlue, _, _ = createSolidBrush.Call(colorRef(56, 189, 248))
	u.brushYellow, _, _ = createSolidBrush.Call(colorRef(245, 158, 11))
	u.brushRed, _, _ = createSolidBrush.Call(colorRef(255, 23, 68))
	u.brushGray, _, _ = createSolidBrush.Call(colorRef(148, 163, 184))
	u.rebuildFonts()

	defer func() {
		for _, obj := range []uintptr{u.font, u.stockFont, u.capFont, u.bgBrush, u.brushGreen, u.brushBlue, u.brushYellow, u.brushRed, u.brushGray} {
			if obj != 0 {
				deleteObject.Call(obj)
			}
		}
	}()

	cursor, _, _ := loadCursor.Call(0, 32512)
	u.callback = syscall.NewCallback(u.windowProc)
	clsName := winapi.UTF16(winapi.WindowClass)
	wc := windowClass{
		Size:       uint32(unsafe.Sizeof(windowClass{})),
		Procedure:  u.callback,
		Instance:   u.instance,
		Cursor:     cursor,
		Background: u.bgBrush,
		ClassName:  clsName,
	}
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return winapi.Error("RegisterClassExW", err)
	}
	defer unregisterClass.Call(uintptr(unsafe.Pointer(clsName)), u.instance)

	x, y := u.initialPosition()
	exStyle := uintptr(0x00080000 | 0x80 | 0x8 | 0x08000000) // WS_EX_LAYERED | WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE
	if u.cfg.Locked {
		exStyle |= 0x20 // WS_EX_TRANSPARENT
	}
	title := winapi.UTF16("Samuel")
	hwnd, _, err := createWindow.Call(
		exStyle,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(title)),
		0x80000000, // WS_POPUP
		uintptr(x), uintptr(y), uintptr(u.width), uintptr(u.height),
		0, 0, u.instance, 0,
	)
	if hwnd == 0 {
		return winapi.Error("CreateWindowExW", err)
	}
	u.hwnd = hwnd
	defer destroyWindow.Call(hwnd)
	defer u.closeCustomPanel()
	defer u.closeFluentMenu()

	setLayeredAttrs.Call(hwnd, colorRef(1, 1, 1), 255, 1) // LWA_COLORKEY = 1

	taskbar := winapi.UTF16("TaskbarCreated")
	r, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(taskbar)))
	u.taskbarMessage = uint32(r)

	u.data = notifyIcon{
		Size:     uint32(unsafe.Sizeof(notifyIcon{})),
		Window:   hwnd,
		ID:       1,
		Flags:    1 | 2 | 4 | 0x80,
		Callback: wmTray,
	}
	defer func() {
		u.callNotify(2)
		u.releaseTrayIcons()
	}()

	if err := u.update(app.Snapshot{Status: monitor.Unknown}); err != nil {
		return err
	}
	u.initTooltip()
	showWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE

	stop := context.AfterFunc(ctx, func() { postMessage.Call(hwnd, 0x0010, 0, 0) })
	defer stop()
	ready()

	var msg message
	for {
		r, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return winapi.Error("GetMessageW", err)
		}
		if r == 0 {
			return nil
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func ShowError(err error) {
	text, title := winapi.UTF16(err.Error()), winapi.UTF16("Samuel")
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
