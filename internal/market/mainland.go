package market

import "time"

func currentCashTick(local time.Time, tradeTime int64, auctionStart int) bool {
	trade := time.Unix(tradeTime, 0).In(local.Location())
	return tradeTime > 0 && tradeTime <= local.Add(30*time.Second).Unix() && trade.Year() == local.Year() && trade.YearDay() == local.YearDay() && trade.Hour()*60+trade.Minute() >= auctionStart
}

func resolveMainlandSession(now time.Time, tradeTime int64) (SessionState, string) {
	zone := time.FixedZone("CST", 8*3600)
	local := now.In(zone)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return StateClosed, "周末休市"
	}
	m := local.Hour()*60 + local.Minute()
	switch {
	case m >= 9*60+15 && m < 9*60+30:
		if !currentCashTick(local, tradeTime, 9*60+15) {
			return StateClosed, "集合竞价报价未更新；显示最近报价"
		}
		return StatePreMarket, "集合竞价 (Pre-Market)"
	case (m >= 9*60+30 && m < 11*60+30) || (m >= 13*60 && m < 15*60):
		start := 9*60 + 30
		if m >= 13*60 {
			start = 13 * 60
		}
		if !currentCashTick(local, tradeTime, start) {
			return StateClosed, "休市或当前时段报价未更新"
		}
		return StateRegular, "盘中 (Regular)"
	case m >= 11*60+30 && m < 13*60:
		return StateClosed, "午间休市 (Lunch Break)"
	default:
		return StateClosed, "休市 (Closed)"
	}
}
