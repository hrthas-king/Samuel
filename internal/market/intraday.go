package market

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
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

// IntradayQuery identifies one instrument and trading window. The quote's
// poll time and changing price are deliberately absent from the cache key.
type IntradayQuery struct {
	symbol, tencentCode string
	start, end          int64
	extended            bool
}

func IntradayFor(q Quote) (IntradayQuery, bool) {
	at := QuoteMarketTime(q)
	if at.IsZero() || q.Symbol == "" {
		return IntradayQuery{}, false
	}
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	query := IntradayQuery{symbol: q.Symbol, start: day.Unix(), end: day.AddDate(0, 0, 1).Unix()}
	if strings.HasPrefix(q.UpdateMode, "yahoo_") {
		switch q.UpdateMode {
		case "yahoo_overnight":
			// Yahoo's public v8 series covers pre, regular and post, not the
			// separately sourced Blue Ocean overnight quote.
			return IntradayQuery{}, false
		case "yahoo_pre", "yahoo_post":
			w := yahooUSWindow(at, q.MarketState, q.RegularTradeTime)
			if w.mode != q.UpdateMode {
				return IntradayQuery{}, false
			}
			query.start, query.end, query.extended = w.start.Unix(), w.end.Unix(), true
		}
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
	return stamp >= q.start && stamp < q.end && stamp <= now
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
	var response struct {
		Code int `json:"code"`
		Data map[string]struct {
			Minute struct {
				Date string   `json:"date"`
				Rows []string `json:"data"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	minute := response.Data[q.tencentCode].Minute
	if response.Code != 0 || minute.Date != time.Unix(q.start, 0).In(chinaMarketZone).Format("20060102") {
		return nil, nil
	}
	points := make([]IntradayPoint, 0, len(minute.Rows))
	for _, row := range minute.Rows {
		fields := strings.Fields(row)
		if len(fields) < 2 || len(fields[0]) != 4 {
			continue
		}
		at, timeErr := time.ParseInLocation("200601021504", minute.Date+fields[0], chinaMarketZone)
		price, priceErr := strconv.ParseFloat(fields[1], 64)
		if timeErr == nil && priceErr == nil && q.includes(at.Unix(), now.Unix()) && positiveFinite(price) {
			points = append(points, IntradayPoint{at.Unix(), price})
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
	if n < 2 {
		return nil
	}
	return points[:n]
}

// IntradaySamples is a fallback for feeds without a verified public minute
// endpoint. The caller keeps it only while a card is visible.
type IntradaySamples struct {
	symbol, source, mode string
	state                SessionState
	day                  int
	points               []IntradayPoint
}

func (s *IntradaySamples) Add(view View, now time.Time) []IntradayPoint {
	q := view.Quote
	if q == nil || q.Reference || q.State == StateClosed || q.State == "" || q.QuoteSource == "" {
		*s = IntradaySamples{}
		return nil
	}
	at := QuoteMarketTime(*q)
	if at.IsZero() || at.After(now) || !positiveFinite(q.Price) {
		return nil
	}
	day := at
	if q.State == StateOvernight && at.Hour() < 4 {
		day = at.AddDate(0, 0, -1)
	}
	dayKey := day.Year()*1000 + day.YearDay()
	if q.Symbol != s.symbol || q.QuoteSource != s.source || q.UpdateMode != s.mode || q.State != s.state || dayKey != s.day {
		s.symbol, s.source, s.mode, s.state, s.day = q.Symbol, q.QuoteSource, q.UpdateMode, q.State, dayKey
		s.points = s.points[:0]
	}
	n := len(s.points)
	if view.Healthy && (n == 0 || q.TradeTime > s.points[n-1].Time) {
		const limit = 256
		if n == limit {
			copy(s.points, s.points[1:])
			s.points = s.points[:n-1]
		}
		s.points = append(s.points, IntradayPoint{q.TradeTime, q.Price})
	}
	return s.points
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

func (c *IntradayClient) Result(q IntradayQuery) ([]IntradayPoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[q]
	return entry.points, ok && entry.expires.IsZero()
}
