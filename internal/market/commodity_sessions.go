package market

import (
	_ "embed"
	"time"
)

//go:embed london.tzif
var londonTZ []byte

var londonZone = func() *time.Location {
	zone, err := time.LoadLocationFromTZData("Europe/London", londonTZ)
	if err != nil {
		panic("invalid embedded London time zone: " + err.Error())
	}
	return zone
}()

func commodityWindow(symbol string, now time.Time) (time.Time, time.Time, bool) {
	zone, openHour, closeHour := newYorkZone, 18, 17
	if symbol == "BZ=F" {
		zone, openHour, closeHour = londonZone, 1, 23
	}
	local := now.In(zone)
	weekday, hour := local.Weekday(), local.Hour()
	day := func(delta, h int) time.Time {
		date := local.AddDate(0, 0, delta)
		return time.Date(date.Year(), date.Month(), date.Day(), h, 0, 0, 0, zone)
	}
	if symbol == "BZ=F" {
		if weekday == time.Saturday || (weekday == time.Friday && hour >= 23) || (weekday == time.Sunday && hour < 23) {
			return time.Time{}, time.Time{}, false
		}
		if weekday == time.Sunday {
			return day(0, 23), day(1, 23), true
		}
		if weekday == time.Monday {
			return day(-1, 23), day(0, 23), hour < 23
		}
		return day(0, 1), day(0, 23), hour >= 1 && hour < 23
	}
	if weekday == time.Saturday || (weekday == time.Friday && hour >= closeHour) || (weekday == time.Sunday && hour < openHour) || (hour >= closeHour && hour < openHour) {
		return time.Time{}, time.Time{}, false
	}
	if hour >= openHour {
		return day(0, openHour), day(1, closeHour), true
	}
	return day(-1, openHour), day(0, closeHour), true
}

func resolveCommodityQuoteSession(symbol string, now time.Time, tradeTime int64) (SessionState, string) {
	start, end, active := commodityWindow(symbol, now)
	if !active {
		return StateClosed, "期货休市或维护时段"
	}
	tick := time.Unix(tradeTime, 0)
	if tradeTime <= 0 || tick.Before(start) || !tick.Before(end) || tick.After(now.Add(30*time.Second)) {
		return StateClosed, "期货报价未更新；显示最近报价"
	}
	return StateRegular, "期货交易中"
}
