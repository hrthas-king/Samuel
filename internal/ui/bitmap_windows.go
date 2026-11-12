//go:build windows && (amd64 || arm64)

package ui

import (
	"runtime"
	"unicode/utf16"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	deleteDC           = gdi32.NewProc("DeleteDC")
	createDIBSection   = gdi32.NewProc("CreateDIBSection")
	flushGDI           = gdi32.NewProc("GdiFlush")
	bitBlt             = gdi32.NewProc("BitBlt")
	updateLayered      = user32.NewProc("UpdateLayeredWindow")
)

type bitmapInfoHeader struct {
	Size            uint32
	Width, Height   int32
	Planes, Bits    uint16
	Compression     uint32
	SizeImage       uint32
	XPels, YPels    int32
	Used, Important uint32
}

// dibSurface owns a top-down BGRA DIB selected into a memory DC. As uint32
// values the pixels are ARGB. Window pixels use premultiplied channels.
type dibSurface struct {
	dc, bitmap, previous uintptr
	pixels               []uint32
	width, height        int32
}

func (d *dibSurface) resize(w, h int32) error {
	if d.dc != 0 && d.width == w && d.height == h {
		return nil
	}
	dc, _, err := createCompatibleDC.Call(0)
	if dc == 0 {
		return winapi.Error("CreateCompatibleDC", err)
	}
	info := bitmapInfoHeader{Size: 40, Width: w, Height: -h, Planes: 1, Bits: 32}
	var bits *uint32
	bitmap, _, err := createDIBSection.Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		deleteDC.Call(dc)
		return winapi.Error("CreateDIBSection", err)
	}
	old, _, err := selectObject.Call(dc, bitmap)
	if old == 0 || old == ^uintptr(0) {
		deleteObject.Call(bitmap)
		deleteDC.Call(dc)
		return winapi.Error("SelectObject", err)
	}
	d.close()
	*d = dibSurface{dc, bitmap, old, unsafe.Slice(bits, int(w)*int(h)), w, h}
	return nil
}

func (d *dibSurface) close() {
	if d.dc != 0 {
		selectObject.Call(d.dc, d.previous)
		deleteObject.Call(d.bitmap)
		deleteDC.Call(d.dc)
		*d = dibSurface{}
	}
}

type alphaFrame struct {
	dibSurface
	mask dibSurface
}

func (f *alphaFrame) close() {
	f.mask.close()
	f.dibSurface.close()
}

type rasterText struct {
	chars  []uint16
	font   uintptr
	color  uint32
	bounds rect
	flags  uintptr
}

func textRun(text string, font uintptr, color uint32, bounds rect, flags uintptr) rasterText {
	return rasterText{utf16.Encode([]rune(text)), font, color, bounds, flags}
}

func (r rasterText) measure(dc uintptr) textSize {
	var size textSize
	if len(r.chars) != 0 {
		old, _, _ := selectObject.Call(dc, r.font)
		getTextExtentPoint32.Call(dc, uintptr(unsafe.Pointer(&r.chars[0])), uintptr(len(r.chars)), uintptr(unsafe.Pointer(&size)))
		selectObject.Call(dc, old)
	}
	return size
}

func newRasterFont(height int32, weight uintptr) uintptr {
	name := winapi.UTF16("Segoe UI")
	font, _, _ := createFont.Call(uintptr(-height), 0, 0, 0, weight, 0, 0, 0, 1, 0, 0, 4, 0, uintptr(unsafe.Pointer(name))) // ANTIALIASED_QUALITY
	return font
}

// GDI supplies grayscale glyph coverage, including its normal CJK font
// fallback. Applying the tint to that coverage avoids any RGB/ClearType matte.
func (f *alphaFrame) drawText(runs []rasterText) error {
	if err := f.mask.resize(f.width, f.height); err != nil {
		return err
	}
	clear(f.mask.pixels)
	setTextColor.Call(f.mask.dc, 0xFFFFFF)
	setBkMode.Call(f.mask.dc, 1)
	for _, run := range runs {
		if len(run.chars) == 0 || run.bounds.Right <= run.bounds.Left || run.bounds.Bottom <= run.bounds.Top {
			continue
		}
		old, _, _ := selectObject.Call(f.mask.dc, run.font)
		bounds := run.bounds
		drawText.Call(f.mask.dc, uintptr(unsafe.Pointer(&run.chars[0])), uintptr(len(run.chars)), uintptr(unsafe.Pointer(&bounds)), 0x20|4|0x800|run.flags)
		selectObject.Call(f.mask.dc, old)
	}
	flushGDI.Call()
	for _, run := range runs {
		if len(run.chars) == 0 {
			continue
		}
		bounds := run.bounds
		for y := max(0, bounds.Top); y < min(f.height, bounds.Bottom); y++ {
			for x := max(0, bounds.Left); x < min(f.width, bounds.Right); x++ {
				i := y*f.width + x
				if coverage := f.mask.pixels[i] & 255; coverage != 0 {
					f.pixels[i] = overCoverage(f.pixels[i], run.color, coverage)
				}
			}
		}
	}
	runtime.KeepAlive(runs)
	return nil
}

func overCoverage(dst, color, coverage uint32) uint32 {
	a := ((color>>24)*coverage + 127) / 255
	inv := 255 - a
	r := (((color>>16)&255)*a + ((dst>>16)&255)*inv + 127) / 255
	g := (((color>>8)&255)*a + ((dst>>8)&255)*inv + 127) / 255
	b := ((color&255)*a + (dst&255)*inv + 127) / 255
	return (a+((dst>>24)*inv+127)/255)<<24 | r<<16 | g<<8 | b
}

func (d *dibSurface) presentLayered(hwnd uintptr, w, h int32) error {
	size := textSize{w, h}
	src := point{}
	blend := [4]byte{0, 0, 255, 1} // AC_SRC_OVER, AC_SRC_ALPHA
	ok, _, err := updateLayered.Call(hwnd, 0, 0, uintptr(unsafe.Pointer(&size)), d.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend[0])), 2)
	if ok == 0 {
		return winapi.Error("UpdateLayeredWindow", err)
	}
	return nil
}
