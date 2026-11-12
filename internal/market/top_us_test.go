package market

import "testing"

func TestTopUSCompaniesRanksDistinctDomesticIssuers(t *testing.T) {
	body := []byte(`{"data":[
		{"d":["BRK.B",310,"US0846707026","United States","stock","common","USD",true]},
		{"d":["BRK.A",320,"US0846701086","United States","stock","common","USD",true]},
		{"d":["TSM",900,"US8740391003","Taiwan","stock","dr","USD",true]},
		{"d":["AAPL",280,"US0378331005","United States","stock","common","USD",true]},
		{"d":["SPY",800,"US78462F1030","United States","fund","etf","USD",true]},
		{"d":["NVDA",700,"US67066G1040","United States","stock","common","USD",false]}
	]}`)
	pair, err := parseTopUSCompanies(body)
	if want := [2]string{"BRK-A", "AAPL"}; err != nil || pair != want {
		t.Fatalf("ranked %v, want %v, error=%v", pair, want, err)
	}
}

func TestTopUSCompaniesRejectsIncompleteRanking(t *testing.T) {
	body := []byte(`{"data":[
		{"d":["GOOGL",350,"US02079K3059","United States","stock","common","USD",true]},
		{"d":["GOOG",340,"US02079K1079","United States","stock","common","USD",true]}
	]}`)
	if pair, err := parseTopUSCompanies(body); err == nil || pair != ([2]string{}) {
		t.Fatalf("partial ranking replaced the pair: %v, error=%v", pair, err)
	}
}
