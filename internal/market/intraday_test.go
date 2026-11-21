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
	query, ok := IntradayFor(q, BeijingIntradayWindow(now))
	if !ok {
		t.Fatal("Yahoo regular quote did not resolve")
	}
	first, last := now.Add(-3*time.Hour).Unix(), now.Add(-time.Minute).Unix()
	body := fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"VOO"},"timestamp":[%d,%d,%d,%d,%d,%d,%d,%d,%d,null,%d],"indicators":{"quote":[{"close":[649,655,null,"NaN","Infinity",1e999,800,410,647,999,0]}]}}]}}`,
		last, first, last-600, last-601, last-602, last-603, now.Unix()+60, query.Start-60, last, first+10)
	want := []IntradayPoint{{first, 655}, {last, 647}}
	points, err := parseYahooIntraday(query, body, now)
	if err != nil || !slices.Equal(points, want) {
		t.Fatalf("points=%+v err=%v", points, err)
	}
	if points, _ := parseYahooIntraday(IntradayQuery{symbol: "QQQ", IntradayWindow: query.IntradayWindow}, body, now); len(points) != 0 {
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
	window := BeijingIntradayWindow(after)
	query, ok := IntradayFor(q, window)
	if !ok || query.IntradayWindow != window {
		t.Fatalf("after-hours window = %+v, ok=%v", query, ok)
	}
	body := fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"QQQ"},"timestamp":[%d,%d,%d],"indicators":{"quote":[{"close":[700,695,694]}]}}]}}`,
		window.Start-60, q.RegularTradeTime-60, after.Unix())
	if points, err := parseYahooIntraday(query, body, after); err != nil || len(points) != 2 || points[0].Price != 695 {
		t.Fatalf("regular and post-market bars should be on one axis: %v %v", points, err)
	}
	for _, mode := range []string{"yahoo_regular", "yahoo_pre", "yahoo_overnight"} {
		q.UpdateMode = mode
		if other, ok := IntradayFor(q, window); !ok || other != query {
			t.Fatalf("%s changed the full-window cache key: %+v", mode, other)
		}
	}
	for _, mode := range []string{"cnbc_realtime", "commodity_realtime"} {
		q.UpdateMode = mode
		if _, ok := IntradayFor(q, window); ok {
			t.Fatalf("%s must use verified quote samples", mode)
		}
	}
	q = Quote{Symbol: "^HSI", Currency: "HKD", QuoteSource: "Tencent Real Time", TradeTime: after.Unix()}
	if query, ok := IntradayFor(q, window); !ok || query.tencentCode != "hkHSI" {
		t.Fatalf("incorrect Hong Kong minute code: %+v", query)
	}
}

func TestTencentIntradayMinuteRows(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 35, 0, 0, chinaMarketZone)
	q := Quote{Symbol: "159632.SZ", Currency: "CNY", QuoteSource: "Tencent Finance", TradeTime: now.Unix()}
	query, ok := IntradayFor(q, BeijingIntradayWindow(now))
	if !ok {
		t.Fatal("Tencent ETF not routed")
	}
	body := []byte(`{"code":0,"data":{"sz159632":{"data":{"date":"20261106","data":["1033 2.424 50","1031 2.422 5","1033 2.425 60","1034 NaN","1034 +Inf","1034 0","2561 3","1036 4","202611051032 20",null,"1032"]}}}}`)
	want := []IntradayPoint{{now.Add(-4 * time.Minute).Unix(), 2.422}, {now.Add(-2 * time.Minute).Unix(), 2.425}}
	points, err := parseTencentIntraday(query, body, now)
	if err != nil || !slices.Equal(points, want) {
		t.Fatalf("points=%+v err=%v", points, err)
	}
	query.IntradayWindow = BeijingIntradayWindow(now.AddDate(0, 0, -1))
	if points, _ := parseTencentIntraday(query, body, now); len(points) != 0 {
		t.Fatal("accepted a mismatched exchange date")
	}
}

func TestIntradaySamplesAcrossMidnightBreakAndRollover(t *testing.T) {
	start := time.Date(2026, 12, 31, 10, 0, 0, 0, chinaMarketZone)
	q := Quote{Symbol: "^N225", Price: 63000, QuoteSource: "Tencent Finance", State: StateRegular}
	view := View{Symbol: q.Symbol, Quote: &q, Healthy: true}
	var history IntradayHistory
	read := func(now time.Time, count int) IntradaySeries {
		t.Helper()
		series := history.Read(view, now, nil)
		if len(series.Points) != count || series.Sampled != (count > 0) {
			t.Fatalf("at %s got %+v, want %d verified samples", now, series, count)
		}
		return series
	}
	read(start, 0)
	q.TradeTime = start.Unix()
	read(start, 1)
	q.Price++
	if read(start.Add(time.Second), 1).Points[0].Price != 63000 {
		t.Fatal("a poll with the same timestamp replaced a verified tick")
	}
	q.TradeTime++
	read(start.Add(time.Second), 2)
	midnight := start.Add(14 * time.Hour)
	q.TradeTime, q.State = midnight.Unix(), StatePostMarket
	if got := read(midnight, 3); !slices.Equal(got.Breaks, []int{2}) {
		t.Fatalf("gap across a session change: %+v", got)
	}
	end := start.Add(23 * time.Hour)
	q.TradeTime = end.Unix()
	read(end, 4)
	q.TradeTime++
	read(end.Add(time.Minute), 4)
	q.State = StateClosed
	read(end.Add(59*time.Minute), 4)
	read(start.AddDate(0, 0, 1), 0) // stale quote at the next 10:00
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
	q := IntradayQuery{symbol: "VOO", IntradayWindow: IntradayWindow{1, 10}}
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
	newSession := IntradayQuery{symbol: q.symbol, IntradayWindow: IntradayWindow{10, 20}}
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
