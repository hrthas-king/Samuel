package market

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestIntradayFixedBeijingHours(t *testing.T) {
	for _, date := range []string{"2026-07-07", "2026-12-07", "2026-12-31"} {
		start, err := time.ParseInLocation("2006-01-02 15:04", date+" 10:00", chinaMarketZone)
		if err != nil {
			t.Fatal(err)
		}
		end := start.Add(23 * time.Hour)
		for _, at := range []time.Time{start, start.Add(14 * time.Hour), end, end.Add(59*time.Minute + 59*time.Second)} {
			q := Quote{Symbol: "VOO", Currency: "USD", UpdateMode: "yahoo_regular", TradeTime: at.Unix()}
			query, ok := IntradayFor(q, BeijingIntradayWindow(at.In(newYorkZone)))
			if !ok || query.Start != start.Unix() || query.End != end.Unix() {
				t.Fatalf("at %s got %+v, want %s through %s", at, query, start, end)
			}
		}
	}
}

func TestBothBeijingWindowBoundariesAndFutureBars(t *testing.T) {
	start := time.Date(2026, 12, 7, 10, 0, 0, 0, chinaMarketZone)
	w := BeijingIntradayWindow(start)
	q := IntradayQuery{symbol: "QQQ", IntradayWindow: w}
	body := fmt.Appendf(nil, `{"chart":{"result":[{"meta":{"symbol":"QQQ"},"timestamp":[%d,%d,%d,%d],"indicators":{"quote":[{"close":[99,100,101,102]}]}}]}}`, w.Start-1, w.Start, w.End, w.End+1)
	if got, err := parseYahooIntraday(q, body, start); err != nil || !slices.Equal(got, []IntradayPoint{{w.Start, 100}}) {
		t.Fatalf("opening trade or future filter: %v %v", got, err)
	}
	if got, err := parseYahooIntraday(q, body, start.Add(24*time.Hour)); err != nil || !slices.Equal(got, []IntradayPoint{{w.Start, 100}, {w.End, 101}}) {
		t.Fatalf("23-hour endpoints: %v %v", got, err)
	}
}

func TestTencentBothDatesInBeijingWindow(t *testing.T) {
	now := time.Date(2026, 12, 8, 9, 45, 0, 0, chinaMarketZone)
	w := BeijingIntradayWindow(now)
	q := IntradayQuery{symbol: "159632.SZ", tencentCode: "sz159632", IntradayWindow: w}
	body := []byte(`{"code":0,"data":{"sz159632":{"data":[{"date":"20261208","data":["0859 3","0900 4","0901 5","0930 6"]},{"date":"20261207","data":["0930 1","1000 2","1500 2.5"]},{"date":"20261206","data":["1500 9"]}]}}}`)
	want := []IntradayPoint{{w.Start, 2}, {w.Start + 5*3600, 2.5}, {w.End - 60, 3}, {w.End, 4}}
	if got, err := parseTencentIntraday(q, body, now); err != nil || !slices.Equal(got, want) {
		t.Fatalf("two dated rows, including the 09:00 break: got %v, error %v", got, err)
	}
}

func TestHistoryMergesSameWindowAndRejectsLateReplies(t *testing.T) {
	now := time.Date(2026, 12, 7, 14, 0, 0, 0, chinaMarketZone)
	w := BeijingIntradayWindow(now)
	q := Quote{Symbol: "QQQ", Currency: "USD", TradeTime: now.Unix(), Price: 102, UpdateMode: "yahoo_overnight", State: StateOvernight, QuoteSource: "Yahoo Finance"}
	view := View{Symbol: q.Symbol, Quote: &q, Healthy: true}
	query, _ := IntradayFor(q, w)
	client := IntradayClient{entries: map[IntradayQuery]intradayEntry{query: {[]IntradayPoint{{w.Start, 99}, {w.Start + 120, 100}}, time.Now().Add(time.Hour)}}}
	var history IntradayHistory
	got := history.Read(view, now, &client)
	if len(got.Points) != 3 || got.Points[2].Time != q.TradeTime || !slices.Equal(got.Breaks, []int{2}) {
		t.Fatalf("extended bars and observed overnight trade: %+v", got)
	}
	client.entries[query] = intradayEntry{}
	if refresh := history.Read(view, now, &client); !refresh.Loading || !slices.Equal(refresh.Points, got.Points) {
		t.Fatalf("chart went blank during refresh: %+v", refresh)
	}
	next := now.AddDate(0, 0, 1)
	nextQuery, _ := IntradayFor(q, BeijingIntradayWindow(next))
	client.entries[nextQuery] = intradayEntry{}
	if fresh := history.Read(view, next, &client); !fresh.Loading || len(fresh.Points) != 0 || fresh.Window.Start == w.Start {
		t.Fatalf("previous day leaked after rollover: %+v", fresh)
	}
}

func TestYahooRequestUsesVisibleRangeAndIncludesExtendedTrades(t *testing.T) {
	q := IntradayQuery{symbol: "^NDX", IntradayWindow: IntradayWindow{100, 1000}}
	for _, c := range []struct{ now, end int64 }{{120, 121}, {1000, 1001}, {1090, 1001}} {
		want := fmt.Sprintf("/v8/finance/chart/%%5ENDX?period1=100&period2=%d&interval=2m&includePrePost=true", c.end)
		if got := yahooIntradayPath(q, time.Unix(c.now, 0)); got != want {
			t.Fatalf("chart request = %q, want %q", got, want)
		}
	}
}
