//go:build (windows || darwin) && (amd64 || arm64)

package market

import (
	"context"
	"net/url"
	"time"
)

func NewIntradayClient(ctx context.Context, notify func()) *IntradayClient {
	return &IntradayClient{ctx: ctx, notify: notify, fetch: fetchIntraday}
}

func fetchIntraday(parent context.Context, q IntradayQuery) []IntradayPoint {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	session, err := newHTTPSession()
	if err != nil {
		return nil
	}
	defer session.close()
	const maxIntradayBody = 128 << 10
	if q.tencentCode != "" {
		path := "/appstock/app/minute/query?code=" + url.QueryEscape(q.tencentCode)
		status, body, err := doHTTPRequestLimited(ctx, session, "web.ifzq.gtimg.cn", path, false, "GET", nil, maxIntradayBody)
		if err != nil || status != 200 {
			return nil
		}
		points, _ := parseTencentIntraday(q, body, time.Now())
		return points
	}
	params := "range=1d&interval=5m"
	if q.extended {
		params = "range=1d&interval=2m&includePrePost=true"
	}
	path := "/v8/finance/chart/" + url.PathEscape(q.symbol) + "?" + params
	for _, host := range []string{FallbackHost, PrimaryHost} {
		if ctx.Err() != nil {
			break
		}
		status, body, err := doHTTPRequestLimited(ctx, session, host, path, false, "GET", nil, maxIntradayBody)
		if err == nil && status == 200 {
			if points, err := parseYahooIntraday(q, body, time.Now()); err == nil {
				return points
			}
		}
	}
	return nil
}
