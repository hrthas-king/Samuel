package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hrthas-king/Samuel/internal/locale"
)

func TestLanguagePersistsAndLegacyConfigDefaultsToChinese(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"market_group":"soxx_smh","interval_seconds":5,"locked":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := LoadFrom(path)
	if c.Language != locale.Chinese || c.MarketGroup != "soxx_smh" || c.IntervalSeconds != 5 || !c.Locked {
		t.Fatal("legacy settings changed:", c)
	}
	c.Language = locale.English
	if err := SaveTo(path, c); err != nil {
		t.Fatal(err)
	}
	if got := LoadFrom(path); got.Language != locale.English || got.MarketGroup != c.MarketGroup || got.IntervalSeconds != 5 || !got.Locked {
		t.Fatal("language did not persist independently:", got)
	}
	c.Language = "invalid"
	if err := SaveTo(path, c); err == nil {
		t.Fatal("invalid language persisted")
	}
	if got := LoadFrom(path); got.Language != locale.English {
		t.Fatal("failed save destroyed previous settings")
	}
}
