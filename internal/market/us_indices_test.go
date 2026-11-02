package market

import (
	"testing"
	"time"
)

func TestUSCashIndicesAreDistinctFromETFAndKeepSourceDelays(t *testing.T) {
	group := GroupByID(GroupUSIndices)
	if len(group.Symbols) != 2 || group.Symbols[0] != "^NDX" || group.Symbols[1] != "^GSPC" {
		t.Fatal("cash indices replaced with ETFs")
	}
	body := []byte(`{"data":[{"s":"NASDAQ:NDX","d":[28384.8452,-3.4611441519,-1017.6632,"index",null,"delayed_streaming_900","NASDAQ-100 Index","out_of_session"]},{"s":"SP:SPX","d":[7580.07,-1.81752476235,-140.32,"index",null,"delayed_streaming_600","S&P 500","out_of_session"]}]}`)
	batch, err := parseIndexQuotes(group.Symbols, body, 20, time.Now())
	if err != nil || batch["^NDX"].Quote == nil || batch["^GSPC"].Quote == nil {
		t.Fatalf("cash index quote parsing failed: %v %#v", err, batch)
	}
	if batch["^NDX"].Quote.DelaySeconds != 900 || batch["^GSPC"].Quote.DelaySeconds != 600 || batch["^NDX"].Quote.Reference || batch["^GSPC"].Quote.Currency != "USD" {
		t.Fatal("cash index identity or delays lost")
	}
	if GroupByID(GroupCNComposite).Symbols[1] != "399001.SZ" || GroupByID(GroupCNIndices).Symbols[1] != "399006.SZ" {
		t.Fatal("Shenzhen component addition removed ChiNext")
	}
}
