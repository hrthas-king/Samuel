package market

import (
	"bytes"
	"regexp"
	"strings"
	"time"
)

// EIA publishes a recurring release time and an exception table. Apply the
// exception by the report's week-ending date, before filtering by release day.
func parseEIASchedule(body []byte, from, to time.Time) ([]MacroEvent, bool) {
	if !bytes.Contains(body, []byte("10:30 a.m. eastern time on Wednesday")) ||
		!bytes.Contains(body, []byte("Alternate release date")) {
		return nil, false
	}
	eiaExceptionRow := regexp.MustCompile(`(?is)<tr\b[^>]*>\s*<th\b[^>]*>\s*([^<]+)</th>\s*<td[^>]*>\s*([^<]+)</td>\s*<td[^>]*>\s*([^<]+)</td>\s*<td[^>]*>\s*([^<]+)</td>`)
	clockCleaner := strings.NewReplacer(".", "", " ", "")
	exceptions := make(map[time.Time]time.Time)
	for _, row := range eiaExceptionRow.FindAllSubmatch(body, -1) {
		week, e1 := time.ParseInLocation("January 2, 2006", strings.TrimSpace(string(row[1])), newYorkZone)
		day, e2 := time.ParseInLocation("January 2, 2006", strings.TrimSpace(string(row[2])), newYorkZone)
		clock := clockCleaner.Replace(strings.ToUpper(strings.TrimSpace(string(row[4]))))
		at, e3 := time.Parse("3:04PM", clock)
		if e1 != nil || e2 != nil || e3 != nil || week.Weekday() != time.Friday ||
			day.Weekday().String() != strings.TrimSpace(string(row[3])) || !day.After(week) || day.After(week.AddDate(0, 0, 14)) {
			return nil, false
		}
		exceptions[week] = time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), 0, 0, newYorkZone)
	}
	if len(exceptions) == 0 {
		return nil, false
	}
	var events []MacroEvent
	for day := marketDate(from.In(newYorkZone)).AddDate(0, 0, -7); day.Before(to); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Wednesday {
			continue
		}
		at := day.Add(10*time.Hour + 30*time.Minute)
		if alternate, ok := exceptions[day.AddDate(0, 0, -5)]; ok {
			at = alternate
		}
		if !at.Before(from) && at.Before(to) {
			events = append(events, MacroEvent{Kind: MacroEIA, Country: "US", At: at.UTC(), Source: "U.S. EIA"})
		}
	}
	return events, true
}
