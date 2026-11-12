//go:build (windows || darwin) && (amd64 || arm64)

package market

import "context"

func FetchTopUSCompanies(ctx context.Context) ([2]string, error) {
	const query = `{
		"filter": [
			{"left":"market_cap_basic","operation":"greater","right":0},
			{"left":"country","operation":"equal","right":"United States"},
			{"left":"type","operation":"equal","right":"stock"},
			{"left":"subtype","operation":"in_range","right":["common"]},
			{"left":"is_primary","operation":"equal","right":true}
		],
		"options":{"lang":"en"},
		"markets":["america"],
		"symbols":{"query":{"types":[]},"tickers":[]},
		"columns":["name","market_cap_basic","isin","country","type","subtype","currency","is_primary"],
		"sort":{"sortBy":"market_cap_basic","sortOrder":"desc"},
		"range":[0,16]
	}`
	session, err := newHTTPSession()
	if err != nil {
		return [2]string{}, err
	}
	defer session.close()
	status, body, err := doHTTPRequest(ctx, session, "scanner.tradingview.com", "/america/scan", false, "POST", []byte(query))
	if err != nil {
		return [2]string{}, err
	}
	if status != 200 {
		return [2]string{}, HTTPError{StatusCode: status}
	}
	return parseTopUSCompanies(body)
}
