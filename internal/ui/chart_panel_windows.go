//go:build windows && (amd64 || arm64)

package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
)

const chartMaxHeight = 628

type chartExtras struct {
	data     market.DetailData
	now      time.Time
	scroll   int32
	mouse    point
	selected int64
}

type chartLink struct {
	bounds rect
	url    string
}

type chartFill struct {
	bounds rect
	tint   uint32
}

type chartPanel struct {
	text   []rasterText
	fills  []chartFill
	links  []chartLink
	events []market.MacroEvent
	height int32
}

func (r *chartRaster) detailPanel(w int32, view market.View, extra *chartExtras, lang string) chartPanel {
	var panel chartPanel
	left, right := r.px(22), w-r.px(22)
	y := r.px(chartHeight)
	tr := func(s string) string { return locale.Detail(lang, s) }
	line := func(label string, font uintptr, tint uint32, x1, x2, top, height int32, flags uintptr) {
		panel.text = append(panel.text, textRun(label, font, tint, rect{x1, top, x2, top + height}, 0x8000|flags))
	}
	section := func(title string) {
		panel.fills = append(panel.fills, chartFill{rect{left, y, right, y + 1}, 0xFF242B34})
		y += r.px(7)
		line(tr(title), r.name, barWhite, left, right, y, r.px(23), 0)
		y += r.px(29)
	}
	status := func(label string) {
		line(tr(label), r.detail, chartMuted, left, right, y, r.px(22), 0)
		y += r.px(28)
	}

	section("盘口与基本面")
	stats, note := chartMetrics(view, extra.data, lang)
	column := (right - left) / 3
	for i, stat := range stats {
		x := left + int32(i%3)*column
		yRow := y + int32(i/3)*r.px(39)
		panel.fills = append(panel.fills, chartFill{rect{x, yRow, x + column - r.px(5), yRow + r.px(36)}, 0xFF12171E})
		line(stat.label, r.detail, chartMuted, x+r.px(6), x+column-r.px(10), yRow+r.px(2), r.px(14), 0)
		line(stat.value, r.currency, 0xFFE5EAF2, x+r.px(6), x+column-r.px(10), yRow+r.px(15), r.px(19), 0)
	}
	y += int32((len(stats)+2)/3) * r.px(39)
	if extra.data.Metrics.Loading || extra.data.Year.Loading {
		status("正在读取盘口…")
	}
	if note != "" {
		line(note, r.detail, chartMuted, left, right, y, r.px(19), 0)
		y += r.px(21)
	}
	kind := ""
	if view.Quote != nil {
		kind = view.Quote.QuoteType
	}
	if kind == "EQUITY" {
		earnings := extra.data.Metrics.Value.Earnings
		if !extra.data.Mainland.Value.Earnings.Start.IsZero() {
			earnings = extra.data.Mainland.Value.Earnings
		}
		title, date := chartEarningsText(earnings, lang, extra.now)
		if earnings.Start.IsZero() && (extra.data.Metrics.Loading || extra.data.Mainland.Loading) {
			date = tr("正在读取财报日期…")
		}
		line(title, r.detail, chartMuted, left, right, y, r.px(16), 0)
		line(date, r.currency, 0xFFE5EAF2, left, right, y+r.px(16), r.px(19), 0)
		y += r.px(41)
	}

	section("关键宏观与议息节点")
	official := append(append([]market.MacroEvent(nil), extra.data.Fed.Value...), extra.data.Energy.Value...)
	panel.events = market.RelevantMacro(view.Symbol, kind, market.MergeMacro(extra.data.Calendar.Value, official))
	milestones := market.MacroMilestones(view.Symbol, kind, panel.events, extra.now)
	if len(milestones) == 0 {
		msg := "暂无匹配的高影响事件"
		if extra.data.Calendar.Loading || extra.data.Fed.Loading || extra.data.Energy.Loading {
			msg = "正在读取宏观日历…"
		} else if !extra.data.Calendar.OK && !extra.data.Fed.OK && !extra.data.Energy.OK {
			msg = "宏观日历暂不可用"
		}
		status(msg)
	}
	for _, event := range milestones {
		when, values, published := chartMacroText(event, lang, extra.now)
		title := chartMacroTitle(event, lang)
		if event.Kind == market.MacroFOMC && event.At.After(extra.now) {
			title = fmt.Sprintf(tr("下一次 %s"), title)
		}
		timeRun := textRun(when, r.detail, chartMuted, rect{}, 0)
		x := right - timeRun.measure(r.dc).CX - r.px(8)
		line(title, r.currency, 0xFFE5EAF2, left, x, y, r.px(20), 0)
		line(when, r.detail, 0xFFBAC6D5, x+r.px(4), right, y, r.px(20), 2)
		tint := uint32(chartMuted)
		if published {
			tint = 0xFF9CD8C5
		}
		line(values, r.detail, tint, left, right, y+r.px(20), r.px(17), 0)
		y += r.px(43)
	}

	section("财报与关联资讯")
	if len(extra.data.News.Value) == 0 {
		msg := "暂无匹配的近期资讯"
		if extra.data.News.Loading {
			msg = "正在读取关联资讯…"
		} else if !extra.data.News.OK {
			msg = "资讯源暂不可用"
		}
		status(msg)
	}
	for _, item := range extra.data.News.Value {
		tag := "[" + tr(item.Category) + "]"
		afterTag := left + textRun(tag, r.detail, 0, rect{}, 0).measure(r.dc).CX + r.px(13)
		line(tag, r.detail, 0xFF96B8E4, left+r.px(4), afterTag, y, r.px(21), 0)
		line(item.Title, r.currency, barWhite, afterTag, right-r.px(5), y, r.px(21), 0)
		local := item.Published.In(market.MacroZone(market.AssetRegion(view.Symbol)))
		line(item.Source+" · "+local.Format("01-02")+" "+marketClock(local), r.detail, chartMuted,
			afterTag, right-r.px(5), y+r.px(20), r.px(16), 0)
		if link := market.SecureNewsURL(item.URL); link != "" {
			panel.links = append(panel.links, chartLink{rect{left, y, right, y + r.px(37)}, link})
		}
		y += r.px(40)
	}
	panel.height = y + r.px(10)
	return panel
}

func scrollRect(r rect, dy int32) rect {
	r.Top, r.Bottom = r.Top-dy, r.Bottom-dy
	return r
}

func (r *chartRaster) fillArea(bounds rect, tint uint32) {
	left, right := max(0, bounds.Left), min(r.width, bounds.Right)
	for y := max(0, bounds.Top); y < min(r.height, bounds.Bottom); y++ {
		for x := left; x < right; x++ {
			r.pixels[y*r.width+x] = tint
		}
	}
}

func (r *chartRaster) drawPanel(p chartPanel, scroll int32, mouse point) {
	for _, fill := range p.fills {
		r.fillArea(scrollRect(fill.bounds, scroll), fill.tint)
	}
	r.links = r.links[:0]
	for _, link := range p.links {
		link.bounds = scrollRect(link.bounds, scroll)
		link.bounds.Top, link.bounds.Bottom = max(0, link.bounds.Top), min(r.height, link.bounds.Bottom)
		if link.bounds.Top >= link.bounds.Bottom {
			continue
		}
		if within(link.bounds, mouse) {
			r.fillArea(link.bounds, 0xFF1D2835)
		}
		r.links = append(r.links, link)
	}
	if r.scrollMax > 0 {
		track := r.height - r.px(16)
		thumb := max(r.px(22), int32(int64(track)*int64(r.height)/int64(p.height)))
		thumb = min(track, thumb)
		y := r.px(8) + int32(int64(track-thumb)*int64(scroll)/int64(r.scrollMax))
		r.fillArea(rect{r.width - r.px(6), y, r.width - r.px(3), y + thumb}, 0xFF3B485A)
	}
}

func (r *chartRaster) linkAt(p point) string {
	for _, link := range r.links {
		if within(link.bounds, p) {
			return link.url
		}
	}
	return ""
}

type chartMarker struct {
	x     int32
	label string
}

func chartMarkers(events []market.MacroEvent, points []market.IntradayPoint, plot rect, gap int32, lang string) []chartMarker {
	var marks []chartMarker
	for _, e := range events {
		short := ""
		switch e.Kind {
		case market.MacroFOMC:
			short = "FOMC"
		case market.MacroNFP:
			short = locale.Detail(lang, "非农")
		case market.MacroCPI, market.MacroCoreCPI:
			short = "CPI"
		case market.MacroPCE:
			short = "PCE"
		case market.MacroECB, market.MacroECBDeposit:
			short = "ECB"
		case market.MacroBOJ:
			short = "BOJ"
		case market.MacroBOK:
			short = "BOK"
		case market.MacroEIA:
			short = "EIA"
		case market.MacroOPEC:
			short = "OPEC+"
		case market.MacroLPR1, market.MacroLPR5:
			short = "LPR"
		case market.MacroGDP:
			short = "GDP"
		}
		x, in := chartEventX(e.At, points, float32(plot.Left), float32(plot.Right))
		if short == "" || !in {
			continue
		}
		pixel := int32(math.Round(float64(x)))
		close := false
		for _, mark := range marks {
			if pixel > mark.x-gap && pixel < mark.x+gap {
				close = true
				break
			}
		}
		if !close {
			marks = append(marks, chartMarker{pixel, short})
			if len(marks) == 3 {
				break
			}
		}
	}
	return marks
}
