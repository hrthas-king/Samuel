//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/hrthas-king/Samuel/internal/market"
)

type menuQuoteSnapshot struct {
	views []market.View
	at    time.Time
}

type menuPercent struct {
	text      string
	value     float64
	available bool
}

func (u *UI) SetMenuQuoteFetcher(ctx context.Context, fetch func(context.Context, []string) (map[string]market.SymbolResult, error)) {
	u.menuContext, u.menuFetch = ctx, fetch
}

func (u *UI) requestMenuQuotes() {
	if u.menuFetch == nil || u.menuContext == nil || u.menuContext.Err() != nil || u.menuBusy.Swap(true) {
		return
	}
	symbols := market.MenuSymbols()
	fetch, parent := u.menuFetch, u.menuContext
	go func() {
		defer u.menuBusy.Store(false)
		ctx, cancel := context.WithTimeout(parent, 8*time.Second)
		defer cancel()
		if u.menuTracker == nil {
			u.menuTracker = market.NewTrackerForSymbols(symbols)
		} else {
			u.menuTracker.SetSymbols(symbols)
		}
		batch, err := fetch(ctx, symbols)
		if parent.Err() != nil {
			return
		}
		now := time.Now()
		if err != nil {
			u.menuTracker.FailAll("菜单行情暂不可用")
		} else {
			u.menuTracker.ApplyBatch(batch, now)
		}
		u.menuQuotes.Store(&menuQuoteSnapshot{views: u.menuTracker.Views(now, 5), at: now})
	}()
}

func (u *UI) groupPercents(id string, now time.Time) [2]menuPercent {
	cells := [2]menuPercent{{text: "--"}, {text: "--"}}
	bySymbol := make(map[string]market.View)
	if overview := u.menuQuotes.Load(); overview != nil && now.Sub(overview.at) <= 15*time.Second {
		for _, view := range overview.views {
			bySymbol[view.Symbol] = view
		}
	}
	if current := u.latest.Load(); current != nil {
		for _, view := range current.Quotes {
			// Keep the active row consistent with the transparent ticker.
			if view.Quote != nil {
				bySymbol[view.Symbol] = view
			}
		}
	}
	group := u.menuGroup(id)
	for i, sym := range group.Symbols {
		if i >= len(cells) {
			break
		}
		view := bySymbol[sym]
		if !view.SessionAvailable(now) {
			continue
		}
		value := view.Quote.ChangePercent
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		cells[i] = menuPercent{text: fmt.Sprintf("%+.2f%%", value), value: value, available: true}
	}
	return cells
}

func (u *UI) menuGroup(id string) market.GroupSpec {
	for _, pair := range u.cfg.CustomPairs {
		if pair.ID() == id {
			return market.GroupSpec{ID: id, MenuLabel: pair.Label(), Symbols: pair.Symbols()}
		}
	}
	return market.GroupByID(id)
}

func (u *UI) groupMenuLabel(group market.GroupSpec) string {
	cells := u.groupPercents(group.ID, time.Now())
	return group.MenuLabel + "\t" + cells[0].text + " / " + cells[1].text
}

func (u *UI) menuPercentColor(cell menuPercent, palette fluentPalette) uintptr {
	if !cell.available || cell.value == 0 {
		return palette.muted
	}
	if (cell.value > 0) == u.cfg.UpIsRed {
		if !palette.dark {
			return colorRef(196, 43, 28)
		}
		return colorRef(255, 96, 96)
	}
	if !palette.dark {
		return colorRef(0, 128, 74)
	}
	return colorRef(96, 225, 146)
}
