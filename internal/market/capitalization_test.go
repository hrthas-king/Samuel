package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCompanyCapitalizationPreservesMultiClassTotalWithRegularHeadline(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 0, 0, 0, time.UTC)
	for _, state := range []string{"REGULAR", "POST"} {
		t.Run(state, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"quoteResponse":{"result":[{"symbol":"GOOGL","currency":"USD","quoteType":"EQUITY","marketState":%q,"gmtOffSetMilliseconds":-14400000,"regularMarketPrice":100,"regularMarketChange":1,"regularMarketChangePercent":1.010101,"regularMarketTime":1793304000,"postMarketPrice":110,"postMarketChange":10,"postMarketChangePercent":10,"postMarketTime":1793304060,"marketCap":3000000000000,"sharesOutstanding":10000000000}]}}`, state))
			batch, err := ParseYahooQuoteBatchForSymbols([]string{"GOOGL"}, body, 20, now)
			if err != nil || batch["GOOGL"].Quote == nil {
				t.Fatalf("quote failed: %v %#v", err, batch)
			}
			q := batch["GOOGL"].Quote
			want := 3e12
			if q.Price != 100 || q.Capitalization == nil || math.Abs(q.Capitalization.USD-want) > 1 || q.Capitalization.Estimated {
				t.Fatalf("company-wide market cap replaced by one share class or not repriced: %#v", q.Capitalization)
			}
		})
	}
}

func TestOptionalCapitalizationNeverInvalidatesStockPricesOrLeaksIntoETF(t *testing.T) {
	for _, raw := range []string{"null", "0", "-1", `"not-a-number"`, "1e999", "{}"} {
		t.Run(raw, func(t *testing.T) {
			body := []byte(`{"quoteResponse":{"result":[{"symbol":"AAPL","currency":"USD","quoteType":"EQUITY","marketState":"REGULAR","regularMarketPrice":100,"regularMarketChange":1,"regularMarketChangePercent":1.010101,"marketCap":` + raw + `}]}}`)
			batch, err := ParseYahooQuoteBatchForSymbols([]string{"AAPL"}, body, 20, time.Now())
			q := batch["AAPL"].Quote
			if err != nil || q == nil || q.Price != 100 || q.Capitalization != nil || q.CapitalizationText() != "市值 --" {
				t.Fatalf("optional market cap poisoned valid stock price: %v %#v", err, q)
			}
		})
	}
	q := Quote{Symbol: "VOO", QuoteType: "ETF", Currency: "USD", Price: 100}
	q.setYahooCapitalization(json.RawMessage("100000000000"), 100)
	if q.Capitalization != nil || q.CapitalizationText() != "" || q.capitalizationTooltip() != "" {
		t.Fatal("ETF assets displayed as a stock company market cap")
	}
}

func TestMainlandTotalCapitalizationConvertsToUSDWithoutUsingFloatCap(t *testing.T) {
	row := customRow("sh", "600519", "贵州茅台", "GP-A")
	key, payload, _ := strings.Cut(row, "=")
	parts := strings.Split(strings.Trim(payload, `";`), "~")
	parts[44], parts[45] = "7.25", "14.50"
	body := []byte(key + `="` + strings.Join(parts, "~") + `";`)
	now := time.Date(2026, 10, 28, 2, 0, 1, 0, time.UTC)
	batch, err := ParseTencentQuoteBatch([]string{"600519.SS"}, body, 10, now)
	q := batch["600519.SS"].Quote
	if err != nil || q == nil || q.Capitalization == nil || q.Capitalization.Local != 1.45e9 || q.Capitalization.USD != 0 {
		t.Fatalf("wrong total/float field or RMB unit: %v %#v", err, q)
	}
	convertCapitalizationUSD(batch, fxQuote{rate: 7.25, at: now.Unix()})
	if q.Capitalization.USD != 2e8 || !q.Capitalization.Estimated || q.CapitalizationText() != "市值 ≈$200.00M" {
		t.Fatalf("wrong FX direction or amount: %#v", q.Capitalization)
	}
	if !strings.Contains(q.capitalizationTooltip(), "1 USD = 7.2500 CNY") {
		t.Fatal("FX conversion not disclosed")
	}
}

func TestCNYRateRejectsWrongPairDirectionAndStaleMetadata(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 0, 0, 0, time.UTC)
	valid := fmt.Sprintf(`{"chart":{"result":[{"meta":{"symbol":"CNY=X","currency":"CNY","instrumentType":"CURRENCY","regularMarketPrice":7.25,"regularMarketTime":%d}}]}}`, now.Unix())
	if fx, err := parseCNYRate([]byte(valid), now); err != nil || fx.rate != 7.25 {
		t.Fatalf("valid FX data unavailable: %#v %v", fx, err)
	}
	for _, tc := range []struct{ name, from, to string }{
		{"wrong_pair", "CNY=X", "USDCNH=X"},
		{"wrong_currency", `"currency":"CNY"`, `"currency":"USD"`},
		{"wrong_type", `"CURRENCY"`, `"INDEX"`},
		{"missing_price", "7.25", "null"},
		{"negative_price", "7.25", "-7.25"},
		{"old_rate", fmt.Sprint(now.Unix()), fmt.Sprint(now.Add(-73 * time.Hour).Unix())},
		{"future_rate", fmt.Sprint(now.Unix()), fmt.Sprint(now.Add(time.Hour).Unix())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(valid, tc.from, tc.to, 1)
			if _, err := parseCNYRate([]byte(body), now); err == nil {
				t.Fatal("unverifiable FX data accepted")
			}
		})
	}
}

func TestCapitalizationCurrencyAndCompactRounding(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{5.16e12, "市值 $5.16T"},
		{255.29e9, "市值 $255.29B"},
		{12.34e6, "市值 $12.34M"},
		{5432, "市值 $5.43K"},
		{1e12 - 1, "市值 $1.00T"},
		{0, "市值 --"},
		{math.NaN(), "市值 --"},
		{math.Inf(1), "市值 --"},
	} {
		q := Quote{QuoteType: "EQUITY", Capitalization: &Capitalization{USD: tc.value}}
		if got := q.CapitalizationText(); got != tc.want {
			t.Errorf("%.2f: got %q want %q", tc.value, got, tc.want)
		}
	}
}
