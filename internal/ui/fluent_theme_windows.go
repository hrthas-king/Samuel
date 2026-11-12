//go:build windows && (amd64 || arm64)

package ui

import (
	"math"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	fluentDWM       = winapi.DLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	fluentRegGet    = winapi.DLL("advapi32.dll").NewProc("RegGetValueW")
	fluentSysParams = user32.NewProc("SystemParametersInfoW")
	fluentSysColor  = user32.NewProc("GetSysColor")
	fluentGetClient = user32.NewProc("GetClientRect")
	fluentGetDC     = user32.NewProc("GetDC")
	fluentReleaseDC = user32.NewProc("ReleaseDC")
	fluentRoundRect = gdi32.NewProc("RoundRect")
	fluentCreatePen = gdi32.NewProc("CreatePen")
	fluentSetBk     = gdi32.NewProc("SetBkColor")
	fluentFocusRect = user32.NewProc("DrawFocusRect")
)

type fluentPalette struct {
	background, input, hover, border, text, muted, accent, onAccent, danger uintptr
	dark, nativeMenu                                                        bool
}

func fluentColors(dark bool) fluentPalette {
	if dark {
		return fluentPalette{colorRef(44, 44, 44), colorRef(32, 32, 32), colorRef(60, 60, 60), colorRef(73, 73, 73), colorRef(245, 245, 245), colorRef(180, 180, 180), colorRef(96, 205, 255), colorRef(0, 40, 58), colorRef(255, 153, 164), true, false}
	}
	return fluentPalette{colorRef(249, 249, 249), colorRef(255, 255, 255), colorRef(235, 235, 235), colorRef(218, 218, 218), colorRef(27, 27, 27), colorRef(105, 105, 105), colorRef(0, 103, 192), colorRef(255, 255, 255), colorRef(196, 43, 28), false, false}
}

func systemFluentColors() fluentPalette {
	light, size := uint32(1), uint32(4)
	key := winapi.UTF16(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name := winapi.UTF16("AppsUseLightTheme")
	fluentRegGet.Call(0x80000001, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(name)), 0x10, 0, uintptr(unsafe.Pointer(&light)), uintptr(unsafe.Pointer(&size)))
	p := fluentColors(light != 1)
	var hc struct {
		Size, Flags uint32
		Scheme      *uint16
	}
	hc.Size = uint32(unsafe.Sizeof(hc))
	fluentSysParams.Call(0x42, 0, uintptr(unsafe.Pointer(&hc)), 0) // SPI_GETHIGHCONTRAST
	var reader int32
	fluentSysParams.Call(0x46, 0, uintptr(unsafe.Pointer(&reader)), 0) // SPI_GETSCREENREADER
	p.nativeMenu = reader != 0 || hc.Flags&1 != 0
	if hc.Flags&1 != 0 {
		p.background, _, _ = fluentSysColor.Call(4)
		p.input = p.background
		p.text, _, _ = fluentSysColor.Call(7)
		p.border, p.muted = p.text, p.text
		p.hover, _, _ = fluentSysColor.Call(13)
		p.accent = p.hover
		p.onAccent, _, _ = fluentSysColor.Call(14)
		p.danger = p.text
	}
	return p
}

type fluentVisuals struct {
	palette                                        fluentPalette
	scale                                          float64
	font, small, heading, icons, background, input uintptr
}

func newFluentVisuals(scale float64, palette fluentPalette) *fluentVisuals {
	if scale <= 0 {
		scale = 1
	}
	v := &fluentVisuals{scale: scale, palette: palette}
	font := func(name string, size, weight int) uintptr {
		face := winapi.UTF16(name)
		h, _, _ := createFont.Call(uintptr(-int(v.px(size))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
		return h
	}
	v.font, v.small = font("Segoe UI", 14, 400), font("Segoe UI", 12, 400)
	v.heading, v.icons = font("Segoe UI", 17, 600), font("Segoe MDL2 Assets", 15, 400)
	v.refresh(palette)
	return v
}

func (v *fluentVisuals) px(n int) int32 { return int32(math.Round(float64(n) * v.scale)) }
func (v *fluentVisuals) refresh(p fluentPalette) {
	for _, h := range []uintptr{v.background, v.input} {
		if h != 0 {
			deleteObject.Call(h)
		}
	}
	v.palette = p
	v.background, _, _ = createSolidBrush.Call(p.background)
	v.input, _, _ = createSolidBrush.Call(p.input)
}
func (v *fluentVisuals) close() {
	for _, h := range []uintptr{v.font, v.small, v.heading, v.icons, v.background, v.input} {
		if h != 0 {
			deleteObject.Call(h)
		}
	}
}

func applyFluentFrame(hwnd uintptr, p fluentPalette) bool {
	if fluentDWM.Find() != nil {
		return false
	}
	corner, dark := uint32(2), uint32(0)
	if p.dark {
		dark = 1
	}
	status, _, _ := fluentDWM.Call(hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	fluentDWM.Call(hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
	border := uint32(p.border)
	fluentDWM.Call(hwnd, 34, uintptr(unsafe.Pointer(&border)), 4)
	return int32(status) >= 0
}

func fluentBox(dc uintptr, bounds rect, radius int32, fill, border uintptr) {
	brush, _, _ := createSolidBrush.Call(fill)
	pen, _, _ := fluentCreatePen.Call(0, 1, border)
	oldBrush, _, _ := selectObject.Call(dc, brush)
	oldPen, _, _ := selectObject.Call(dc, pen)
	fluentRoundRect.Call(dc, uintptr(bounds.Left), uintptr(bounds.Top), uintptr(bounds.Right), uintptr(bounds.Bottom), uintptr(radius), uintptr(radius))
	selectObject.Call(dc, oldPen)
	selectObject.Call(dc, oldBrush)
	deleteObject.Call(pen)
	deleteObject.Call(brush)
}

func fluentText(dc uintptr, text string, bounds rect, font, color uintptr, flags uintptr) {
	old, _, _ := selectObject.Call(dc, font)
	setBkMode.Call(dc, 1)
	setTextColor.Call(dc, color)
	p := winapi.UTF16(text)
	drawText.Call(dc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&bounds)), 0x20|4|0x800|0x8000|flags)
	selectObject.Call(dc, old)
}

func popupWorkArea(anchor point) rect {
	r := rect{anchor.X, anchor.Y, anchor.X + 1, anchor.Y + 1}
	monitor, _, _ := monitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 2)
	var info struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		return info.Work
	}
	w, _, _ := getSystemMetrics.Call(0)
	h, _, _ := getSystemMetrics.Call(1)
	return rect{0, 0, int32(w), int32(h)}
}

func within(r rect, p point) bool {
	return p.X >= r.Left && p.X < r.Right && p.Y >= r.Top && p.Y < r.Bottom
}
