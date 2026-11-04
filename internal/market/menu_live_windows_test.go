//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveMenuQuoteClient(t *testing.T) {
	if os.Getenv("SAMUEL_TEST_MENU_QUOTES") != "1" {
		t.Skip("opt-in menu quote network check")
	}
	client := NewMenuClient()
	defer client.Close()
	beforeGroup, beforeSession, beforeCrumb := ActiveGroup().ID, sessHandle, cachedCrumb
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	batch, err := client.Fetch(ctx, MenuSymbols())
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range MenuSymbols() {
		q := batch[sym].Quote
		if q == nil {
			t.Fatalf("menu quote unavailable: %s: %s", sym, batch[sym].Err)
		}
		if err := ValidateQuote(*q); err != nil {
			t.Fatal(sym, err)
		}
		t.Logf("%s %+.4f%% mode=%s", sym, q.ChangePercent, q.UpdateMode)
	}
	if ActiveGroup().ID != beforeGroup || sessHandle != beforeSession || cachedCrumb != beforeCrumb {
		t.Fatal("menu fetch modified the main pair or its transport")
	}
	if dir := os.Getenv("SAMUEL_MENU_ARTIFACTS"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body, _ := json.MarshalIndent(batch, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "live-menu.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
