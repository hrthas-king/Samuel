//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
)

func TestLiveMenuPercentageBinding(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_MENU_UI") != "1" {
		t.Skip("opt-in complete menu quote binding check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	client := market.NewMenuClient()
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	u := NewUI("v1.0.14", config.Default())
	u.SetMenuQuoteFetcher(ctx, client.Fetch)
	menu, commands := u.buildMenu()
	defer destroyMenu.Call(menu)
	entries := fluentFind(readFluentMenu(menu, commands), "More")
	if len(entries) != len(market.Groups) {
		t.Fatal("menu entries missing")
	}
	for {
		frame := u.menuQuotes.Load()
		complete := frame != nil
		if frame != nil {
			bySymbol := make(map[string]market.View)
			for _, view := range frame.views {
				bySymbol[view.Symbol] = view
			}
			for _, group := range market.Groups {
				cells := u.groupPercents(group.ID, time.Now())
				for i, symbol := range group.Symbols {
					view := bySymbol[symbol]
					if view.Quote == nil || !view.SessionAvailable(time.Now()) {
						complete = false
						continue
					}
					if cells[i].text != fmt.Sprintf("%+.2f%%", view.Quote.ChangePercent) {
						t.Fatal("live percentage is swapped or converted twice:", group.ID, cells)
					}
				}
			}
		}
		if complete {
			t.Logf("all %d menu groups bound to valid provider percentages", len(market.Groups))
			return
		}
		if ctx.Err() != nil {
			t.Fatal("menu binding did not receive all groups:", ctx.Err())
		}
		if !u.menuBusy.Load() {
			u.requestMenuQuotes()
		}
		time.Sleep(50 * time.Millisecond)
	}
}
