package market

import (
	"testing"
	"time"
)

func fixtureTime(t *testing.T, raw string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestMacroCalendarFiltersImportanceAndPreservesValues(t *testing.T) {
	from := fixtureTime(t, "2026-11-06T00:00:00Z")
	body := []byte(`{"status":"ok","result":[
		{"title":"Non Farm Payrolls","country":"US","date":"2026-11-06T13:30:00.000Z","importance":1,"actual":0,"forecast":65,"previous":-4,"scale":"K"},
		{"title":"Core Inflation Rate YoY","country":"US","date":"2026-11-10T13:30:00Z","importance":1,"actual":null,"forecast":2.6,"previous":2.4,"unit":"%"},
		{"title":"Initial Jobless Claims","country":"US","date":"2026-11-06T13:30:00Z","importance":0,"actual":200},
		{"title":"BoJ Interest Rate Decision","country":"JP","date":"2026-11-06T00:00:00Z","importance":1},
		{"title":"Fed Interest Rate Decision","country":"US","date":"2026-11-19T19:00:00Z","importance":1},
		{"title":"Unrelated Speech","country":"US","date":"2026-11-06T15:00:00Z","importance":1},
		{"title":"Non Farm Payrolls","country":"US","date":"2026-11-06T13:30:00Z","importance":1,"actual":0}
	]}`)
	events, ok := parseMacroCalendar(body, from, from.AddDate(0, 0, 7))
	if !ok || len(events) != 2 {
		t.Fatalf("filtered events: %+v (valid %v)", events, ok)
	}
	if e := events[0]; e.Kind != MacroNFP || e.Country != "US" || e.At.In(newYorkZone).Format("15:04 MST") != "08:30 EST" ||
		e.Actual != "0K" || e.Forecast != "65K" || e.Previous != "-4K" {
		t.Fatalf("lost a published value or timestamp: %+v", e)
	}
	if e := events[1]; e.Kind != MacroCoreCPI || e.Actual != "" || e.Forecast != "2.6%" || e.Previous != "2.4%" {
		t.Fatalf("forecast was relabeled as actual: %+v", e)
	}
	weekly := []byte(`[{"title":"Non-Farm Employment Change","country":"USD","date":"2026-11-06T08:30:00-05:00","impact":"High","forecast":"65K","previous":"-4K"},{"title":"FOMC Meeting Minutes","country":"USD","date":"2026-11-06T14:00:00-05:00","impact":"Medium"}]`)
	fallback, ok := parseWeeklyCalendar(weekly, from, from.AddDate(0, 0, 7))
	if !ok || len(fallback) != 1 || !fallback[0].At.Equal(events[0].At) || fallback[0].Actual != "" {
		t.Fatalf("unexpected weekly fallback: %+v", fallback)
	}
}

func TestFedOfficialScheduleCrossWeekAndDST(t *testing.T) {
	html := []byte(`
	<div class="panel-heading"><h4><a id="1">2026 FOMC Meetings</a></h4></div>
	<div class="fomc-meeting__month x"><strong>March</strong></div>
	<div class="fomc-meeting__date x">17-18*</div>
	<div class="fomc-meeting__month x"><strong>December</strong></div>
	<div class="fomc-meeting__date x">8-9*</div>
	<div class="fomc-meeting__month x"><strong>February</strong></div>
	<div class="fomc-meeting__date x">29-30</div>
	<div class="panel-heading"><h4><a id="2">2027 FOMC Meetings</a></h4></div>
	<div class="fomc-meeting__month x"><strong>January</strong></div>
	<div class="fomc-meeting__date x">26-27</div>
	`)
	events := parseFedCalendar(html)
	if len(events) != 8 {
		t.Fatalf("official meetings: %+v", events)
	}
	if events[0].At.Format(time.RFC3339) != "2026-03-18T18:00:00Z" ||
		events[1].At.Format(time.RFC3339) != "2026-03-18T18:30:00Z" || events[2].Kind != MacroSEP ||
		events[3].At.Format(time.RFC3339) != "2026-12-09T19:00:00Z" {
		t.Fatalf("ET rules lost a DST hour, SEP, or press conference: %+v", events)
	}
	for _, c := range []struct{ now, next string }{
		{"2026-11-06T15:00:00Z", "2026-12-09T19:00:00Z"},
		{"2026-12-31T15:00:00Z", "2027-01-27T19:00:00Z"},
	} {
		now := fixtureTime(t, c.now)
		picks := MacroMilestones("GC=F", "FUTURE", RelevantMacro("GC=F", "FUTURE", events), now)
		if len(picks) != 1 || picks[0].Kind != MacroFOMC || picks[0].At.Format(time.RFC3339) != c.next {
			t.Fatalf("cross-week/annual next decision at %s: %+v", c.now, picks)
		}
	}
	decision := events[3]
	decision.Actual, decision.Previous, decision.Source = "4.25%", "4.50%", macroSource
	if merged := MergeMacro([]MacroEvent{decision}, events); len(merged) != len(events) || RateDirection(merged[3]) != "降息" {
		t.Fatalf("official calendar and reported decision did not merge: %+v", merged)
	}
}

func TestMacroMatchingUsesAssetExposure(t *testing.T) {
	kinds := []MacroEvent{{Kind: MacroNFP, Country: "US"}, {Kind: MacroEIA, Country: "US"}, {Kind: MacroOPEC, Country: "WD"},
		{Kind: MacroLPR1, Country: "CN"}, {Kind: MacroBOJ, Country: "JP"}, {Kind: MacroBOK, Country: "KR"},
		{Kind: MacroECB, Country: "EU"}, {Kind: MacroFOMC, Country: "US"}}
	for _, c := range []struct {
		symbol, typ string
		count       int
	}{
		{"159632.SZ", "ETF", 2}, {"^HSI", "INDEX", 3}, {"399001.SZ", "INDEX", 3}, {"^N225", "INDEX", 3},
		{"^KS11", "INDEX", 3}, {"^FCHI", "INDEX", 3}, {"GC=F", "FUTURE", 4}, {"BZ=F", "FUTURE", 4},
		{"NVDA", "EQUITY", 2}, {"600519.SS", "EQUITY", 1},
	} {
		if got := RelevantMacro(c.symbol, c.typ, kinds); len(got) != c.count {
			t.Errorf("%s matched %+v", c.symbol, got)
		}
	}
}

func TestEIAOfficialExceptionsReplaceRegularRelease(t *testing.T) {
	from := fixtureTime(t, "2026-11-06T00:00:00Z")
	body := []byte(`<p>The standard release time will be at 10:30 a.m. eastern time on Wednesday.</p>
		<table><tr><th>Week ending</th><th>Alternate release date</th></tr>
		<tr><th scope="row">November 6, 2026</th><td>November 12, 2026</td><td>Thursday</td><td>12:00 p.m.</td><td>Veterans Day</td></tr></table>`)
	events, ok := parseEIASchedule(body, from, from.AddDate(0, 0, 15))
	if !ok || len(events) != 2 || events[0].At.Format(time.RFC3339) != "2026-11-12T17:00:00Z" ||
		events[1].At.Format(time.RFC3339) != "2026-11-18T15:30:00Z" || events[0].Actual != "" {
		t.Fatalf("invalid official release times: %+v", events)
	}
}
