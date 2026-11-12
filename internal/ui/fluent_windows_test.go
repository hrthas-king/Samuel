//go:build windows && (amd64 || arm64)

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/watchlist"
)

func fluentFind(entries []fluentEntry, prefix string) []fluentEntry {
	for _, e := range entries {
		if strings.HasPrefix(e.label, prefix) {
			return e.children
		}
		if found := fluentFind(e.children, prefix); found != nil {
			return found
		}
	}
	return nil
}

func TestFluentMenuPreservesActionsAndSideDelete(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if unsafe.Sizeof(menuItemInfo{}) != 80 || unsafe.Sizeof(fluentDrawItem{}) != 64 {
		t.Fatal("Windows popup ABI mismatch")
	}
	pair, _ := watchlist.NewPair("513500", "AAPL")
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	market.SetCustomPairs([]watchlist.Pair{pair})
	cfg := config.Default()
	cfg.CustomPairs = []watchlist.Pair{pair}
	cfg.MarketGroup = pair.ID()
	u := NewUI("v1.0.0", cfg)
	menu, commands := u.buildMenu()
	defer destroyMenu.Call(menu)
	entries := readFluentMenu(menu, commands)
	found := make(map[Action]bool)
	var walk func([]fluentEntry)
	walk = func(es []fluentEntry) {
		for _, e := range es {
			found[e.action] = true
			walk(e.children)
		}
	}
	walk(entries)
	for _, action := range []Action{ActionChartFirst, ActionChartSecond, ActionRefresh, ActionToggleNetDetail, ActionTrayIconApplication, ActionTrayIconNetwork, ActionToggleLock, ActionDockLeft, ActionToggleColor, ActionGroupVOOQQQ, ActionGroupETF159632159655, ActionScale75, ActionScale130, ActionInterval1, ActionInterval5, ActionDiagnostics, ActionAutostart, ActionExit, actionAddCustom} {
		if !found[action] {
			t.Errorf("menu lost action %d", action)
		}
	}
	other := fluentFind(entries, "其他")
	if len(other) != 2 || other[1].label != "513500 & AAPL" || other[1].remove == 0 || !other[1].checked {
		t.Fatalf("custom pair menu: %+v", other)
	}
	if commands[other[1].remove].id != pair.ID() || !commands[other[1].remove].remove {
		t.Fatal("side delete targets a different pair")
	}
}

func TestFluentNativeKeyboardCascadeScrollAndClose(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	process, _, _ := kernel32.NewProc("GetCurrentProcess").Call()
	u := NewUI("test", config.Default())
	s := &fluentMenu{u: u, visual: newFluentVisuals(1, fluentColors(false))}
	u.flyout = s
	defer u.closeFluentMenu()
	children := make([]fluentEntry, 100)
	for i := range children {
		children[i] = fluentEntry{label: "Saved stock pair", action: ActionRefresh, enabled: true}
	}
	entries := []fluentEntry{{label: "Samuel"}, {label: "Refresh", action: ActionRefresh, enabled: true}, {label: "More", enabled: true, children: children}}
	if !s.open(entries, point{100, 100}, nil) {
		t.Fatal("popup creation failed")
	}
	root := s.windows[0]
	sendMessage.Call(root.hwnd, 0x100, 40, 0)
	if root.hover != 1 {
		t.Fatal("keyboard did not skip disabled heading")
	}
	sendMessage.Call(root.hwnd, 0x100, 40, 0)
	sendMessage.Call(root.hwnd, 0x100, 39, 0)
	if len(s.windows) != 2 {
		t.Fatal("Right did not open the cascade")
	}
	child := s.windows[1]
	rootOffset := root.offset
	delta := int16(-120)
	at := point{child.bounds.Left + 20, child.bounds.Top + 20}
	sendMessage.Call(root.hwnd, 0x20a, uintptr(uint32(uint16(delta))<<16), uintptr(uint32(uint16(at.X))|uint32(uint16(at.Y))<<16))
	if child.offset <= 0 || root.offset != rootOffset {
		t.Fatalf("wheel did not scroll hovered cascade: root=%d child=%d at=%+v bounds=%+v windows=%d", root.offset, child.offset, at, child.bounds, len(s.windows))
	}
	sendMessage.Call(root.hwnd, 0x100, 35, 0)
	r := child.rowBounds(child.hover)
	if child.hover != 99 || child.offset <= 0 || r.Bottom > child.height {
		t.Fatalf("End did not reveal final row: hover=%d offset=%d bounds=%+v", child.hover, child.offset, r)
	}
	sendMessage.Call(root.hwnd, 0x100, 36, 0)
	if child.hover != 0 || child.rowBounds(0).Top < 0 {
		t.Fatal("Home did not reveal the first row")
	}
	sendMessage.Call(root.hwnd, 0x100, 37, 0)
	if len(s.windows) != 1 {
		t.Fatal("Left did not return to parent")
	}
	sendMessage.Call(root.hwnd, 0x100, 38, 0)
	sendMessage.Call(root.hwnd, 0x100, 13, 0)
	select {
	case action := <-u.Actions:
		if action != ActionRefresh {
			t.Fatal(action)
		}
	default:
		t.Fatal("Enter did not dispatch the original action")
	}
	if u.flyout != nil || !s.closed {
		t.Fatal("selected menu remained open")
	}
	if alive, _, _ := user32.NewProc("IsWindow").Call(root.hwnd); alive != 0 {
		t.Fatal("popup native window leaked")
	}
	gdi32.NewProc("GdiFlush").Call()
	after, _, _ := user32.NewProc("GetGuiResources").Call(process, 0)
	for i := 0; i < 20; i++ {
		trial := &fluentMenu{u: u, visual: newFluentVisuals(1, fluentColors(false))}
		if !trial.open(entries, point{100, 100}, nil) {
			t.Fatal("trial popup")
		}
		trial.close()
	}
	end, _, _ := user32.NewProc("GetGuiResources").Call(process, 0)
	if end > after {
		t.Fatalf("GDI objects grew across 20 more menu open/close cycles: %d -> %d", after, end)
	}
}

func TestFluentNativeDeleteUsesAdjacentButton(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	a, _ := watchlist.NewPair("513100", "513500")
	b, _ := watchlist.NewPair("AAPL", "MSFT")
	cfg := config.Default()
	cfg.CustomPairs = []watchlist.Pair{a, b}
	cfg.MarketGroup = a.ID()
	market.SetCustomPairs(cfg.CustomPairs)
	u := NewUI("test", cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	menu, commands := u.buildMenu()
	defer destroyMenu.Call(menu)
	other := fluentFind(readFluentMenu(menu, commands), "其他")
	s := &fluentMenu{u: u, commands: commands, visual: newFluentVisuals(1, fluentColors(false))}
	u.flyout = s
	defer u.closeFluentMenu()
	if !s.open(other, point{100, 100}, nil) {
		t.Fatal("popup creation failed")
	}
	p := s.windows[0]
	r := p.rowBounds(2)
	x, y := p.width-16, (r.Top+r.Bottom)/2
	sendMessage.Call(p.hwnd, 0x202, 0, uintptr(uint32(uint16(x))|uint32(uint16(y))<<16))
	loaded := config.LoadFrom(path)
	if len(loaded.CustomPairs) != 1 || loaded.CustomPairs[0] != a || loaded.MarketGroup != a.ID() {
		t.Fatalf("side deletion: %+v", loaded)
	}
}

func renderFluentBitmap(t *testing.T, w, h int, draw func(uintptr)) *image.NRGBA {
	t.Helper()
	var bitmap struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		XPels, YPels           int32
		Colors, Important      uint32
		Color                  uint32
	}
	bitmap.Size = 40
	bitmap.Width = int32(w)
	bitmap.Height = -int32(h)
	bitmap.Planes = 1
	bitmap.Bits = 32
	dc, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(0)
	defer gdi32.NewProc("DeleteDC").Call(dc)
	var bits uintptr
	bmp, _, _ := gdi32.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&bitmap)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dc == 0 || bmp == 0 || bits == 0 {
		t.Fatal("native rendering surface failed")
	}
	defer deleteObject.Call(bmp)
	old, _, _ := selectObject.Call(dc, bmp)
	defer selectObject.Call(dc, old)
	draw(dc)
	gdi32.NewProc("GdiFlush").Call()
	data := make([]byte, w*h*4)
	rtlMoveMemory.Call(uintptr(unsafe.Pointer(&data[0])), bits, uintptr(len(data)))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(data); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = data[i+2], data[i+1], data[i], 255
	}
	return img
}

func TestFluentNativeLightDarkRenderingAndInputFocus(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cfg := config.Default()
	cfg.UIScalePercent = 75
	u := NewUI("v1.0.8", cfg)
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	p := u.customPanel
	if p == nil {
		t.Fatal("input panel missing")
	}
	dc, _, _ := fluentGetDC.Call(0)
	old, _, _ := selectObject.Call(dc, p.visual.font)
	var face [64]uint16
	gdi32.NewProc("GetTextFaceW").Call(dc, 64, uintptr(unsafe.Pointer(&face[0])))
	if got := syscall.UTF16ToString(face[:]); got != "Segoe UI" {
		t.Fatalf("popup body font: %s", got)
	}
	selectObject.Call(dc, old)
	fluentReleaseDC.Call(0, dc)
	putPanelText(p.first, "513100")
	putPanelText(p.second, "513500")
	setFocus.Call(p.first)
	sendMessage.Call(p.first, 0x100, 9, 0)
	focus, _, _ := user32.NewProc("GetFocus").Call()
	if focus != p.second {
		t.Fatal("Tab did not move to second input")
	}
	sendMessage.Call(p.second, 0x100, 9, 0)
	focus, _, _ = user32.NewProc("GetFocus").Call()
	if focus != p.cancel {
		t.Fatal("Tab did not reach cancel")
	}
	for _, dark := range []bool{false, true} {
		palette := fluentColors(dark)
		p.visual.refresh(palette)
		mode := "light"
		if dark {
			mode = "dark"
		}
		input := renderFluentBitmap(t, 520, 374, func(dc uintptr) { sendMessage.Call(p.hwnd, 0x317, dc, 4|8|16) })
		pixel := input.NRGBAAt(5, 60)
		if pixel.R != byte(palette.background) || pixel.G != byte(palette.background>>8) || pixel.B != byte(palette.background>>16) {
			t.Fatalf("%s panel background: %+v", mode, pixel)
		}
		menu, commands := u.buildMenu()
		s := &fluentMenu{u: u, commands: commands, visual: newFluentVisuals(1, palette)}
		if !s.open(readFluentMenu(menu, commands), point{100, 100}, nil) {
			t.Fatal("menu creation failed")
		}
		popup := s.windows[0]
		popup.hover = 5
		menuImage := renderFluentBitmap(t, int(popup.width), int(popup.height), popup.draw)
		s.close()
		destroyMenu.Call(menu)
		if dir := os.Getenv("PULSEBAR_UI_ARTIFACTS"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for name, img := range map[string]*image.NRGBA{"input-" + mode + ".png": input, "menu-" + mode + ".png": menuImage} {
				f, err := os.Create(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, img)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestCustomPanelDarkButtonColorsAndRoundedCorners(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := NewUI("test", config.Default())
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	p := u.customPanel
	if p == nil {
		t.Fatal("input panel missing")
	}
	p.visual.refresh(fluentColors(true))
	img := renderFluentBitmap(t, 122, 41, func(dc uintptr) {
		p.drawButton(&fluentDrawItem{Window: p.submit, DC: dc, Bounds: rect{0, 0, 122, 41}})
	})
	corner := img.NRGBAAt(0, 0)
	if corner.R != 44 || corner.G != 44 || corner.B != 44 {
		t.Fatalf("corner should match panel background: %+v", corner)
	}
	fill := img.NRGBAAt(12, 20)
	if fill.R != 96 || fill.G != 205 || fill.B != 255 {
		t.Fatalf("primary button should retain original light blue: %+v", fill)
	}
}
