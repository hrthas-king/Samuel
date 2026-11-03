package market

import "time"

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
