//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hrthas-king/Samuel/internal/market"
)

type Headline struct {
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	URL       string    `json:"url,omitempty"`
	Published time.Time `json:"published_at"`
}
type Event struct {
	Date       string `json:"date"`
	Event      string `json:"event"`
	Importance string `json:"importance"`
	Source     string `json:"source,omitempty"`
}

func (e Event) window() (time.Time, time.Time, bool) {
	if at, err := time.Parse(time.RFC3339, e.Date); err == nil {
		return at, at, true
	}
	d, err := time.ParseInLocation(dayFormat, e.Date, ny)
	return d, d.AddDate(0, 0, 1).Add(-time.Nanosecond), err == nil
}
func (e Event) within(now time.Time, horizon time.Duration) bool {
	start, end, ok := e.window()
	return ok && !end.Before(now) && !start.After(now.Add(horizon))
}

var tags = regexp.MustCompile("(?s)<[^>]*>")

func cleanText(s string, limit int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(tags.ReplaceAllString(html.UnescapeString(s), " "))), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}
func parseRSS(body []byte, source string, now time.Time) ([]Headline, error) {
	type item struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Link        string `xml:"link"`
		PubDate     string `xml:"pubDate"`
		Date        string `xml:"date"`
	}
	var rss struct {
		XMLName xml.Name
		Channel []item `xml:"channel>item"`
		Items   []item `xml:"item"`
	}
	if xml.Unmarshal(body, &rss) != nil || (rss.XMLName.Local != "rss" && rss.XMLName.Local != "RDF") {
		return nil, errors.New("RSS 格式无效")
	}
	out := []Headline{}
	for _, r := range append(rss.Channel, rss.Items...) {
		text := r.PubDate
		if text == "" {
			text = r.Date
		}
		var at time.Time
		for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700"} {
			var err error
			at, err = time.ParseInLocation(layout, strings.TrimSpace(text), ny)
			if err == nil {
				break
			}
		}
		title := cleanText(r.Title, 180)
		if title != "" && !at.After(now) && !at.Before(now.Add(-48*time.Hour)) {
			out = append(out, Headline{
				Title:     title,
				Summary:   cleanText(r.Description, 240),
				Source:    source,
				URL:       market.SecureNewsURL(r.Link),
				Published: at.UTC(),
			})
		}
	}
	return out, nil
}
func latestNews(rows []Headline) []Headline {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Published.Equal(rows[j].Published) {
			return rows[i].Source < rows[j].Source
		}
		return rows[i].Published.After(rows[j].Published)
	})
	seen := map[string]bool{}
	out := []Headline{}
	for _, r := range rows {
		key := strings.ToLower(r.Title)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
		if len(out) == 20 {
			break
		}
	}
	return out
}
func normalizeEvents(rows []Event, now time.Time) ([]Event, int) {
	seen := map[string]bool{}
	out := []Event{}
	invalid := 0
	for _, e := range rows {
		e.Event = cleanText(e.Event, 160)
		start, _, ok := e.window()
		if !ok || e.Event == "" || !slices.Contains([]string{"high", "medium", "low"}, e.Importance) {
			invalid++
			continue
		}
		if !e.within(now, 72*time.Hour) {
			continue
		}
		key := start.UTC().Format(time.RFC3339) + "|" + strings.ToLower(e.Event)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _, _ := out[i].window()
		b, _, _ := out[j].window()
		if a.Equal(b) {
			return out[i].Event < out[j].Event
		}
		return a.Before(b)
	})
	return out, invalid
}
func (f Fetcher) economics(ctx context.Context) ([]Event, error) {
	end := f.Now.Add(72 * time.Hour)
	var events []market.MacroEvent
	var ok bool
	var coverageWarning error
	if body, err := f.get(ctx, market.EconomicCalendarURL(f.Now, end), 768<<10); err == nil {
		events, ok = market.ParseEconomicCalendar(body, f.Now, end.Add(time.Second))
	}
	if !ok {
		var err error
		var body []byte
		body, err = f.get(ctx, "https://nfs.faireconomy.media/ff_calendar_thisweek.json", 256<<10)
		if err != nil {
			return nil, err
		}
		events, ok = market.ResearchCalendar(body, f.Now, end.Add(time.Second))
		if !ok {
			return nil, errors.New("公开周历格式无效")
		}
		// The backup feed publishes only the current week (Sunday–Saturday).
		if end.In(ny).Weekday() < f.Now.In(ny).Weekday() {
			coverageWarning = errors.New("备用周历仅覆盖本周，跨周时段请结合本地日历核对")
		}
	}
	out := []Event{}
	for _, row := range events {
		if row.Country == "US" {
			out = append(out, Event{row.At.UTC().Format(time.RFC3339), string(row.Kind) + " · " + row.Title, "high", row.Source})
		}
	}
	return out, coverageWarning
}
func parseEarnings(body []byte, date string) ([]Event, error) {
	var v struct {
		Data   *struct{ Rows json.RawMessage }
		Status struct{ RCode int } `json:"status"`
	}
	if json.Unmarshal(body, &v) != nil || v.Status.RCode != 200 || v.Data == nil || len(v.Data.Rows) == 0 {
		return nil, errors.New("Nasdaq 财报日历无有效响应")
	}
	var rows []struct{ Symbol string }
	if json.Unmarshal(v.Data.Rows, &rows) != nil {
		return nil, errors.New("Nasdaq 财报记录格式无效")
	}
	out := []Event{}
	for _, r := range rows {
		if slices.Contains([]string{"AAPL", "MSFT", "GOOG", "GOOGL", "AMZN", "META", "NVDA", "TSLA", "AVGO"}, r.Symbol) {
			sym := r.Symbol
			if sym == "GOOG" {
				sym = "GOOGL"
			}
			out = append(out, Event{date, sym + " 财报（Nasdaq 预计日期）", "high", "Nasdaq"})
		}
	}
	return out, nil
}

func (f Fetcher) catalysts(ctx context.Context) ([]Headline, []Event, []string) {
	var headlines []Headline
	var events []Event
	var warnings []string
	var newsAvailable, calendarAvailable bool
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(src string, h []Headline, e []Event, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			warnings = append(warnings, src+": "+err.Error())
		}
		newsAvailable = newsAvailable || h != nil
		calendarAvailable = calendarAvailable || e != nil
		headlines = append(headlines, h...)
		events = append(events, e...)
	}
	for _, feed := range []struct{ name, address string }{
		{"Federal Reserve", "https://www.federalreserve.gov/feeds/press_all.xml"},
		{"CNBC Economy", "https://search.cnbc.com/rs/search/combinedcms/view.xml?partnerId=wrss01&id=20910258"},
		{"WSJ US Business", "https://feeds.a.dj.com/rss/WSJcomUSBusiness.xml"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := f.get(ctx, feed.address, 2<<20)
			var h []Headline
			if err == nil {
				h, err = parseRSS(raw, feed.name, f.Now)
			}
			add(feed.name, h, nil, err)
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); e, err := f.economics(ctx); add("宏观日历", nil, e, err) }()
	end := nyDate(f.Now.Add(72 * time.Hour))
	for d := f.Now.In(ny); nyDate(d) <= end; d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		date := nyDate(d)
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := f.get(ctx, "https://api.nasdaq.com/api/calendar/earnings?date="+date, 2<<20)
			var e []Event
			if err == nil {
				e, err = parseEarnings(body, date)
			}
			add("Nasdaq "+date, nil, e, err)
		}()
	}
	wg.Wait()
	path := filepath.Join(f.Dir, "calendar.json")
	if body, err := os.ReadFile(path); err == nil {
		var local []Event
		if json.Unmarshal(body, &local) != nil || local == nil {
			warnings = append(warnings, "本地 calendar.json 格式无效")
		} else {
			calendarAvailable = true
			events = append(local, events...)
		}
	} else if !os.IsNotExist(err) {
		warnings = append(warnings, err.Error())
	}
	filtered, invalid := normalizeEvents(events, f.Now)
	if invalid > 0 {
		warnings = append(warnings, fmt.Sprintf("日历中 %d 条日期、名称或重要性无效", invalid))
	}
	if len(headlines) == 0 {
		warnings = append(warnings, "近 48 小时无可核实新闻")
	}
	sort.Strings(warnings)
	var news []Headline
	if newsAvailable {
		news = latestNews(headlines)
	}
	if !calendarAvailable {
		filtered = nil
	}
	return news, filtered, warnings
}
