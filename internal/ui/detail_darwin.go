package ui

import (
	"fmt"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

func GroupLabel(language string, group market.GroupSpec) string { return groupLabel(language, group) }
func QuoteName(language string, view market.View) string        { return quoteName(language, view) }
func QuoteTime(language string, q *market.Quote) string         { return chartTimeText(language, q) }
func SessionName(language string, view market.View) string {
	return locale.Text(language, chartSession(view.EffectiveState()))
}

type DetailRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Milestone struct {
	Title     string `json:"title"`
	When      string `json:"when"`
	Values    string `json:"values"`
	Published bool   `json:"published"`
	Time      int64  `json:"time"`
	Marker    string `json:"marker"`
}

type Article struct {
	Title  string `json:"title"`
	Byline string `json:"byline"`
	Tag    string `json:"tag"`
	URL    string `json:"url"`
}

type DetailPresentation struct {
	Metrics     []DetailRow `json:"metrics"`
	Milestones  []Milestone `json:"milestones"`
	News        []Article   `json:"news"`
	MetricNote  string      `json:"metricNote"`
	MacroStatus string      `json:"macroStatus"`
	NewsStatus  string      `json:"newsStatus"`
	CloseDate   string      `json:"closeDate"`
	CloseChange *float64    `json:"closeChange,omitempty"`
}

func PresentDetail(view market.View, data market.DetailData, lang string, now time.Time) DetailPresentation {
	p := DetailPresentation{Metrics: []DetailRow{}, Milestones: []Milestone{}, News: []Article{}}
	tr := func(s string) string { return locale.Detail(lang, s) }
	rows, note := chartMetrics(view, data, lang)
	for _, row := range rows {
		p.Metrics = append(p.Metrics, DetailRow{row.label, row.value})
	}
	p.MetricNote = note
	if data.Metrics.Loading || data.Year.Loading {
		p.MetricNote = tr("正在读取盘口…")
	}
	close := data.Regular.Value
	if close.Date.IsZero() {
		close = data.Year.Value.Regular
	}
	if !close.Date.IsZero() {
		p.CloseDate, p.CloseChange = close.Date.Format("01-02"), &close.ChangePercent
	}
	kind := ""
	if view.Quote != nil {
		kind = view.Quote.QuoteType
	}
	if kind == "EQUITY" {
		earnings := data.Metrics.Value.Earnings
		if !data.Mainland.Value.Earnings.Start.IsZero() {
			earnings = data.Mainland.Value.Earnings
		}
		title, date := chartEarningsText(earnings, lang, now)
		if earnings.Start.IsZero() && (data.Metrics.Loading || data.Mainland.Loading) {
			date = tr("正在读取财报日期…")
		}
		p.Metrics = append(p.Metrics, DetailRow{title, date})
	}
	official := append(append([]market.MacroEvent(nil), data.Fed.Value...), data.Energy.Value...)
	relevant := market.RelevantMacro(view.Symbol, kind, market.MergeMacro(data.Calendar.Value, official))
	for _, event := range market.MacroMilestones(view.Symbol, kind, relevant, now) {
		when, values, published := chartMacroText(event, lang, now)
		title := chartMacroTitle(event, lang)
		if event.Kind == market.MacroFOMC && event.At.After(now) {
			title = fmt.Sprintf(tr("下一次 %s"), title)
		}
		p.Milestones = append(p.Milestones, Milestone{title, when, values, published, event.At.Unix(), milestoneMarker(event.Kind, lang)})
	}
	p.MacroStatus = tr("暂无匹配的高影响事件")
	if data.Calendar.Loading || data.Fed.Loading || data.Energy.Loading {
		p.MacroStatus = tr("正在读取宏观日历…")
	} else if !data.Calendar.OK && !data.Fed.OK && !data.Energy.OK {
		p.MacroStatus = tr("宏观日历暂不可用")
	}
	for _, item := range data.News.Value {
		local := item.Published.In(market.MacroZone(market.AssetRegion(view.Symbol)))
		p.News = append(p.News, Article{item.Title, item.Source + " · " + local.Format("01-02") + " " + marketClock(local), tr(item.Category), market.SecureNewsURL(item.URL)})
	}
	p.NewsStatus = tr("暂无匹配的近期资讯")
	if data.News.Loading {
		p.NewsStatus = tr("正在读取关联资讯…")
	} else if !data.News.OK {
		p.NewsStatus = tr("资讯源暂不可用")
	}
	return p
}

func milestoneMarker(kind market.MacroKind, lang string) string {
	switch kind {
	case market.MacroFOMC:
		return "FOMC"
	case market.MacroNFP:
		return locale.Detail(lang, "非农就业")
	case market.MacroCPI, market.MacroCoreCPI:
		return "CPI"
	case market.MacroPCE:
		return "PCE"
	case market.MacroEIA:
		return "EIA"
	}
	return ""
}
