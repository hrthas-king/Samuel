//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	gdiplus          = winapi.DLL("gdiplus.dll")
	gdipStartup      = gdiplus.NewProc("GdiplusStartup")
	gdipShutdown     = gdiplus.NewProc("GdiplusShutdown")
	gdipCreateBitmap = gdiplus.NewProc("GdipCreateBitmapFromScan0")
	gdipDisposeImage = gdiplus.NewProc("GdipDisposeImage")
	gdipGraphics     = gdiplus.NewProc("GdipGetImageGraphicsContext")
	gdipDeleteGraph  = gdiplus.NewProc("GdipDeleteGraphics")
	gdipSmoothing    = gdiplus.NewProc("GdipSetSmoothingMode")
	gdipSolid        = gdiplus.NewProc("GdipCreateSolidFill")
	gdipSolidColor   = gdiplus.NewProc("GdipSetSolidFillColor")
	gdipLinear       = gdiplus.NewProc("GdipCreateLineBrushI")
	gdipDeleteBrush  = gdiplus.NewProc("GdipDeleteBrush")
	gdipPolygon      = gdiplus.NewProc("GdipFillPolygon")
	gdipEllipse      = gdiplus.NewProc("GdipFillEllipseI")
	gdipRects        = gdiplus.NewProc("GdipFillRectanglesI")
)

func startChartGraphics() (uintptr, error) {
	var token uintptr
	input := struct {
		version                  uint32
		debugCallback            uintptr
		suppressThread, noCodecs int32
	}{version: 1}
	status, _, _ := gdipStartup.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&input)), 0)
	if status != 0 {
		return 0, fmt.Errorf("GdiplusStartup status %d", status)
	}
	return token, nil
}

func fillChartPolygon(graphics, brush uintptr, points []chartPoint) {
	gdipPolygon.Call(graphics, brush, uintptr(unsafe.Pointer(&points[0])), uintptr(len(points)), 1) // FillModeWinding
}

// Outline the polyline with bounded miters. GDI+ fills the outline with
// antialiasing. PointF arrays avoid passing scalar floats through the Win32
// integer syscall ABI on arm64.
func chartStroke(line []chartPoint, radius float64) []chartPoint {
	normal := func(a, b chartPoint) (float64, float64) {
		dx, dy := float64(b.X-a.X), float64(b.Y-a.Y)
		length := math.Hypot(dx, dy)
		if length == 0 {
			return 0, 1
		}
		return -dy / length, dx / length
	}
	n := len(line)
	outline := make([]chartPoint, 2*n)
	for i, p := range line {
		x1, y1 := normal(line[max(0, i-1)], line[max(1, i)])
		x2, y2 := normal(line[min(n-2, i)], line[min(n-1, i+1)])
		factor := radius / max(.45, 1+x1*x2+y1*y2)
		dx, dy := float32((x1+x2)*factor), float32((y1+y2)*factor)
		outline[i] = chartPoint{p.X + dx, p.Y + dy}
		outline[2*n-i-1] = chartPoint{p.X - dx, p.Y - dy}
	}
	return outline
}

func drawSparkline(d *dibSurface, bounds rect, line []chartPoint, baseline float32, color uint32, scale float64, selected *chartPoint) bool {
	var image uintptr
	status, _, _ := gdipCreateBitmap.Call(uintptr(d.width), uintptr(d.height), uintptr(d.width*4), 0xE200B, // PixelFormat32bppPARGB
		uintptr(unsafe.Pointer(&d.pixels[0])), uintptr(unsafe.Pointer(&image)))
	if status != 0 {
		return false
	}
	defer gdipDisposeImage.Call(image)
	var graphics uintptr
	status, _, _ = gdipGraphics.Call(image, uintptr(unsafe.Pointer(&graphics)))
	if status != 0 {
		return false
	}
	defer gdipDeleteGraph.Call(graphics)
	gdipSmoothing.Call(graphics, 4) // SmoothingModeAntiAlias

	var solid uintptr
	status, _, _ = gdipSolid.Call(0, uintptr(unsafe.Pointer(&solid)))
	if status != 0 {
		return false
	}
	defer gdipDeleteBrush.Call(solid)
	dash := max(1, int32(math.Round(3*scale)))
	type gridDash struct{ x, y, width, height int32 }
	dashes := make([]gridDash, 0, (bounds.Right-bounds.Left)/(2*dash)+1)
	grid := func(y int32, tint uint32) {
		gdipSolidColor.Call(solid, uintptr(tint))
		dashes = dashes[:0]
		for x := bounds.Left; x < bounds.Right; x += 2 * dash {
			dashes = append(dashes, gridDash{x, y, min(dash, bounds.Right-x), 1})
		}
		gdipRects.Call(graphics, solid, uintptr(unsafe.Pointer(&dashes[0])), uintptr(len(dashes)))
	}
	height := bounds.Bottom - bounds.Top
	grid(bounds.Top+height/4, 0xFF252B33)
	grid(bounds.Top+3*height/4, 0xFF252B33)
	if baseline >= 0 {
		grid(int32(math.Round(float64(baseline))), 0xFF414A57)
	}

	last := line[len(line)-1]
	from, to := point{bounds.Left, bounds.Top}, point{bounds.Left, bounds.Bottom}
	var shade uintptr
	rgb := color & 0xFFFFFF
	status, _, _ = gdipLinear.Call(uintptr(unsafe.Pointer(&from)), uintptr(unsafe.Pointer(&to)),
		uintptr(0x40000000|rgb), uintptr(rgb), 2, uintptr(unsafe.Pointer(&shade))) // WrapModeTileFlipY
	if status == 0 {
		area := make([]chartPoint, 0, len(line)+2)
		area = append(area, line...)
		area = append(area, chartPoint{last.X, float32(bounds.Bottom)}, chartPoint{line[0].X, float32(bounds.Bottom)})
		fillChartPolygon(graphics, shade, area)
		gdipDeleteBrush.Call(shade)
	}
	if selected != nil {
		gdipSolidColor.Call(solid, 0xFF637C96)
		x := int32(math.Round(float64(selected.X)))
		dashes = dashes[:0]
		for y := bounds.Top; y < bounds.Bottom; y += 2 * dash {
			dashes = append(dashes, gridDash{x, y, 1, min(dash, bounds.Bottom-y)})
		}
		gdipRects.Call(graphics, solid, uintptr(unsafe.Pointer(&dashes[0])), uintptr(len(dashes)))
	}
	gdipSolidColor.Call(solid, uintptr(color))
	fillChartPolygon(graphics, solid, chartStroke(line, 1.05*scale))
	dot := func(p chartPoint, radius float64, tint uint32) {
		x, y := int32(math.Round(float64(p.X))), int32(math.Round(float64(p.Y)))
		r := max(1, int32(math.Round(radius*scale)))
		gdipSolidColor.Call(solid, uintptr(tint))
		gdipEllipse.Call(graphics, solid, uintptr(x-r), uintptr(y-r), uintptr(2*r), uintptr(2*r))
	}
	dot(last, 10, 0x0C000000|rgb)
	dot(last, 8, 0x14000000|rgb)
	dot(last, 6, 0x24000000|rgb)
	dot(last, 4, 0x38000000|rgb)
	dot(last, 3, color)
	if selected != nil {
		dot(*selected, 5, 0xFFE5EEF8)
		dot(*selected, 3, color)
	}
	return true
}
