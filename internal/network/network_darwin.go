//go:build darwin

package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/hrthas-king/Samuel/internal/monitor"
)

func ReadCounters(target string) (monitor.Counters, error) {
	conn, err := net.Dial("udp", net.JoinHostPort(target, "443"))
	if err != nil {
		return monitor.Counters{}, err
	}
	address := conn.LocalAddr().(*net.UDPAddr).IP
	conn.Close()
	interfaces, err := net.Interfaces()
	if err != nil {
		return monitor.Counters{}, err
	}
	for _, nic := range interfaces {
		if nic.Flags&net.FlagUp == 0 || nic.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := nic.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ip, _, err := net.ParseCIDR(addr.String()); err == nil && ip.Equal(address) {
				return interfaceCounters(nic)
			}
		}
	}
	return monitor.Counters{}, fmt.Errorf("未找到出口网卡")
}

func interfaceCounters(nic net.Interface) (monitor.Counters, error) {
	data, err := syscall.RouteRIB(syscall.NET_RT_IFLIST2, nic.Index)
	if err != nil {
		return monitor.Counters{}, err
	}
	// Darwin's if_msghdr2 contains a 32-byte header followed by if_data64.
	// The receive and send byte counters are at offsets 64 and 72 in if_data64.
	const headerSize, receivedOffset, sentOffset = 32, 64, 72
	for len(data) >= 4 {
		length := int(binary.NativeEndian.Uint16(data))
		if length < 4 || length > len(data) {
			break
		}
		message := data[:length]
		if message[2] == syscall.RTM_VERSION && message[3] == syscall.RTM_IFINFO2 &&
			len(message) >= headerSize+sentOffset+8 && int(binary.NativeEndian.Uint16(message[12:])) == nic.Index {
			counters := message[headerSize:]
			return monitor.Counters{
				LUID: uint64(nic.Index), Index: uint32(nic.Index), Name: nic.Name, At: time.Now(),
				Received: binary.NativeEndian.Uint64(counters[receivedOffset:]),
				Sent:     binary.NativeEndian.Uint64(counters[sentOffset:]),
			}, nil
		}
		data = data[length:]
	}
	return monitor.Counters{}, fmt.Errorf("未取得 %s 的网卡计数", nic.Name)
}

func Ping(target string) monitor.Probe {
	start := time.Now()
	p := monitor.Probe{Target: target, Method: "tcp", Uplink: monitor.UplinkUnknown}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(target, "443"), 900*time.Millisecond)
	p.At = time.Now()
	if err != nil {
		p.Detail = err.Error()
		return p
	}
	conn.Close()
	p.Success, p.Uplink, p.RTT = true, monitor.UplinkConfirmed, p.At.Sub(start)
	return p
}

func MeasureLatency(ctx context.Context) monitor.Latency {
	target := "https://www.cloudflare.com/cdn-cgi/trace"
	sample := monitor.Latency{Target: target, Method: "https"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	client := http.Client{Transport: transport}
	start := time.Now()
	response, err := client.Do(request)
	sample.At = time.Now()
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		sample.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
		return sample
	}
	sample.RTT = sample.At.Sub(start)
	return sample
}
