//go:build darwin && (amd64 || arm64)

package market

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type httpSession struct {
	transport          *http.Transport
	withCookies, plain http.Client
}

func newHTTPSession() (*httpSession, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	session := &httpSession{transport: transport}
	session.withCookies = http.Client{Transport: transport, Jar: jar, CheckRedirect: httpsOnly}
	session.plain = http.Client{Transport: transport, CheckRedirect: httpsOnly}
	_ = session.setTimeouts(800, TimeoutMillis, TimeoutMillis, TimeoutMillis)
	return session, nil
}

func httpsOnly(req *http.Request, previous []*http.Request) error {
	if req.URL.Scheme != "https" || len(previous) >= 10 {
		return http.ErrUseLastResponse
	}
	return nil
}

func (s *httpSession) close() {
	s.transport.CloseIdleConnections()
}

func (s *httpSession) setTimeouts(resolve, connect, send, receive int) error {
	dialer := &net.Dialer{Timeout: time.Duration(resolve+connect) * time.Millisecond, KeepAlive: 30 * time.Second}
	s.transport.DialContext = dialer.DialContext
	s.transport.TLSHandshakeTimeout = time.Duration(connect) * time.Millisecond
	s.transport.ResponseHeaderTimeout = time.Duration(send+receive) * time.Millisecond
	return nil
}

func doHTTPRequestLimited(ctx context.Context, session *httpSession, host, path string, enableCookies bool, method string, requestBody []byte, maxBytes int, extraHeaders ...string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, bytes.NewReader(requestBody))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	for _, line := range strings.Split(requestHeaders+strings.Join(extraHeaders, ""), "\r\n") {
		if key, value, ok := strings.Cut(line, ":"); ok {
			req.Header.Set(key, strings.TrimSpace(value))
		}
	}
	if len(requestBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &session.plain
	if enableCookies {
		client = &session.withCookies
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	if encoding := strings.TrimSpace(response.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return response.StatusCode, nil, fmt.Errorf("拒绝压缩响应 (%s)", encoding)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if len(body) > maxBytes {
		return response.StatusCode, nil, fmt.Errorf("行情响应超过 %d 字节上限", maxBytes)
	}
	return response.StatusCode, body, nil
}
