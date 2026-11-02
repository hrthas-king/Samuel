package market

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxValuationBody = 1 << 20

// Only series identified as the same index are eligible. The provider's
// Nikkei/CAC country-market proxies are deliberately excluded.
var valuationCodes = map[string]string{
	"^NDX": "NDX", "^GSPC": "INX", "^GDAXI": "GDAXI",
	"^HSI": "HSI", "399006.SZ": "399006",
}

type ValuationMetric struct {
	Value       float64  `json:"value"`
	Percentile  *float64 `json:"percentile,omitempty"`
	Date        string   `json:"date"`
	HistoryFrom string   `json:"history_from,omitempty"`
	Samples     int      `json:"samples,omitempty"`
}

type IndexValuation struct {
	PE     *ValuationMetric `json:"pe,omitempty"`
	PB     *ValuationMetric `json:"pb,omitempty"`
	Source string           `json:"source"`
	URL    string           `json:"url,omitempty"`
	Method string           `json:"method"`
}

type valuationPoint struct {
	Date  string   `json:"d"`
	Value *float64 `json:"v"`
}

func historicalMetric(points []valuationPoint, asOf string) (*ValuationMetric, error) {
	end, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		return nil, fmt.Errorf("估值统计日期无效")
	}
	unique := make(map[string]*float64, len(points))
	for _, point := range points {
		at, err := time.Parse("2006-01-02", point.Date)
		if err != nil {
			return nil, fmt.Errorf("估值历史日期无效")
		}
		if at.After(end) {
			continue
		}
		if previous, exists := unique[point.Date]; exists && previous != nil && point.Value != nil && *previous != *point.Value {
			return nil, fmt.Errorf("估值历史日期重复且数值冲突")
		}
		unique[point.Date] = point.Value
	}
	var dates []string
	for date := range unique {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return nil, nil
	}
	latest := dates[len(dates)-1]
	value := unique[latest]
	if value == nil || !positiveFinite(*value) {
		return nil, nil
	}
	metric := &ValuationMetric{Value: *value, Date: latest}
	at, _ := time.Parse("2006-01-02", latest)
	start := at.AddDate(-10, 0, 0).Format("2006-01-02")
	lessOrEqual := 0
	for _, date := range dates {
		v := unique[date]
		if date < start || v == nil || !positiveFinite(*v) {
			continue
		}
		if metric.HistoryFrom == "" {
			metric.HistoryFrom = date
		}
		metric.Samples++
		if *v <= metric.Value {
			lessOrEqual++
		}
	}
	if metric.Samples >= 30 {
		percentile := float64(lessOrEqual) / float64(metric.Samples) * 100
		metric.Percentile = &percentile
	}
	return metric, nil
}

func parseIndexValuation(symbol string, body []byte, now time.Time) (*IndexValuation, error) {
	code, supported := valuationCodes[symbol]
	if !supported || len(body) > maxValuationBody {
		return nil, fmt.Errorf("未配置可核实的同指数历史来源")
	}
	var response struct {
		Index struct {
			Code string `json:"code"`
			Date string `json:"date"`
		} `json:"index"`
		Series struct {
			PE []valuationPoint `json:"pe_ttm"`
			PB []valuationPoint `json:"pb"`
		} `json:"series"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Index.Code != code {
		return nil, fmt.Errorf("估值响应与指数身份不匹配")
	}
	at, err := time.Parse("2006-01-02", response.Index.Date)
	if err != nil || at.After(now.AddDate(0, 0, 1)) || at.Before(now.AddDate(0, 0, -90)) {
		return nil, fmt.Errorf("估值统计日期无效或已过期")
	}
	pe, err := historicalMetric(response.Series.PE, response.Index.Date)
	if err != nil {
		return nil, err
	}
	pb, err := historicalMetric(response.Series.PB, response.Index.Date)
	if err != nil {
		return nil, err
	}
	if pe == nil && pb == nil {
		return nil, fmt.Errorf("未取得有效的同指数 PE/PB")
	}
	for _, metric := range []*ValuationMetric{pe, pb} {
		if metric != nil && metric.Date < now.AddDate(0, 0, -90).Format("2006-01-02") {
			return nil, fmt.Errorf("估值序列的最新数据已过期")
		}
	}
	return &IndexValuation{PE: pe, PB: pb, Source: "股叉叉（同指数历史序列）", URL: "https://guchacha.com/index-valuation/" + code,
		Method: "PE-TTM / PB，指数整体口径；分位为近10年有效样本中不高于当前值的比例，历史不足10年按实际区间，少于30个样本不计算"}, nil
}

func (q *Quote) setTencentIndexValuation(peRaw, pbRaw string) {
	if q.QuoteType != "INDEX" || q.TradeTime <= 0 || valuationCodes[q.Symbol] != "" {
		return
	}
	valuation := &IndexValuation{Source: "腾讯财经（同指数行情字段）", Method: "数据源 PE/PB 口径；未提供可核实历史，百分位缺失"}
	date := time.Unix(q.TradeTime, 0).In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	for i, raw := range []string{peRaw, pbRaw} {
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || !positiveFinite(value) {
			continue
		}
		metric := &ValuationMetric{Value: value, Date: date}
		if i == 0 {
			valuation.PE = metric
		} else {
			valuation.PB = metric
		}
	}
	if valuation.PE != nil || valuation.PB != nil {
		q.Valuation = valuation
	}
}

func (q Quote) HasSecondaryLine() bool { return q.IsEquity() || q.QuoteType == "INDEX" }

func (q Quote) SecondaryText() string {
	if q.QuoteType != "INDEX" {
		return q.CapitalizationText()
	}
	format := func(name string, metric *ValuationMetric) string {
		if metric == nil || !positiveFinite(metric.Value) {
			return name + " --(--)"
		}
		percentile := "--"
		if metric.Percentile != nil {
			percentile = fmt.Sprintf("%.1f%%", *metric.Percentile)
		}
		return fmt.Sprintf("%s %.2f(%s)", name, metric.Value, percentile)
	}
	var pe, pb *ValuationMetric
	if q.Valuation != nil {
		pe, pb = q.Valuation.PE, q.Valuation.PB
	}
	return format("PE", pe) + "  " + format("PB", pb)
}

func (q Quote) valuationTooltip() string {
	if q.QuoteType != "INDEX" {
		return ""
	}
	if q.Valuation == nil {
		return "\n  指数估值: PE -- / PB -- / 历史分位 --；暂无可核实的同指数数据，未使用全国市场或ETF代理"
	}
	v := q.Valuation
	text := "\n  指数估值（括号内为历史百分位）；来源: " + v.Source + "；" + v.Method
	for _, item := range []struct {
		name   string
		metric *ValuationMetric
	}{{"PE", v.PE}, {"PB", v.PB}} {
		if item.metric == nil {
			text += "\n  " + item.name + ": --"
			continue
		}
		m := item.metric
		text += fmt.Sprintf("\n  %s %.2f；数据日期 %s", item.name, m.Value, m.Date)
		if m.Percentile != nil {
			text += fmt.Sprintf("；分位 %.1f%%；实际历史 %s 至 %s（%d个有效样本）", *m.Percentile, m.HistoryFrom, m.Date, m.Samples)
		} else {
			text += "；历史分位 --（缺少历史或有效样本不足30）"
		}
	}
	if v.URL != "" {
		text += "\n  " + v.URL
	}
	return text
}

type valuationFetch func(context.Context, string) (*IndexValuation, error)
type valuationEntry struct {
	value              *IndexValuation
	fetchedAt, nextTry time.Time
	loading            bool
}
type valuationStore struct {
	mu      sync.Mutex
	entries map[string]valuationEntry
	fetch   valuationFetch
}

// Slow historical-data requests never hold up the price pipeline. Published
// valuations are immutable; a later ordinary quote poll picks up the result.
func (s *valuationStore) attach(batch map[string]SymbolResult, now time.Time) {
	for _, result := range batch {
		q := result.Quote
		if q == nil || q.QuoteType != "INDEX" || valuationCodes[q.Symbol] == "" {
			continue
		}
		s.mu.Lock()
		if s.entries == nil {
			s.entries = make(map[string]valuationEntry)
		}
		entry := s.entries[q.Symbol]
		if entry.value != nil && now.Sub(entry.fetchedAt) <= 7*24*time.Hour {
			q.Valuation = entry.value
		}
		start := !entry.loading && !now.Before(entry.nextTry)
		if start {
			entry.loading = true
			entry.nextTry = now.Add(15 * time.Minute)
		}
		s.entries[q.Symbol] = entry
		s.mu.Unlock()
		if start {
			go s.refresh(q.Symbol)
		}
	}
}

func (s *valuationStore) refresh(symbol string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, err := s.fetch(ctx, symbol)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[symbol]
	entry.loading = false
	if err == nil && value != nil {
		entry.value, entry.fetchedAt, entry.nextTry = value, now, now.Add(6*time.Hour)
	} else {
		entry.nextTry = now.Add(15 * time.Minute)
	}
	s.entries[symbol] = entry
}
