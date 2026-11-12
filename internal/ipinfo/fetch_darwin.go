//go:build darwin

package ipinfo

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func Fetch(ctx context.Context) (Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("Cloudflare HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return Info{}, err
	}
	return ParseTrace(body)
}
