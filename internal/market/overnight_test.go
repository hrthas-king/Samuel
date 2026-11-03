package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestVOOQQQOvernightUsesNightPriceAndMatchingChange(t *testing.T) {
	now := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	for _, symbol := range []string{"VOO", "QQQ"} {
		t.Run(symbol, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"quoteResponse":{"result":[{"symbol":%q,"currency":"USD","quoteType":"ETF","marketState":"OVERNIGHT","quoteSourceName":"BOATS Real Time Price","gmtOffSetMilliseconds":-18000000,"regularMarketPrice":110,"regularMarketPreviousClose":100,"regularMarketChange":10,"regularMarketChangePercent":10,"regularMarketTime":%d,"overnightMarketPrice":108,"overnightMarketChange":-2,"overnightMarketChangePercent":-1.818181818,"overnightMarketTime":%d}]}}`, symbol, now.Add(-9*time.Hour).Unix(), now.Add(-30*time.Second).Unix()))
			batch, err := ParseYahooQuoteBatchForSymbols([]string{symbol}, body, 1, now)
			q := batch[symbol].Quote
			if err != nil || q == nil {
				t.Fatalf("missing quote: %v %+v", err, batch[symbol])
			}
			if q.Price != 108 || q.ChangeAbs != -2 || math.Abs(q.ChangePercent-(-1.818181818)) > 1e-7 || q.Previous() != 110 || q.UpdateMode != "yahoo_overnight" || q.TradeTime != now.Add(-30*time.Second).Unix() {
				t.Fatalf("night badge must use Yahoo overnight tuple, not regular close: %+v", q)
			}
		})
	}
}

func TestYahooNightWindowUsesNewYorkDSTAndTradingWeek(t *testing.T) {
	for _, tc := range []struct {
		name, at string
		active   bool
	}{
		{"summer_open", "2026-10-26T00:00:00Z", true},
		{"winter_open", "2026-11-03T01:00:00Z", true},
		{"winter_before_open", "2026-11-03T00:59:59Z", false},
		{"winter_before_close", "2026-11-03T08:59:59Z", true},
		{"winter_close", "2026-11-03T09:00:00Z", false},
		{"friday_early", "2026-11-06T08:59:59Z", true},
		{"friday_evening", "2026-11-07T01:00:00Z", false},
		{"saturday", "2026-11-07T06:00:00Z", false},
		{"sunday_early_dst_change", "2026-11-01T06:00:00Z", false},
		{"sunday_evening_after_dst", "2026-11-02T01:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			start, end, active := yahooNightWindow(at)
			if active != tc.active || (active && (at.Before(start) || !at.Before(end))) {
				t.Fatalf("unexpected night window: %v %v %v", start, end, active)
			}
		})
	}
}

func nightFixture(symbol string, now time.Time, overrides map[string]any) []byte {
	item := map[string]any{
		"symbol": symbol, "currency": "USD", "quoteType": "ETF", "marketState": "OVERNIGHT", "quoteSourceName": "BOATS Real Time Price",
		"regularMarketPrice": 110, "regularMarketPreviousClose": 100, "regularMarketChange": 10, "regularMarketChangePercent": 10,
		"regularMarketTime":    now.Add(-9 * time.Hour).Unix(),
		"overnightMarketPrice": 108, "overnightMarketChange": -2, "overnightMarketChangePercent": -1.818181818,
		"overnightMarketTime": now.Add(-30 * time.Second).Unix(),
		// These total-day fields intentionally disagree with the night return.
		"fulldayPrice": 108, "fulldayChange": 8, "fulldayChangePercent": 8,
	}
	for key, value := range overrides {
		item[key] = value
	}
	body, err := json.Marshal(map[string]any{"quoteResponse": map[string]any{"result": []any{item}}})
	if err != nil {
		panic(err)
	}
	return body
}

func TestYahooNightRejectsMissingStaleFutureOrMismatchedTuples(t *testing.T) {
	now := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		data map[string]any
	}{
		{"missing_price", map[string]any{"overnightMarketPrice": nil}},
		{"missing_change", map[string]any{"overnightMarketChange": nil}},
		{"missing_percent", map[string]any{"overnightMarketChangePercent": nil}},
		{"missing_timestamp", map[string]any{"overnightMarketTime": nil}},
		{"zero_price", map[string]any{"overnightMarketPrice": 0}},
		{"old_night", map[string]any{"overnightMarketTime": now.Add(-24 * time.Hour).Unix()}},
		{"before_night_open", map[string]any{"overnightMarketTime": time.Date(2026, 11, 3, 0, 59, 59, 0, time.UTC).Unix()}},
		{"future_tick", map[string]any{"overnightMarketTime": now.Add(time.Minute).Unix()}},
		{"newer_regular_close", map[string]any{"regularMarketTime": now.Unix()}},
		{"full_day_delta", map[string]any{"overnightMarketChange": 8, "overnightMarketChangePercent": 8}},
		{"wrong_percent_base", map[string]any{"overnightMarketChangePercent": -2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batch, err := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, nightFixture("VOO", now, tc.data), 1, now)
			q := batch["VOO"].Quote
			if err != nil || q == nil || q.Price != 110 || q.ChangePercent != 10 || q.State != StateClosed || q.UpdateMode != "yahoo_regular" || !strings.Contains(q.Session, "夜盘数据不可用") {
				t.Fatalf("unverifiable night tuple must be an explicit regular-close fallback: %v %+v", err, q)
			}
		})
	}
}

func TestYahooNightSelectionSupportsAllUSAssetsAndRejectsStaleSessions(t *testing.T) {
	night := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	for _, raw := range []string{"OVERNIGHT", "PREPRE", "POSTPOST"} {
		t.Run(raw, func(t *testing.T) {
			batch, err := ParseYahooQuoteBatchForSymbols([]string{"QQQ"}, nightFixture("QQQ", night, map[string]any{"marketState": raw, "regularMarketPreviousClose": nil, "regularMarketChange": nil, "regularMarketChangePercent": nil}), 1, night)
			q := batch["QQQ"].Quote
			if err != nil || q == nil || q.Price != 108 || q.State != StateOvernight || q.Previous() != 110 {
				t.Fatalf("valid night tuple must not depend on regular daily change or cached state: %v %+v", err, q)
			}
		})
	}
	for _, tc := range []struct {
		name, symbol, raw, at string
		state                 SessionState
	}{
		{"premarket", "VOO", "PRE", "2026-11-03T12:00:00Z", StateClosed},
		{"regular", "QQQ", "REGULAR", "2026-11-03T17:00:00Z", StateClosed},
		{"postmarket", "VOO", "POST", "2026-11-03T23:00:00Z", StateClosed},
		{"expired_night_flag", "QQQ", "OVERNIGHT", "2026-11-03T09:00:00Z", StateClosed},
		{"weekend", "VOO", "OVERNIGHT", "2026-11-07T06:00:00Z", StateClosed},
		{"other_etf", "SOXX", "OVERNIGHT", "2026-11-03T06:00:00Z", StateOvernight},
		{"stock", "NVDA", "OVERNIGHT", "2026-11-03T06:00:00Z", StateOvernight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, tc.at)
			kind := "ETF"
			if tc.symbol == "NVDA" {
				kind = "EQUITY"
			}
			body := nightFixture(tc.symbol, night, map[string]any{"marketState": tc.raw, "quoteType": kind, "gmtOffSetMilliseconds": -18000000})
			batch, err := ParseYahooQuoteBatchForSymbols([]string{tc.symbol}, body, 1, at)
			q := batch[tc.symbol].Quote
			price, percent, mode := 110.0, 10.0, "yahoo_regular"
			if tc.state == StateOvernight {
				price, percent, mode = 108, -1.818181818, "yahoo_overnight"
			}
			if err != nil || q == nil || q.Price != price || q.ChangePercent != percent || q.UpdateMode != mode || q.State != tc.state {
				t.Fatalf("wrong price or status for the available session: %v %+v", err, q)
			}
		})
	}
}

func TestYahooChartFullDayCannotPretendToBeVerifiedOvernight(t *testing.T) {
	body := []byte(`{"chart":{"result":[{"meta":{"symbol":"VOO","currency":"USD","instrumentType":"ETF","marketState":"OVERNIGHT","regularMarketPrice":110,"previousClose":100,"regularMarketTime":1793653200,"fulldayPrice":108,"fulldayChange":8,"fulldayChangePercent":8}}]}}`)
	q, err := ParseYahooChart("VOO", body, 1, time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC))
	if err != nil || q.Price != 110 || q.State != StateClosed || q.UpdateMode != "yahoo_regular" || !strings.Contains(q.Session, "夜盘数据不可用") {
		t.Fatalf("chart response without night tick must be explicit fallback: %v %+v", err, q)
	}
}
