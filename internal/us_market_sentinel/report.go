//go:build darwin

package sentinel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

const Left = "left_dca"
const Right = "right_confirm"

type Prior struct {
	Timestamp   time.Time `json:"timestamp"`
	Date        string    `json:"as_of_date"`
	Temperature int       `json:"market_temperature"`
	Signal      string    `json:"action_signal"`
	Index       string    `json:"index_preference"`
	Style       string    `json:"strategy_style"`
	SPXClose    *float64  `json:"spx_close_at_signal"`
	NDXClose    *float64  `json:"ndx_close_at_signal"`
	Scores      Scores    `json:"scores"`
	Final       Result    `json:"report"`
}
type Input struct {
	AsOf  time.Time `json:"as_of"`
	Style string    `json:"strategy_style"`
	Scores
	Sources      map[string]string        `json:"observation_sources"`
	Observations map[string]ObservedValue `json:"latest_observations"`
	Baseline     Preference               `json:"index_preference_baseline"`
	Previous     *Prior                   `json:"previous_snapshot"`
	Market       MarketData               `json:"market_data"`
	Macro        MacroData                `json:"macro_and_fed"`
	News         []Headline               `json:"news_and_fed_headlines_48h"`
	Events       []Event                  `json:"upcoming_macro_calendar_72h"`
	Warnings     []string                 `json:"missing_data_warnings"`
}
type Dimension struct {
	Score   *float64 `json:"score"`
	Summary string   `json:"summary"`
}
type NewsDimension struct {
	Adjustment float64 `json:"adjustment"`
	Tone       string  `json:"fed_tone"`
	Assessment string  `json:"priced_in_assessment"`
}
type Breakdown struct {
	Panic  Dimension     `json:"panic_and_breadth"`
	Fed    Dimension     `json:"fed_and_liquidity"`
	Credit Dimension     `json:"systemic_credit_risk"`
	News   NewsDimension `json:"news_and_catalysts"`
}
type Report struct {
	Date         string    `json:"as_of_date"`
	Style        string    `json:"strategy_style"`
	Base         int       `json:"quant_base_score"`
	Adjustment   float64   `json:"news_adjustment"`
	Temperature  int       `json:"market_temperature"`
	Delta        *int      `json:"temperature_delta_vs_prev"`
	Confidence   string    `json:"confidence"`
	Regime       string    `json:"regime"`
	Breaker      bool      `json:"recession_circuit_breaker_triggered"`
	Throttle     bool      `json:"event_throttle_applied"`
	Signal       string    `json:"action_signal"`
	Multiplier   float64   `json:"suggested_dca_multiplier"`
	Index        string    `json:"index_preference"`
	Rationale    string    `json:"index_preference_rationale"`
	Dimensions   Breakdown `json:"dimension_breakdown"`
	Drivers      []string  `json:"key_drivers"`
	Risks        []string  `json:"upcoming_event_risks"`
	Invalidation []string  `json:"invalidation_triggers"`
	Warnings     []string  `json:"missing_data_warnings"`
	Summary      string    `json:"executive_summary"`
}
type Result struct {
	Report
	Headlines     []Headline   `json:"headlines_48h"`
	CreditMissing bool         `json:"credit_data_missing"`
	DataThrottle  bool         `json:"risk_data_throttle_applied"`
	RiskWarning   string       `json:"risk_data_warning,omitempty"`
	FearIndices   *FearIndices `json:"fear_indices,omitempty"`
	SignalLabel   string       `json:"action_label_zh"`
	SPXDate       string       `json:"spx_close_date,omitempty"`
	NDXDate       string       `json:"ndx_close_date,omitempty"`
	Coverage      float64      `json:"coverage_ratio"`
	Mode          string       `json:"analysis_mode"`
	Guards        []string     `json:"guardrail_log"`
}
type band struct {
	signal string
	lo, hi float64
}

func signalBand(temp int) band {
	switch {
	case temp <= 20:
		return band{"strong_buy_dca", 1.5, 2}
	case temp <= 40:
		return band{"accumulate", 1.2, 1.5}
	case temp <= 60:
		return band{"hold_normal_dca", 1, 1}
	case temp <= 80:
		return band{"cautious", .5, .8}
	default:
		return band{"pause_or_trim", 0, .3}
	}
}
func regime(temp int) string {
	switch {
	case temp <= 40:
		return "panic_opportunity"
	case temp > 80:
		return "overheated_euphoria"
	default:
		return "neutral_trend"
	}
}

func signalLabel(signal string) string {
	switch signal {
	case "strong_buy_dca":
		return "加倍定投"
	case "buy_confirmed":
		return "反转确认买入"
	case "watch_for_reversal":
		return "等待反转"
	case "accumulate":
		return "分批增持"
	case "hold_normal_dca":
		return "维持常规定投"
	case "cautious":
		return "谨慎减量"
	case "pause_or_trim":
		return "暂停或减持"
	default:
		return signal
	}
}
func val(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *v)
}

type facts []string

func (items *facts) add(v *float64, format string) {
	if v != nil {
		*items = append(*items, fmt.Sprintf(format, *v))
	}
}
func (items facts) summary(empty string) string {
	if len(items) == 0 {
		return empty
	}
	return strings.Join(items, "，") + "。"
}

func quantReport(in Input) Report {
	b := signalBand(in.Base)
	m, f := in.Market.Measures, in.Macro.Measures
	var panic, fed, credit, drivers facts
	panic.add(m["vix"].Level, "VIX 收盘 %.2f 点")
	panic.add(in.Market.CNN.Level, "CNN 情绪指数 %.2f/100")
	fed.add(f["us2y_minus_dff_chg_20d_bps"].Level, "2Y−DFF 20 日变化 %+.1f bp")
	fed.add(f["us10y_tips_chg_20d_bps"].Level, "10 年实际利率 20 日变化 %+.1f bp")
	fed.add(f["net_liquidity_chg_4w_billions"].Level, "净流动性四周变化 %+.1f 十亿美元")
	credit.add(f["hy_oas_pct"].Level, "HY OAS %.2f%%")
	credit.add(f["hy_oas_chg_20d_bps"].Level, "信用利差 20 日变化 %+.1f bp")
	term := m["vix_vix3m_ratio"].Level
	if above(term, 1) {
		drivers.add(term, "VIX/VIX3M 倒挂至 %.2f")
	} else {
		drivers.add(term, "VIX/VIX3M 为 %.2f")
	}
	for _, idx := range []struct {
		name     string
		measures Measures
	}{{"标普", in.Market.SPX}, {"纳指", in.Market.NDX}} {
		var distances facts
		distances.add(idx.measures["dist_to_50ma_pct"].Level, "距 50 日线 %+.2f%%")
		distances.add(idx.measures["dist_to_200ma_pct"].Level, "距 200 日线 %+.2f%%")
		if len(distances) > 0 {
			drivers = append(drivers, idx.name+strings.Join(distances, "，"))
		}
	}
	drivers.add(m["rsp_spy_chg_20d_pct"].Level, "RSP/SPY 20 日广度变化 %+.2f%%")
	drivers.add(f["net_liquidity_billions"].Level, "美联储净流动性 %.1f 十亿美元")
	drivers.add(f["us10y_tips_pct"].Level, "10 年实际利率 %.2f%%")
	drivers.add(f["hy_oas_1y_pct"].Level, "高收益债利差一年分位 %.1f%%")
	if len(drivers) == 0 {
		drivers = append(drivers, panic...)
		drivers = append(drivers, fed...)
		drivers = append(drivers, credit...)
	}
	if len(drivers) > 4 {
		drivers = drivers[:4]
	}
	r := Report{Date: nyDate(in.AsOf), Style: in.Style, Base: in.Base, Temperature: in.Base, Confidence: "low", Regime: regime(in.Base),
		Signal: b.signal, Multiplier: (b.lo + b.hi) / 2, Index: in.Baseline.Index, Rationale: in.Baseline.Reason,
		Dimensions: Breakdown{Dimension{in.D1, panic.summary("暂无可核实的情绪指数读数。")}, Dimension{in.D2, fed.summary("缺少利率与流动性观测。")}, Dimension{in.D3, credit.summary("缺少信用利差观测。")}, NewsDimension{0, "neutral", "纯量化模式，消息调节为零。"}},
		Drivers:    append([]string{}, drivers...), Risks: []string{}, Warnings: []string{},
		Invalidation: []string{"HY OAS 超过 5.0% 或 20 个交易日走阔超过 80 bp，需按衰退熔断重新评估。", "VIX 连续两日回落或短均线确认发生变化时，重新核对右侧信号。"},
	}
	for _, e := range in.Events {
		if e.Importance == "high" {
			r.Risks = append(r.Risks, e.Date+" "+e.Event)
		}
	}
	return r
}
func appendUnique(items []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(items, v) {
			items = append(items, v)
		}
	}
	return items
}

func applyGuardrails(in Input, r Report, mode string) Result {
	submitted := r
	out := Result{Coverage: in.Coverage, Mode: mode, Guards: []string{}, Headlines: append([]Headline{}, in.News...), FearIndices: &FearIndices{
		VIX: FearReading{Metric: in.Market.Measures["vix"], Source: in.Sources["^VIX"]},
		CNN: in.Market.CNN,
	}}
	if v := in.Market.SPX["close"]; v.Level != nil {
		out.SPXDate = v.Date
	}
	if v := in.Market.NDX["close"]; v.Level != nil {
		out.NDXDate = v.Date
	}
	note := func(s string) { out.Guards = append(out.Guards, s) }
	// A. The engine owns the base, component scores and final temperature.
	adj := clamp(r.Adjustment, -15, 15)
	temp := int(math.RoundToEven(clamp(float64(in.Base)+adj, 0, 100)))
	sameScore := func(a, b *float64) bool {
		return a == nil && b == nil || a != nil && b != nil && math.Abs(*a-*b) < 1e-9
	}
	if r.Adjustment != adj || r.Base != in.Base || r.Temperature != temp || r.Dimensions.News.Adjustment != adj ||
		!sameScore(r.Dimensions.Panic.Score, in.D1) || !sameScore(r.Dimensions.Fed.Score, in.D2) || !sameScore(r.Dimensions.Credit.Score, in.D3) {
		note(fmt.Sprintf("A · 基础分 %d，消息调节 %+.2f，重算温度 %d。", in.Base, adj, temp))
	}
	r.Date, r.Style, r.Base, r.Adjustment, r.Temperature = nyDate(in.AsOf), in.Style, in.Base, adj, temp
	r.Dimensions.Panic.Score, r.Dimensions.Fed.Score, r.Dimensions.Credit.Score = in.D1, in.D2, in.D3
	r.Dimensions.News.Adjustment = adj
	r.Regime = regime(temp)
	r.Delta = nil
	if in.Previous != nil {
		d := temp - in.Previous.Temperature
		r.Delta = &d
	}
	r.Breaker, r.Throttle = false, false
	// Rebuild the temperature band even if the model proposed an inconsistent
	// signal. B/C caps operate on the original amount and never increase it.
	b := signalBand(temp)
	confirmed := yes(in.Market.VIXDeclining) && yes(in.Market.Reclaimed)
	if in.Style == Right && temp <= 40 && !in.Macro.Breaker && confirmed {
		b = band{"buy_confirmed", 1.5, 2}
	}
	original := r.Multiplier
	if in.Macro.Breaker {
		// B. A more defensive pause remains possible during the credit stop.
		r.Breaker = true
		r.Regime = "recession_warning"
		if r.Signal == "pause_or_trim" || b.signal == "pause_or_trim" {
			r.Signal = "pause_or_trim"
			r.Multiplier = clamp(original, 0, .3)
		} else {
			r.Signal = "cautious"
			r.Multiplier = clamp(original, 0, .5)
		}
		note("B · 信用衰退熔断，防接飞刀，倍率上限 0.50×。")
	} else if in.Style == Right && temp <= 40 && !confirmed {
		// C. Confirmation requires both independently observed conditions.
		r.Signal = "watch_for_reversal"
		r.Multiplier = clamp(original, 0, 1)
		note("C · VIX 与短均线尚未同时确认，等待企稳。")
	} else {
		if r.Signal != b.signal || original < b.lo || original > b.hi {
			note("信号映射 · 已按最终温度与策略校正信号及倍率。")
		}
		r.Signal = b.signal
		r.Multiplier = clamp(original, b.lo, b.hi)
	}
	// D. Dates without a release time are treated as an exchange-local day.
	for _, e := range in.Events {
		if e.Importance == "high" && e.within(in.AsOf, 48*time.Hour) && r.Multiplier > 1.2 {
			r.Multiplier = 1.2
			r.Throttle = true
			r.Risks = appendUnique(r.Risks, "事件落地前分批执行 · "+e.Event)
			note("D · 48 小时内有高危事件，单次倍率上限 1.20×。")
			break
		}
	}
	// E is the final sizing cap, including after a successful reversal or an
	// event throttle. Preserve any tighter cap already imposed by B or C.
	out.CreditMissing = in.Macro.Measures["hy_oas_pct"].Level == nil
	if out.CreditMissing || in.Coverage < .60-1e-12 {
		out.DataThrottle = true
		if out.CreditMissing {
			out.RiskWarning = "高收益债信用利差（HY OAS）缺失，无法排除衰退风险，已暂停倍投加码"
			r.Dimensions.Credit.Summary = "缺少 HY OAS 水平，无法核实当前信用状况。"
		} else {
			out.RiskWarning = fmt.Sprintf("有效指标覆盖率 %.1f%%，低于 60%%，已暂停倍投加码", 100*in.Coverage)
		}
		r.Multiplier = min(r.Multiplier, 1)
		switch r.Signal {
		case "strong_buy_dca", "buy_confirmed", "accumulate":
			r.Signal = "cautious"
		}
		note("E · " + out.RiskWarning + "，倍率上限 1.00×。")
	}
	conf := []string{"low", "medium", "high"}
	if slices.Index(conf, r.Confidence) > slices.Index(conf, in.Confidence) {
		r.Confidence = in.Confidence
	}
	if mode == "llm" && (submitted.Date != r.Date || submitted.Style != r.Style || submitted.Confidence != r.Confidence ||
		(submitted.Regime != r.Regime && !r.Breaker) || (submitted.Breaker && !r.Breaker) || (submitted.Throttle && !r.Throttle)) {
		note("快照校验 · 已依据运行日期、策略、数据覆盖率及实际触发条件校正报告。")
	}
	r.Warnings = appendUnique(r.Warnings, in.Warnings...)
	out.SignalLabel = signalLabel(r.Signal)
	if mode == "quant_only" || len(out.Guards) > 0 {
		risk := "留意信用利差与实际利率变化"
		switch {
		case out.DataThrottle:
			risk = out.RiskWarning
			if r.Breaker {
				risk += "；信用熔断已触发"
			}
		case in.Coverage == 0:
			risk = "缺少可评分数据，温度采用默认值"
		case r.Breaker:
			risk = "信用利差触发衰退熔断，防接飞刀"
		case r.Throttle:
			risk = "高危事件落地前分批执行"
		case r.Signal == "watch_for_reversal":
			risk = "等待 VIX 与短均线同时确认"
		case r.Confidence == "low":
			risk = "研判置信度低"
		}
		r.Summary = fmt.Sprintf("温度 %d/100，%s，定投 %.2f×，指数偏好 %s。%s。", temp, out.SignalLabel, r.Multiplier, r.Index, risk)
	}
	out.Report = r
	return out
}
func Brief(r Result) string {
	var b strings.Builder
	n := int(math.Round(float64(r.Temperature) / 5))
	fmt.Fprintf(&b, "美股宏观情绪与择时 · %s  %s\n%s%s  %d/100", r.Date, r.Style, strings.Repeat("█", n), strings.Repeat("░", 20-n), r.Temperature)
	if r.Delta != nil {
		fmt.Fprintf(&b, "（较上次 %+d）", *r.Delta)
	}
	fmt.Fprintf(&b, "\n[%s · %s]  %.2f×  ·  %s\n%s\n%s\n置信度 %s · 指标覆盖 %.1f%% · %s\n",
		r.SignalLabel, r.Signal, r.Multiplier, r.Index, r.Summary, r.Rationale, r.Confidence, 100*r.Coverage, r.Mode)
	if r.SPXDate != "" || r.NDXDate != "" {
		fmt.Fprintf(&b, "已核实收盘  SPX %s  ·  NDX %s\n", r.SPXDate, r.NDXDate)
	}
	if r.FearIndices != nil {
		v, c := r.FearIndices.VIX, r.FearIndices.CNN
		if v.Level == nil {
			fmt.Fprintln(&b, "VIX 恐慌指数  暂无数据")
		} else {
			fmt.Fprintf(&b, "VIX 恐慌指数  %s 点 · 收盘 %s", val(v.Level), v.Date)
			if v.Percentile != nil {
				fmt.Fprintf(&b, " · 一年分位 %s%%", val(v.Percentile))
			}
			fmt.Fprintln(&b)
		}
		if c.Level == nil || c.PublishedAt == nil {
			fmt.Fprintln(&b, "CNN 美股恐惧与贪婪指数  暂无数据")
		} else {
			fmt.Fprintf(&b, "CNN 美股恐惧与贪婪指数  %s/100", val(c.Level))
			if c.RatingZH != "" {
				fmt.Fprintf(&b, " · %s", c.RatingZH)
			}
			fmt.Fprintf(&b, " · %s · %s\n", c.PublishedAt.In(ny).Format("2006-01-02 15:04 MST"), c.Source)
		}
	}
	for _, d := range []struct {
		name string
		d    Dimension
	}{{"恐慌与广度", r.Dimensions.Panic}, {"美联储与流动性", r.Dimensions.Fed}, {"系统性信用", r.Dimensions.Credit}} {
		if d.name == "系统性信用" && r.CreditMissing {
			if d.d.Score != nil {
				fmt.Fprintf(&b, "\n%s  %s/100（仅含趋势项，缺信用数据）\n%s\n", d.name, val(d.d.Score), d.d.Summary)
			} else {
				fmt.Fprintf(&b, "\n%s  —（缺信用数据）\n%s\n", d.name, d.d.Summary)
			}
		} else if d.d.Score == nil {
			fmt.Fprintf(&b, "\n%s · 暂无评分\n", d.name)
		} else {
			fmt.Fprintf(&b, "\n%s  %s/100\n%s\n", d.name, val(d.d.Score), d.d.Summary)
		}
	}
	tone := r.Dimensions.News.Tone
	if r.Mode == "quant_only" {
		tone = "鹰鸽立场未评估"
	}
	fmt.Fprintf(&b, "\n消息与催化  %+.2f · %s\n%s\n", r.Adjustment, tone, r.Dimensions.News.Assessment)
	if len(r.Headlines) > 0 {
		fmt.Fprintln(&b, "\n近 48 小时宏观与美联储快讯")
		for _, h := range r.Headlines {
			fmt.Fprintf(&b, "  • %s · %s · %s\n", h.Title, h.Source, h.Published.In(ny).Format("01-02 15:04 MST"))
		}
	}
	for _, section := range []struct {
		title string
		items []string
	}{{"关键事实", r.Drivers}, {"事件风险", r.Risks}, {"风控覆写", r.Guards}, {"失效条件", r.Invalidation}, {"缺失数据", r.Warnings}} {
		if len(section.items) > 0 {
			fmt.Fprintf(&b, "\n%s\n", section.title)
			for _, s := range section.items {
				fmt.Fprintf(&b, "  • %s\n", s)
			}
		}
	}
	return b.String()
}
