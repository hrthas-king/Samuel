package market

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestMenuBatchesPinProvidersBoundSizeAndIsolateFailures(t *testing.T) {
	before := ActiveGroup().ID
	groupMu.RLock()
	previousRank := topUSCompanies
	groupMu.RUnlock()
	t.Cleanup(func() {
		groupMu.Lock()
		topUSCompanies = previousRank
		if activeGroup.ID == GroupTop2US {
			activeGroup = groupByIDLocked(GroupTop2US)
			Symbols = append([]string(nil), activeGroup.Symbols...)
		}
		groupMu.Unlock()
	})
	SetTopUSCompanies([2]string{"AAPL", "NVDA"})
	symbols := MenuSymbols()
	if !slices.Contains(symbols, "AAPL") || !slices.Contains(symbols, "NVDA") {
		t.Fatalf("menu quote request missed the ranked symbols: %v", symbols)
	}
	yahooCalls, tencentCalls := 0, 0
	yahoo := func(_ context.Context, symbols []string) (map[string]SymbolResult, error) {
		yahooCalls++
		if len(symbols) > 8 {
			t.Fatal("menu batch exceeds the response budget")
		}
		for _, symbol := range symbols {
			if !yahooPreferred(symbol) {
				t.Fatal("wrong provider:", symbol)
			}
		}
		return nil, errors.New("Yahoo unavailable")
	}
	tencent := func(_ context.Context, symbols []string) (map[string]SymbolResult, error) {
		tencentCalls++
		if len(symbols) > 8 {
			t.Fatal("menu batch exceeds the response budget")
		}
		result := make(map[string]SymbolResult)
		for _, symbol := range symbols {
			if yahooPreferred(symbol) {
				t.Fatal("failed Yahoo symbol leaked into Tencent:", symbol)
			}
			q := Quote{Symbol: symbol, Price: 101, ChangeAbs: 1, ChangePercent: 1, ChangeBase: 100, UpdateMode: "fixture", State: StateRegular}
			result[symbol] = SymbolResult{Quote: &q}
		}
		return result, nil
	}
	batch, err := fetchMenuBatches(context.Background(), append(symbols, "VOO"), yahoo, tencent)
	if err != nil || yahooCalls < 2 || tencentCalls < 2 || batch["VOO"].Quote != nil || batch["VOO"].Err == "" || batch["000001.SS"].Quote == nil || ActiveGroup().ID != before {
		t.Fatalf("menu fetch changes the main group or loses partial success: %v %d %d", err, yahooCalls, tencentCalls)
	}
}

func TestMenuSessionAvailabilityDoesNotAdvertiseFallbackOrExpiredNight(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 1, 0, time.UTC)
	q := Quote{Symbol: "VOO", Currency: "USD", QuoteType: "ETF", State: StateClosed, MarketState: "PRE", UpdateMode: "yahoo_regular", RegularTradeTime: now.Add(-12 * time.Hour).Unix()}
	v := View{Symbol: "VOO", Quote: &q, Healthy: true}
	if v.SessionAvailable(now) {
		t.Fatal("daily fallback was advertised as a live premarket percentage")
	}
	q.State, q.UpdateMode, q.TradeTime = StateOvernight, "yahoo_overnight", now.Add(-2*time.Second).Unix()
	if v.SessionAvailable(now) {
		t.Fatal("prior night percentage was advertised as current premarket")
	}
	q.State, q.UpdateMode, q.TradeTime = StatePreMarket, "yahoo_pre", now.Unix()
	if !v.SessionAvailable(now) {
		t.Fatal("current premarket quote unavailable")
	}
}

func TestTencentMenuBatchSeparatesBuiltinIndicesETFsAndCustomStocks(t *testing.T) {
	now := time.Date(2026, 10, 28, 2, 0, 1, 0, time.UTC)
	symbols := []string{"159632.SZ", "159655.SZ", "399001.SZ", "600519.SS"}
	body := customRow("sz", "159632", "ETF one", "ETF") + customRow("sz", "159655", "ETF two", "ETF") + customRow("sz", "399001", "Shenzhen component", "ZS") + customRow("sh", "600519", "Stock", "GP-A")
	batch, err := ParseTencentQuoteBatch(symbols, []byte(body), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols {
		if batch[symbol].Quote == nil {
			t.Errorf("%s misclassified in a mixed batch: %s", symbol, batch[symbol].Err)
		}
	}
	if q := batch["399001.SZ"].Quote; q != nil && q.QuoteType != "INDEX" {
		t.Fatal("cash index treated as a stock")
	}
	if q := batch["600519.SS"].Quote; q != nil && q.QuoteType != "EQUITY" {
		t.Fatal("custom stock treated as an index")
	}
}
