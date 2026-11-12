package market

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

type MacroKind string

const (
	MacroFOMC          MacroKind = "FOMC 利率决议"
	MacroSEP           MacroKind = "FOMC 经济预测与点阵图"
	MacroFedPress      MacroKind = "美联储主席新闻发布会"
	MacroFedMinutes    MacroKind = "FOMC 会议纪要"
	MacroNFP           MacroKind = "非农就业"
	MacroUnemployment  MacroKind = "失业率"
	MacroCPI           MacroKind = "CPI"
	MacroCoreCPI       MacroKind = "核心 CPI"
	MacroPCE           MacroKind = "核心 PCE"
	MacroGDP           MacroKind = "GDP"
	MacroClaims        MacroKind = "初请失业金"
	MacroRetail        MacroKind = "零售销售"
	MacroISM           MacroKind = "ISM 制造业 PMI"
	MacroISMServices   MacroKind = "ISM 服务业 PMI"
	MacroECB           MacroKind = "欧洲央行利率决议"
	MacroECBDeposit    MacroKind = "欧洲央行存款利率"
	MacroBOJ           MacroKind = "日本央行利率决议"
	MacroBOK           MacroKind = "韩国央行利率决议"
	MacroLPR1          MacroKind = "一年期 LPR"
	MacroLPR5          MacroKind = "五年期 LPR"
	MacroChinaPMI      MacroKind = "制造业 PMI"
	MacroChinaServices MacroKind = "非制造业 PMI"
	MacroChinaCredit   MacroKind = "社会融资规模"
	MacroEIA           MacroKind = "EIA 原油库存"
	MacroOPEC          MacroKind = "OPEC+ 产油政策会议"
)

const (
	macroSource = "TradingView"
	fedSource   = "Federal Reserve"
)

type MacroEvent struct {
	Kind     MacroKind
	Title    string
	Country  string
	At       time.Time
	Actual   string
	Forecast string
	Previous string
	Source   string
}

func MacroZone(country string) *time.Location {
	switch country {
	case "US":
		return newYorkZone
	case "EU", "DE", "FR":
		return centralEuropeZone
	case "CN", "HK":
		return chinaMarketZone
	case "JP", "KR":
		return eastAsiaZone
	default:
		return time.UTC
	}
}

func classifyMacro(country, title string) MacroKind {
	switch country {
	case "US":
		switch {
		case strings.Contains(title, "fomc") && strings.Contains(title, "minutes"):
			return MacroFedMinutes
		case strings.Contains(title, "fomc") && (strings.Contains(title, "economic projections") || strings.Contains(title, "dot plot")):
			return MacroSEP
		case (strings.Contains(title, "fed") || strings.Contains(title, "fomc")) && strings.Contains(title, "press conference"):
			return MacroFedPress
		case strings.Contains(title, "fed interest rate") || strings.Contains(title, "federal funds rate") || strings.Contains(title, "fomc rate decision"):
			return MacroFOMC
		case strings.Contains(title, "non farm payroll") || strings.Contains(title, "nonfarm payroll") || strings.Contains(title, "non farm employment"):
			return MacroNFP
		case strings.HasPrefix(title, "unemployment rate"):
			return MacroUnemployment
		case strings.HasPrefix(title, "core pce"):
			return MacroPCE
		case strings.Contains(title, "initial jobless claims") || strings.Contains(title, "unemployment claims"):
			return MacroClaims
		case strings.Contains(title, "ism manufacturing pmi"):
			return MacroISM
		case strings.Contains(title, "ism services pmi") || strings.Contains(title, "ism non manufacturing pmi"):
			return MacroISMServices
		case strings.Contains(title, "eia crude oil stocks") || strings.Contains(title, "eia crude oil inventories"):
			return MacroEIA
		}
	case "EU":
		if strings.Contains(title, "deposit facility rate") {
			return MacroECBDeposit
		}
		if strings.Contains(title, "interest rate decision") || strings.Contains(title, "ecb policy rate") {
			return MacroECB
		}
	case "JP":
		if strings.Contains(title, "interest rate decision") || strings.Contains(title, "boj policy rate") {
			return MacroBOJ
		}
	case "KR":
		if strings.Contains(title, "interest rate decision") || strings.Contains(title, "bok policy rate") {
			return MacroBOK
		}
	case "CN":
		switch {
		case strings.Contains(title, "loan prime rate") && (strings.Contains(title, "5 year") || strings.Contains(title, "five year")):
			return MacroLPR5
		case strings.Contains(title, "loan prime rate") && (strings.Contains(title, "1 year") || strings.Contains(title, "one year")):
			return MacroLPR1
		case strings.Contains(title, "non manufacturing pmi"):
			return MacroChinaServices
		case strings.Contains(title, "manufacturing pmi"):
			return MacroChinaPMI
		case strings.Contains(title, "social financing"):
			return MacroChinaCredit
		}
	}
	if strings.Contains(title, "opec") && strings.Contains(title, "meeting") {
		return MacroOPEC
	}
	switch {
	case strings.HasPrefix(title, "core inflation rate"), strings.HasPrefix(title, "core cpi"):
		return MacroCoreCPI
	case strings.HasPrefix(title, "inflation rate"), strings.HasPrefix(title, "cpi "), title == "cpi":
		return MacroCPI
	case strings.HasPrefix(title, "gdp growth rate"), strings.HasPrefix(title, "gdp yoy"), strings.Contains(title, "gdp q/q"), strings.HasPrefix(title, "gdp qoq"):
		return MacroGDP
	case strings.Contains(title, "retail sales") && country == "US":
		return MacroRetail
	}
	return ""
}

func parseMacroCalendar(body []byte, from, until time.Time) ([]MacroEvent, bool) {
	var response struct {
		Status string
		Result []struct {
			Title, Country, Date, Unit, Scale string
			Importance                        int
			Actual, Forecast, Previous        json.RawMessage
		}
	}
	if json.Unmarshal(body, &response) != nil || response.Status != "ok" || response.Result == nil {
		return nil, false
	}
	var events []MacroEvent
	seen := make(map[struct {
		country, title string
		stamp          int64
	}]bool)
	for _, row := range response.Result {
		if row.Importance != 1 || len(row.Title) > 120 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, row.Date)
		// Midnight UTC is also used for date-only entries. Without a
		// verifiable release time those rows can't anchor a chart.
		if err != nil || at.Before(from) || !at.Before(until) || (at.UTC().Hour() == 0 && at.UTC().Minute() == 0) {
			continue
		}
		title := strings.Join(strings.Fields(row.Title), " ")
		normalized := strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(title), "-", " ")), " ")
		kind := classifyMacro(row.Country, normalized)
		key := struct {
			country, title string
			stamp          int64
		}{row.Country, normalized, at.Unix()}
		if kind == "" || seen[key] {
			continue
		}
		seen[key] = true
		events = append(events, MacroEvent{
			Kind: kind, Title: title, Country: row.Country, At: at.UTC(), Source: macroSource,
			Actual: macroValue(row.Actual, row.Scale, row.Unit), Forecast: macroValue(row.Forecast, row.Scale, row.Unit), Previous: macroValue(row.Previous, row.Scale, row.Unit),
		})
	}
	return events, true
}

func macroValue(raw json.RawMessage, scale, unit string) string {
	var number *float64
	if json.Unmarshal(raw, &number) != nil || number == nil {
		return ""
	}
	switch scale {
	case "K", "M", "B", "T":
	default:
		scale = ""
	}
	if unit != "%" {
		unit = ""
	}
	return strconv.FormatFloat(*number, 'f', -1, 64) + scale + unit
}

func parseWeeklyCalendar(body []byte, from, until time.Time) ([]MacroEvent, bool) {
	var rows []struct {
		Title, Country, Date, Impact, Actual, Forecast, Previous string
	}
	if json.Unmarshal(body, &rows) != nil || rows == nil {
		return nil, false
	}
	var events []MacroEvent
	countries := map[string]string{"USD": "US", "EUR": "EU", "CNY": "CN", "JPY": "JP", "KRW": "KR"}
	for _, row := range rows {
		country := countries[row.Country]
		if row.Impact != "High" || country == "" || len(row.Title) > 120 {
			continue
		}
		at, err := time.Parse(time.RFC3339, row.Date)
		kind := classifyMacro(country, strings.ReplaceAll(strings.ToLower(row.Title), "-", " "))
		if err != nil || at.Before(from) || !at.Before(until) || kind == "" || (at.Hour() == 0 && at.Minute() == 0) {
			continue
		}
		events = append(events, MacroEvent{Kind: kind, Title: row.Title, Country: country, At: at.UTC(), Source: "Forex Factory",
			Actual: compactFeedText(row.Actual, 32), Forecast: compactFeedText(row.Forecast, 32), Previous: compactFeedText(row.Previous, 32)})
	}
	return events, true
}

func fedMeeting(events []MacroEvent, year int, month time.Month, day int, sep bool) []MacroEvent {
	at := time.Date(year, month, day, 14, 0, 0, 0, newYorkZone)
	if at.Month() != month || at.Day() != day {
		return events
	}
	events = append(events,
		MacroEvent{Kind: MacroFOMC, Country: "US", At: at.UTC(), Source: fedSource},
		MacroEvent{Kind: MacroFedPress, Country: "US", At: at.Add(30 * time.Minute).UTC(), Source: fedSource})
	if sep {
		events = append(events, MacroEvent{Kind: MacroSEP, Country: "US", At: at.UTC(), Source: fedSource})
	}
	return events
}

func parseFedCalendar(body []byte) []MacroEvent {
	fedYear := regexp.MustCompile(`(?i)>\s*(20\d\d)\s+FOMC Meetings\s*<`)
	fedMonth := regexp.MustCompile(`(?is)<div class="[^"]*fomc-meeting__month[^"]*">\s*(?:<strong>)?\s*([^<]+?)\s*(?:</strong>)?\s*</div>\s*<div class="[^"]*fomc-meeting__date[^"]*">([^<]+)</div>`)
	fedDays := regexp.MustCompile(`^(\d{1,2})\s*[-–]\s*(\d{1,2})\s*(\*)?$`)
	var events []MacroEvent
	sections := fedYear.FindAllSubmatchIndex(body, -1)
	for i, match := range sections {
		year, _ := strconv.Atoi(string(body[match[2]:match[3]]))
		end := len(body)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		for _, row := range fedMonth.FindAllSubmatch(body[match[1]:end], -1) {
			months := strings.Split(html.UnescapeString(string(row[1])), "/")
			month, err := time.Parse("January", strings.TrimSpace(months[len(months)-1]))
			days := fedDays.FindStringSubmatch(strings.TrimSpace(html.UnescapeString(string(row[2]))))
			if err != nil || days == nil {
				continue
			}
			first, _ := strconv.Atoi(days[1])
			day, _ := strconv.Atoi(days[2])
			if first == 0 || first > 31 || (len(months) == 1 && day != first+1) {
				continue
			}
			meetingYear := year
			if strings.EqualFold(strings.TrimSpace(months[0]), "December") && month.Month() == time.January {
				meetingYear++
			}
			events = fedMeeting(events, meetingYear, month.Month(), day, days[3] == "*")
		}
	}
	return events
}

// MergeMacro keeps official release times and attaches calendar values only
// when the publication instants agree.
func MergeMacro(live, official []MacroEvent) []MacroEvent {
	events := append([]MacroEvent(nil), official...)
	for _, e := range live {
		matched := false
		for i, f := range official {
			if e.Kind == f.Kind && e.Country == f.Country &&
				e.At.In(newYorkZone).Format("2006-01-02") == f.At.In(newYorkZone).Format("2006-01-02") {
				matched = true
				if e.At.Equal(f.At) && (e.Actual != "" || e.Forecast != "" || e.Previous != "") {
					events[i].Actual, events[i].Forecast, events[i].Previous = e.Actual, e.Forecast, e.Previous
					events[i].Source = f.Source + " / " + e.Source
				}
				break
			}
		}
		if !matched {
			events = append(events, e)
		}
	}
	return events
}

// AssetRegion treats the two mainland-listed U.S. trackers as U.S. exposure.
func AssetRegion(symbol string) string {
	switch symbol {
	case "^GDAXI", "^FCHI":
		return "EU"
	case "^N225":
		return "JP"
	case "^KS11":
		return "KR"
	case "^HSI", "^HSTECH", "000001.SS", "399001.SZ", "399006.SZ":
		return "CN"
	}
	if !isTrackedSZETF(symbol) && watchlist.IsMainlandSymbol(symbol) {
		return "CN"
	}
	return "US"
}

func macroRelated(symbol, quoteType string, e MacroEvent) bool {
	region := AssetRegion(symbol)
	switch e.Kind {
	case MacroEIA, MacroOPEC:
		return symbol == "BZ=F"
	case MacroECB, MacroECBDeposit, MacroBOJ:
		return symbol == "GC=F" || e.Country == region
	case MacroBOK, MacroLPR1, MacroLPR5, MacroChinaPMI, MacroChinaServices, MacroChinaCredit:
		return e.Country == region
	}
	if e.Country == region || (region == "EU" && (e.Country == "DE" || e.Country == "FR")) {
		return true
	}
	return quoteType != "EQUITY" && e.Country == "US" &&
		(e.Kind == MacroFOMC || e.Kind == MacroNFP || e.Kind == MacroUnemployment ||
			e.Kind == MacroFedPress || e.Kind == MacroFedMinutes || e.Kind == MacroSEP ||
			(symbol == "GC=F" || symbol == "BZ=F") && (e.Kind == MacroCPI || e.Kind == MacroCoreCPI || e.Kind == MacroPCE))
}

func macroToday(e MacroEvent, now time.Time) bool {
	zone := MacroZone(e.Country)
	return e.At.In(zone).Format("2006-01-02") == now.In(zone).Format("2006-01-02")
}

func RelevantMacro(symbol, quoteType string, events []MacroEvent) []MacroEvent {
	var out []MacroEvent
	for _, e := range events {
		if macroRelated(symbol, quoteType, e) {
			out = append(out, e)
		}
	}
	return out
}

func MacroMilestones(symbol, quoteType string, relevant []MacroEvent, now time.Time) []MacroEvent {
	var nextFed MacroEvent
	var nearby []MacroEvent
	for _, e := range relevant {
		if quoteType == "EQUITY" && !macroToday(e, now) {
			continue
		}
		if quoteType != "EQUITY" && e.Kind == MacroFOMC && e.At.After(now) &&
			(nextFed.At.IsZero() || e.At.Before(nextFed.At)) {
			nextFed = e
		}
		if macroToday(e, now) || (e.At.After(now) && e.At.Before(now.AddDate(0, 0, 7))) {
			nearby = append(nearby, e)
		}
	}
	slices.SortStableFunc(nearby, func(a, b MacroEvent) int {
		focus := func(e MacroEvent) bool {
			switch symbol {
			case "BZ=F":
				return e.Kind == MacroEIA || e.Kind == MacroOPEC
			case "GC=F":
				return e.Kind == MacroECB || e.Kind == MacroBOJ
			}
			region := AssetRegion(symbol)
			return region != "US" && (e.Country == region || region == "EU" && (e.Country == "DE" || e.Country == "FR"))
		}
		if focusedA, focusedB := focus(a), focus(b); focusedA != focusedB {
			if focusedA {
				return -1
			}
			return 1
		}
		publishedA, publishedB := !a.At.After(now) && a.Actual != "", !b.At.After(now) && b.Actual != ""
		if publishedA != publishedB {
			if publishedA {
				return -1
			}
			return 1
		}
		if a.At.Before(now) && b.At.Before(now) {
			return b.At.Compare(a.At)
		}
		return a.At.Compare(b.At)
	})
	var out []MacroEvent
	limit := 3
	if !nextFed.At.IsZero() {
		limit--
	}
	for _, e := range nearby {
		if !nextFed.At.IsZero() && e.Kind == MacroFOMC && e.At.Equal(nextFed.At) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, e)
	}
	if !nextFed.At.IsZero() {
		out = append(out, nextFed)
	}
	return out
}

// RateDirection is defined only for comparable, published Fed rates.
func RateDirection(event MacroEvent) string {
	if event.Kind != MacroFOMC {
		return ""
	}
	actual, err1 := strconv.ParseFloat(strings.TrimSuffix(event.Actual, "%"), 64)
	prior, err2 := strconv.ParseFloat(strings.TrimSuffix(event.Previous, "%"), 64)
	if err1 != nil || err2 != nil {
		return ""
	}
	switch {
	case actual > prior:
		return "加息"
	case actual < prior:
		return "降息"
	default:
		return "维持利率"
	}
}

func macroCalendarPath(from, to time.Time) string {
	const layout = "2006-01-02T15:04:05.000Z"
	return "/events?from=" + url.QueryEscape(from.UTC().Format(layout)) + "&to=" + url.QueryEscape(to.UTC().Format(layout)) +
		"&countries=US%2CEU%2CCN%2CJP%2CKR%2CDE%2CFR%2CSA%2CWD&minImportance=1"
}
