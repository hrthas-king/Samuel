package market

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

type EarningsDate struct {
	Start, End time.Time // exchange-local calendar dates
	Session    SessionState
	Estimated  bool
	Reported   bool
	Source     string
}

type QuoteMetrics struct {
	At                         int64
	Open, High, Low, PrevClose *float64
	Volume, Turnover           *float64
	VolumeLots                 bool
	YearHigh, YearLow          *float64
	TrailingPE, ForwardPE, EPS *float64
	Earnings                   EarningsDate
	Source                     string
}

type MainlandFundamentals struct {
	Earnings EarningsDate
	EPS      *float64
	EPSAsOf  time.Time
}

type YearPriceRange struct {
	Low, High float64
	AsOf      string
	Regular   RegularClose
}

// Tencent's extended wire columns differ across asset classes. For its cash
// securities use the explicitly unadjusted daily high/low series instead.
func parseTencentYearRange(code string, body []byte, from, to, now time.Time) (YearPriceRange, bool) {
	var response struct {
		Code int
		Data map[string]struct{ Day [][]string }
	}
	if json.Unmarshal(body, &response) != nil || response.Code != 0 {
		return YearPriceRange{}, false
	}
	earliest, latest := time.Time{}, time.Time{}
	low, high := math.Inf(1), 0.0
	days := make(map[string]bool)
	through := tencentCloseThrough(now, strings.HasPrefix(code, "hk"))
	closes := make(map[time.Time]float64)
	for _, row := range response.Data[code].Day {
		if len(row) < 5 {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", row[0], to.Location())
		if err != nil || day.Before(from) || day.After(to) {
			continue
		}
		if !day.After(through) && !day.Before(through.AddDate(0, 0, -14)) {
			price := 0.0
			if p := metricNumber(row[2], 0, false); p != nil {
				price = *p
			}
			if existing, seen := closes[day]; seen && existing != price {
				price = 0
			}
			closes[day] = price
		}
		h, l := metricNumber(row[3], 0, false), metricNumber(row[4], 0, false)
		if h == nil || l == nil || *h < *l || days[row[0]] {
			continue
		}
		days[row[0]] = true
		low, high = min(low, *l), max(high, *h)
		if earliest.IsZero() || day.Before(earliest) {
			earliest = day
		}
		if day.After(latest) {
			latest = day
		}
	}
	regular, _ := completedCloseReturn(closes)
	if len(days) < 150 || earliest.After(from.AddDate(0, 0, 14)) || latest.Before(to.AddDate(0, 0, -7)) {
		return YearPriceRange{Regular: regular}, false
	}
	return YearPriceRange{Low: low, High: high, AsOf: to.Format("2006-01-02"), Regular: regular}, true
}

func tencentCloseThrough(now time.Time, hongKong bool) time.Time {
	local := now.In(chinaMarketZone)
	day := marketDate(local)
	closeMinute := 15 * 60
	if hongKong {
		closeMinute = 16*60 + 10 // Include the HK closing auction.
	}
	if local.Hour()*60+local.Minute() < closeMinute {
		day = day.AddDate(0, 0, -1)
	}
	return day
}

type yahooMetricFields struct {
	DataGranularity                                                                           string
	Symbol, Currency, QuoteType, InstrumentType                                               string
	RegularMarketTime, EarningsTimestamp, EarningsTimestampStart, EarningsTimestampEnd        int64
	IsEarningsDateEstimate                                                                    bool
	EarningsReleaseTime                                                                       string
	RegularMarketOpen, RegularMarketDayHigh, RegularMarketDayLow, RegularMarketPreviousClose  *float64
	PreviousClose, ChartPreviousClose, RegularMarketVolume, FiftyTwoWeekHigh, FiftyTwoWeekLow *float64
	TrailingPE, ForwardPE, EPSTrailingTwelveMonths                                            *float64
}

func validMetric(v *float64, minimum float64, includeMinimum bool) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < minimum || (!includeMinimum && *v == minimum) {
		return nil
	}
	return v
}

func metricNumber(text string, minimum float64, includeMinimum bool) *float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return nil
	}
	return validMetric(&n, minimum, includeMinimum)
}

func marketDate(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
}

func (m QuoteMetrics) SameQuoteDay(q Quote) bool {
	if m.At <= 0 || q.TradeTime <= 0 {
		return false
	}
	if strings.HasPrefix(q.UpdateMode, "yahoo_") && q.RegularTradeTime > 0 {
		q.TradeTime = q.RegularTradeTime
	}
	want := marketDate(QuoteMarketTime(q))
	q.TradeTime = m.At
	return marketDate(QuoteMarketTime(q)).Equal(want)
}

func (m QuoteMetrics) Amplitude() *float64 {
	if m.PrevClose == nil || m.High == nil || m.Low == nil || *m.High < *m.Low || *m.PrevClose <= 0 {
		return nil
	}
	a := (*m.High - *m.Low) / *m.PrevClose * 100
	return validMetric(&a, 0, true)
}

func yahooEarnings(f yahooMetricFields, now time.Time) EarningsDate {
	stamp := f.EarningsTimestampStart
	if stamp == 0 {
		stamp = f.EarningsTimestamp
	}
	start := time.Unix(stamp, 0).In(newYorkZone)
	if stamp <= 0 || start.Before(now.AddDate(-1, 0, 0)) || start.After(now.AddDate(1, 0, 0)) {
		return EarningsDate{}
	}
	date := EarningsDate{Start: marketDate(start), Estimated: f.IsEarningsDateEstimate, Source: "Yahoo Finance"}
	if end := time.Unix(f.EarningsTimestampEnd, 0).In(newYorkZone); f.EarningsTimestampEnd >= stamp && !end.After(start.AddDate(0, 3, 0)) {
		date.End = marketDate(end)
	}
	switch strings.ToUpper(strings.TrimSpace(f.EarningsReleaseTime)) {
	case "BMO", "BEFOREMARKETOPEN":
		date.Session = StatePreMarket
	case "AMC", "AFTERMARKETCLOSE":
		date.Session = StatePostMarket
	}
	return date
}

func parseYahooMetrics(q Quote, body []byte, chart bool, now time.Time) (QuoteMetrics, bool) {
	var fields yahooMetricFields
	if chart {
		var r struct {
			Chart struct {
				Result []struct {
					Meta       yahooMetricFields
					Timestamp  []int64
					Indicators struct{ Quote []struct{ Open []*float64 } }
				}
			}
		}
		if json.Unmarshal(body, &r) != nil || len(r.Chart.Result) != 1 {
			return QuoteMetrics{}, false
		}
		result := r.Chart.Result[0]
		fields = result.Meta
		fields.QuoteType = fields.InstrumentType
		if fields.RegularMarketOpen == nil && fields.DataGranularity == "1d" && len(result.Indicators.Quote) == 1 {
			quoteDay := q
			quoteDay.TradeTime = fields.RegularMarketTime
			day := marketDate(QuoteMarketTime(quoteDay))
			opens := result.Indicators.Quote[0].Open
			for i, stamp := range result.Timestamp[:min(len(result.Timestamp), len(opens))] {
				quoteDay.TradeTime = stamp
				if stamp > 0 && stamp <= fields.RegularMarketTime && marketDate(QuoteMarketTime(quoteDay)).Equal(day) {
					fields.RegularMarketOpen = opens[i]
					break
				}
			}
		}
	} else {
		var r struct {
			QuoteResponse struct{ Result []yahooMetricFields }
		}
		if json.Unmarshal(body, &r) != nil || len(r.QuoteResponse.Result) != 1 {
			return QuoteMetrics{}, false
		}
		fields = r.QuoteResponse.Result[0]
	}
	if normalizeSymbol(fields.Symbol) != q.Symbol || fields.Currency != q.Currency || fields.QuoteType != q.QuoteType ||
		fields.RegularMarketTime <= 0 || fields.RegularMarketTime > now.Add(30*time.Second).Unix() {
		return QuoteMetrics{}, false
	}
	positive := func(v *float64) *float64 { return validMetric(v, 0, false) }
	m := QuoteMetrics{
		At: fields.RegularMarketTime, Source: "Yahoo Finance",
		Open: positive(fields.RegularMarketOpen), High: positive(fields.RegularMarketDayHigh), Low: positive(fields.RegularMarketDayLow),
		PrevClose: positive(fields.RegularMarketPreviousClose), Volume: validMetric(fields.RegularMarketVolume, 0, true),
		YearHigh: positive(fields.FiftyTwoWeekHigh), YearLow: positive(fields.FiftyTwoWeekLow),
	}
	if m.PrevClose == nil {
		m.PrevClose = positive(fields.PreviousClose)
	}
	if m.PrevClose == nil {
		m.PrevClose = positive(fields.ChartPreviousClose)
	}
	if q.IsEquity() {
		m.TrailingPE, m.ForwardPE = positive(fields.TrailingPE), positive(fields.ForwardPE)
		m.EPS = validMetric(fields.EPSTrailingTwelveMonths, -math.MaxFloat64, true)
		m.Earnings = yahooEarnings(fields, now)
	}
	return m, true
}

func parseTencentMetrics(q Quote, code string, body []byte, now time.Time) (QuoteMetrics, bool) {
	var m QuoteMetrics
	for _, line := range strings.Split(string(body), ";") {
		wire, fields, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (wire != "v_"+code && wire != "v_"+strings.TrimPrefix(code, "r_")) {
			continue
		}
		fields = strings.Trim(fields, "\"\r\n ")
		m.Source = "Tencent Finance"
		if q.QuoteType == "FUTURE" && strings.HasPrefix(code, "hf_") {
			p := strings.Split(fields, ",")
			if len(p) < 13 {
				return QuoteMetrics{}, false
			}
			at, err := time.ParseInLocation("2006-01-02 15:04:05", p[12]+" "+p[6], chinaMarketZone)
			if err != nil || at.After(now.Add(30*time.Second)) {
				return QuoteMetrics{}, false
			}
			m.At = at.Unix()
			m.High, m.Low, m.PrevClose = metricNumber(p[4], 0, false), metricNumber(p[5], 0, false), metricNumber(p[7], 0, false)
			return m, true
		}
		p := strings.Split(fields, "~")
		if len(p) < 38 {
			return QuoteMetrics{}, false
		}
		want := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(code, "r_"), "sh"), "sz"), "bj")
		want = strings.TrimPrefix(want, "hk")
		if !strings.EqualFold(strings.TrimSpace(p[2]), want) {
			return QuoteMetrics{}, false
		}
		at, err := time.ParseInLocation("20060102150405", p[30], chinaMarketZone)
		if err != nil {
			at, err = time.ParseInLocation("2006/01/02 15:04:05", p[30], chinaMarketZone)
		}
		if err != nil || at.After(now.Add(30*time.Second)) {
			return QuoteMetrics{}, false
		}
		m.At = at.Unix()
		m.Open, m.PrevClose = metricNumber(p[5], 0, false), metricNumber(p[4], 0, false)
		m.High, m.Low = metricNumber(p[33], 0, false), metricNumber(p[34], 0, false)
		if q.Currency == "CNY" {
			m.Volume, m.VolumeLots = metricNumber(p[36], 0, true), true
			// The bundled price/volume/amount field is in yuan. The
			// dedicated amount column is in units of ten thousand yuan.
			if tuple := strings.Split(p[35], "/"); len(tuple) == 3 {
				m.Turnover = metricNumber(tuple[2], 0, true)
			}
			if m.Turnover == nil {
				if amount := metricNumber(p[37], 0, true); amount != nil {
					n := *amount * 10000
					m.Turnover = validMetric(&n, 0, true)
				}
			}
			if q.IsEquity() && len(p) > 39 {
				m.TrailingPE = metricNumber(p[39], 0, false)
			}
		}
		return m, true
	}
	return m, false
}

func eastMoneyDate(raw string) time.Time {
	if len(raw) < 10 {
		return time.Time{}
	}
	at, _ := time.ParseInLocation("2006-01-02", raw[:10], chinaMarketZone)
	return at
}

func eastMoneySymbol(symbol string) string {
	if strings.HasSuffix(symbol, ".SS") {
		return strings.TrimSuffix(symbol, ".SS") + ".SH"
	}
	return symbol
}

func parseMainlandEarnings(symbol string, body []byte, now time.Time) EarningsDate {
	var r struct {
		Result struct {
			Data []struct {
				Symbol      string `json:"SECUCODE"`
				Actual      string `json:"ACTUAL_PUBLISH_DATE"`
				Appointment string `json:"APPOINT_PUBLISH_DATE"`
				Published   string `json:"IS_PUBLISH"`
			}
		}
	}
	if json.Unmarshal(body, &r) != nil {
		return EarningsDate{}
	}
	today := marketDate(now.In(chinaMarketZone))
	var next, recent EarningsDate
	for _, row := range r.Result.Data {
		if row.Symbol != eastMoneySymbol(symbol) {
			continue
		}
		at := eastMoneyDate(row.Actual)
		if row.Published == "1" && !at.IsZero() && !at.After(today) && at.After(recent.Start) {
			recent = EarningsDate{Start: at, Reported: true, Source: "东方财富"}
		} else if row.Published == "0" {
			at = eastMoneyDate(row.Appointment)
			if !at.Before(today) && (next.Start.IsZero() || at.Before(next.Start)) {
				next = EarningsDate{Start: at, Estimated: true, Source: "东方财富"}
			}
		}
	}
	if !next.Start.IsZero() {
		return next
	}
	return recent
}

func parseMainlandEPS(symbol string, body []byte, now time.Time) (*float64, time.Time) {
	var r struct {
		Result struct {
			Data []struct {
				Symbol string   `json:"SECUCODE"`
				EPS    *float64 `json:"BASIC_EPS"`
				Period string   `json:"REPORTDATE"`
				Notice string   `json:"NOTICE_DATE"`
			}
		}
	}
	if json.Unmarshal(body, &r) != nil {
		return nil, time.Time{}
	}
	today := marketDate(now.In(chinaMarketZone))
	var latest time.Time
	var eps *float64
	for _, row := range r.Result.Data {
		period, notice := eastMoneyDate(row.Period), eastMoneyDate(row.Notice)
		if row.Symbol == eastMoneySymbol(symbol) && !notice.IsZero() && !notice.After(today) && !notice.Before(period) &&
			period.After(latest) && validMetric(row.EPS, -math.MaxFloat64, true) != nil {
			latest, eps = period, row.EPS
		}
	}
	return eps, latest
}
