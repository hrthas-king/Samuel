package market

import "time"

func yahooNightETF(symbol string) bool {
	return symbol == "VOO" || symbol == "QQQ"
}

// Yahoo's overnight quote is a separate session, measured from the latest
// regular close. Full-day changes instead include the previous regular day.
func yahooNightWindow(now time.Time) (time.Time, time.Time, bool) {
	local := now.In(newYorkZone)
	weekday, hour := local.Weekday(), local.Hour()
	if weekday == time.Saturday || (weekday == time.Friday && hour >= 4) || (weekday == time.Sunday && hour < 20) || (hour >= 4 && hour < 20) {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), 20, 0, 0, 0, newYorkZone)
	if hour < 4 {
		start = start.AddDate(0, 0, -1)
	}
	endDate := start.AddDate(0, 0, 1)
	end := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 4, 0, 0, 0, newYorkZone)
	return start, end, true
}

func yahooNightFallback(q *Quote, now time.Time) bool {
	if !yahooNightETF(q.Symbol) || q.Currency != "USD" || q.QuoteType != "ETF" {
		return false
	}
	if _, _, active := yahooNightWindow(now); active {
		q.State = StateClosed
		q.Session = "夜盘数据不可用；显示Yahoo常规收盘"
		q.QuoteSource = "Yahoo Finance（常规收盘）"
		return true
	}
	// A cached OVERNIGHT flag must not keep the moon lit after 04:00 or
	// through the weekend. Resolve this pair using the embedded DST rules.
	if q.State == StateOvernight {
		_, offset := now.In(newYorkZone).Zone()
		q.State, q.Session = inferSessionByClock(now, int64(offset))
		q.Session += "；显示Yahoo常规主报价"
	}
	return false
}

func applyYahooNightQuote(q *Quote, item yahooQuoteItem, now time.Time) bool {
	if !yahooNightETF(q.Symbol) || q.Currency != "USD" || q.QuoteType != "ETF" {
		return false
	}
	start, end, active := yahooNightWindow(now)
	if !active || item.OvernightMarketPrice == nil || item.OvernightMarketChange == nil || item.OvernightMarketChangePercent == nil || item.OvernightMarketTime == nil || !positiveFinite(q.RegularPrice) {
		return false
	}
	at := time.Unix(*item.OvernightMarketTime, 0)
	if at.Before(start) || !at.Before(end) || at.After(now.Add(30*time.Second)) || (item.RegularMarketTime != nil && *item.RegularMarketTime >= *item.OvernightMarketTime) {
		return false
	}
	candidate := *q
	candidate.Price = *item.OvernightMarketPrice
	candidate.ChangeAbs = *item.OvernightMarketChange
	candidate.ChangePercent = *item.OvernightMarketChangePercent
	candidate.ChangeBase = q.RegularPrice
	candidate.ChangeBasis = "Yahoo最近常规收盘（夜盘涨跌基准）"
	candidate.TradeTime = *item.OvernightMarketTime
	candidate.State = StateOvernight
	candidate.UpdateMode = "yahoo_overnight"
	candidate.Session = "夜盘 (Overnight)；显示Yahoo夜盘报价"
	if err := ValidateQuote(candidate); err != nil {
		return false
	}
	*q = candidate
	return true
}
