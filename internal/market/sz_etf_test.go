package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// 构造与实测腾讯返回的 33 个前导字段一致的 ETF 行情。
func etfRow(variable, code, price, previous, timestamp string) string {
	fields := make([]string, 33)
	fields[0], fields[1], fields[2] = "51", "ETF", code
	fields[3], fields[4], fields[30] = price, previous, timestamp
	fields[31], fields[32] = "0", "0"
	return fmt.Sprintf("v_%s=\"%s\";\r\n", variable, strings.Join(fields, "~"))
}

func TestSZETFUsesNamesInAllQuoteTooltips(t *testing.T) {
	const expectedMenu = "华安纳指ETF / 华夏标普500ETF"
	if got := GroupByID(GroupETF159632159655).MenuLabel; got != expectedMenu {
		t.Fatalf("group menu = %q, want %q", got, expectedMenu)
	}
	for _, tc := range []struct {
		symbol, legacyAlias, alias, fullName string
	}{
		{"159632.SZ", "159632", "纳指ETF", "纳斯达克ETF华安"},
		{"159655.SZ", "159655", "标普500ETF", "标普500ETF华夏"},
	} {
		t.Run(tc.symbol, func(t *testing.T) {
			if got := AliasFor(tc.symbol); got != tc.alias {
				t.Fatalf("alias = %q, want %q", got, tc.alias)
			}
			quote := Quote{Symbol: tc.symbol, Alias: tc.legacyAlias, Description: commodityDescriptions[tc.symbol], Price: 2.509, RegularPrice: 2.447, ChangeAbs: 0.062, ChangePercent: 2.53, State: StateRegular, Session: "盘中", TradeTime: time.Now().Add(-time.Second).Unix()}
			closedQuote := quote
			closedQuote.State, closedQuote.Session = StateClosed, "休市"
			for _, state := range []struct {
				name string
				view View
			}{
				{"normal", View{Symbol: tc.symbol, Alias: tc.legacyAlias, Quote: &quote, Healthy: true}},
				{"closed", View{Symbol: tc.symbol, Alias: tc.legacyAlias, Quote: &closedQuote, Healthy: true}},
				{"missing", View{Symbol: tc.symbol, Alias: tc.legacyAlias, Reason: "行情源不可用"}},
			} {
				t.Run(state.name, func(t *testing.T) {
					if got := state.view.DisplayName(); got != tc.alias {
						t.Errorf("display name = %q, want %q", got, tc.alias)
					}
					text := state.view.Tooltip()
					if !strings.Contains(text, tc.alias) || !strings.Contains(text, tc.fullName) ||
						strings.Contains(text, "159632") || strings.Contains(text, "159655") || strings.Contains(text, "SZSE:") {
						t.Fatalf("tooltip should use fund names without codes: %s", text)
					}
					if state.view.Quote != nil && (!strings.Contains(text, "2.509") || !strings.Contains(text, "行情时间")) {
						t.Fatalf("tooltip lost price precision or quote timestamp: %s", text)
					}
					if state.view.Quote == nil && !strings.Contains(text, "行情源不可用") {
						t.Fatalf("tooltip lost error detail: %s", text)
					}
				})
			}
			encoded, err := json.Marshal(View{Symbol: tc.symbol, Quote: &quote})
			if err != nil || !strings.Contains(string(encoded), tc.symbol) {
				t.Fatalf("internal JSON must retain instrument code: %q, %v", encoded, err)
			}
		})
	}
	other := View{Symbol: "VOO", Quote: &Quote{Symbol: "VOO", Price: 699.05, Description: "Vanguard S&P 500 ETF"}, Healthy: true}.Tooltip()
	if !strings.Contains(other, "NYSEArca:VOO") {
		t.Fatalf("existing ticker tooltip changed: %s", other)
	}
}

func TestSZETFGroupTencentQuotes(t *testing.T) {
	defer SetActiveGroup(DefaultGroup)
	g := SetActiveGroup(GroupETF159632159655)
	want := []string{"159632.SZ", "159655.SZ"}
	if len(g.Symbols) != 2 || g.Symbols[0] != want[0] || g.Symbols[1] != want[1] {
		t.Fatalf("unexpected group symbols: %+v", g.Symbols)
	}
	query, ok := TencentQueryForSymbols(g.Symbols)
	if !ok || query != "sz159632,sz159655" {
		t.Fatalf("unexpected ETF quote query %q (supported=%v)", query, ok)
	}
	now := time.Date(2026, 10, 28, 2, 1, 57, 0, time.UTC)
	response := etfRow("sz159632", "159632", "2.509", "2.447", "20261028100157") +
		etfRow("sz159655", "159655", "2.012", "1.995", "20261028100154")
	batch, err := ParseTencentQuoteBatch(want, []byte(response), 61, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		symbol, alias string
		price, prev   float64
		timeOffset    time.Duration
	}{
		{"159632.SZ", "纳指ETF", 2.509, 2.447, 0},
		{"159655.SZ", "标普500ETF", 2.012, 1.995, -3 * time.Second},
	} {
		result := batch[tc.symbol]
		if result.Err != "" || result.Quote == nil {
			t.Fatalf("%s missing: %+v", tc.symbol, result)
		}
		q := result.Quote
		if q.Alias != tc.alias || q.Price != tc.price || q.RegularPrice != tc.prev ||
			q.Currency != "CNY" || q.QuoteType != "ETF" || q.State != StateRegular ||
			q.QuoteSource != "Tencent Finance" || q.UpdateMode != "sz_etf_quote" || q.TradeTime != now.Add(tc.timeOffset).Unix() {
			t.Fatalf("%s parsed incorrectly: %+v", tc.symbol, q)
		}
		if math.Abs(q.ChangePercent-(tc.price-tc.prev)/tc.prev*100) > 1e-9 {
			t.Fatalf("%s has incorrect change percentage: %+v", tc.symbol, q)
		}
		tooltip := (View{Symbol: tc.symbol, Quote: q, Healthy: true}).Tooltip()
		if !strings.Contains(tooltip, fmt.Sprintf("%.3f", tc.price)) ||
			!strings.Contains(tooltip, fmt.Sprintf("%.3f", tc.prev)) || !strings.Contains(tooltip, "行情时间") ||
			!strings.Contains(tooltip, "行情数据正常") || strings.Contains(tooltip, "实时数据正常") {
			t.Fatalf("%s tooltip missing quote details or overstating freshness: %s", tc.symbol, tooltip)
		}
	}
	if PriceDecimals("159632.SZ") != 3 || PriceDecimals("159655.SZ") != 3 || PriceDecimals("VOO") != 2 {
		t.Fatal("incorrect displayed price precision")
	}
}

func TestSZETFRejectsInvalidSymbolOrTimestampIndependently(t *testing.T) {
	now := time.Date(2026, 10, 28, 2, 1, 57, 0, time.UTC)
	syms := []string{"159632.SZ", "159655.SZ"}
	valid := etfRow("sz159655", "159655", "2.012", "1.995", "20261028100154")
	for _, tc := range []struct{ name, code, timestamp string }{
		{"wrong_code", "159655", "20261028100157"},
		{"missing_time", "159632", ""},
		{"bad_time", "159632", "not-a-date"},
		{"future_time", "159632", "20261028110000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := etfRow("sz159632", tc.code, "2.509", "2.447", tc.timestamp)
			batch, err := ParseTencentQuoteBatch(syms, []byte(bad+valid), 61, now)
			if err != nil || batch["159632.SZ"].Quote != nil || batch["159632.SZ"].Err == "" || batch["159655.SZ"].Quote == nil {
				t.Fatalf("independent validation failed: batch=%+v err=%v", batch, err)
			}
		})
	}
	// 行情时间较旧可以是长时间无成交；仅凭时间年龄不将其当作请求故障。
	oldQuote := etfRow("sz159632", "159632", "2.509", "2.447", "20261028093100")
	batch, err := ParseTencentQuoteBatch(syms, []byte(oldQuote+valid), 61, now)
	if err != nil || batch["159632.SZ"].Quote == nil || batch["159632.SZ"].Quote.State != StateRegular {
		t.Fatalf("old same-day quote incorrectly rejected: %+v err=%v", batch, err)
	}
}

func TestSZETFTradingSessions(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	for _, tc := range []struct {
		name    string
		at      time.Time
		tradeAt time.Time
		want    SessionState
		label   string
	}{
		{"before_market", time.Date(2026, 10, 28, 9, 0, 0, 0, zone), time.Date(2026, 10, 27, 15, 0, 0, 0, zone), StateClosed, "休市"},
		{"auction", time.Date(2026, 10, 28, 9, 20, 0, 0, zone), time.Date(2026, 10, 28, 9, 20, 0, 0, zone), StatePreMarket, "集合竞价"},
		{"morning", time.Date(2026, 10, 28, 9, 30, 0, 0, zone), time.Date(2026, 10, 28, 9, 30, 0, 0, zone), StateRegular, "盘中"},
		{"lunch", time.Date(2026, 10, 28, 11, 30, 0, 0, zone), time.Date(2026, 10, 28, 11, 29, 0, 0, zone), StateClosed, "午间休市"},
		{"afternoon", time.Date(2026, 10, 28, 13, 0, 0, 0, zone), time.Date(2026, 10, 28, 13, 0, 0, 0, zone), StateRegular, "盘中"},
		{"close", time.Date(2026, 10, 28, 15, 0, 0, 0, zone), time.Date(2026, 10, 28, 15, 0, 0, 0, zone), StateClosed, "休市"},
		{"weekend", time.Date(2026, 10, 31, 10, 0, 0, 0, zone), time.Date(2026, 10, 30, 15, 0, 0, 0, zone), StateClosed, "周末休市"},
		{"missing_current_day", time.Date(2026, 10, 28, 10, 0, 0, 0, zone), time.Date(2026, 10, 27, 15, 0, 0, 0, zone), StateClosed, "未更新"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, sym := range []string{"159632.SZ", "159655.SZ"} {
				state, label := resolveIndexSession(sym, tc.at, tc.tradeAt.Unix())
				if state != tc.want || !strings.Contains(label, tc.label) {
					t.Fatalf("%s at %v: got %q %q; want %q containing %q", sym, tc.at, state, label, tc.want, tc.label)
				}
			}
		})
	}
}
