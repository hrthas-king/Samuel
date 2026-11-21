//go:build darwin

package sentinel

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// These symbols identify settled cash index closes published by Cboe.
var cboeIndexes = map[string]string{"^GSPC": "SPX", "^VIX": "VIX", "^VIX3M": "VIX3M"}

func publishedCloses(body []byte, id, start, end string) (Series, error) {
	rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil || len(rows) < 2 {
		return nil, errors.New("Cboe 日线为空")
	}
	column := 1
	if id == "SPX" {
		if strings.Join(rows[0], ",") != "DATE,SPX" {
			return nil, errors.New("Cboe SPX 表头无效")
		}
	} else {
		if strings.Join(rows[0], ",") != "DATE,OPEN,HIGH,LOW,CLOSE" {
			return nil, errors.New("Cboe 波动率日线表头无效")
		}
		column = 4
	}
	out := Series{}
	for _, r := range rows[1:] {
		date, err := time.Parse("01/02/2006", r[0])
		key := date.Format(dayFormat)
		if err != nil || key < start || key > end || date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r[column]), 64)
		if err == nil && above(number(v), 0) {
			out[key] = number(v)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("公开指数日线为空")
	}
	return out, nil
}
func nasdaqCloses(body []byte, symbol, start, end string) (Series, error) {
	var res struct {
		Data *struct {
			Symbol      string
			TradesTable struct {
				Rows []struct{ Date, Close string }
			}
		}
	}
	if json.Unmarshal(body, &res) != nil || res.Data == nil || res.Data.Symbol != symbol {
		return nil, errors.New("Nasdaq 标的不匹配")
	}
	out := Series{}
	for _, r := range res.Data.TradesTable.Rows {
		d, err := time.Parse("01/02/2006", r.Date)
		if err != nil {
			continue
		}
		key := d.Format(dayFormat)
		// The exchange's date-only historical table cannot establish that a
		// current-day row is final. Use this fallback through the previous day.
		if key < start || key >= end || d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(r.Close), "$"), ",", ""), 64)
		if err != nil || !above(number(v), 0) {
			out[key] = nil
		} else {
			out[key] = number(v)
		}
	}
	for _, v := range out {
		if v != nil {
			return out, nil
		}
	}
	return nil, errors.New("Nasdaq 日线为空")
}
func (f Fetcher) publishedHistory(ctx context.Context, key, start, end string) (Series, string, error) {
	if id := cboeIndexes[key]; id != "" {
		body, err := f.get(ctx, "https://cdn.cboe.com/api/global/us_indices/daily_prices/"+id+"_History.csv", 4<<20)
		if err != nil {
			return nil, "", err
		}
		s, err := publishedCloses(body, id, start, end)
		return s, "Cboe / " + id, err
	}
	asset := "etf"
	switch key {
	case "^NDX":
		key = "NDX"
		asset = "index"
	case "RSP", "SPY":
	default:
		return nil, "", errors.New("没有匹配的历史源")
	}
	q := url.Values{"assetclass": {asset}, "fromdate": {start}, "todate": {end}, "limit": {"5000"}}
	body, err := f.get(ctx, "https://api.nasdaq.com/api/quote/"+key+"/historical?"+q.Encode(), 4<<20)
	if err != nil {
		return nil, "", err
	}
	s, err := nasdaqCloses(body, key, start, end)
	return s, "Nasdaq / " + key, err
}
