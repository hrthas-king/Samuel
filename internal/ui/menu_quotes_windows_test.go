//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
)

func menuFixture(symbol string, value float64) market.View {
	q := market.Quote{Symbol: symbol, Price: 100 + value, ChangeAbs: value, ChangePercent: value, ChangeBase: 100, State: market.StateRegular, UpdateMode: "fixture"}
	return market.View{Symbol: symbol, Quote: &q, State: market.StateRegular, Healthy: true}
}

func TestMenuPercentsFollowPairOrderActiveQuotesAndColorChoice(t *testing.T) {
	cfg := config.Default()
	cfg.MarketGroup = market.GroupHKIndices
	u := NewUI("test", cfg)
	now := time.Now()
	u.menuQuotes.Store(&menuQuoteSnapshot{at: now, views: []market.View{menuFixture("^HSI", 1), menuFixture("^HSTECH", -2), menuFixture("SOXX", -3.4), menuFixture("SMH", 5.6)}})
	u.latest.Store(&app.Snapshot{MarketGroup: market.GroupHKIndices, Quotes: []market.View{menuFixture("^HSI", 1.23), menuFixture("^HSTECH", -.89)}})
	cells := u.groupPercents(market.GroupHKIndices, now)
	if cells[0].text != "+1.23%" || cells[1].text != "-0.89%" {
		t.Fatal("active menu row disagrees with the ticker or swaps its symbols:", cells)
	}
	cells = u.groupPercents(market.GroupSOXXSMH, now)
	if cells[0].text != "-3.40%" || cells[1].text != "+5.60%" {
		t.Fatal("non-selected pair has no menu percentage:", cells)
	}
	palette := fluentColors(true)
	green, red := u.menuPercentColor(cells[1], palette), u.menuPercentColor(cells[0], palette)
	if green == red || green == palette.muted {
		t.Fatal("rise/fall distinction missing")
	}
	u.cfg.UpIsRed = !u.cfg.UpIsRed
	if u.menuPercentColor(cells[1], palette) != red || u.menuPercentColor(cells[0], palette) != green {
		t.Fatal("configured rise/fall colors are not respected")
	}
	cells = u.groupPercents(market.GroupSOXXSMH, now.Add(16*time.Second))
	if cells[0].text != "--" || cells[1].text != "--" {
		t.Fatal("an old menu cache was presented as fresh")
	}
}

func TestMenuQuoteFetchIsAsynchronousDeduplicatedAndCancelable(t *testing.T) {
	u := NewUI("test", config.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	u.SetMenuQuoteFetcher(ctx, func(ctx context.Context, symbols []string) (map[string]market.SymbolResult, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			v := menuFixture("SOXX", 1.2)
			return map[string]market.SymbolResult{"SOXX": {Quote: v.Quote}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	u.requestMenuQuotes()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("menu fetch not started")
	}
	for i := 0; i < 100; i++ {
		u.requestMenuQuotes()
	}
	u.Publish(app.Snapshot{MarketGroup: market.DefaultGroup})
	if u.latest.Load() == nil || calls.Load() != 1 {
		t.Fatal("a slow menu blocks the ticker or duplicates network work")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for u.menuBusy.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if u.menuQuotes.Load() == nil || u.groupPercents(market.GroupSOXXSMH, time.Now())[0].text != "+1.20%" {
		t.Fatal("completed menu data was not published")
	}
	cancel()
	u.requestMenuQuotes()
	if calls.Load() != 1 {
		t.Fatal("closed application started another menu fetch")
	}
}

func TestFluentPercentageColumnRendersAndUpdatesInBothThemes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, dark := range []bool{true, false} {
		cfg := config.Default()
		u := NewUI("v1.0.14", cfg)
		var views []market.View
		for _, group := range market.Groups {
			views = append(views, menuFixture(group.Symbols[0], 1.25), menuFixture(group.Symbols[1], -.75))
		}
		u.menuQuotes.Store(&menuQuoteSnapshot{at: time.Now(), views: views})
		menu, commands := u.buildMenu()
		entries := fluentFind(readFluentMenu(menu, commands), "More")
		if len(entries) != len(market.Groups) {
			t.Fatal("More menu lost a group or custom trigger")
		}
		visual := newFluentVisuals(1, fluentColors(dark))
		s := &fluentMenu{u: u, visual: visual}
		p := &fluentPopup{menu: s, entries: entries, width: 680, height: int32(len(entries)*34 + 12), hover: 4}
		before := renderFluentBitmap(t, int(p.width), int(p.height), p.draw)
		green, red := 0, 0
		for y := 8; y < int(p.height)-8; y++ {
			for x := int(p.width) - 205; x < int(p.width)-30; x++ {
				pixel := before.NRGBAAt(x, y)
				if int(pixel.G) > int(pixel.R)+20 && int(pixel.G) > int(pixel.B)+15 && pixel.R < 160 {
					green++
				}
				if int(pixel.R) > int(pixel.G)+40 && int(pixel.R) > int(pixel.B)+20 {
					red++
				}
			}
		}
		if green == 0 || red == 0 {
			t.Fatal("native right column did not paint both colors:", green, red)
		}
		first := market.GroupByID(entries[0].groupID).Symbols[0]
		for i, view := range views {
			if view.Symbol == first {
				views[i] = menuFixture(first, 4.56)
			}
		}
		u.menuQuotes.Store(&menuQuoteSnapshot{at: time.Now(), views: views})
		after := renderFluentBitmap(t, int(p.width), int(p.height), p.draw)
		changed := false
		for y := 8; y < 38; y++ {
			for x := int(p.width) - 205; x < int(p.width)-30; x++ {
				if before.NRGBAAt(x, y) != after.NRGBAAt(x, y) {
					changed = true
				}
			}
		}
		if !changed {
			t.Fatal("the open popup's percentage did not update")
		}
		if dir := os.Getenv("SAMUEL_MENU_ARTIFACTS"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			name := "menu-percent-light.png"
			if dark {
				name = "menu-percent-dark.png"
			}
			file, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, before)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		visual.close()
		destroyMenu.Call(menu)
	}
}
