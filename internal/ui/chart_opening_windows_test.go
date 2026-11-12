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
)

func TestChartOpeningOnDesktop(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_CHART_UI") != "1" {
		t.Skip("requires an interactive Windows desktop")
	}
	run := func(t *testing.T, locked bool, check func(*testing.T, *UI)) {
		t.Helper()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		t.Setenv("SAMUEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var original point
		getCursor.Call(uintptr(unsafe.Pointer(&original)))
		defer user32.NewProc("SetCursorPos").Call(uintptr(original.X), uintptr(original.Y))
		work := popupWorkArea(point{100, 100})
		x, y := int(work.Left+50), int(work.Bottom-62)
		cfg := config.Default()
		cfg.Locked, cfg.Placement, cfg.X, cfg.Y = locked, "manual", &x, &y
		cfg.TrayIcon = config.TrayIconNetwork
		u := NewUI("chart-opening-test", cfg)
		snap := barRenderFixture()
		u.latest.Store(&snap)
		if err := u.Run(ctx, func() {
			offline, stop := context.WithCancel(ctx)
			stop()
			u.chartContext = offline
			check(t, u)
			cancel()
		}); err != nil {
			t.Fatal(err)
		}
	}
	button := func(u *UI, msg uintptr, p point) {
		sendMessage.Call(u.barInput, msg, 0, uintptr(uint32(uint16(p.X))|uint32(uint16(p.Y))<<16))
	}
	move := func(p point) { user32.NewProc("SetCursorPos").Call(uintptr(p.X), uintptr(p.Y)) }
	t.Run("locked tray click", func(t *testing.T) {
		run(t, true, func(t *testing.T, u *UI) {
			for _, event := range []uintptr{0x0202, 0x0400, 0x0401} {
				sendMessage.Call(u.hwnd, wmTray, 0, event)
				if !u.chartOpen() || u.chart.symbol != u.barHits[1].symbol || !u.hover.timer {
					t.Fatalf("tray activation %#x didn't open the first chart while the bar was locked", event)
				}
			}
			u.handleMenuSelection(uintptr(ActionChartSecond), nil)
			if !u.chartOpen() || u.chart.symbol != u.barHits[2].symbol || !u.cfg.Locked {
				t.Fatal("the second chart wasn't reachable through the menu while locked")
			}
		})
	})
	t.Run("small pointer motion", func(t *testing.T) {
		run(t, false, func(t *testing.T, u *UI) {
			hit := u.barHits[1].bounds
			p := point{(hit.Left + hit.Right) / 2, 1}
			bar, _, _ := u.chartAnchor(u.barHits[1].symbol)
			move(point{bar.Left + p.X, bar.Top + p.Y})
			button(u, 0x0201, p)
			pressed := u.press
			p.X += u.px(5)
			// The live cursor may have moved beyond a queued click event.
			move(point{bar.Left + p.X + u.px(25), bar.Top + p.Y})
			button(u, 0x0200, p)
			motion := u.press
			button(u, 0x0202, p)
			if !u.chartOpen() || u.chart.symbol != u.barHits[1].symbol {
				t.Fatalf("slight pointer motion canceled the quote click: down=%+v move=%+v", pressed, motion)
			}
			if moved, _, _ := u.chartAnchor(u.barHits[1].symbol); moved != bar {
				t.Fatal("slight pointer motion moved the bar")
			}
		})
	})
	t.Run("click remains visible", func(t *testing.T) {
		run(t, false, func(t *testing.T, u *UI) {
			hit := u.barHits[1].bounds
			p := point{(hit.Left + hit.Right) / 2, 1}
			bar, _, _ := u.chartAnchor(u.barHits[1].symbol)
			move(point{bar.Left + p.X, bar.Top + p.Y})
			button(u, 0x0201, p)
			button(u, 0x0202, p)
			if !u.chartOpen() {
				t.Fatal("ordinary quote click did not open a chart")
			}
			work := popupWorkArea(point{bar.Left, bar.Top})
			move(point{work.Right - 5, work.Top + 5})
			now := time.Now()
			u.barHoverTick(now)
			u.barHoverTick(now.Add(2 * time.Second))
			if !u.chartOpen() {
				t.Fatal("clicked chart disappeared before the user could reach it")
			}
		})
	})
	t.Run("rate width changes while pointing", func(t *testing.T) {
		run(t, false, func(t *testing.T, u *UI) {
			hit := u.barHitBounds(1)
			p := point{hit.Left + 1, 1}
			bar, _, _ := u.chartAnchor(u.barHits[1].symbol)
			move(point{bar.Left + p.X, bar.Top + p.Y})
			button(u, 0x0200, p)
			if u.hover.due.IsZero() {
				t.Fatal("hover didn't start")
			}
			now := u.hover.due
			u.cfg.ShowNetDetail = true
			if err := u.renderBar(); err != nil {
				t.Fatal(err)
			}
			u.barHoverTick(now)
			if u.hover.hit != 1 || u.hover.due.IsZero() {
				t.Fatal("width change swallowed the pending hover instead of resolving the new target")
			}
			u.cfg.ShowNetDetail = false
			if err := u.renderBar(); err != nil {
				t.Fatal(err)
			}
			u.barHoverTick(u.hover.due)
			u.barHoverTick(u.hover.due)
			if !u.chartOpen() || u.chart.symbol != u.barHits[1].symbol {
				t.Fatal("hover didn't recover after the bar layout changed back")
			}
			bounds := u.chart.bounds
			u.cfg.ShowNetDetail = true
			if err := u.renderBar(); err != nil {
				t.Fatal(err)
			}
			if u.chart.bounds != bounds {
				t.Fatal("an open card jumped as the rate text changed")
			}
		})
	})
}

func TestBarQuoteHitTargetsIncludeDividersAndPadding(t *testing.T) {
	u := NewUI("test", config.Default())
	u.width = 410
	u.barHits = [3]barHit{
		{bounds: rect{0, 0, 76, 30}},
		{rect{90, 0, 228, 30}, "VOO"},
		{rect{246, 0, 404, 30}, "QQQ"},
	}
	for _, tt := range []struct {
		x    int32
		want string
	}{{82, ""}, {83, "VOO"}, {233, "VOO"}, {238, "QQQ"}, {409, "QQQ"}, {410, ""}} {
		if got := u.symbolAt(point{tt.x, 0}); got != tt.want {
			t.Errorf("symbol at x=%d was %q, want %q", tt.x, got, tt.want)
		}
	}
}

func TestChartOutsideClickWaitsForOpeningButtonRelease(t *testing.T) {
	var card chartFlyout
	if card.outsideClick(true, true) || card.outsideClick(true, true) || card.outsideClick(true, false) {
		t.Fatal("the opening click closed the card")
	}
	if card.outsideClick(false, true) || card.outsideClick(true, true) {
		t.Fatal("a click begun inside was treated as an outside click")
	}
	card.outsideClick(true, false)
	if !card.outsideClick(true, true) {
		t.Fatal("a new click outside did not dismiss the card")
	}
}
