package market

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestYahooIntradayFiltersPointsAndRetainsTradingDay(t *testing.T) {
	now := time.Date(2026, 11, 5, 16, 0, 0, 0, newYorkZone)
	q := Quote{Symbol: "VOO", Currency: "USD", UpdateMode: "yahoo_regular", TradeTime: now.Unix()}
	query, ok := IntradayFor(q)
	if !ok {
		t.Fatal("Yahoo regular quote did not resolve")
	}
	first, last := now.Add(-3*time.Hour).Unix(), now.Add(-time.Minute).Unix()
	body := fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"VOO"},"timestamp":[%d,%d,%d,%d,%d,%d,%d,%d,%d,null,%d],"indicators":{"quote":[{"close":[649,655,null,"NaN","Infinity",1e999,800,410,647,999,0]}]}}]}}`,
		last, first, last-600, last-601, last-602, last-603, now.Unix()+60, query.start-60, last, first+10)
	want := []IntradayPoint{{first, 655}, {last, 647}}
	points, err := parseYahooIntraday(query, body, now)
	if err != nil || !slices.Equal(points, want) {
		t.Fatalf("points=%+v err=%v", points, err)
	}
	if points, _ := parseYahooIntraday(IntradayQuery{symbol: "QQQ", start: query.start, end: query.end}, body, now); len(points) != 0 {
		t.Fatal("accepted a different instrument")
	}
	if points, _ := parseYahooIntraday(query, []byte(`{"chart":{"result":[{"meta":{"symbol":"VOO"},"timestamp":[null,1],"indicators":{"quote":[{"close":[]}]}}]}}`), now); len(points) != 0 {
		t.Fatal("missing or unaligned arrays produced points")
	}
}

func TestIntradayQuoteSessionAndProviderRouting(t *testing.T) {
	// Early close: a verified 13:00 close starts this day's post-market.
	after := time.Date(2026, 11, 27, 13, 12, 0, 0, newYorkZone)
	q := Quote{Symbol: "QQQ", Currency: "USD", QuoteType: "ETF", UpdateMode: "yahoo_post", MarketState: "POST",
		TradeTime: after.Unix(), RegularTradeTime: after.Add(-12 * time.Minute).Unix()}
	query, ok := IntradayFor(q)
	if !ok || !query.extended || query.start != q.RegularTradeTime || query.end != after.Add(3*time.Hour+48*time.Minute).Unix() {
		t.Fatalf("early-close after-hours window = %+v, ok=%v", query, ok)
	}
	body := fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"QQQ"},"timestamp":[%d,%d,%d],"indicators":{"quote":[{"close":[700,695,694]}]}}]}}`,
		query.start-60, query.start+60, after.Unix())
	if points, err := parseYahooIntraday(query, body, after); err != nil || len(points) != 2 || points[0].Price != 695 {
		t.Fatalf("regular points leaked into the post-market chart: %v %v", points, err)
	}
	for _, mode := range []string{"yahoo_overnight", "cnbc_realtime", "commodity_realtime"} {
		q.UpdateMode = mode
		if _, ok := IntradayFor(q); ok {
			t.Fatalf("%s must degrade to the verified quote", mode)
		}
	}
	q = Quote{Symbol: "^HSI", Currency: "HKD", QuoteSource: "Tencent Real Time", TradeTime: after.Unix()}
	if query, ok := IntradayFor(q); !ok || query.tencentCode != "hkHSI" {
		t.Fatalf("incorrect Hong Kong minute code: %+v", query)
	}
}

func TestTencentIntradayMinuteRows(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 35, 0, 0, chinaMarketZone)
	q := Quote{Symbol: "159632.SZ", Currency: "CNY", QuoteSource: "Tencent Finance", TradeTime: now.Unix()}
	query, ok := IntradayFor(q)
	if !ok {
		t.Fatal("Tencent ETF not routed")
	}
	body := []byte(`{"code":0,"data":{"sz159632":{"data":{"date":"20261106","data":["0933 2.424 50","0931 2.422 5","0933 2.425 60","0934 NaN","0934 +Inf","0934 0","2561 3","0936 4","202611050932 20",null,"0932"]}}}}`)
	want := []IntradayPoint{{now.Add(-4 * time.Minute).Unix(), 2.422}, {now.Add(-2 * time.Minute).Unix(), 2.425}}
	points, err := parseTencentIntraday(query, body, now)
	if err != nil || !slices.Equal(points, want) {
		t.Fatalf("points=%+v err=%v", points, err)
	}
	query.start -= 86400
	if points, _ := parseTencentIntraday(query, body, now); len(points) != 0 {
		t.Fatal("accepted a mismatched exchange date")
	}
}

func TestIntradaySamplesUseNewVerifiedTicks(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, eastAsiaZone)
	q := Quote{Symbol: "^N225", Price: 63000, QuoteSource: "Tencent Finance", UpdateMode: "global_index_quote", State: StateRegular}
	view := View{Quote: &q, Healthy: true}
	var samples IntradaySamples
	if got := samples.Add(view, now); len(got) != 0 {
		t.Fatal("a poll time must not create a chart point")
	}
	q.TradeTime = now.Unix()
	if got := samples.Add(view, now); len(got) != 1 {
		t.Fatalf("first quote: %v", got)
	}
	q.Price++
	if got := samples.Add(view, now); len(got) != 1 || got[0].Price != 63000 {
		t.Fatalf("duplicate source timestamp: %v", got)
	}
	q.TradeTime++
	if got := samples.Add(view, now.Add(time.Second)); len(got) != 2 || got[1].Price != 63001 {
		t.Fatalf("a new verified quote did not extend the chart: %v", got)
	}
	next := now.AddDate(0, 0, 1)
	q.TradeTime = next.Unix()
	if got := samples.Add(view, next); len(got) != 1 || got[0].Time != next.Unix() {
		t.Fatalf("samples mixed trading days: %v", got)
	}
	q.State = StateClosed
	if got := samples.Add(view, next); len(got) != 0 {
		t.Fatalf("closed instrument retained a misleading chart: %v", got)
	}
}

func TestIntradayCacheIsLazyDeduplicatedAndScoped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	results := make(chan struct{}, 4)
	release := make(chan struct{})
	want := []IntradayPoint{{1, 5}, {2, 6}}
	client := IntradayClient{ctx: ctx, notify: func() { results <- struct{}{} },
		fetch: func(ctx context.Context, _ IntradayQuery) []IntradayPoint {
			calls.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return want
		}}
	if calls.Load() != 0 || client.entries != nil {
		t.Fatal("inactive chart allocated a cache or started a request")
	}
	q := IntradayQuery{symbol: "VOO", start: 1, end: 10}
	for range 20 {
		if data, loading := client.Request(q); !loading || data != nil {
			t.Fatal("same request did not join the flight")
		}
	}
	close(release)
	await := func() {
		t.Helper()
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("chart request never completed")
		}
	}
	await()
	if data, loading := client.Request(q); loading || !slices.Equal(data, want) || calls.Load() != 1 {
		t.Fatalf("cached request: data=%v loading=%v calls=%d", data, loading, calls.Load())
	}
	newSession := IntradayQuery{symbol: q.symbol, start: 10, end: 20}
	if _, loading := client.Request(newSession); !loading {
		t.Fatal("a new session must not reuse old points")
	}
	await()
	client.mu.Lock()
	client.entries[q] = intradayEntry{want, time.Now().Add(-time.Second)}
	client.mu.Unlock()
	if _, loading := client.Request(q); !loading {
		t.Fatal("expired points incorrectly returned")
	}
	await()
	if calls.Load() != 3 {
		t.Fatalf("fetch count %d, want 3", calls.Load())
	}
}
