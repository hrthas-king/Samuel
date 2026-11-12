// Package ui 提供状态文本格式化、托盘像素图标生成以及任务栏悬浮条与系统托盘原生界面。
package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
)

func Country(s app.Snapshot) string {
	if s.Status == monitor.Offline || !ipinfo.ValidCountry(s.Geo.Country) {
		return "--"
	}
	return s.Geo.Country
}

func StatusText(status monitor.Status) string {
	switch status {
	case monitor.Online:
		return "正常"
	case monitor.Degraded:
		return "待确认"
	case monitor.Offline:
		return "断网警告"
	default:
		return "检测中"
	}
}

func Speed(bytes float64) string {
	if bytes < 0 || math.IsNaN(bytes) || math.IsInf(bytes, 0) {
		return "--"
	}
	units := []string{"B/s", "KiB/s", "MiB/s", "GiB/s"}
	i := 0
	for bytes >= 1024 && i < len(units)-1 {
		bytes /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", bytes, units[i])
	}
	return fmt.Sprintf("%.1f %s", bytes, units[i])
}

func CompactSpeed(bytes float64) string {
	if bytes < 0 || math.IsNaN(bytes) || math.IsInf(bytes, 0) {
		return "--"
	}
	units := []string{"B", "K", "M", "G"}
	i := 0
	for bytes >= 1024 && i < len(units)-1 {
		bytes /= 1024
		i++
	}
	if i == 0 || bytes >= 100 {
		return fmt.Sprintf("%.0f%s", bytes, units[i])
	}
	return fmt.Sprintf("%.1f%s", bytes, units[i])
}

func RTT(s app.Snapshot) string {
	if s.Status == monitor.Offline || !s.Latency.Valid(time.Now()) {
		return "--"
	}
	if s.Latency.RTT < time.Millisecond {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", s.Latency.RTT.Milliseconds())
}

func CompactRTT(s app.Snapshot) string {
	return strings.ReplaceAll(RTT(s), " ", "")
}

// quotePriceChangeText is shared by the taskbar and tray summary so that
// exchange-specific price precision remains consistent between both views.
func quotePriceChangeText(q market.Quote) string {
	text := fmt.Sprintf("%.*f %+.2f%%", market.PriceDecimals(q.Symbol), q.Price, q.ChangePercent)
	// This value copy only changes compact presentation; source provenance
	// remains available in the menu, full tooltip and diagnostics.
	q.Reference = false
	if tag := q.FeedTag(time.Now()); tag != "" {
		text += " [" + tag + "]"
	}
	return text
}

func QuoteSummary(s app.Snapshot) string {
	if len(s.Quotes) == 0 {
		g := market.GroupByID(s.MarketGroup)
		if g.ID == market.GroupTop2US && len(g.Symbols) == 0 {
			return "等待市值排名"
		}
		parts := make([]string, 0, len(g.Symbols))
		for _, sym := range g.Symbols {
			parts = append(parts, fmt.Sprintf("%s --", market.AliasFor(sym)))
		}
		return strings.Join(parts, " | ")
	}
	parts := make([]string, 0, len(s.Quotes))
	for _, v := range s.Quotes {
		name := v.DisplayName()
		if v.Quote != nil {
			mark := ""
			if !v.Healthy {
				mark = "!"
			}
			parts = append(parts, fmt.Sprintf("%s %s%s", name, quotePriceChangeText(*v.Quote), mark))
		} else {
			parts = append(parts, fmt.Sprintf("%s --", name))
		}
	}
	return strings.Join(parts, " | ")
}

// TrayTooltip 生成系统托盘悬停文字（控制在 Windows 128 UTF-16 字符上限内）。
func TrayTooltip(s app.Snapshot) string {
	return TrayTooltipLanguage(s, locale.Chinese)
}

func TrayTooltipLanguage(s app.Snapshot, language string) string {
	ip := s.Geo.IP
	if ip == "" {
		ip = locale.Text(language, "未知")
	}
	if s.GeoError != "" || s.Status == monitor.Offline {
		ip += "*"
	}
	up, down := "--", "--"
	if s.Rates.Valid {
		up, down = CompactSpeed(s.Rates.Up), CompactSpeed(s.Rates.Down)
	}
	prefix := fmt.Sprintf("Samuel [%s] %s\nIP: ", Country(s), locale.Text(language, StatusText(s.Status)))
	suffix := fmt.Sprintf(" | HTTPS: %s\n↑%s ↓%s\n%s", CompactRTT(s), up, down, QuoteSummaryLanguage(s, language))
	tip := prefix + ip + suffix
	if len(utf16.Encode([]rune(tip))) <= 127 {
		return tip
	}
	// NOTIFYICONDATAW reserves one of its 128 UTF-16 units for the terminator.
	// Keep the market and network data intact and direct users to the diagnostic
	// window for the complete IP when that address makes the tip too long.
	hint := "…(见诊断)"
	if strings.HasSuffix(ip, "*") {
		hint = "…*(见诊断)"
	}
	hint = locale.Text(language, hint)
	budget := 127 - len(utf16.Encode([]rune(prefix+hint+suffix)))
	if budget < 0 {
		budget = 0
	}
	ipUnits := utf16.Encode([]rune(ip))
	if budget < len(ipUnits) {
		ipUnits = ipUnits[:budget]
		if len(ipUnits) > 0 && ipUnits[len(ipUnits)-1] >= 0xd800 && ipUnits[len(ipUnits)-1] <= 0xdbff {
			ipUnits = ipUnits[:len(ipUnits)-1]
		}
	}
	return prefix + string(utf16.Decode(ipUnits)) + hint + suffix
}

func networkTooltip(s app.Snapshot) string {
	ip := s.Geo.IP
	if ip == "" {
		ip = "查询中/未知"
	}
	if s.GeoError != "" || s.Status == monitor.Offline {
		ip += "*"
	}
	up, down := "--", "--"
	if s.Rates.Valid {
		up, down = Speed(s.Rates.Up), Speed(s.Rates.Down)
	}
	evidence := "上行待确认"
	if s.Probe.Uplink == monitor.UplinkConfirmed {
		evidence = "上下行已验证"
	} else if s.Probe.Uplink == monitor.UplinkLocalError {
		evidence = "本地发送错误"
	}
	nic := s.NIC
	if nic == "" {
		nic = "未知网卡"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("【网络】[%s] %s | IP: %s | 网卡: %s", Country(s), StatusText(s.Status), ip, nic))
	lines = append(lines, fmt.Sprintf("  速率: ↑ %s  ↓ %s | HTTPS 延迟: %s (%s)", up, down, RTT(s), evidence))
	lines = append(lines, fmt.Sprintf("  延迟目标: %s | 每 %.0f 秒采样（含建连与 TLS）", valueOr(s.Latency.Target, "等待测量"), app.LatencyPoll.Seconds()))
	if s.Latency.Error != "" {
		lines = append(lines, "  延迟测量提示: "+s.Latency.Error)
	}
	if s.EgressActive {
		lines = append(lines, "  模式: 检测到 TUN / 系统代理活跃（30s 自动复核出口）")
	}
	return strings.Join(lines, "\n")
}

// OverlayTooltip combines network and market details for diagnostics.
func OverlayTooltip(s app.Snapshot) string {
	lines := []string{networkTooltip(s), ""}
	for _, v := range s.Quotes {
		lines = append(lines, "【行情】"+v.Tooltip())
	}
	lines = append(lines, "注：时段信号 ☀早盘/盘前(黄) | 🟢盘中(绿) | 🔵盘后(蓝) | 🌙夜盘(紫) | ⚪休市(灰)（异常时右侧显示 !）。")
	return strings.Join(lines, "\n")
}

// DiagnosticsText 生成完整诊断报告（供菜单诊断弹窗或 --probe 使用）。
func DiagnosticsText(version string, s app.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Samuel %s | UTC %s\n", version, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "================================================================\n")
	fmt.Fprintf(&b, "[GeoPulse 网络与出口监控]\n")
	fmt.Fprintf(&b, "  连通状态: %s (%s) | 连续失败: %d\n", StatusText(s.Status), s.Status, s.Failures)
	fmt.Fprintf(&b, "  出口国家: %s | 公网 IP: %s\n", Country(s), valueOr(s.Geo.IP, "未知/查询中"))
	fmt.Fprintf(&b, "  出口网卡: %s | TUN/代理活跃: %v\n", valueOr(s.NIC, "未知"), s.EgressActive)
	up, down := "--", "--"
	if s.Rates.Valid {
		up, down = Speed(s.Rates.Up), Speed(s.Rates.Down)
	}
	fmt.Fprintf(&b, "  实时速率: ↑ %s | ↓ %s\n", up, down)
	fmt.Fprintf(&b, "  连通性探针: %s | 目标 %s | 上行证据 %s\n", valueOr(s.Probe.Method, "未知"), valueOr(s.Probe.Target, "1.1.1.1"), s.Probe.Uplink)
	fmt.Fprintf(&b, "  HTTPS 请求延迟: %s | 目标 %s | 每 %.0f 秒采样（含 DNS、建连、TLS、响应头）\n", RTT(s), valueOr(s.Latency.Target, "等待测量"), app.LatencyPoll.Seconds())
	if !s.Latency.At.IsZero() {
		fmt.Fprintf(&b, "  延迟采样时间 (UTC): %s\n", s.Latency.At.UTC().Format(time.RFC3339))
	}
	if s.Latency.Error != "" {
		fmt.Fprintf(&b, "  延迟测量提示: %s\n", s.Latency.Error)
	}
	if s.GeoError != "" {
		fmt.Fprintf(&b, "  定位提示: %s\n", s.GeoError)
	}
	if s.NICError != "" {
		fmt.Fprintf(&b, "  网卡提示: %s\n", s.NICError)
	}
	groupSpec := market.GroupByID(s.MarketGroup)
	fmt.Fprintf(&b, "\n[TickBar 多市场行情与五态时段监控 — %s]\n", groupSpec.MenuLabel)
	fmt.Fprintf(&b, "  时段信号说明: ☀ 早盘/盘前(黄) | 🟢 盘中(绿) | 🔵 盘后(蓝) | 🌙 夜盘(紫) | ⚪ 休市/周末(灰)\n\n")
	for _, v := range s.Quotes {
		fmt.Fprintf(&b, "%s\n", v.Tooltip())
		if q := v.Quote; q != nil {
			precision := market.PriceDecimals(q.Symbol)
			fmt.Fprintf(&b, "  标的名称: %s (别名: %s) | 时段状态: %s %s | 数据源: %s | 模式: %s\n",
				q.Description, v.DisplayName(), v.EffectiveState().Badge(), q.SessionText(), valueOr(q.QuoteSource, "Yahoo Finance"), q.UpdateMode)
			fmt.Fprintf(&b, "  当前最新价: %.*f (常规盘收盘: %.*f) | 涨跌额: %+.*f | 涨跌幅: %+.3f%%\n", precision, q.Price, precision, q.RegularPrice, precision, q.ChangeAbs, q.ChangePercent)
			tradeStr := "未知"
			if q.TradeTime > 0 {
				tradeStr = time.Unix(q.TradeTime, 0).UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(&b, "  参考基准价: %.*f | 最近成交(UTC): %s | HTTP耗时: %.0f ms\n", precision, q.Previous(), tradeStr, q.RTTMs)
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

var glyphs = map[byte][7]byte{
	'A': {14, 17, 17, 31, 17, 17, 17}, 'B': {30, 17, 17, 30, 17, 17, 30}, 'C': {14, 17, 16, 16, 16, 17, 14},
	'D': {30, 17, 17, 17, 17, 17, 30}, 'E': {31, 16, 16, 30, 16, 16, 31}, 'F': {31, 16, 16, 30, 16, 16, 16},
	'G': {14, 17, 16, 23, 17, 17, 15}, 'H': {17, 17, 17, 31, 17, 17, 17}, 'I': {31, 4, 4, 4, 4, 4, 31},
	'J': {7, 2, 2, 2, 18, 18, 12}, 'K': {17, 18, 20, 24, 20, 18, 17}, 'L': {16, 16, 16, 16, 16, 16, 31},
	'M': {17, 27, 21, 21, 17, 17, 17}, 'N': {17, 25, 21, 19, 17, 17, 17}, 'O': {14, 17, 17, 17, 17, 17, 14},
	'P': {30, 17, 17, 30, 16, 16, 16}, 'Q': {14, 17, 17, 17, 21, 18, 13}, 'R': {30, 17, 17, 30, 20, 18, 17},
	'S': {15, 16, 16, 14, 1, 1, 30}, 'T': {31, 4, 4, 4, 4, 4, 4}, 'U': {17, 17, 17, 17, 17, 17, 14},
	'V': {17, 17, 17, 17, 17, 10, 4}, 'W': {17, 17, 17, 21, 21, 21, 10}, 'X': {17, 17, 10, 4, 10, 17, 17},
	'Y': {17, 17, 10, 4, 4, 4, 4}, 'Z': {31, 1, 2, 4, 8, 16, 31}, '-': {0, 0, 0, 31, 0, 0, 0},
}

// Pixels 生成 32×32 托盘动态图标（深色圆角底、两位国家码、状态圆点、上下行流量闪烁箭头）。
func Pixels(country string, status monitor.Status, up, down, pulse bool) []byte {
	if len(country) != 2 || (country != "--" && !ipinfo.ValidCountry(country)) {
		country = "--"
	}
	pixels := make([]byte, 32*32*4)
	set := func(x, y int, r, g, b byte) {
		i := (y*32 + x) * 4
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = r, g, b, 255
	}
	for y := 1; y < 31; y++ {
		for x := 1; x < 31; x++ {
			set(x, y, 15, 23, 42)
		}
	}
	for n := 0; n < 2; n++ {
		glyph := glyphs[country[n]]
		for y, row := range glyph {
			for x := 0; x < 5; x++ {
				if row&(1<<uint(4-x)) != 0 {
					for dy := 0; dy < 2; dy++ {
						for dx := 0; dx < 2; dx++ {
							set(5+n*12+x*2+dx, 4+y*2+dy, 240, 246, 255)
						}
					}
				}
			}
		}
	}
	r, g, b := byte(148), byte(163), byte(184)
	switch status {
	case monitor.Online:
		r, g, b = 0, 230, 118
	case monitor.Offline:
		r, g, b = 255, 23, 68
	case monitor.Degraded:
		r, g, b = 245, 158, 11
	}
	for y := 21; y <= 29; y++ {
		for x := 12; x <= 20; x++ {
			if (x-16)*(x-16)+(y-25)*(y-25) <= 16 {
				set(x, y, r, g, b)
			}
		}
	}
	for n, active := range []bool{up, down} {
		color := byte(92)
		if active {
			color = 180
			if pulse {
				color = 255
			}
		}
		x := 5 + n*22
		for y := 22; y <= 28; y++ {
			set(x, y, 56, color, color)
		}
		for d := 1; d <= 2; d++ {
			y := 22 + d
			if n == 1 {
				y = 28 - d
			}
			set(x-d, y, 56, color, color)
			set(x+d, y, 56, color, color)
		}
	}
	return pixels
}

func IconKey(s app.Snapshot) string {
	return strings.Join([]string{
		Country(s),
		string(s.Status),
		fmt.Sprint(s.Rates.Up > 0, s.Rates.Down > 0, s.At.Unix()%2 == 0),
	}, "/")
}
