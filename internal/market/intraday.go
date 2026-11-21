package market

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed berlin.tzif
var berlinTZ []byte

// Both supported continental European indices use CET/CEST.
var centralEuropeZone = func() *time.Location {
	zone, err := time.LoadLocationFromTZData("Europe/Berlin", berlinTZ)
	if err != nil {
		panic("invalid embedded Berlin time zone: " + err.Error())
	}
	return zone
}()

var (
	chinaMarketZone = time.FixedZone("GMT+8", 8*3600)
	eastAsiaZone    = time.FixedZone("GMT+9", 9*3600)
)

// QuoteMarketTime reports the displayed quote's source timestamp in its
// exchange's time zone, including DST at the instant of the quote.
func QuoteMarketTime(q Quote) time.Time {
	if q.TradeTime <= 0 {
		return time.Time{}
	}
	zone := newYorkZone
	switch q.Symbol {
	case "^FCHI", "^GDAXI":
		zone = centralEuropeZone
	case "^N225", "^KS11":
		zone = eastAsiaZone
	case "BZ=F":
		zone = londonZone
	default:
		if q.Currency == "CNY" || q.Currency == "HKD" {
			zone = chinaMarketZone
		}
	}
	return time.Unix(q.TradeTime, 0).In(zone)
}

type IntradayPoint struct {
	Time  int64
	Price float64
}

// IntradayWindow is the fixed, inclusive time axis shared by every instrument.
type IntradayWindow struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func BeijingTime(stamp int64) time.Time {
	return time.Unix(stamp, 0).In(chinaMarketZone)
}

// Before 10:00, including the 09:00–10:00 break, keep the preceding window.
func BeijingIntradayWindow(now time.Time) IntradayWindow {
	at := now.In(chinaMarketZone)
	start := time.Date(at.Year(), at.Month(), at.Day(), 10, 0, 0, 0, chinaMarketZone)
	if at.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return IntradayWindow{start.Unix(), start.Add(23 * time.Hour).Unix()}
}

func (w IntradayWindow) Contains(stamp int64) bool {
	return w.Start < w.End && stamp >= w.Start && stamp <= w.End
}

func (w IntradayWindow) Fraction(stamp int64) float64 {
	return float64(stamp-w.Start) / float64(w.End-w.Start)
}

// All Yahoo sessions for a symbol share one cache entry. Only timestamped bars
// actually supplied by the provider enter the chart.
type IntradayQuery struct {
	IntradayWindow
	symbol, tencentCode string
}

func IntradayFor(q Quote, window IntradayWindow) (IntradayQuery, bool) {
	if q.Symbol == "" || window.Start >= window.End {
		return IntradayQuery{}, false
	}
	query := IntradayQuery{IntradayWindow: window, symbol: q.Symbol}
	if strings.HasPrefix(q.UpdateMode, "yahoo_") {
		return query, true
	}
	if strings.HasPrefix(q.QuoteSource, "Tencent ") {
		code, ok := TencentQueryForSymbols([]string{q.Symbol})
		if ok && (q.Currency == "CNY" || q.Currency == "HKD") {
			query.tencentCode = strings.TrimPrefix(code, "r_")
			return query, true
		}
	}
	return IntradayQuery{}, false
}

func (q IntradayQuery) includes(stamp, now int64) bool {
	return q.Contains(stamp) && stamp <= now
}

func yahooIntradayPath(q IntradayQuery, now time.Time) string {
	// Yahoo's period2 is exclusive. Include the 09:00 closing tick, but
	// never request bars beyond the current clock.
	return fmt.Sprintf("/v8/finance/chart/%s?period1=%d&period2=%d&interval=2m&includePrePost=true",
		url.PathEscape(q.symbol), q.Start, min(q.End, now.Unix())+1)
}

func parseYahooIntraday(q IntradayQuery, body []byte, now time.Time) ([]IntradayPoint, error) {
	var response struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol string `json:"symbol"`
				} `json:"meta"`
				Timestamp  []json.RawMessage `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []json.RawMessage `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if len(response.Chart.Result) != 1 {
		return nil, nil
	}
	result := response.Chart.Result[0]
	if normalizeSymbol(result.Meta.Symbol) != q.symbol || len(result.Indicators.Quote) != 1 {
		return nil, nil
	}
	closePrices := result.Indicators.Quote[0].Close
	count := min(len(result.Timestamp), len(closePrices))
	points := make([]IntradayPoint, 0, count)
	for i := range count {
		stamp, timeErr := strconv.ParseInt(string(result.Timestamp[i]), 10, 64)
		price, priceErr := strconv.ParseFloat(string(closePrices[i]), 64)
		if timeErr == nil && priceErr == nil && q.includes(stamp, now.Unix()) && positiveFinite(price) {
			points = append(points, IntradayPoint{stamp, price})
		}
	}
	return sortedIntraday(points), nil
}

func parseTencentIntraday(q IntradayQuery, body []byte, now time.Time) ([]IntradayPoint, error) {
	type minutes struct {
		Date string   `json:"date"`
		Rows []string `json:"data"`
	}
	var response struct {
		Code int `json:"code"`
		Data map[string]struct {
			Data json.RawMessage `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	raw := response.Data[q.tencentCode].Data
	if response.Code != 0 || len(raw) == 0 {
		return nil, nil
	}
	var days []minutes
	if raw[0] == '{' { // The single-day fallback uses the same minute rows.
		var day minutes
		if err := json.Unmarshal(raw, &day); err != nil {
			return nil, err
		}
		days = []minutes{day}
	} else if err := json.Unmarshal(raw, &days); err != nil {
		return nil, err
	}
	first, last := BeijingTime(q.Start).Format("20060102"), BeijingTime(q.End).Format("20060102")
	var points []IntradayPoint
	for _, day := range days {
		if day.Date != first && day.Date != last {
			continue
		}
		for _, row := range day.Rows {
			fields := strings.Fields(row)
			if len(fields) < 2 || len(fields[0]) != 4 {
				continue
			}
			at, timeErr := time.ParseInLocation("200601021504", day.Date+fields[0], chinaMarketZone)
			price, priceErr := strconv.ParseFloat(fields[1], 64)
			if timeErr == nil && priceErr == nil && q.includes(at.Unix(), now.Unix()) && positiveFinite(price) {
				points = append(points, IntradayPoint{at.Unix(), price})
			}
		}
	}
	return sortedIntraday(points), nil
}

func sortedIntraday(points []IntradayPoint) []IntradayPoint {
	slices.SortStableFunc(points, func(a, b IntradayPoint) int {
		return cmp.Compare(a.Time, b.Time)
	})
	n := 0
	for _, p := range points {
		if n == 0 || points[n-1].Time != p.Time {
			n++
		}
		points[n-1] = p
	}
	return points[:n]
}

const intradayTTL = 45 * time.Second

type intradayEntry struct {
	points  []IntradayPoint
	expires time.Time // zero while a request for this key is in flight
}

// IntradayClient starts network work only on Request. The returned points are
// immutable. notify runs on a worker and must not access UI state directly.
type IntradayClient struct {
	ctx    context.Context
	notify func()
	fetch  func(context.Context, IntradayQuery) []IntradayPoint

	mu      sync.Mutex
	entries map[IntradayQuery]intradayEntry
}

func (c *IntradayClient) Request(q IntradayQuery) ([]IntradayPoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if !entry.expires.IsZero() && !now.Before(entry.expires) {
			delete(c.entries, key)
		}
	}
	if entry, ok := c.entries[q]; ok {
		return entry.points, entry.expires.IsZero()
	}
	if c.ctx.Err() != nil {
		return nil, false
	}
	if len(c.entries) >= 16 {
		var oldestKey IntradayQuery
		var oldest time.Time
		for key, entry := range c.entries {
			if !entry.expires.IsZero() && (oldest.IsZero() || entry.expires.Before(oldest)) {
				oldestKey, oldest = key, entry.expires
			}
		}
		if oldest.IsZero() {
			return nil, false
		}
		delete(c.entries, oldestKey)
	}
	if c.entries == nil {
		c.entries = make(map[IntradayQuery]intradayEntry)
	}
	c.entries[q] = intradayEntry{}
	go func() {
		points := c.fetch(c.ctx, q)
		expires := time.Now().Add(intradayTTL)
		c.mu.Lock()
		c.entries[q] = intradayEntry{points, expires}
		c.mu.Unlock()
		time.AfterFunc(intradayTTL, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.entries[q].expires == expires {
				delete(c.entries, q)
				if len(c.entries) == 0 {
					c.entries = nil
				}
			}
		})
		if c.ctx.Err() == nil && c.notify != nil {
			c.notify()
		}
	}()
	return nil, true
}
