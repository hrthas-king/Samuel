//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/watchlist"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

func TestCustomPairSaveBeforeDisplayAndDeleteFallback(t *testing.T) {
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	path := filepath.Join(t.TempDir(), "config.json")
	u := NewUI("test", config.Default())
	u.latest.Store(&app.Snapshot{MarketGroup: market.DefaultGroup})
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	a, _ := watchlist.NewPair("600519", "000858")
	b, _ := watchlist.NewPair("300750", "920002")
	if err := u.addCustomPair(a); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadFrom(path); got.MarketGroup != a.ID() || len(got.CustomPairs) != 1 {
		t.Fatal("new pair not on disk")
	}
	if group := <-u.GroupChanges; group != a.ID() {
		t.Fatal("new pair not immediately selected")
	}
	u.saveSettings = func(config.Config) error { return errors.New("disk full") }
	if err := u.addCustomPair(b); err == nil || len(u.cfg.CustomPairs) != 1 || market.ValidGroup(b.ID()) {
		t.Fatal("failed save changed UI registry")
	}
	if err := u.deleteCustomPair(a.ID()); err == nil || u.cfg.MarketGroup != a.ID() {
		t.Fatal("failed deletion changed display")
	}
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	u.addCustomPair(b)
	u.deleteCustomPair(a.ID())
	if u.cfg.MarketGroup != b.ID() {
		t.Fatal("deleting inactive pair changed group")
	}
	u.deleteCustomPair(b.ID())
	if u.cfg.MarketGroup != market.DefaultGroup || (<-u.GroupChanges) != market.DefaultGroup {
		t.Fatal("active deletion did not select default")
	}
	if got := config.LoadFrom(path); len(got.CustomPairs) != 0 || got.MarketGroup != config.DefaultMarketGroup {
		t.Fatal("deleted rows came back on restart")
	}
}

// Opt-in real-provider integration; ordinary CI remains deterministic and offline.
func TestLiveNativeCustomPairFlow(t *testing.T) {
	if os.Getenv("PULSEBAR_LIVE_CUSTOM") != "1" {
		t.Skip("live provider integration is opt-in")
	}
	path := os.Getenv("PULSEBAR_LIVE_CONFIG")
	if path == "" {
		t.Fatal("an isolated test config path is required")
	}
	for _, tc := range []struct{ name, first, second string }{
		{"mainland_stocks", "600519", "000858"},
		{"shenzhen_etfs", "159632", "159655"},
		{"shanghai_etfs", "513100", "513500"},
		{"us_stocks", "aapl", "msft"},
		{"us_etfs", "VOO", "QQQ"},
		{"us_share_class", "brk.b", "AAPL"},
		{"mixed_markets", "513500", "AAPL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runLiveNativeCustomPairFlow(t, path+"-"+tc.name+".json", tc.first, tc.second)
		})
	}
}

func runLiveNativeCustomPairFlow(t *testing.T, path, first, second string) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	u := NewUI("live", config.Default())
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	if u.customPanel == nil {
		t.Fatal("native panel missing")
	}
	putPanelText(u.customPanel.first, first)
	putPanelText(u.customPanel.second, second)
	sendMessage.Call(u.customPanel.first, 0x100, 13, 0)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(12 * time.Second)
	for u.customPanel != nil {
		select {
		case <-deadline:
			t.Fatalf("validation did not finish: %s", panelText(u.customPanel.status))
		case <-tick.C:
			u.handleCustomResults()
			if u.customPanel != nil && !u.customPanel.busy {
				t.Fatalf("live validation failed: %s", panelText(u.customPanel.status))
			}
		}
	}
	loaded := config.LoadFrom(path)
	p, _ := watchlist.NewPair(first, second)
	if loaded.MarketGroup != p.ID() || len(loaded.CustomPairs) != 1 {
		t.Fatal("Enter did not persist selection")
	}
	market.SetCustomPairs(loaded.CustomPairs)
	market.SetActiveGroup(loaded.MarketGroup)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	batch, err := market.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range p.Symbols() {
		q := batch[sym].Quote
		if q == nil {
			t.Fatal(batch[sym].Err)
		}
		t.Logf("restored quote: %s %.*f (%s)", q.DisplayName(), market.PriceDecimals(sym), q.Price, q.Session)
	}
	u = NewUI("restarted", loaded)
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	if err := u.deleteCustomPair(p.ID()); err != nil {
		t.Fatal(err)
	}
	after := config.LoadFrom(path)
	if len(after.CustomPairs) != 0 || after.MarketGroup != config.DefaultMarketGroup {
		t.Fatal("delete did not survive restart")
	}
	t.Log("native Enter -> live validation -> atomic save -> restart -> quotes -> delete/default -> restart: PASS")
}

func TestNativeCustomPanelAcceptsUSCodes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := NewUI("test", config.Default())
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	if u.customPanel == nil {
		t.Fatal("native panel missing")
	}
	for _, c := range "BRK.B" {
		sendMessage.Call(u.customPanel.first, 0x102, uintptr(c), 0) // WM_CHAR: exercise the real edit control.
	}
	if got := panelText(u.customPanel.first); got != "BRK.B" {
		t.Fatalf("US ticker rejected by native input: %q", got)
	}
	sendMessage.Call(u.customPanel.second, 0xc, 0, uintptr(unsafe.Pointer(winapi.UTF16("513500"))))
	if got := panelText(u.customPanel.second); got != "513500" {
		t.Fatalf("mainland ETF input: %q", got)
	}
}

func TestCustomSelectionRejectsPriorSnapshot(t *testing.T) {
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	u := NewUI("test", config.Default())
	u.saveSettings = func(config.Config) error { return nil }
	u.latest.Store(&app.Snapshot{MarketGroup: market.DefaultGroup})
	p, _ := watchlist.NewPair("600519", "000858")
	if err := u.addCustomPair(p); err != nil {
		t.Fatal(err)
	}
	u.Publish(app.Snapshot{MarketGroup: market.DefaultGroup})
	if u.latest.Load().MarketGroup != p.ID() {
		t.Fatal("old group snapshot replaced new selection")
	}
	u.Publish(app.Snapshot{MarketGroup: p.ID()})
	if err := u.deleteCustomPair(p.ID()); err != nil {
		t.Fatal(err)
	}
	u.Publish(app.Snapshot{MarketGroup: p.ID()})
	if u.latest.Load().MarketGroup != market.DefaultGroup {
		t.Fatal("deleted group snapshot returned")
	}
}

func TestAddSelectionDoesNotOpenInsideMenuCleanup(t *testing.T) {
	if wmShowCustom == winapi.WMDockLeft || wmShowCustom == winapi.WMDiag || wmShowCustom == wmCustomResult {
		t.Fatal("panel message collides with an existing window command")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := NewUI("test", config.Default())
	defer u.closeCustomPanel()
	u.handleMenuSelection(uintptr(actionAddCustom), nil)
	var msg message
	peek := user32.NewProc("PeekMessageW")
	n, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, wmShowCustom, wmShowCustom, 1)
	if n == 0 || u.customPanel != nil {
		t.Fatal("Add opened synchronously instead of scheduling the input after native menu cleanup")
	}
}

func TestInitialPanelDeactivationDoesNotDismiss(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := NewUI("test", config.Default())
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	if u.customPanel == nil {
		t.Fatal("no panel")
	}
	sendMessage.Call(u.customPanel.hwnd, 6, 0, 0)
	if u.customPanel == nil {
		t.Fatal("initial activation transition dismissed the new input panel")
	}
}

func TestNativeOtherMenuHasDirectSelectionAndSideDelete(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	a, _ := watchlist.NewPair("600519", "000858")
	c := config.Default()
	c.CustomPairs = []watchlist.Pair{a}
	c.MarketGroup = a.ID()
	u := NewUI("test", c)
	parent, _, _ := createMenu.Call()
	defer destroyMenu.Call(parent)
	commands := make(map[Action]customMenuCommand)
	u.appendCustomMenu(parent, commands)
	getSub := user32.NewProc("GetSubMenu")
	getID := user32.NewProc("GetMenuItemID")
	getString := user32.NewProc("GetMenuStringW")
	getState := user32.NewProc("GetMenuState")
	other, _, _ := getSub.Call(parent, 0)
	selection, _, _ := getID.Call(other, 1)
	if command, ok := commands[Action(selection)]; !ok || command.id != a.ID() || command.remove {
		t.Fatal("pair row is not directly selectable")
	}
	var text [128]uint16
	getString.Call(other, 1, uintptr(unsafe.Pointer(&text[0])), 128, 0x400)
	if label := syscall.UTF16ToString(text[:]); strings.SplitN(label, "\t", 2)[0] != "600519 && 000858" {
		t.Fatalf("literal ampersand not escaped: %s", label)
	}
	flags, _, _ := getState.Call(other, 2, 0x400)
	if flags&0x20 == 0 {
		t.Fatal("delete actions not in adjacent column")
	}
	deleteID, _, _ := getID.Call(other, 3)
	if command, ok := commands[Action(deleteID)]; !ok || !command.remove || command.id != a.ID() {
		t.Fatal("side deletion targets wrong pair")
	}
}

func putPanelText(hwnd uintptr, text string) {
	p := winapi.UTF16(text)
	setWindowText.Call(hwnd, uintptr(unsafe.Pointer(p)))
}
func awaitPanelResult(t *testing.T, u *UI) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("no validation result")
		case <-tick.C:
			if len(u.customResults) > 0 {
				u.handleCustomResults()
				return
			}
		}
	}
}

func TestNativeCustomPanelEnterOutsideCloseAndStaleResult(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer market.SetCustomPairs(nil)
	defer market.SetActiveGroup(market.DefaultGroup)
	path := filepath.Join(t.TempDir(), "config.json")
	u := NewUI("test", config.Default())
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	u.validatePair = func(_ context.Context, a, b string) (watchlist.Pair, error) { return watchlist.NewPair(a, b) }
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	if u.customPanel == nil {
		t.Fatal("panel not created")
	}
	p := u.customPanel
	style, _, _ := getWindowLongPtr.Call(p.hwnd, ^uintptr(15))
	if style&0x80000000 == 0 || style&0xc00000 != 0 {
		t.Fatal("panel has a caption or is not a popup")
	}
	exStyle, _, _ := getWindowLongPtr.Call(p.hwnd, ^uintptr(19))
	if exStyle&8 == 0 {
		t.Fatal("input panel is not topmost")
	}
	putPanelText(p.first, "123")
	putPanelText(p.second, "000858")
	sendMessage.Call(p.first, 0x100, 13, 0)
	if p.busy || !strings.Contains(panelText(p.status), "6位") {
		t.Fatal("invalid input not shown inline")
	}
	putPanelText(p.first, "600519")
	sendMessage.Call(p.first, 0x100, 13, 0)
	awaitPanelResult(t, u)
	if u.customPanel != nil || len(config.LoadFrom(path).CustomPairs) != 1 {
		t.Fatal("Enter did not save and close")
	}
	u.openCustomPanel(false)
	oldEpoch := u.customEpoch
	u.customPanel.focusEstablished = true
	sendMessage.Call(u.customPanel.hwnd, 6, 0, 0) // actual WM_ACTIVATE inactive path
	if u.customPanel != nil {
		t.Fatal("outside activation did not close")
	}
	u.openCustomPanel(false)
	late, _ := watchlist.NewPair("300750", "920002")
	u.customResults <- pairValidation{epoch: oldEpoch, pair: late}
	u.handleCustomResults()
	if len(u.cfg.CustomPairs) != 1 || u.customPanel == nil {
		t.Fatal("late result altered a reopened panel")
	}
	u.validatePair = func(context.Context, string, string) (watchlist.Pair, error) {
		return watchlist.Pair{}, errors.New("股票不存在")
	}
	putPanelText(u.customPanel.first, "600519")
	putPanelText(u.customPanel.second, "300750")
	sendMessage.Call(u.customPanel.second, 0x100, 13, 0)
	awaitPanelResult(t, u)
	if u.customPanel == nil || !strings.Contains(panelText(u.customPanel.status), "不存在") || u.customPanel.busy {
		t.Fatal("remote validation error not recoverable")
	}
	sendMessage.Call(u.customPanel.second, 0x100, 27, 0)
	if u.customPanel != nil {
		t.Fatal("Escape did not close")
	}
	u.openCustomPanel(false)
	p = u.customPanel
	outside := point{-30000, -30000}
	p.outsideClick(outside, true)
	if u.customPanel == nil {
		t.Fatal("opening mouse click closed the panel")
	}
	p.outsideClick(outside, false)
	p.outsideClick(outside, true)
	if u.customPanel != nil {
		t.Fatal("clicking a non-activating outside window did not close panel")
	}
	u.openCustomPanel(false)
	u.validatePair = func(_ context.Context, a, b string) (watchlist.Pair, error) { return watchlist.NewPair(a, b) }
	u.saveSettings = func(config.Config) error { return errors.New("disk full") }
	putPanelText(u.customPanel.first, "300750")
	putPanelText(u.customPanel.second, "920002")
	sendMessage.Call(u.customPanel.first, 0x100, 13, 0)
	awaitPanelResult(t, u)
	if u.customPanel == nil || !strings.Contains(panelText(u.customPanel.status), "保存组合失败") || len(u.cfg.CustomPairs) != 1 {
		t.Fatal("save failure did not preserve the panel and original list")
	}
}
