package market

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestYahooMainQuoteKeepsRegularPriceAndDailyChangeDuringPremarket(t *testing.T) {
	body := []byte(`{"quoteResponse":{"result":[{"symbol":"NVDA","currency":"USD","quoteType":"EQUITY","marketState":"PRE","regularMarketPrice":204.64,"regularMarketPreviousClose":213.66,"regularMarketChange":-9.020004,"regularMarketChangePercent":-4.2216625,"regularMarketTime":1793390401,"preMarketPrice":206.15,"preMarketChange":1.5099945,"preMarketChangePercent":0.7378785,"preMarketTime":1793626197}]}}`)
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"NVDA"}, body, 10, time.Date(2026, 11, 2, 14, 20, 0, 0, time.UTC))
	q := batch["NVDA"].Quote
	if err != nil || q == nil || q.Price != 204.64 || math.Abs(q.ChangePercent-(-4.2216625)) > 1e-7 || math.Abs(q.Previous()-213.66) > .001 {
		t.Fatalf("Yahoo main line mixed with premarket: %v %#v", err, q)
	}
}

func TestYahooRegularChartDoesNotRebaseFullDayOntoCurrentClose(t *testing.T) {
	body := []byte(`{"chart":{"result":[{"meta":{"symbol":"VOO","currency":"USD","instrumentType":"ETF","marketState":"POST","regularMarketPrice":698.58,"regularMarketChangePercent":-0.529688,"regularMarketTime":1792785600,"previousClose":702.30,"chartPreviousClose":702.30,"fulldayPrice":699.16,"fulldayChange":-3.14,"fulldayChangePercent":-0.447}}]}}`)
	q, err := ParseYahooChart("VOO", body, 10, time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC))
	if err != nil || q.Price != 698.58 || math.Abs(q.ChangeAbs-(-3.72)) > .001 || q.ChangePercent >= 0 {
		t.Fatalf("full-day or post-market rebase survived: %v %#v", err, q)
	}
}

func TestYahooChartPrefersRegularPreviousCloseOverChartRangeStart(t *testing.T) {
	body := []byte(`{"chart":{"result":[{"meta":{"symbol":"AAPL","currency":"USD","instrumentType":"EQUITY","marketState":"POST","regularMarketPrice":110,"previousClose":100,"chartPreviousClose":110,"regularMarketChangePercent":10,"fulldayPrice":112,"fulldayChange":2,"fulldayChangePercent":1.81818}}]}}`)
	q, err := ParseYahooChart("AAPL", body, 10, time.Now())
	if err != nil || q.Price != 110 || q.Previous() != 100 || q.ChangeAbs != 10 || q.ChangePercent != 10 {
		t.Fatalf("chart-range start replaced true headline previous close: %v %#v", err, q)
	}
}

func TestYahooHeadlineCanDeriveMissingChangesFromAuthoritativePreviousClose(t *testing.T) {
	body := []byte(`{"quoteResponse":{"result":[{"symbol":"AAPL","currency":"USD","quoteType":"EQUITY","marketState":"REGULAR","regularMarketPrice":110,"regularMarketPreviousClose":100}]}}`)
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, body, 10, time.Now())
	q := batch["AAPL"].Quote
	if err != nil || q == nil || q.ChangeAbs != 10 || q.ChangePercent != 10 {
		t.Fatalf("missing change fields prevented valid regular quote: %v %#v", err, q)
	}
}

func TestMarketSourcePinningForAllBuiltinGroupsAndNoCrossSourceFallback(t *testing.T) {
	for _, group := range Groups {
		calls := []string{}
		yahoo := func(context.Context, []string) (map[string]SymbolResult, error) {
			calls = append(calls, "Yahoo")
			return nil, errors.New("Yahoo unavailable")
		}
		tencent := func(context.Context, []string) (map[string]SymbolResult, error) {
			calls = append(calls, "Tencent")
			return nil, errors.New("Tencent unavailable")
		}
		_, _ = fetchPinnedQuotes(context.Background(), group.Symbols, yahoo, tencent)
		want := "Tencent"
		if group.ID == GroupVOOQQQ || group.ID == GroupSOXXSMH || group.ID == GroupTop2US || group.ID == GroupUSIndices || group.ID == GroupEUIndices {
			want = "Yahoo"
		}
		if len(calls) != 1 || calls[0] != want {
			t.Errorf("%s: requested %s, got %v", group.ID, want, calls)
		}
	}
}

func TestQuoteBasisIsVisibleAndNotAnInferredDifferentSession(t *testing.T) {
	body := []byte(`{"quoteResponse":{"result":[{"symbol":"NVDA","currency":"USD","quoteType":"EQUITY","marketState":"PRE","regularMarketPrice":204.64,"regularMarketPreviousClose":213.66,"regularMarketChange":-9.02,"regularMarketChangePercent":-4.22166}]}}`)
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"NVDA"}, body, 10, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tip := (View{Symbol: "NVDA", Quote: batch["NVDA"].Quote, Healthy: true}).Tooltip()
	if !strings.Contains(tip, "涨跌基准") || !strings.Contains(tip, "常规主报价") {
		t.Fatal("price/change convention not visible:", tip)
	}
}
