package market

import (
	"encoding/json"
	"math"
	"time"
)

// RegularClose is the return from the preceding regular close to the latest
// verified completed regular close, with its exchange-local session date.
type RegularClose struct {
	Date          time.Time
	ChangePercent float64
}

func completedCloseReturn(closes map[time.Time]float64) (RegularClose, bool) {
	var last, prev time.Time
	for day := range closes {
		if day.After(last) {
			prev, last = last, day
		} else if day.After(prev) && day.Before(last) {
			prev = day
		}
	}
	closePrice, previousClose := closes[last], closes[prev]
	if !positiveFinite(closePrice) || !positiveFinite(previousClose) {
		return RegularClose{}, false
	}
	change := (closePrice - previousClose) / previousClose * 100
	if math.IsNaN(change) || math.IsInf(change, 0) {
		return RegularClose{}, false
	}
	return RegularClose{Date: last, ChangePercent: change}, true
}

func parseYahooRegularClose(q Quote, body []byte, now time.Time) (RegularClose, bool) {
	var response struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol, Currency, InstrumentType, DataGranularity string
					RegularMarketTime                                 int64
					CurrentTradingPeriod                              struct {
						Regular struct{ Start, End int64 }
					}
				}
				Timestamp  []int64
				Indicators struct {
					Quote []struct{ Close []*float64 }
				}
			}
		}
	}
	if json.Unmarshal(body, &response) != nil || len(response.Chart.Result) != 1 {
		return RegularClose{}, false
	}
	r := response.Chart.Result[0]
	m := r.Meta
	if normalizeSymbol(m.Symbol) != q.Symbol || m.Currency != q.Currency || m.InstrumentType != q.QuoteType ||
		m.DataGranularity != "1d" || len(r.Indicators.Quote) != 1 {
		return RegularClose{}, false
	}

	q.TradeTime = now.Unix()
	zone := QuoteMarketTime(q).Location()
	today := marketDate(now.In(zone))
	through := today.AddDate(0, 0, -1)
	period := m.CurrentTradingPeriod.Regular
	stamp := func(at int64) time.Time { return time.Unix(at, 0).In(zone) }
	// The current daily candle is partial until the provider's session
	// end has passed and its regular quote has caught up to that end.
	if period.Start > 0 && period.Start < period.End && period.End <= m.RegularMarketTime &&
		m.RegularMarketTime <= now.Unix() &&
		marketDate(stamp(period.Start)).Equal(today) && marketDate(stamp(period.End)).Equal(today) &&
		marketDate(stamp(m.RegularMarketTime)).Equal(today) {
		through = today
	}
	values := r.Indicators.Quote[0].Close
	closes := make(map[time.Time]float64, len(r.Timestamp))
	for i, at := range r.Timestamp {
		day := marketDate(stamp(at))
		if at <= 0 || at > now.Unix() || day.After(through) || day.Before(today.AddDate(0, 0, -14)) {
			continue
		}
		price := 0.0
		if i < len(values) && values[i] != nil && positiveFinite(*values[i]) {
			price = *values[i]
		}
		// Preserve a missing or conflicting day. Skipping it could turn
		// a multiple-session move into a single-session percentage.
		if existing, seen := closes[day]; seen && existing != price {
			price = 0
		}
		closes[day] = price
	}
	return completedCloseReturn(closes)
}
