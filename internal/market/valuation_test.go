package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func valuationFixture(t *testing.T, code string, now time.Time) []byte {
	t.Helper()
	var pe, pb []map[string]any
	for i := 0; i < 40; i++ {
		date := now.AddDate(0, 0, -(39-i)*7).Format("2006-01-02")
		p, b := float64(i+1), float64(i+1)
		if i == 39 {
			p, b = 20.5, .5
		}
		pe = append(pe, map[string]any{"d": date, "v": p})
		pb = append(pb, map[string]any{"d": date, "v": b})
	}
	pe = append(pe, map[string]any{"d": "2010-01-01", "v": .1}, pe[0])
	body, err := json.Marshal(map[string]any{"index": map[string]string{"code": code, "date": now.Format("2006-01-02")}, "series": map[string]any{"pe_ttm": pe, "pb": pb}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestIndexValuationUsesSameSourceHistoryAndActualWindow(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	v, err := parseIndexValuation("^NDX", valuationFixture(t, "NDX", now), now)
	if err != nil || v.PE == nil || v.PB == nil {
		t.Fatalf("valid valuation missing: %v %#v", err, v)
	}
	if v.PE.Samples != 40 || v.PE.Percentile == nil || *v.PE.Percentile != 52.5 || v.PB.Percentile == nil || *v.PB.Percentile != 2.5 {
		t.Fatalf("wrong percentile, duplicate weighting or window: %#v %#v", v.PE, v.PB)
	}
	q := Quote{QuoteType: "INDEX", Valuation: v}
	if got := q.SecondaryText(); got != "PE 20.50(52.5%)  PB 0.50(2.5%)" {
		t.Fatalf("wrong estimate text: %s", got)
	}
	if tip := q.valuationTooltip(); !strings.Contains(tip, "40个有效样本") || !strings.Contains(tip, v.PE.HistoryFrom) || !strings.Contains(tip, "2026-11-02") {
		t.Fatal("history window or date hidden from tooltip:", tip)
	}
	short := []valuationPoint{{Date: "2026-11-01", Value: &v.PE.Value}, {Date: "2026-11-02", Value: &v.PE.Value}}
	m, err := historicalMetric(short, "2026-11-02")
	if err != nil || m == nil || m.Percentile != nil {
		t.Fatal("too few samples fabricated a percentile")
	}
}

func TestIndexValuationRejectsProxiesWrongIdentityAndBadDates(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	valid := valuationFixture(t, "NDX", now)
	for _, symbol := range []string{"^N225", "^FCHI", "^KS11", "AAPL", "VOO"} {
		if _, err := parseIndexValuation(symbol, valid, now); err == nil {
			t.Errorf("unsupported proxy/asset accepted: %s", symbol)
		}
	}
	if _, err := parseIndexValuation("^GSPC", valid, now); err == nil {
		t.Fatal("Nasdaq data attached to S&P 500")
	}
	for _, age := range []int{-91, 3} {
		body := valuationFixture(t, "NDX", now.AddDate(0, 0, age))
		if _, err := parseIndexValuation("^NDX", body, now); err == nil {
			t.Errorf("stale/future data accepted: %d", age)
		}
	}
	negative := -1.0
	if metric, err := historicalMetric([]valuationPoint{{Date: "2026-11-02", Value: &negative}}, "2026-11-02"); err != nil || metric != nil {
		t.Fatal("negative earnings ratio treated as cheap positive valuation")
	}
	other := 30.0
	if _, err := historicalMetric([]valuationPoint{{Date: "2026-11-02", Value: &negative}, {Date: "2026-11-02", Value: &other}}, "2026-11-02"); err == nil {
		t.Fatal("conflicting duplicate observations accepted")
	}
}

func TestValuationBackgroundFetchDoesNotBlockPricesOrAffectFunds(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	store := valuationStore{fetch: func(context.Context, string) (*IndexValuation, error) {
		calls.Add(1)
		close(started)
		<-release
		return &IndexValuation{PE: &ValuationMetric{Value: 20, Date: "2026-11-02"}}, nil
	}}
	defer close(release)
	index := &Quote{Symbol: "^NDX", QuoteType: "INDEX", Price: 100}
	fund := &Quote{Symbol: "VOO", QuoteType: "ETF", Price: 100}
	batch := map[string]SymbolResult{"^NDX": {Quote: index}, "VOO": {Quote: fund}}
	done := make(chan struct{})
	go func() { store.attach(batch, time.Now()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("historical data blocked the price pipeline")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background request not started")
	}
	store.attach(batch, time.Now())
	if calls.Load() != 1 || index.Price != 100 || index.Valuation != nil || fund.Valuation != nil || batch["^NDX"].Err != "" {
		t.Fatal("background request changed prices, duplicated requests or contaminated ETF")
	}
}

func TestValuationCachePublishesOnlyOnNextQuoteAndBacksOffFailures(t *testing.T) {
	var calls atomic.Int32
	store := valuationStore{fetch: func(context.Context, string) (*IndexValuation, error) {
		calls.Add(1)
		return &IndexValuation{PE: &ValuationMetric{Value: 20, Date: "2026-11-02"}}, nil
	}}
	old := &Quote{Symbol: "^NDX", QuoteType: "INDEX", Price: 100}
	store.attach(map[string]SymbolResult{"^NDX": {Quote: old}}, time.Now())
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		ready := store.entries["^NDX"].value != nil
		store.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache was not updated")
		}
		time.Sleep(time.Millisecond)
	}
	if old.Valuation != nil {
		t.Fatal("published snapshot was mutated asynchronously")
	}
	next := &Quote{Symbol: "^NDX", QuoteType: "INDEX", Price: 101}
	store.attach(map[string]SymbolResult{"^NDX": {Quote: next}}, time.Now())
	if next.Valuation == nil || calls.Load() != 1 {
		t.Fatal("cache not reused or fetched on every price poll")
	}
	failure := valuationStore{entries: make(map[string]valuationEntry), fetch: func(context.Context, string) (*IndexValuation, error) { return nil, errors.New("source down") }}
	failure.refresh("^NDX")
	entry := failure.entries["^NDX"]
	if entry.value != nil || entry.loading || !entry.nextTry.After(time.Now().Add(14*time.Minute)) {
		t.Fatal("failed source was not throttled")
	}
}

func TestIndexDetailsNeverReuseStockMarketCapOrFundAssets(t *testing.T) {
	for _, kind := range []string{"ETF", "MUTUALFUND", "FUTURE"} {
		q := Quote{QuoteType: kind, Capitalization: &Capitalization{USD: 1e12}, Valuation: &IndexValuation{PE: &ValuationMetric{Value: 20}}}
		if q.HasSecondaryLine() || q.SecondaryText() != "" {
			t.Fatalf("extra line on %s", kind)
		}
	}
	q := Quote{QuoteType: "INDEX", Capitalization: &Capitalization{USD: 1e12}}
	if !q.HasSecondaryLine() || q.SecondaryText() != "PE --(--)  PB --(--)" {
		t.Fatal("index market cap substituted for missing valuation")
	}
	zero := 0.0
	q.Valuation = &IndexValuation{PE: &ValuationMetric{Value: 20, Percentile: &zero}}
	if !strings.Contains(q.SecondaryText(), "0%") {
		t.Fatal("valid zero percentile confused with missing data")
	}
}

func TestTencentIndexPEIsCurrentOnlyAndDoesNotInventPBOrHistory(t *testing.T) {
	row := customRow("sh", "000001", "上证指数", "ZS")
	key, payload, _ := strings.Cut(row, "=")
	parts := strings.Split(strings.Trim(payload, `";`), "~")
	parts[30], parts[39], parts[46] = "20261102161100", "16.56", "0.00"
	body := []byte(fmt.Sprintf(`%s="%s";`, key, strings.Join(parts, "~")))
	now := time.Date(2026, 11, 2, 8, 12, 0, 0, time.UTC)
	batch, err := ParseTencentQuoteBatch([]string{"000001.SS"}, body, 10, now)
	q := batch["000001.SS"].Quote
	if err != nil || q == nil || q.Valuation == nil || q.Valuation.PE == nil || q.Valuation.PE.Value != 16.56 || q.Valuation.PB != nil || q.Valuation.PE.Percentile != nil {
		t.Fatalf("current PE/PB fields wrong: %v %#v", err, q)
	}
	if q.Valuation.PE.Date != "2026-11-02" {
		t.Fatal("wrong source date")
	}
}
