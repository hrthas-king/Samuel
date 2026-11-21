package market

import "time"

// Break the stroke across missing bars or a market recess. Sources sampled
// at one to five minute intervals can miss a bar without breaking the line.
const IntradayGapSeconds = 15 * 60

type IntradaySeries struct {
	Window  IntradayWindow  `json:"window"`
	Points  []IntradayPoint `json:"points"`
	Breaks  []int           `json:"breaks"` // indices of the first point following a gap
	Loading bool            `json:"loading"`
	Sampled bool            `json:"sampled"`
}

// IntradayHistory lives only for an open card. Fetched bars and observed
// trades share the Beijing window, including across local midnight.
type IntradayHistory struct {
	symbol  string
	window  IntradayWindow
	query   IntradayQuery
	bars    []IntradayPoint
	samples []IntradayPoint
}

func (h *IntradayHistory) Read(view View, now time.Time, client *IntradayClient) IntradaySeries {
	window := BeijingIntradayWindow(now)
	if h.symbol != view.Symbol || h.window != window {
		*h = IntradayHistory{symbol: view.Symbol, window: window}
	}
	out := IntradaySeries{Window: window}
	if q := view.Quote; q != nil && q.Symbol == view.Symbol {
		if query, ok := IntradayFor(*q, window); ok && client != nil {
			if h.query != query {
				h.bars, h.query = nil, query
			}
			// Request coalesces concurrent reads and refreshes after its TTL.
			// Keep the last verified bars while the new request is in flight.
			bars, loading := client.Request(query)
			if len(bars) > 0 {
				h.bars = bars
			}
			out.Loading = loading
		}
		if view.Healthy && q.QuoteSource != "" && !q.Reference && q.State != StateClosed && q.State != "" &&
			window.Contains(q.TradeTime) && q.TradeTime <= now.Unix() && positiveFinite(q.Price) {
			h.observe(IntradayPoint{q.TradeTime, q.Price})
		}
	}
	out.Points, out.Sampled = mergeIntraday(h.bars, h.samples)
	out.Breaks = []int{}
	for i := 1; i < len(out.Points); i++ {
		if out.Points[i].Time-out.Points[i-1].Time > IntradayGapSeconds {
			out.Breaks = append(out.Breaks, i)
		}
	}
	return out
}

func (h *IntradayHistory) observe(p IntradayPoint) {
	n := len(h.samples)
	if n > 0 && p.Time <= h.samples[n-1].Time {
		return
	}
	// Retain the first and most recent real tick per minute. This bounds
	// memory while keeping the full window when a user leaves the card open.
	if n >= 2 && h.samples[n-2].Time/60 == p.Time/60 {
		h.samples[n-1] = p
		return
	}
	h.samples = append(h.samples, p)
}

func mergeIntraday(bars, samples []IntradayPoint) ([]IntradayPoint, bool) {
	if len(samples) == 0 {
		if len(bars) == 0 {
			return []IntradayPoint{}, false // The native app expects a JSON array.
		}
		return bars, false
	}
	if len(bars) == 0 {
		return samples, true
	}
	points := make([]IntradayPoint, 0, len(bars)+len(samples))
	sampled := false
	for len(bars) > 0 && len(samples) > 0 {
		if samples[0].Time < bars[0].Time {
			points = append(points, samples[0])
			samples = samples[1:]
			sampled = true
		} else {
			if samples[0].Time == bars[0].Time {
				samples = samples[1:]
			}
			points = append(points, bars[0])
			bars = bars[1:]
		}
	}
	points = append(points, bars...)
	return append(points, samples...), sampled || len(samples) > 0
}
