//go:build windows && (amd64 || arm64)

package market

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	winhttp         = winapi.DLL("winhttp.dll")
	open            = winhttp.NewProc("WinHttpOpen")
	connect         = winhttp.NewProc("WinHttpConnect")
	openRequest     = winhttp.NewProc("WinHttpOpenRequest")
	setTimeouts     = winhttp.NewProc("WinHttpSetTimeouts")
	setOption       = winhttp.NewProc("WinHttpSetOption")
	sendRequest     = winhttp.NewProc("WinHttpSendRequest")
	receiveResponse = winhttp.NewProc("WinHttpReceiveResponse")
	queryHeaders    = winhttp.NewProc("WinHttpQueryHeaders")
	readData        = winhttp.NewProc("WinHttpReadData")
	closeHandle     = winhttp.NewProc("WinHttpCloseHandle")
)

type httpSession struct{ handle uintptr }

func (s *httpSession) close() {
	closeHandle.Call(s.handle)
}

func (s *httpSession) setTimeouts(resolve, connect, send, receive int) error {
	if ok, _, err := setTimeouts.Call(s.handle, uintptr(resolve), uintptr(connect), uintptr(send), uintptr(receive)); ok == 0 {
		return winapi.Error("WinHttpSetTimeouts", err)
	}
	return nil
}

func newHTTPSession() (*httpSession, error) {
	agent := winapi.UTF16("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	s, _, err := open.Call(uintptr(unsafe.Pointer(agent)), 4, 0, 0, 0)
	runtime.KeepAlive(agent)
	if s == 0 {
		return nil, winapi.Error("WinHttpOpen", err)
	}
	if ok, _, err := setTimeouts.Call(s, 800, TimeoutMillis, TimeoutMillis, TimeoutMillis); ok == 0 {
		closeHandle.Call(s)
		return nil, winapi.Error("WinHttpSetTimeouts", err)
	}
	return &httpSession{handle: s}, nil
}

func doHTTPRequestLimited(ctx context.Context, session *httpSession, host, path string, enableCookies bool, method string, requestBody []byte, maxBytes int, extraHeaders ...string) (int, []byte, error) {
	defer runtime.KeepAlive(requestBody)
	hostPtr := winapi.UTF16(host)
	conn, _, err := connect.Call(session.handle, uintptr(unsafe.Pointer(hostPtr)), 443, 0)
	runtime.KeepAlive(hostPtr)
	if conn == 0 {
		return 0, nil, winapi.Error("WinHttpConnect", err)
	}
	defer closeHandle.Call(conn)

	verb := winapi.UTF16(method)
	pathPtr := winapi.UTF16(path)
	request, _, err := openRequest.Call(conn, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(pathPtr)), 0, 0, 0, 0x00800000)
	if request == 0 {
		return 0, nil, winapi.Error("WinHttpOpenRequest", err)
	}
	defer closeHandle.Call(request)

	if !enableCookies {
		var disable uint32 = 2 | 1
		if ok, _, err := setOption.Call(request, 63, uintptr(unsafe.Pointer(&disable)), 4); ok == 0 {
			return 0, nil, winapi.Error("WinHttpSetOption", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	headers := requestHeaders
	for _, header := range extraHeaders {
		headers += header
	}
	var payload uintptr
	if len(requestBody) > 0 {
		headers += "Content-Type: application/json\r\n"
		payload = uintptr(unsafe.Pointer(&requestBody[0]))
	}
	hdrPtr := winapi.UTF16(headers)
	ok, _, sendErr := sendRequest.Call(request, uintptr(unsafe.Pointer(hdrPtr)), ^uintptr(0), payload, uintptr(len(requestBody)), uintptr(len(requestBody)), 0)
	if ok == 0 {
		return 0, nil, winapi.Error("WinHttpSendRequest", sendErr)
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if ok, _, err := receiveResponse.Call(request, 0); ok == 0 {
		return 0, nil, winapi.Error("WinHttpReceiveResponse", err)
	}
	var status uint32
	size := uint32(4)
	if ok, _, err := queryHeaders.Call(request, 19|0x20000000, 0, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&size)), 0); ok == 0 {
		return 0, nil, winapi.Error("WinHttpQueryHeaders", err)
	}
	var encBuf [64]uint16
	encSize := uint32(unsafe.Sizeof(encBuf))
	if ok, _, _ := queryHeaders.Call(request, 29, 0, uintptr(unsafe.Pointer(&encBuf[0])), uintptr(unsafe.Pointer(&encSize)), 0); ok != 0 {
		enc := strings.ToLower(strings.TrimSpace(utf16ToString(encBuf[:])))
		if enc != "" && enc != "identity" {
			return int(status), nil, fmt.Errorf("拒绝压缩响应 (%s)", enc)
		}
	}

	body := make([]byte, 0, 2048)
	var chunk [1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return int(status), nil, err
		}
		var n uint32
		ok, _, err := readData.Call(request, uintptr(unsafe.Pointer(&chunk[0])), uintptr(len(chunk)), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			return int(status), nil, winapi.Error("WinHttpReadData", err)
		}
		if n == 0 {
			break
		}
		if n > uint32(len(chunk)) || len(body)+int(n) > maxBytes {
			return int(status), nil, fmt.Errorf("行情响应超过 %d 字节上限", maxBytes)
		}
		body = append(body, chunk[:n]...)
	}
	runtime.KeepAlive(verb)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(hdrPtr)
	return int(status), body, nil
}

func utf16ToString(u []uint16) string {
	for i, v := range u {
		if v == 0 {
			u = u[:i]
			break
		}
	}
	var sb strings.Builder
	for _, r := range u {
		sb.WriteRune(rune(r))
	}
	return sb.String()
}
