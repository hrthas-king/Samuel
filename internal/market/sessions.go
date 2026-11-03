package market

import (
	"strings"
	"time"
)

type yahooSessionWindow struct {
	state       SessionState
	label, mode string
	start, end  time.Time
}

func yahooUSAsset(q Quote) bool {
	return q.Currency == "USD" && (q.QuoteType == "EQUITY" || q.QuoteType == "ETF")
}

func yahooUSWindow(now time.Time, raw string, regularAt int64) yahooSessionWindow {
	local := now.In(newYorkZone)
	at := func(hour, minute int) time.Time {
		return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, newYorkZone)
	}
	if start, end, active := yahooNightWindow(now); active {
		return yahooSessionWindow{StateOvernight, "夜盘", "yahoo_overnight", start, end}
	}
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || local.Hour() >= 20 {
		return yahooSessionWindow{state: StateClosed, label: "休市"}
	}
	if strings.EqualFold(raw, "CLOSED") || strings.EqualFold(raw, "POSTPOST") {
		return yahooSessionWindow{state: StateClosed, label: "休市"}
	}
	if local.Before(at(9, 30)) {
		return yahooSessionWindow{StatePreMarket, "盘前", "yahoo_pre", at(4, 0), at(9, 30)}
	}
	// On a shortened trading day the provider's POST state plus a same-day
	// 13:00 closing tick establishes the earlier after-hours boundary.
	regular := time.Unix(regularAt, 0).In(newYorkZone)
	earlyClose := strings.EqualFold(raw, "POST") && regular.Year() == local.Year() && regular.YearDay() == local.YearDay() && regular.Hour() == 13 && regular.Minute() < 2 && !local.Before(regular)
	if earlyClose {
		if !local.Before(at(17, 0)) {
			return yahooSessionWindow{state: StateClosed, label: "提前收盘日盘后已结束"}
		}
		return yahooSessionWindow{StatePostMarket, "盘后", "yahoo_post", at(13, 0), at(17, 0)}
	}
	if local.Before(at(16, 0)) {
		return yahooSessionWindow{StateRegular, "盘中", "yahoo_regular", at(9, 30), at(16, 0)}
	}
	return yahooSessionWindow{StatePostMarket, "盘后", "yahoo_post", at(16, 0), at(20, 0)}
}

func (w yahooSessionWindow) contains(at int64, now time.Time) bool {
	tick := time.Unix(at, 0)
	return at > 0 && !tick.Before(w.start) && tick.Before(w.end) && !tick.After(now.Add(30*time.Second))
}

func applyYahooSessionQuote(q *Quote, item yahooQuoteItem, now time.Time) bool {
	if !yahooUSAsset(*q) {
		return false
	}
	w := yahooUSWindow(now, q.MarketState, q.RegularTradeTime)
	var price, change, percent *float64
	var stamp *int64
	switch w.state {
	case StatePreMarket:
		price, change, percent, stamp = item.PreMarketPrice, item.PreMarketChange, item.PreMarketChangePercent, item.PreMarketTime
	case StatePostMarket:
		price, change, percent, stamp = item.PostMarketPrice, item.PostMarketChange, item.PostMarketChangePercent, item.PostMarketTime
	case StateOvernight:
		price, change, percent, stamp = item.OvernightMarketPrice, item.OvernightMarketChange, item.OvernightMarketChangePercent, item.OvernightMarketTime
	default:
		return false
	}
	if price == nil || change == nil || percent == nil || stamp == nil || !positiveFinite(q.RegularPrice) || q.RegularTradeTime <= 0 || !w.contains(*stamp, now) || *stamp < q.RegularTradeTime || (*stamp == q.RegularTradeTime && w.state != StatePostMarket) {
		return false
	}
	if w.state == StatePostMarket {
		regular := time.Unix(q.RegularTradeTime, 0).In(newYorkZone)
		local := now.In(newYorkZone)
		if regular.Year() != local.Year() || regular.YearDay() != local.YearDay() {
			return false
		}
	}
	candidate := *q
	candidate.Price, candidate.ChangeAbs, candidate.ChangePercent = *price, *change, *percent
	candidate.TradeTime, candidate.ChangeBase = *stamp, q.RegularPrice
	candidate.State, candidate.UpdateMode = w.state, w.mode
	candidate.ChangeBasis = "Yahoo最近常规收盘（" + w.label + "涨跌基准）"
	candidate.Session = w.label + "；显示Yahoo" + w.label + "报价"
	if err := ValidateQuote(candidate); err != nil {
		return false
	}
	*q = candidate
	return true
}

func yahooSessionFallback(q *Quote, now time.Time) {
	if yahooUSAsset(*q) {
		w := yahooUSWindow(now, q.MarketState, q.RegularTradeTime)
		q.State = StateClosed
		switch w.state {
		case StatePreMarket, StatePostMarket, StateOvernight:
			q.Session = w.label + "数据不可用；显示Yahoo常规收盘"
			q.QuoteSource = "Yahoo Finance（常规收盘）"
		case StateRegular:
			if q.MarketState != "CLOSED" && w.contains(q.TradeTime, now) {
				q.State, q.Session = StateRegular, "盘中；显示Yahoo常规报价"
			} else {
				q.Session = "盘中报价未更新；显示Yahoo最近常规报价"
			}
		default:
			q.Session = "休市；显示Yahoo最近常规报价"
		}
		return
	}
	if q.QuoteType == "INDEX" {
		// Cash-index prices are not their futures, ETFs or overnight proxies.
		if q.State != StateRegular || q.TradeTime <= 0 {
			q.State, q.Session = StateClosed, "休市或报价未更新；显示Yahoo最近指数报价"
		}
		if q.Currency == "USD" {
			w := yahooUSWindow(now, q.MarketState, q.RegularTradeTime)
			if w.state != StateRegular || !w.contains(q.TradeTime, now) || q.MarketState != "REGULAR" {
				q.State, q.Session = StateClosed, "休市或报价未更新；显示Yahoo最近指数报价"
			}
		}
	}
}

func yahooCachedSessionExpired(q Quote, now time.Time) bool {
	if !yahooUSAsset(q) || q.State == StateClosed || !strings.HasPrefix(q.UpdateMode, "yahoo_") {
		return false
	}
	w := yahooUSWindow(now, q.MarketState, q.RegularTradeTime)
	return w.mode != q.UpdateMode || !w.contains(q.TradeTime, now)
}
