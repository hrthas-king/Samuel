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
		// During the 09:00–10:00 break the newest exchange date alone
		// can no longer supply the preceding afternoon.
		for _, endpoint := range []string{"day", "minute"} {
			path := "/appstock/app/" + endpoint + "/query?code=" + url.QueryEscape(q.tencentCode)
			status, body, err := doHTTPRequestLimited(ctx, session, "web.ifzq.gtimg.cn", path, false, "GET", nil, maxIntradayBody)
			if err == nil && status == 200 {
				if points, _ := parseTencentIntraday(q, body, time.Now()); len(points) > 0 {
					return points
				}
			}
		}
		return nil
	}
	path := yahooIntradayPath(q, time.Now())
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
