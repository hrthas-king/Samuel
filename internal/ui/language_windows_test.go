//go:build windows && (amd64 || arm64)

package ui

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
)

func hasHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func TestLanguageSwitchPersistsWithoutChangingQuotesOrOtherSettings(t *testing.T) {
	cfg := config.Default()
	cfg.UpIsRed = true
	cfg.IntervalSeconds = 5
	u := NewUI("test", cfg)
	snapshot := &app.Snapshot{MarketGroup: cfg.MarketGroup, Quotes: []market.View{menuFixture("VOO", 1.23), menuFixture("QQQ", -.45)}}
	u.latest.Store(snapshot)
	path := filepath.Join(t.TempDir(), "config.json")
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	u.handleAction(ActionLanguageEnglish)
	if u.Language() != locale.English || config.LoadFrom(path).Language != locale.English || u.latest.Load() != snapshot || len(u.Actions) != 0 || len(u.GroupChanges) != 0 {
		t.Fatal("language switch changed market data or was not saved")
	}
	expected := cfg
	expected.Language = locale.English
	if !reflect.DeepEqual(u.cfg, expected) {
		t.Fatal("unrelated settings changed:", u.cfg)
	}
	u.saveSettings = func(config.Config) error { return errors.New("read-only") }
	if err := u.setLanguage(locale.Chinese); err == nil || u.Language() != locale.English || config.LoadFrom(path).Language != locale.English {
		t.Fatal("failed save changed the live language")
	}
	u.saveSettings = func(c config.Config) error { return config.SaveTo(path, c) }
	u.handleAction(ActionLanguageChinese)
	if u.Language() != locale.Chinese {
		t.Fatal("switching back failed")
	}
}

func TestEnglishMenuCaptionsPanelAndChoiceAreLocalized(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cfg := config.Default()
	cfg.Language = locale.English
	u := NewUI("v1.0.15", cfg)
	u.latest.Store(&app.Snapshot{Status: monitor.Online, Geo: ipinfo.Info{Country: "US", IP: "203.0.113.10"}, MarketGroup: cfg.MarketGroup, Quotes: []market.View{menuFixture("VOO", 1.23), menuFixture("QQQ", -.45)}})
	menu, commands := u.buildMenu()
	defer destroyMenu.Call(menu)
	entries := readFluentMenu(menu, commands)
	checkedChinese, checkedEnglish := false, false
	var walk func([]fluentEntry)
	walk = func(es []fluentEntry) {
		for _, e := range es {
			if e.action == ActionLanguageChinese {
				checkedChinese = e.checked
			}
			if e.action == ActionLanguageEnglish {
				checkedEnglish = e.checked
			}
			if e.label != "Language / 语言" && e.label != "简体中文" && hasHan(e.label) {
				t.Errorf("untranslated menu caption: %q", e.label)
			}
			walk(e.children)
		}
	}
	walk(entries)
	if checkedChinese || !checkedEnglish {
		t.Fatal("language menu does not check only English")
	}
	u.openCustomPanel(false)
	defer u.closeCustomPanel()
	if u.customPanel == nil {
		t.Fatal("panel did not open")
	}
	read := func(hwnd uintptr) string {
		var text [1024]uint16
		sendMessage.Call(hwnd, 0xd, 1024, uintptr(unsafe.Pointer(&text[0])))
		return syscall.UTF16ToString(text[:])
	}
	for _, control := range u.customPanel.controls {
		if text := read(control); hasHan(text) {
			t.Error("untranslated panel control:", text)
		}
	}
	u.customPanel.setStatus("请输入两个不同的股票或ETF代码")
	if text := read(u.customPanel.status); text != "Enter two different stock or ETF symbols" {
		t.Fatal("validation status not translated:", text)
	}
	if dir := os.Getenv("SAMUEL_LANGUAGE_ARTIFACTS"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		p := u.customPanel
		p.busy = true
		p.setStatus("例如：513100 + 513500 / AAPL + MSFT\nEnter 添加 · Esc 或点击外部关闭")
		p.busy = false
		for _, dark := range []bool{true, false} {
			p.visual.refresh(fluentColors(dark))
			img := renderFluentBitmap(t, 520, 374, func(dc uintptr) { sendMessage.Call(p.hwnd, 0x317, dc, 4|8|16) })
			name := "input-english-light.png"
			if dark {
				name = "input-english-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			visual := newFluentVisuals(1, fluentColors(dark))
			s := &fluentMenu{u: u, visual: visual}
			if !s.open(entries, point{100, 100}, nil) {
				t.Fatal("menu render failed")
			}
			popup := s.windows[0]
			menuImage := renderFluentBitmap(t, int(popup.width), int(popup.height), popup.draw)
			name = "menu-english-light.png"
			if dark {
				name = "menu-english-dark.png"
			}
			f, err = os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, menuImage)
			f.Close()
			s.close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestEnglishHoverAndDiagnosticsTranslateLabelsAndPreserveValues(t *testing.T) {
	q := market.Quote{Symbol: "VOO", Alias: "VOO", Description: "Vanguard ETF", Price: 101.23, ChangeAbs: 1.23, ChangePercent: 1.23, ChangeBase: 100, RegularPrice: 100, Currency: "USD", QuoteType: "ETF", State: market.StatePreMarket, Session: "盘前；显示Yahoo盘前报价", QuoteSource: "Yahoo Finance", ChangeBasis: "Yahoo最近常规收盘（盘前涨跌基准）", UpdateMode: "yahoo_pre", TradeTime: time.Now().Add(-time.Second).Unix()}
	s := app.Snapshot{Status: monitor.Online, EgressActive: true, Geo: ipinfo.Info{Country: "US", IP: "203.0.113.10"}, MarketGroup: market.GroupVOOQQQ, Quotes: []market.View{{Symbol: "VOO", Quote: &q, Healthy: true}}}
	for _, text := range []string{OverlayTooltipLanguage(s, locale.English), DiagnosticsTextLanguage("test", s, locale.English)} {
		if hasHan(text) {
			t.Errorf("English text contains untranslated labels:\n%s", text)
		}
		if !strings.Contains(text, "101.23") || !strings.Contains(text, "+1.23%") {
			t.Error("localized output changed or lost price/percent:", text)
		}
	}
}

func TestEnglishTrayBudgetKeepsBothETFQuotes(t *testing.T) {
	s := app.Snapshot{Status: monitor.Online, Geo: ipinfo.Info{Country: "US", IP: "2001:0db8:85a3:0000:0000:8a2e:0370:7334"}, MarketGroup: market.GroupVOOQQQ, Quotes: []market.View{menuFixture("VOO", 1.23), menuFixture("QQQ", -.45)}}
	text := TrayTooltipLanguage(s, locale.English)
	if len(utf16.Encode([]rune(text))) > 127 || !strings.Contains(text, "VOO 101.23 +1.23%") || !strings.Contains(text, "QQQ 99.55 -0.45%") || hasHan(text) {
		t.Fatal("English tooltip lost a quote or overflowed:", text)
	}
	if got := QuoteSummaryLanguage(s, locale.English); got != "VOO 101.23 +1.23% | QQQ 99.55 -0.45%" {
		t.Fatal("localization changed numbers:", got)
	}
}

func TestEnglishLocalizationPreservesProviderAndAdapterNames(t *testing.T) {
	q := market.Quote{Symbol: "600547.SS", Alias: "山东黄金", Description: "山东黄金", Price: 10, ChangePercent: 0, State: market.StateRegular}
	s := app.Snapshot{NIC: "黄金隧道", MarketGroup: market.DefaultGroup, Quotes: []market.View{{Symbol: q.Symbol, Quote: &q, Healthy: true}}}
	u := NewUI("test", config.Config{Language: locale.English})
	u.latest.Store(&s)
	for _, text := range []string{OverlayTooltipLanguage(s, locale.English), DiagnosticsTextLanguage("test", s, locale.English), u.tr("行情: 山东黄金 10.00 +0.00%")} {
		if !strings.Contains(text, "山东黄金") || strings.Contains(text, "山东Gold") {
			t.Fatal("provider proper name was partially translated:", text)
		}
	}
	if text := OverlayTooltipLanguage(s, locale.English); !strings.Contains(text, "黄金隧道") {
		t.Fatal("user adapter name changed")
	}
}
