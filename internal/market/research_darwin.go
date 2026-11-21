package market

import (
	"net/http"
	"time"
)

// NewResearchClient gives on-demand research its own transport with the same
// proxy, connection and HTTPS redirect policy as the macOS quote client.
func NewResearchClient() (*http.Client, error) {
	session, err := newHTTPSession()
	if err != nil {
		return nil, err
	}
	if err := session.setTimeouts(1500, 4500, 10000, 35000); err != nil {
		session.close()
		return nil, err
	}
	return &session.plain, nil
}

// ResearchCalendar parses the public weekly JSON calendar used by quote cards.
func ResearchCalendar(body []byte, from, until time.Time) ([]MacroEvent, bool) {
	return parseWeeklyCalendar(body, from, until)
}

func EconomicCalendarURL(from, until time.Time) string {
	return "https://economic-calendar.tradingview.com" + macroCalendarPath(from, until)
}

func ParseEconomicCalendar(body []byte, from, until time.Time) ([]MacroEvent, bool) {
	return parseMacroCalendar(body, from, until)
}
