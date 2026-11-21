//go:build darwin

package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTradingSessionCalendarForVolatility(t *testing.T) {
	dates := tradingDates("2024-01-01", 300)
	data := Dataset{"^GSPC": {}, "^VIX": {}, "^VIX3M": {}}
	for i, d := range dates {
		data["^VIX"][d] = number(20 + float64(i)/100)
		if d == "2024-07-04" {
			continue
		} // VIX-only publication on a cash holiday
		data["^GSPC"][d] = number(5000)
		data["^VIX3M"][d] = number(22)
	}
	m := marketIndicators(data, dates[len(dates)-1])
	near(t, m.Measures["vix_vix3m_ratio"].Percentile, 100, 1e-10)
}
func TestStyleConfigurationAndEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAMUEL_CONFIG", filepath.Join(dir, "config.json"))
	sentinelDir := filepath.Join(dir, "us_market_sentinel")
	if err := os.Mkdir(sentinelDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentinelDir, ".env"), []byte("SAMUEL_SENTINEL_STYLE='right_confirm'\nSAMUEL_LLM_MODEL=offline-model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMUEL_LLM_MODEL", "preferred")
	env, err := settings(sentinelDir)
	if err != nil || env["SAMUEL_LLM_MODEL"] != "preferred" || env["SAMUEL_SENTINEL_STYLE"] != "right_confirm" {
		t.Fatal(env, err)
	}
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if err := Command(ctx, []string{"run", "--style", "bad"}, &stdout, &stderr); err == nil {
		t.Fatal("invalid style")
	}
	if err := Command(ctx, []string{"calibrate", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var c Calibration
	if err := json.Unmarshal(stdout.Bytes(), &c); err != nil || c.Total != 0 || len(c.Groups) != 0 {
		t.Fatal(stdout.String(), err)
	}
	if err := Command(ctx, []string{"--help"}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "--force-refresh") {
		t.Fatal(stdout.String(), err)
	}
}
func TestLockHonorsCancellationAndReleases(t *testing.T) {
	store := Store{t.TempDir()}
	release, err := store.lock(context.Background(), Left)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err = store.lock(ctx, Left); err == nil {
		t.Fatal("overlapped snapshots")
	}
	release()
	unlock, err := store.lock(context.Background(), Left)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
func TestBandsAndRenormalizedCoverage(t *testing.T) {
	for _, tc := range []struct {
		temp   int
		signal string
	}{{0, "strong_buy_dca"}, {20, "strong_buy_dca"}, {21, "accumulate"}, {40, "accumulate"}, {41, "hold_normal_dca"}, {60, "hold_normal_dca"}, {61, "cautious"}, {80, "cautious"}, {81, "pause_or_trim"}, {100, "pause_or_trim"}} {
		if b := signalBand(tc.temp); b.signal != tc.signal {
			t.Fatal(tc)
		}
	}
	in := scenario(0)
	delete(in.Macro.Measures, "net_liquidity_chg_12w_billions")
	delete(in.Macro.Measures, "hy_oas_chg_20d_bps")
	s := computeQuantBaseScore(in.Market, in.Macro)
	if s.Confidence != "medium" {
		t.Fatal(s)
	}
	near(t, s.D2, (.35*36+.25*36.5)/.6, 1e-8)
}

func TestStrictCreditBoundaryFromPublishedDecimals(t *testing.T) {
	dates := tradingDates("2025-01-02", 21)
	data := Dataset{"BAMLH0A0HYM2": {}}
	for _, d := range dates {
		data["BAMLH0A0HYM2"][d] = number(4.1)
	}
	lastDay := dates[20]
	data["BAMLH0A0HYM2"][lastDay] = number(4.9)
	m := macroIndicators(data, lastDay)
	near(t, m.Measures["hy_oas_chg_20d_bps"].Level, 80, 0)
	if m.Breaker {
		t.Fatal("an exact 80 bp move tripped the breaker")
	}
	data["BAMLH0A0HYM2"][lastDay] = number(4.91)
	if !macroIndicators(data, lastDay).Breaker {
		t.Fatal("81 bp did not trip the breaker")
	}
}

func TestFallbackSourcesAndNullCatalysts(t *testing.T) {
	now := time.Date(2025, 11, 29, 12, 0, 0, 0, ny)
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "cdn.cboe.com":
			if strings.HasSuffix(r.URL.Path, "SPX_History.csv") {
				return reply(200, "DATE,SPX\n11/28/2025,100"), nil
			}
			return reply(200, "DATE,OPEN,HIGH,LOW,CLOSE\n11/28/2025,18,24,15,20"), nil
		case "api.nasdaq.com":
			if r.URL.Path == "/api/calendar/earnings" {
				return reply(503, ""), nil
			}
			parts := strings.Split(r.URL.Path, "/")
			symbol := parts[3]
			if (symbol == "NDX") != (r.URL.Query().Get("assetclass") == "index") {
				t.Error("wrong Nasdaq asset class", symbol)
			}
			return reply(200, `{"data":{"symbol":"`+symbol+`","tradesTable":{"rows":[{"date":"11/28/2025","close":"$100.00"}]}}}`), nil
		default:
			return reply(503, ""), nil
		}
	})}
	f := Fetcher{Client: client, Now: now, Dir: t.TempDir()}
	data, sources, warnings := f.history(context.Background(), priceSymbols, nil, "2025-01-01")
	if len(warnings) > 0 || len(sources) != 6 {
		t.Fatal(sources, warnings)
	}
	for _, k := range priceSymbols {
		if data[k]["2025-11-28"] == nil {
			t.Fatalf("%s: missing official close", k)
		}
	}
	h, e, w := f.catalysts(context.Background())
	if h != nil || e != nil || len(w) == 0 {
		t.Fatal(h, e, w)
	}
}

func TestBreadthStrictDivergenceBoundary(t *testing.T) {
	dates := tradingDates("2025-01-02", 21)
	data := Dataset{"SPY": {}, "RSP": {}}
	for _, d := range dates {
		data["SPY"][d] = number(100)
		data["RSP"][d] = number(100)
	}
	end := dates[20]
	data["RSP"][end] = number(98.5)
	m := marketIndicators(data, end)
	if m.Divergence == nil || *m.Divergence {
		t.Fatal("exact -1.5% is outside the strict divergence condition")
	}
	data["RSP"][end] = number(98.4)
	if !yes(marketIndicators(data, end).Divergence) {
		t.Fatal("expected divergence")
	}
}
