//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

func TestVisibleChartInputAndResources(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_CHART_UI") != "1" {
		t.Skip("requires an interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.Setenv("SAMUEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var original point
	getCursor.Call(uintptr(unsafe.Pointer(&original)))
	setCursor := user32.NewProc("SetCursorPos")
	defer setCursor.Call(uintptr(original.X), uintptr(original.Y))
	windowAt := user32.NewProc("WindowFromPoint")
	pointArg := func(p point) uintptr { return uintptr(uint64(uint32(p.X)) | uint64(uint32(p.Y))<<32) }
	work := popupWorkArea(point{100, 100})
	x, y := int(work.Left+50), int(work.Bottom-62)
	cfg := config.Default()
	cfg.Placement, cfg.X, cfg.Y = "manual", &x, &y
	cfg.TrayIcon = config.TrayIconNetwork
	u := NewUI("chart-test", cfg)
	snap := barRenderFixture()
	u.latest.Store(&snap)
	if err := u.Run(ctx, func() {
		offline, stopRequests := context.WithCancel(ctx)
		stopRequests()
		u.chartContext = offline
		var bar rect
		getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&bar)))
		hit := u.barHits[1].bounds
		p := point{(hit.Left + hit.Right) / 2, 1}
		screen := point{bar.Left + p.X, bar.Top + p.Y}
		if u.barBitmap.pixels[p.Y*u.barBitmap.width+p.X] != 0 {
			t.Fatal("test point needs to be a transparent pixel")
		}
		if window, _, _ := windowAt.Call(pointArg(screen)); u.barInput == 0 || window != u.barInput {
			t.Fatalf("transparent part of the unlocked quote did not receive input: got %#x want %#x", window, u.barInput)
		}
		// Hiding the input window cannot change the composed screen pixel.
		dwmFlush := winapi.DLL("dwmapi.dll").NewProc("DwmFlush")
		getPixel := gdi32.NewProc("GetPixel")
		screenPixel := func() uintptr {
			dwmFlush.Call()
			dc, _, _ := fluentGetDC.Call(0)
			defer fluentReleaseDC.Call(0, dc)
			color, _, _ := getPixel.Call(dc, uintptr(screen.X), uintptr(screen.Y))
			return color
		}
		withInput := screenPixel()
		showWindow.Call(u.barInput, 0)
		withoutInput := screenPixel()
		showWindow.Call(u.barInput, 4)
		if withInput == 0xffffffff || withInput != withoutInput {
			t.Fatalf("input surface changed the composed taskbar pixel: %x -> %x", withoutInput, withInput)
		}
		setCursor.Call(uintptr(screen.X), uintptr(screen.Y))
		u.barMouseMove(u.barInput, p)
		due := u.hover.due
		u.barHoverTick(due.Add(-time.Millisecond))
		if due.IsZero() || u.chartOpen() {
			t.Fatal("quote hover was not debounced")
		}
		active, _, _ := panelForeground.Call()
		u.barHoverTick(due)
		if !u.chartOpen() || u.chart.symbol != snap.Quotes[0].Symbol || u.chart.bounds.Bottom > bar.Top {
			t.Fatal("hover did not open the intended card above the bar")
		}
		if focused, _, _ := panelForeground.Call(); focused != active {
			t.Fatal("the hover card took foreground focus")
		}
		// Leave the hit region and traverse the gap to the popup.
		u.barMouseLeave()
		c := u.chart
		setCursor.Call(uintptr(c.bounds.Left+20), uintptr(c.bounds.Bottom+2))
		u.barHoverTick(due.Add(50 * time.Millisecond))
		setCursor.Call(uintptr(c.bounds.Left+20), uintptr(c.bounds.Top+20))
		u.barHoverTick(due.Add(100 * time.Millisecond))
		if !u.chartOpen() || !c.leaveAt.IsZero() {
			t.Fatal("pointer entry did not cancel the flyout leave grace")
		}
		setCursor.Call(uintptr(work.Right-5), uintptr(work.Top+5))
		u.barHoverTick(due.Add(110 * time.Millisecond))
		u.barHoverTick(due.Add(110*time.Millisecond + chartGrace))
		if u.chartOpen() || u.hover.timer {
			t.Fatal("flyout stayed open after the leave grace")
		}
		setCursor.Call(uintptr(screen.X), uintptr(screen.Y))
		view, points := chartRenderFixture()
		lparam := uintptr(uint32(uint16(p.X)) | uint32(uint16(p.Y))<<16)
		cycle := func() {
			sendMessage.Call(u.barInput, 0x0201, 0, lparam)
			sendMessage.Call(u.barInput, 0x0202, 0, lparam)
			if !u.chartOpen() {
				t.Fatal("click on a transparent part of the quote did not open the card")
			}
			r := &u.chart.raster
			if err := r.render(r.width, r.height, r.scale, u.Language(), false, view, chartSeries{points: points}, nil); err != nil {
				t.Fatal(err)
			}
			sendMessage.Call(u.chart.hwnd, 0x0100, 27, 0)
			if u.chartOpen() || u.hover.timer {
				t.Fatal("Esc did not close the chart")
			}
		}
		cycle() // Warm up the system's font and graphics caches.
		flushGDI.Call()
		before, err := winapi.ProcessMemory()
		if err != nil {
			t.Fatal(err)
		}
		for range 20 {
			cycle()
		}
		flushGDI.Call()
		after, err := winapi.ProcessMemory()
		if err != nil {
			t.Fatal(err)
		}
		if after.GDIObjects > before.GDIObjects || after.USERObjects > before.USERObjects {
			t.Fatalf("chart handle count grew after 20 cycles: GDI %d -> %d, USER %d -> %d",
				before.GDIObjects, after.GDIObjects, before.USERObjects, after.USERObjects)
		}
		u.showChart(snap.Quotes[0].Symbol, chartActivation{pinned: true})
		c = u.chart
		detailView, detailSeries, detail := chartDetailFixture(t)
		c.view, c.series, c.detail = detailView, detailSeries, detail.data
		u.paintChartFrame()
		plot := c.raster.plot
		samplePoint := point{plot.Left + (plot.Right-plot.Left)/3, (plot.Top + plot.Bottom) / 2}
		sampleParam := uintptr(uint32(uint16(samplePoint.X)) | uint32(uint16(samplePoint.Y))<<16)
		wantStamp := detailSeries.points[2].Time
		if stamp := c.raster.sampleAt(samplePoint, c.series.points); stamp != wantStamp {
			t.Fatalf("plot hit returned %d, want %d", stamp, wantStamp)
		}
		sendMessage.Call(c.hwnd, 0x0200, 0, sampleParam)
		sendMessage.Call(c.hwnd, 0x0201, 0, sampleParam)
		sendMessage.Call(c.hwnd, 0x0202, 0, sampleParam)
		sendMessage.Call(c.hwnd, 0x02A3, 0, 0)
		if c.selected != wantStamp {
			t.Fatal("selected plot sample did not survive mouse leave")
		}
		sendMessage.Call(c.hwnd, 0x0201, 0, sampleParam)
		sendMessage.Call(c.hwnd, 0x0202, 0, sampleParam)
		if c.selected != 0 {
			t.Fatal("clicking the selected plot sample did not unpin it")
		}
		wheel := int16(-960)
		sendMessage.Call(c.hwnd, 0x020A, uintptr(uint16(wheel))<<16, 0)
		if !c.scrollTimer || c.scrollTarget <= 0 || c.scroll != 0 {
			t.Fatalf("wheel didn't start a scroll transition: %d -> %d", c.scroll, c.scrollTarget)
		}
		for i := 0; i < 30 && c.scrollTimer; i++ {
			sendMessage.Call(c.hwnd, 0x0113, chartScrollTimer, 0)
		}
		if c.scrollTimer || c.scroll != c.scrollTarget || len(c.raster.links) == 0 {
			t.Fatal("wheel didn't reveal and settle on the news area")
		}
		var clicked string
		c.openLink = func(_ uintptr, url string) { clicked = url }
		target := c.raster.links[0]
		click := point{target.bounds.Left + 10, target.bounds.Top + 4}
		clickParam := uintptr(uint32(uint16(click.X)) | uint32(uint16(click.Y))<<16)
		sendMessage.Call(c.hwnd, 0x0200, 0, clickParam)
		sendMessage.Call(c.hwnd, 0x0201, 0, clickParam)
		sendMessage.Call(c.hwnd, 0x0202, 0, clickParam)
		if clicked != target.url {
			t.Fatalf("scrolled link opened %q, want %q", clicked, target.url)
		}
		c.openLink = nil
		sendMessage.Call(c.hwnd, 0x0100, 27, 0)
		sendMessage.Call(u.barInput, 0x0201, 0, lparam)
		setCursor.Call(uintptr(screen.X+24), uintptr(screen.Y-14))
		dragParam := uintptr(uint32(uint16(p.X+24)) | uint32(uint16(p.Y-14))<<16)
		sendMessage.Call(u.barInput, 0x0200, 0, dragParam)
		sendMessage.Call(u.barInput, 0x0202, 0, lparam)
		var moved rect
		getWindowRect.Call(u.hwnd, uintptr(unsafe.Pointer(&moved)))
		if u.chartOpen() || moved.Left != bar.Left+24 || moved.Top != bar.Top-14 {
			t.Fatalf("drag moved incorrectly or opened the card: %+v -> %+v", bar, moved)
		}
		screen.X, screen.Y = moved.Left+p.X, moved.Top+p.Y
		u.cfg.Locked = true
		u.applyLockStyle()
		window, _, _ := windowAt.Call(pointArg(screen))
		style, _, _ := getWindowLongPtr.Call(u.hwnd, ^uintptr(19))
		if style&0x20 == 0 || window == u.hwnd || window == u.barInput {
			t.Fatal("lock did not restore mouse click-through")
		}
		cancel()
	}); err != nil {
		t.Fatal(err)
	}
}
