package market

import (
	"math"
	"testing"
	"time"
)

func TestParseYahooQuoteBatchAllSessions(t *testing.T) {
	SetActiveGroup(GroupVOOQQQ)
	batchJSON := []byte(`{"quoteResponse":{"result":[` +
		`{"symbol":"VOO","currency":"USD","quoteType":"ETF","longName":"Vanguard S&P 500 ETF",` +
		`"marketState":"OVERNIGHT","quoteSourceName":"BOATS Real Time Price","gmtOffSetMilliseconds":-14400000,` +
		`"regularMarketPrice":698.58,"regularMarketChange":-3.72,"regularMarketChangePercent":-0.53,"regularMarketTime":1792785600,` +
		`"postMarketPrice":699.53,"postMarketChange":0.95,"postMarketChangePercent":0.14,"postMarketTime":1792799914,` +
		`"overnightMarketPrice":699.05,"overnightMarketChange":0.47,"overnightMarketChangePercent":0.06728,"overnightMarketTime":1792997930},` +
		`{"symbol":"QQQ","currency":"USD","quoteType":"ETF","longName":"Invesco QQQ Trust",` +
		`"marketState":"OVERNIGHT","quoteSourceName":"BOATS Real Time Price","gmtOffSetMilliseconds":-14400000,` +
		`"regularMarketPrice":704.40,"regularMarketChange":-0.07,"regularMarketChangePercent":-0.01,"regularMarketTime":1792785600,` +
		`"postMarketPrice":704.88,"postMarketChange":0.48,"postMarketChangePercent":0.07,"postMarketTime":1792799985,` +
		`"overnightMarketPrice":703.55,"overnightMarketChange":-0.85,"overnightMarketChangePercent":-0.12067,"overnightMarketTime":1792997935}` +
		`]}}`)

	overnightNow := time.Date(2026, 10, 26, 6, 59, 0, 0, time.UTC)
	batch, err := ParseYahooQuoteBatch(batchJSON, 185.0, overnightNow)
	if err != nil {
		t.Fatalf("unexpected ParseYahooQuoteBatch error: %v", err)
	}
	voo := batch["VOO"].Quote
	qqq := batch["QQQ"].Quote
	if voo == nil || qqq == nil {
		t.Fatalf("expected both VOO and QQQ quotes: %+v", batch)
	}
	if voo.Price != 699.05 || math.Abs(voo.ChangePercent-0.06728) > 1e-4 || voo.State != StateOvernight || voo.Alias != "VOO" || voo.UpdateMode != "yahoo_overnight" {
		t.Fatalf("unexpected VOO overnight quote: %+v", voo)
	}
	if qqq.Price != 703.55 || math.Abs(qqq.ChangePercent - -0.12067) > 1e-4 || qqq.State != StateOvernight || qqq.Alias != "QQQ" {
		t.Fatalf("unexpected QQQ overnight quote: %+v", qqq)
	}
}

func TestMultiMarketGroupsCurrenciesAndIndices(t *testing.T) {
	defer SetActiveGroup(DefaultGroup)

	// 1. A股指数 (CNY / INDEX)
	SetActiveGroup(GroupCNIndices)
	cnJSON := []byte(`{"quoteResponse":{"result":[` +
		`{"symbol":"000001.SS","currency":"CNY","quoteType":"INDEX","shortName":"SSE Composite Index","marketState":"REGULAR","gmtOffSetMilliseconds":28800000,"regularMarketPrice":3697.52,"regularMarketChange":7.04,"regularMarketChangePercent":0.1908,"regularMarketTime":1792998024},` +
		`{"symbol":"399006.SZ","currency":"CNY","quoteType":"INDEX","shortName":"ChiNext","marketState":"REGULAR","gmtOffSetMilliseconds":28800000,"regularMarketPrice":2638.23,"regularMarketChange":-45.87,"regularMarketChangePercent":-1.7089,"regularMarketTime":1793003353}` +
		`]}}`)
	cnBatch, err := ParseYahooQuoteBatch(cnJSON, 95.0, time.Now())
	if err != nil {
		t.Fatalf("CN indices parse failed: %v", err)
	}
	if cnBatch["000001.SS"].Quote == nil || cnBatch["000001.SS"].Quote.Alias != "上证" || cnBatch["000001.SS"].Quote.State != StateRegular {
		t.Fatalf("unexpected SSE quote: %+v", cnBatch["000001.SS"])
	}
	if cnBatch["399006.SZ"].Quote == nil || cnBatch["399006.SZ"].Quote.Alias != "创指" || cnBatch["399006.SZ"].Quote.Currency != "CNY" {
		t.Fatalf("unexpected ChiNext quote: %+v", cnBatch["399006.SZ"])
	}

	// 2. 港股指数 (^HSI + HSTECH.HK 归一化为 ^HSTECH)：
	// 验证凌晨 01:20 HKT (17:20 UTC) Yahoo 返回 PREPRE 时应识别为休市 (StateClosed)，
	// 而在早间 09:15 HKT (01:15 UTC) 集合竞价窗口时才识别为盘前 (StatePreMarket)。
	SetActiveGroup(GroupHKIndices)
	hkJSON := []byte(`{"quoteResponse":{"result":[` +
		`{"symbol":"^HSI","currency":"HKD","quoteType":"INDEX","shortName":"HANG SENG INDEX","marketState":"PREPRE","gmtOffSetMilliseconds":28800000,"regularMarketPrice":23211.81,"regularMarketChange":118.44,"regularMarketChangePercent":0.5129,"regularMarketTime":1793002108},` +
		`{"symbol":"HSTECH.HK","quoteType":"INDEX","shortName":"Hang Seng TECH Index","marketState":"PREPRE","gmtOffSetMilliseconds":28800000,"regularMarketPrice":3857.53,"regularMarketChange":-14.13,"regularMarketChangePercent":-0.365,"regularMarketTime":1793002108}` +
		`]}}`)
	midnightHK := time.Date(2026, 10, 26, 17, 20, 0, 0, time.UTC) // 2026-10-27 01:20 HKT (周二凌晨休市)
	hkClosedBatch, err := ParseYahooQuoteBatch(hkJSON, 110.0, midnightHK)
	if err != nil {
		t.Fatalf("HK indices midnight parse failed: %v", err)
	}
	if hkClosedBatch["^HSI"].Quote == nil || hkClosedBatch["^HSI"].Quote.Alias != "恒指" || hkClosedBatch["^HSI"].Quote.State != StateClosed {
		t.Fatalf("expected HSI at 01:20 HKT with PREPRE to be StateClosed, got: %+v", hkClosedBatch["^HSI"].Quote)
	}
	if hkClosedBatch["^HSTECH"].Quote == nil || hkClosedBatch["^HSTECH"].Quote.Alias != "恒科" || hkClosedBatch["^HSTECH"].Quote.State != StateClosed {
		t.Fatalf("expected HSTECH at 01:20 HKT with PREPRE to be StateClosed, got: %+v", hkClosedBatch["^HSTECH"].Quote)
	}

	auctionHK := time.Date(2026, 10, 26, 1, 15, 0, 0, time.UTC) // 2026-10-26 09:15 HKT (周一盘前竞价)
	hkPreBatch, err := ParseYahooQuoteBatch(hkJSON, 110.0, auctionHK)
	if err != nil {
		t.Fatalf("HK indices auction parse failed: %v", err)
	}
	if hkPreBatch["^HSI"].Quote.State != StatePreMarket || hkPreBatch["^HSTECH"].Quote.State != StatePreMarket {
		t.Fatalf("expected HSI/HSTECH at 09:15 HKT to be StatePreMarket, got: %v / %v", hkPreBatch["^HSI"].Quote.State, hkPreBatch["^HSTECH"].Quote.State)
	}

	// 3. 欧洲指数 (EUR & POST/POSTPOST/CLOSED 现金指数收盘后均应为 StateClosed)
	SetActiveGroup(GroupEUIndices)
	euJSON := []byte(`{"quoteResponse":{"result":[` +
		`{"symbol":"^FCHI","currency":"EUR","quoteType":"INDEX","shortName":"CAC 40","marketState":"POSTPOST","gmtOffSetMilliseconds":3600000,"regularMarketPrice":7847.32,"regularMarketChange":64.50,"regularMarketChangePercent":0.8287,"regularMarketTime":1793032860},` +
		`{"symbol":"^GDAXI","currency":"EUR","quoteType":"INDEX","shortName":"DAX P","marketState":"POST","gmtOffSetMilliseconds":7200000,"regularMarketPrice":25410.84,"regularMarketChange":229.19,"regularMarketChangePercent":0.9101,"regularMarketTime":1793032214}` +
		`]}}`)
	euBatch, err := ParseYahooQuoteBatch(euJSON, 120.0, midnightHK)
	if err != nil {
		t.Fatalf("EU indices parse failed: %v", err)
	}
	if euBatch["^FCHI"].Quote == nil || euBatch["^FCHI"].Quote.Alias != "CAC40" || euBatch["^FCHI"].Quote.State != StateClosed {
		t.Fatalf("unexpected CAC40 quote: %+v", euBatch["^FCHI"].Quote)
	}
	if euBatch["^GDAXI"].Quote == nil || euBatch["^GDAXI"].Quote.Alias != "DAX" || euBatch["^GDAXI"].Quote.State != StateClosed {
		t.Fatalf("unexpected DAX quote: %+v", euBatch["^GDAXI"].Quote)
	}
}

func TestFiveStateSessionResolutionAndFallback(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) // 08:00 EDT (美股盘前时段)
	cases := []struct {
		raw   string
		want  SessionState
		badge string
	}{
		{"PRE", StatePreMarket, "☀"},
		{"PREPRE", StatePreMarket, "☀"},
		{"REGULAR", StateRegular, "🟢"},
		{"POST", StatePostMarket, "🔵"},
		{"POSTPOST", StateClosed, "⚪"},
		{"OVERNIGHT", StateOvernight, "🌙"},
		{"CLOSED", StateClosed, "⚪"},
	}
	for _, tc := range cases {
		got, _ := ResolveSessionState(tc.raw, now, -14400)
		if got != tc.want || got.Badge() != tc.badge {
			t.Fatalf("ResolveSessionState(%q) = (%v, %q), want (%v, %q)", tc.raw, got, got.Badge(), tc.want, tc.badge)
		}
	}

	// 验证 A股/港股/日韩指数在午夜 PREPRE 时必须返回休市 StateClosed，绝不能误报太阳 StatePreMarket
	midnightCST := time.Date(2026, 10, 26, 17, 25, 0, 0, time.UTC) // 01:25 CST
	if got, _ := ResolveSessionStateForType("PREPRE", "INDEX", midnightCST, 28800); got != StateClosed {
		t.Fatalf("expected 01:25 CST INDEX PREPRE to be StateClosed, got %v", got)
	}

	// Clock fallback when marketState is empty
	stOvn, _ := ResolveSessionState("", time.Date(2026, 10, 26, 6, 30, 0, 0, time.UTC), -14400)
	if stOvn != StateOvernight {
		t.Fatalf("expected clock fallback StateOvernight, got %v", stOvn)
	}
	stPre, _ := ResolveSessionState("", time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), -14400)
	if stPre != StatePreMarket {
		t.Fatalf("expected clock fallback StatePreMarket, got %v", stPre)
	}
	stReg, _ := ResolveSessionState("", time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC), -14400)
	if stReg != StateRegular {
		t.Fatalf("expected clock fallback StateRegular, got %v", stReg)
	}
	stPost, _ := ResolveSessionState("", time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC), -14400)
	if stPost != StatePostMarket {
		t.Fatalf("expected clock fallback StatePostMarket, got %v", stPost)
	}
	stWknd, _ := ResolveSessionState("", time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC), -14400)
	if stWknd != StateClosed {
		t.Fatalf("expected clock fallback StateClosed on Saturday, got %v", stWknd)
	}
}

func TestParseYahooChartAndSession(t *testing.T) {
	SetActiveGroup(GroupVOOQQQ)
	vooJSON := []byte(`{"chart":{"result":[{"meta":{` +
		`"currency":"USD","symbol":"VOO","instrumentType":"ETF",` +
		`"longName":"Vanguard S&P 500 ETF","gmtoffset":-14400,` +
		`"regularMarketPrice":698.58,"regularMarketChangePercent":-0.53,` +
		`"fulldayPrice":699.16,"fulldayChange":-3.14,"fulldayChangePercent":-0.447,` +
		`"chartPreviousClose":702.3` +
		`}}]}}`)

	qqqJSON := []byte(`{"chart":{"result":[{"meta":{` +
		`"currency":"USD","symbol":"QQQ","instrumentType":"ETF",` +
		`"longName":"Invesco QQQ Trust","gmtoffset":-14400,` +
		`"regularMarketPrice":704.4,"regularMarketChangePercent":-0.01,` +
		`"fulldayPrice":703.68,"fulldayChange":-0.79,"fulldayChangePercent":-0.112,` +
		`"chartPreviousClose":704.47` +
		`}}]}}`)

	overnightTime := time.Date(2026, 10, 26, 6, 30, 0, 0, time.UTC)
	qVOO, err := ParseYahooChart("VOO", vooJSON, 120.0, overnightTime)
	if err != nil {
		t.Fatalf("unexpected VOO parse error: %v", err)
	}
	if qVOO.Price != 698.58 || math.Abs(qVOO.ChangeAbs-(-3.72)) > 1e-4 || qVOO.State != StateClosed || qVOO.UpdateMode != "yahoo_regular" {
		t.Fatalf("unexpected VOO quote: %+v", qVOO)
	}

	preTime := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	qQQQ, err := ParseYahooChart("QQQ", qqqJSON, 110.0, preTime)
	if err != nil {
		t.Fatalf("unexpected QQQ parse error: %v", err)
	}
	if qQQQ.Price != 704.40 || math.Abs(qQQQ.ChangeAbs - -0.07) > 1e-4 || qQQQ.State != StatePreMarket || qQQQ.UpdateMode != "yahoo_regular" {
		t.Fatalf("unexpected QQQ quote: %+v", qQQQ)
	}

	tr := NewTracker()
	tr.ApplyBatch(map[string]SymbolResult{
		"VOO": {Quote: &qVOO},
		"QQQ": {Quote: &qQQQ},
	}, overnightTime)
	views := tr.Views(overnightTime.Add(time.Second), 2)
	if len(views) != 2 || !views[0].Healthy || !views[1].Healthy {
		t.Fatalf("expected both VOO and QQQ healthy: %+v", views)
	}

	tr.FailAll("transient proxy switch")
	views = tr.Views(overnightTime.Add(2*time.Second), 2)
	if !views[0].Healthy || !views[1].Healthy {
		t.Fatalf("expected 1 transient failure to stay healthy: %+v", views)
	}
	tr.FailAll("transient proxy switch 2")
	views = tr.Views(overnightTime.Add(4*time.Second), 2)
	if !views[0].Healthy || !views[1].Healthy {
		t.Fatalf("expected 2 transient failures to stay healthy: %+v", views)
	}
	tr.FailAll("persistent failure 3")
	views = tr.Views(overnightTime.Add(6*time.Second), 2)
	if views[0].Healthy || views[1].Healthy {
		t.Fatalf("expected 3rd consecutive failure to mark unhealthy: %+v", views)
	}

	// Test dynamic group switch on Tracker
	tr.SetGroup(GroupAsiaIndices)
	asiaViews := tr.Views(overnightTime, 2)
	if len(asiaViews) != 2 || asiaViews[0].Alias != "日经" || asiaViews[1].Alias != "韩指" || asiaViews[0].Healthy {
		t.Fatalf("unexpected asiaViews after SetGroup: %+v", asiaViews)
	}

	badVOO := []byte(`{"chart":{"result":[{"meta":{` +
		`"currency":"USD","symbol":"VOO","instrumentType":"ETF",` +
		`"fulldayPrice":699.16,"fulldayChange":-3.14,"fulldayChangePercent":5.55` +
		`}}]}}`)
	if _, err := ParseYahooChart("VOO", badVOO, 50.0, overnightTime); err == nil {
		t.Fatal("expected inconsistent fulldayChangePercent to fail validation")
	}
}

func TestTencentQueryForSymbols(t *testing.T) {
	qCN, okCN := TencentQueryForSymbols([]string{"000001.SS", "399006.SZ"})
	if !okCN || qCN != "sh000001,sz399006" {
		t.Fatalf("unexpected CN query: %q, ok=%v", qCN, okCN)
	}

	qHK, okHK := TencentQueryForSymbols([]string{"^HSI", "^HSTECH"})
	if !okHK || qHK != "r_hkHSI,r_hkHSTECH" {
		t.Fatalf("unexpected HK query: %q, ok=%v", qHK, okHK)
	}

	qGoldOil, okGoldOil := TencentQueryForSymbols([]string{"GC=F", "BZ=F"})
	if !okGoldOil || qGoldOil != "hf_GC,hf_OIL" {
		t.Fatalf("unexpected Gold/Oil query: %q, ok=%v", qGoldOil, okGoldOil)
	}

	qAsia, okAsia := TencentQueryForSymbols([]string{"^N225", "^KS11"})
	if !okAsia || qAsia != "gzN225,gzKS11" {
		t.Fatalf("unexpected Asia query: %q, ok=%v", qAsia, okAsia)
	}

	qEU, okEU := TencentQueryForSymbols([]string{"^FCHI", "^GDAXI"})
	if !okEU || qEU != "gzFCHI,gzGDAXI" {
		t.Fatalf("unexpected EU query: %q, ok=%v", qEU, okEU)
	}

	_, okUS := TencentQueryForSymbols([]string{"VOO", "QQQ"})
	if okUS {
		t.Fatalf("expected VOO/QQQ to not match Tencent query")
	}
}

func TestParseTencentQuoteBatch_AsiaIndices(t *testing.T) {
	// Provider timestamps are Beijing time and must precede the first sample.
	raw := []byte("v_gzN225=\"N225~日经225指数~2026-10-27 08:45:26~63254.28~1064.63~1.71~AP~\";\r\n" +
		"v_gzKS11=\"KS11~韩国首尔综合~2026-10-27 08:40:00~5626.49~157.02~2.87~AP~\";\r\n")
	syms := []string{"^N225", "^KS11"}

	// Tuesday 10:00 JST/KST (01:00 UTC) -> Regular trading for both
	tuesdayMorning := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	batch, err := ParseTencentQuoteBatch(syms, raw, 60.0, tuesdayMorning)
	if err != nil {
		t.Fatalf("unexpected Asia parse error: %v", err)
	}
	n225 := batch["^N225"].Quote
	ks11 := batch["^KS11"].Quote
	if n225 == nil || ks11 == nil {
		t.Fatalf("expected both Asia quotes: %+v", batch)
	}
	if n225.Price != 63254.28 || n225.Alias != "日经" || n225.Currency != "JPY" || n225.State != StateRegular {
		t.Fatalf("unexpected Nikkei quote: %+v", n225)
	}
	if ks11.Price != 5626.49 || ks11.Alias != "韩指" || ks11.Currency != "KRW" || ks11.State != StateRegular {
		t.Fatalf("unexpected KOSPI quote: %+v", ks11)
	}

	// Tuesday 12:00 JST (03:00 UTC) -> Tokyo lunch break (StateClosed), Seoul still trading (StateRegular)
	tuesdayLunch := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	batchLunch, err := ParseTencentQuoteBatch(syms, raw, 60.0, tuesdayLunch)
	if err != nil {
		t.Fatalf("unexpected lunch batch error: %v", err)
	}
	if batchLunch["^N225"].Quote.State != StateClosed || batchLunch["^N225"].Quote.Session != "午间休市 (Lunch Break)" {
		t.Fatalf("expected Nikkei at 12:00 JST to be lunch break StateClosed, got %v (%s)",
			batchLunch["^N225"].Quote.State, batchLunch["^N225"].Quote.Session)
	}
	if batchLunch["^KS11"].Quote.State != StateRegular {
		t.Fatalf("expected KOSPI at 12:00 KST to be StateRegular, got %v", batchLunch["^KS11"].Quote.State)
	}

	// Tuesday 16:00 JST (07:00 UTC) -> Post-market close for both (StateClosed)
	tuesdayAfterClose := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	batchClose, err := ParseTencentQuoteBatch(syms, raw, 60.0, tuesdayAfterClose)
	if err != nil {
		t.Fatalf("unexpected close batch error: %v", err)
	}
	if batchClose["^N225"].Quote.State != StateClosed || batchClose["^KS11"].Quote.State != StateClosed {
		t.Fatalf("expected both closed after 15:30, got %v / %v",
			batchClose["^N225"].Quote.State, batchClose["^KS11"].Quote.State)
	}
}

func TestParseTencentQuoteBatch_Commodities(t *testing.T) {
	raw := []byte("v_hf_GC=\"4019.64,-0.46,4019.70,4019.90,4033.50,4016.70,11:16:58,4038.40,4032.80,0,1,1,2026-10-27,\xc5\xa6\xd4\xbc\xbb\xc6\xbd\xf0\";\r\n" +
		"v_hf_OIL=\"109.65,1.20,109.60,109.70,110.20,108.50,11:17:02,108.35,108.35,0,1,1,2026-10-27,\xb2\xbc\xc2\xd7\xcc\xd8\xd4\xad\xd3\xcd\";\r\n")
	syms := []string{"GC=F", "BZ=F"}
	tuesdayNoon := time.Date(2026, 10, 27, 4, 0, 0, 0, time.UTC)
	batch, err := ParseTencentQuoteBatch(syms, raw, 65.0, tuesdayNoon)
	if err != nil {
		t.Fatalf("unexpected ParseTencentQuoteBatch error: %v", err)
	}
	gc := batch["GC=F"].Quote
	bz := batch["BZ=F"].Quote
	if gc == nil || bz == nil {
		t.Fatalf("expected both quotes, got: %+v", batch)
	}
	if gc.Price != 4019.64 || gc.Alias != "黄金" || gc.Currency != "USD" || gc.QuoteType != "FUTURE" {
		t.Fatalf("unexpected GC quote: %+v", gc)
	}
	if gc.State != StateRegular || gc.Session != "24h 实时交易 (Regular)" {
		t.Fatalf("unexpected GC session state: %v (%s)", gc.State, gc.Session)
	}
	if bz.Price != 109.65 || bz.Alias != "布油" || bz.QuoteSource != "Tencent Real Time" {
		t.Fatalf("unexpected BZ quote: %+v", bz)
	}

	saturdayNoon := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	batchWknd, err := ParseTencentQuoteBatch(syms, raw, 65.0, saturdayNoon)
	if err != nil {
		t.Fatalf("unexpected error on weekend: %v", err)
	}
	if batchWknd["GC=F"].Quote.State != StateClosed || batchWknd["BZ=F"].Quote.State != StateClosed {
		t.Fatalf("expected StateClosed on weekend, got %v / %v", batchWknd["GC=F"].Quote.State, batchWknd["BZ=F"].Quote.State)
	}
}

func TestParseTencentQuoteBatch_CNAndHK(t *testing.T) {
	raw := []byte("v_sh000001=\"1~上证指数~000001~3691.37~3697.52~3698.10~192837~120392~72445~3691.37~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~~20261027111742~-6.15~-0.17~3705.12~3688.02~3691.37/192837/24567890123~192837~245678~0.85~12.50~\";\r\n" +
		"v_sz399006=\"1~创业板指~399006~2630.50~2638.23~2640.10~92837~80392~12445~2630.50~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~~20261027111742~-7.73~-0.29~2645.12~2628.02~2630.50/92837/14567890123~92837~145678~1.15~15.50~\";\r\n")
	syms := []string{"000001.SS", "399006.SZ"}
	tuesdayMorning := time.Date(2026, 10, 27, 3, 17, 42, 0, time.UTC)
	batch, err := ParseTencentQuoteBatch(syms, raw, 55.0, tuesdayMorning)
	if err != nil {
		t.Fatalf("unexpected ParseTencentQuoteBatch error: %v", err)
	}
	sh := batch["000001.SS"].Quote
	sz := batch["399006.SZ"].Quote
	if sh == nil || sz == nil {
		t.Fatalf("expected both quotes, got: %+v", batch)
	}
	if sh.Price != 3691.37 || sh.Alias != "上证" || sh.State != StateRegular || sh.Currency != "CNY" {
		t.Fatalf("unexpected SH quote: %+v", sh)
	}
	if sz.Price != 2630.50 || sz.Alias != "创指" || sz.State != StateRegular || sz.Currency != "CNY" {
		t.Fatalf("unexpected SZ quote: %+v", sz)
	}

	lunchTime := time.Date(2026, 10, 27, 4, 15, 0, 0, time.UTC)
	batchLunch, err := ParseTencentQuoteBatch(syms, raw, 55.0, lunchTime)
	if err != nil {
		t.Fatalf("unexpected lunch batch error: %v", err)
	}
	if batchLunch["000001.SS"].Quote.State != StateClosed || batchLunch["000001.SS"].Quote.Session != "午间休市 (Lunch Break)" {
		t.Fatalf("expected lunch break StateClosed, got %v (%s)", batchLunch["000001.SS"].Quote.State, batchLunch["000001.SS"].Quote.Session)
	}

	rawHK := []byte("v_r_hkHSI=\"100~恒生指数~HSI~23211.81~23093.37~23150.00~56789~0~0~23211.81~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~~2026/10/27 11:17:43~118.44~0.51~23250.00~23080.00~23211.81/56789/12345678901~56789~123456~0.00~0.00~\";\r\n" +
		"v_r_hkHSTECH=\"100~恒生科技指数~HSTECH~3857.53~3871.66~3860.00~45678~0~0~3857.53~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~~2026/10/27 11:17:43~-14.13~-0.36~3870.00~3840.00~3857.53/45678/9876543210~45678~98765~0.00~0.00~\";\r\n")
	symsHK := []string{"^HSI", "^HSTECH"}
	batchHK, err := ParseTencentQuoteBatch(symsHK, rawHK, 60.0, tuesdayMorning)
	if err != nil {
		t.Fatalf("unexpected HK batch error: %v", err)
	}
	hsi := batchHK["^HSI"].Quote
	hstech := batchHK["^HSTECH"].Quote
	if hsi == nil || hstech == nil {
		t.Fatalf("expected HK quotes, got: %+v", batchHK)
	}
	if hsi.Price != 23211.81 || hsi.Alias != "恒指" || hsi.Currency != "HKD" || hsi.State != StateRegular {
		t.Fatalf("unexpected HSI quote: %+v", hsi)
	}
	if hstech.Price != 3857.53 || hstech.Alias != "恒科" || hstech.Currency != "HKD" {
		t.Fatalf("unexpected HSTECH quote: %+v", hstech)
	}
}
