//go:build darwin

package sentinel

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hrthas-king/Samuel/internal/market"
)

type Fetcher struct {
	Client  *http.Client
	Dir     string
	FREDKey string
	Now     time.Time
	Force   bool
}

func readResponse(client *http.Client, req *http.Request, limit int64) ([]byte, error) {
	res, err := client.Do(req)
	if err != nil {
		// net/url.Error contains the entire request URL, including FRED credentials.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("请求超时")
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			if strings.Contains(ue.Err.Error(), "stream error: stream ID ") {
				return nil, fmt.Errorf("%s 的 HTTP/2 数据流中断", req.URL.Hostname())
			}
			return nil, fmt.Errorf("网络连接失败 (%s): %w", req.URL.Hostname(), ue.Err)
		}
		return nil, fmt.Errorf("网络连接失败 (%s)", req.URL.Hostname())
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s HTTP %d", req.URL.Hostname(), res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("响应超过大小上限")
	}
	return data, nil
}
func (f Fetcher) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("数据源地址无效")
	}
	if req.URL.Hostname() != "fred.stlouisfed.org" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Safari/537.36")
	}
	req.Header.Set("Accept-Encoding", "identity")
	switch req.URL.Hostname() {
	case "economic-calendar.tradingview.com":
		req.Header.Set("Origin", "https://www.tradingview.com")
		req.Header.Set("Referer", "https://www.tradingview.com/")
	case "production.dataviz.cnn.io":
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Referer", "https://edition.cnn.com/markets/fear-and-greed")
	}
	return readResponse(f.Client, req, limit)
}

func atomicJSON(path string, value any) error {
	return atomicWrite(path, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(value)
	})
}

func atomicWrite(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func parseFREDCSV(body []byte, key, start, end string) (Series, error) {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})))
	header, err := reader.Read()
	if err != nil || len(header) != 2 || (header[0] != "observation_date" && !strings.EqualFold(header[0], "date")) || strings.TrimSpace(header[1]) != key {
		return nil, errors.New("CSV 缺少对应序列表头")
	}
	data := Series{}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("CSV 行格式无效")
		}
		date := strings.TrimSpace(row[0])
		if _, err = time.Parse(dayFormat, date); err != nil || date < start || date > end {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		if err == nil && number(v) != nil {
			data[date] = number(v)
		}
	}
	if len(data) == 0 {
		return nil, errors.New("CSV 无有效观测")
	}
	return data, nil
}

func (f Fetcher) fredHistory(ctx context.Context, key, start, end string) (Series, string, error) {
	var apiErr error
	if f.FREDKey != "" {
		query := url.Values{"series_id": {key}, "api_key": {f.FREDKey}, "file_type": {"json"}, "observation_start": {start}, "observation_end": {end}}
		body, err := f.get(ctx, "https://api.stlouisfed.org/fred/series/observations?"+query.Encode(), 2<<20)
		var data Series
		if err == nil {
			data, err = parseFRED(body, start, end)
		}
		if err == nil {
			return data, "FRED / " + key, nil
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		apiErr = err
	}
	// FRED also publishes a stable full-series CSV. Some connections reject
	// the ranged download but serve the same series at its canonical URL.
	base := "https://fred.stlouisfed.org/graph/fredgraph.csv?id=" + url.QueryEscape(key)
	var err error
	for _, address := range []string{base + "&cosd=" + url.QueryEscape(start), base} {
		var body []byte
		body, err = f.get(ctx, address, 2<<20)
		if err == nil {
			var data Series
			data, err = parseFREDCSV(body, key, start, end)
			if err == nil {
				return data, "FRED CSV / " + key, nil
			}
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
	}
	if apiErr != nil {
		return nil, "", fmt.Errorf("API %v，公开 CSV %w", apiErr, err)
	}
	return nil, "", fmt.Errorf("公开 CSV %w", err)
}

type cachedSeries struct {
	Source  string    `json:"source"`
	Fetched time.Time `json:"fetched_at"`
	Start   string    `json:"requested_start"`
	End     string    `json:"requested_end"`
	Data    Series    `json:"observations"`
}

func parseDaily(body []byte, symbol string, now time.Time, start string) (Series, error) {
	var response struct {
		Chart struct {
			Error  json.RawMessage
			Result []struct {
				Meta struct {
					Symbol, DataGranularity string
					RegularMarketTime       int64
					CurrentTradingPeriod    struct{ Regular struct{ Start, End int64 } }
				}
				Timestamp  []int64
				Indicators struct{ Quote []struct{ Close []*float64 } }
			}
		}
	}
	if json.Unmarshal(body, &response) != nil || len(response.Chart.Result) != 1 {
		return nil, errors.New("日线响应无有效结果")
	}
	r := response.Chart.Result[0]
	if r.Meta.Symbol != symbol || r.Meta.DataGranularity != "1d" || len(r.Indicators.Quote) != 1 || len(r.Timestamp) != len(r.Indicators.Quote[0].Close) {
		return nil, errors.New("日线标的、粒度或数组不匹配")
	}
	out := Series{}
	today := nyDate(now)
	period := r.Meta.CurrentTradingPeriod.Regular
	for i, ts := range r.Timestamp {
		if ts <= 0 || ts > now.Unix() {
			continue
		}
		stamp := time.Unix(ts, 0).In(ny)
		date := nyDate(stamp)
		if date < start || stamp.Weekday() == time.Saturday || stamp.Weekday() == time.Sunday {
			continue
		}
		if date == today && !(period.Start > 0 && period.End > period.Start && nyDate(time.Unix(period.Start, 0)) == today && nyDate(time.Unix(period.End, 0)) == today &&
			period.End <= r.Meta.RegularMarketTime && r.Meta.RegularMarketTime <= now.Unix()) {
			continue
		}
		p := r.Indicators.Quote[0].Close[i]
		if !above(p, 0) {
			p = nil
		}
		if previous, seen := out[date]; seen && (previous == nil || p == nil || *previous != *p) {
			p = nil
		}
		out[date] = p
	}
	for _, p := range out {
		if p != nil {
			return out, nil
		}
	}
	return nil, errors.New("无已收盘日线")
}

func parseFRED(body []byte, start, end string) (Series, error) {
	var response struct {
		Observations []struct{ Date, Value string }
	}
	if json.Unmarshal(body, &response) != nil || response.Observations == nil {
		return nil, errors.New("无效的 FRED 观测响应")
	}
	out := Series{}
	for _, r := range response.Observations {
		if _, err := time.Parse(dayFormat, r.Date); err != nil || r.Date < start || r.Date > end {
			continue
		}
		if v, err := strconv.ParseFloat(r.Value, 64); err == nil && number(v) != nil {
			out[r.Date] = number(v)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("缺少有效 FRED 观测值")
	}
	return out, nil
}
func (f Fetcher) loadSeries(ctx context.Context, key, start string, fred bool) (Series, string, string) {
	end := nyDate(f.Now)
	ttl := 15 * time.Minute
	if fred {
		ttl = 12 * time.Hour
	}
	path := filepath.Join(f.Dir, "cache", key+".json")
	if !f.Force {
		var c cachedSeries
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &c) == nil && len(c.Data) > 0 && c.Start <= start && c.End >= end &&
			!c.Fetched.After(f.Now) && f.Now.Sub(c.Fetched) < ttl {
			return c.Data, c.Source, ""
		}
	}
	var series Series
	var err error
	source := "Yahoo Finance"
	if fred {
		series, source, err = f.fredHistory(ctx, key, start, end)
	} else {
		day, _ := time.ParseInLocation(dayFormat, start, ny)
		q := url.Values{"interval": {"1d"}, "period1": {strconv.FormatInt(day.Unix(), 10)}, "period2": {strconv.FormatInt(f.Now.Unix()+1, 10)}}
		for _, host := range []string{market.FallbackHost, market.PrimaryHost} {
			var body []byte
			body, err = f.get(ctx, "https://"+host+"/v8/finance/chart/"+url.PathEscape(key)+"?"+q.Encode(), 12<<20)
			if err == nil {
				series, err = parseDaily(body, key, f.Now, start)
			}
			if err == nil || ctx.Err() != nil {
				break
			}
		}
		if err != nil && ctx.Err() == nil {
			original := err
			series, source, err = f.publishedHistory(ctx, key, start, end)
			if err != nil {
				err = fmt.Errorf("Yahoo %v；备用源 %w", original, err)
			}
		}
	}
	if err != nil {
		return nil, "", key + ": " + err.Error()
	}
	if err = atomicJSON(path, cachedSeries{source, f.Now, start, end, series}); err != nil {
		return series, source, key + ": 无法写入缓存: " + err.Error()
	}
	return series, source, ""
}

// Each symbol has a single job. The worker bound also applies to bulk backfills.
func (f Fetcher) history(ctx context.Context, prices, fred []string, start string) (Dataset, map[string]string, []string) {
	type job struct {
		key    string
		isFRED bool
	}
	work := make(chan job)
	data := Dataset{}
	sources := map[string]string{}
	var warnings []string
	var fredMissing, fredCauses []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range min(6, len(prices)+len(fred)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				s, source, w := f.loadSeries(ctx, j.key, start, j.isFRED)
				mu.Lock()
				data[j.key] = s
				if source != "" {
					sources[j.key] = source
				}
				if w != "" {
					if j.isFRED && len(s) == 0 {
						fredMissing = append(fredMissing, j.key)
						fredCauses = appendUnique(fredCauses, strings.TrimPrefix(w, j.key+": "))
					} else {
						warnings = append(warnings, w)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, k := range prices {
		work <- job{k, false}
	}
	for _, k := range fred {
		work <- job{k, true}
	}
	close(work)
	wg.Wait()
	if len(fredMissing) > 0 {
		sort.Strings(fredMissing)
		sort.Strings(fredCauses)
		hint := "可在「数据与模型设置」检查免费 FRED_API_KEY 或重新刷新"
		if f.FREDKey == "" {
			hint = "未配置 FRED_API_KEY，可在「数据与模型设置」填写免费密钥后刷新"
		}
		warnings = append(warnings, fmt.Sprintf("FRED 宏观数据缺失（%d 项）：%s。%s。%s。", len(fredMissing), strings.Join(fredMissing, ", "), strings.Join(fredCauses, "；"), hint))
	}
	sort.Strings(warnings)
	return data, sources, warnings
}

func missingMetrics(groups ...Measures) []string {
	var missing []string
	for _, group := range groups {
		for name, m := range group {
			switch {
			case m.Level == nil:
				missing = append(missing, name)
			case m.Delta == nil || m.Percentile == nil:
				missing = append(missing, name+"（历史窗口不足）")
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return []string{strings.Join(missing, "、")}
}
