//go:build darwin

package ipinfo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hrthas-king/Samuel/internal/monitor"
)

// MeasureEgress reads the public IP from the same fresh connection used for latency.
func MeasureEgress(ctx context.Context) (monitor.Latency, Info, error) {
	const target = "https://www.cloudflare.com/cdn-cgi/trace"
	sample := monitor.Latency{Target: target, Method: "https"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		sample.Error = err.Error()
		return sample, Info{}, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	client := http.Client{Transport: transport}
	start := time.Now()
	resp, err := client.Do(req)
	sample.At = time.Now()
	if err != nil {
		sample.Error = err.Error()
		return sample, Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("Cloudflare HTTP %d", resp.StatusCode)
		sample.Error = err.Error()
		return sample, Info{}, err
	}
	sample.RTT = sample.At.Sub(start)
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return sample, Info{}, err
	}
	info, err := ParseTrace(body)
	return sample, info, err
}
