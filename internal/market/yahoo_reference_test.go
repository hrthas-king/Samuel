package market

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestYahooPremarketKeepsItsOwnCoherentReferenceWhenHeadlineCloseDiffers(t *testing.T) {
	// Yahoo VOO web page and v7 both displayed 690.71 / -0.66 (-0.10%)
	// at 04:09:10 EST, while the separate regular close remained 691.32.
	body := []byte(`{"quoteResponse":{"result":[{"symbol":"VOO","currency":"USD","quoteType":"ETF","marketState":"PRE","gmtOffSetMilliseconds":-18000000,"regularMarketPrice":691.32,"regularMarketPreviousClose":694.81,"regularMarketChange":-3.48999,"regularMarketChangePercent":-0.502294,"regularMarketTime":1793653200,"preMarketPrice":690.71,"preMarketChange":-0.65997314,"preMarketChangePercent":-0.095458746,"preMarketTime":1793696950}]}}`)
	now := time.Unix(1793696950, 0).Add(time.Second)
	batch, err := ParseYahooQuoteBatchForSymbols([]string{"VOO"}, body, 1, now)
	q := batch["VOO"].Quote
	if err != nil || q == nil || q.Price != 690.71 || math.Abs(q.ChangePercent-(-0.095458746)) > 1e-9 || q.UpdateMode != "yahoo_pre" || math.Abs(q.Previous()-(690.71+0.65997314)) > 1e-7 {
		t.Fatalf("Yahoo's verified page tuple was rejected because a separate close differs: %v %+v", err, q)
	}
}

func TestYahooExtendedSessionsValidateTheirOwnReferenceWithoutLosingPrices(t *testing.T) {
	for _, tc := range []struct{ name, at, closeAt, field, mode string }{
		{"pre", "2026-11-03T12:00:00Z", "2026-11-02T21:00:00Z", "preMarket", "yahoo_pre"},
		{"post", "2026-11-03T23:00:00Z", "2026-11-03T21:00:00Z", "postMarket", "yahoo_post"},
		{"overnight", "2026-11-03T06:00:00Z", "2026-11-02T21:00:00Z", "overnightMarket", "yahoo_overnight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.at)
			closeAt, _ := time.Parse(time.RFC3339, tc.closeAt)
			item := map[string]any{"symbol": "QQQ", "quoteType": "ETF", "currency": "USD", "regularMarketPrice": 110.05, "regularMarketPreviousClose": 100, "regularMarketTime": closeAt.Unix(), tc.field + "Price": 108, tc.field + "Change": -2, tc.field + "ChangePercent": -2.0 / 110 * 100, tc.field + "Time": now.Add(-time.Second).Unix()}
			body, _ := json.Marshal(map[string]any{"quoteResponse": map[string]any{"result": []any{item}}})
			batch, err := ParseYahooQuoteBatchForSymbols([]string{"QQQ"}, body, 1, now)
			q := batch["QQQ"].Quote
			if err != nil || q == nil || q.Price != 108 || q.Previous() != 110 || q.RegularPrice != 110.05 || q.UpdateMode != tc.mode || !strings.Contains(q.ChangeBasis, "对应基准") {
				t.Fatalf("independent session reference must preserve the verified tuple: %v %+v", err, q)
			}
			item[tc.field+"ChangePercent"] = 9
			body, _ = json.Marshal(map[string]any{"quoteResponse": map[string]any{"result": []any{item}}})
			batch, _ = ParseYahooQuoteBatchForSymbols([]string{"QQQ"}, body, 1, now)
			if q = batch["QQQ"].Quote; q == nil || q.UpdateMode != "yahoo_regular" || q.State != StateClosed {
				t.Fatal("an inconsistent session tuple was accepted:", q)
			}
		})
	}
}
