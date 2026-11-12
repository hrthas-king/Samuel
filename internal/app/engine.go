// Package app 独立调度网卡计数、连通性心跳、HTTPS 延迟、出口定位和行情轮询。
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/monitor"
)

const (
	Heartbeat         = time.Second
	LatencyPoll       = 5 * time.Second
	GeoPoll           = 15 * time.Minute
	GeoCooldown       = 30 * time.Second
	GeoManualCooldown = 2 * time.Second
	GeoTunnelPoll     = 30 * time.Second
	EventDebounce     = 750 * time.Millisecond
	quoteRequestLimit = 12 * time.Second
	topUSPoll         = 30 * time.Second
)

type Snapshot struct {
	Status          monitor.Status  `json:"status"`
	Failures        int             `json:"consecutive_failures"`
	Rates           monitor.Rates   `json:"rates"`
	Probe           monitor.Probe   `json:"probe"`
	Latency         monitor.Latency `json:"latency"`
	Geo             ipinfo.Info     `json:"geo"`
	GeoRefreshing   bool            `json:"geo_refreshing"`
	GeoError        string          `json:"geo_error,omitempty"`
	NIC             string          `json:"nic"`
	NICError        string          `json:"nic_error,omitempty"`
	WatchError      string          `json:"watch_error,omitempty"`
	IntervalSeconds int             `json:"display_interval_seconds"`
	MarketGroup     string          `json:"market_group"`
	EgressActive    bool            `json:"tun_or_proxy_active"`
	Quotes          []market.View   `json:"quotes"`
	At              time.Time       `json:"at"`
}

type Dependencies struct {
	ReadCounters        func(string) (monitor.Counters, error)
	Ping                func(string) monitor.Probe
	MeasureLatency      func(context.Context) monitor.Latency
	FetchGeo            func(context.Context) (ipinfo.Info, error)
	FetchQuotes         func(context.Context) (map[string]market.SymbolResult, error)
	FetchTopUSCompanies func(context.Context) ([2]string, error)
	NetworkEvents       <-chan struct{}
	EgressEvents        <-chan monitor.EgressEvent
	WatchError          string
}

type Engine struct {
	deps          Dependencies
	Refresh       chan struct{}
	Interval      chan int
	Group         chan string
	geoCooldown   time.Duration
	tunnelPoll    time.Duration
	topUSRefresh  chan struct{}
	topUSInterval time.Duration
}

func New(deps Dependencies) *Engine {
	return &Engine{
		deps:          deps,
		Refresh:       make(chan struct{}, 1),
		Interval:      make(chan int, 1),
		Group:         make(chan string, 1),
		geoCooldown:   GeoCooldown,
		tunnelPoll:    GeoTunnelPoll,
		topUSRefresh:  make(chan struct{}, 1),
		topUSInterval: topUSPoll,
	}
}

func (e *Engine) RequestTopUSRefresh() {
	select {
	case e.topUSRefresh <- struct{}{}:
	default:
	}
}

func ValidInterval(seconds int) bool {
	return seconds == 1 || seconds == 2 || seconds == 5
}

func QuotePollInterval(displaySeconds int) int {
	if displaySeconds < 1 {
		return 1
	}
	return displaySeconds
}

type gate struct{ last time.Time }

func (g *gate) waitFor(now time.Time, cooldown time.Duration) time.Duration {
	if g.last.IsZero() {
		return 0
	}
	if remaining := g.last.Add(cooldown).Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}

type counterResult struct {
	value monitor.Counters
	err   error
	epoch uint64
}

type probeResult struct {
	value monitor.Probe
	epoch uint64
}

type latencyResult struct {
	value monitor.Latency
	epoch uint64
}

type geoResult struct {
	value ipinfo.Info
	err   error
	epoch uint64
}

type quoteResult struct {
	batch map[string]market.SymbolResult
	err   error
	at    time.Time
	epoch uint64
}

type topUSResult struct {
	symbols [2]string
	err     error
}

func (e *Engine) Run(ctx context.Context, seconds int, publish func(Snapshot)) {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	if !ValidInterval(seconds) {
		seconds = 2
	}
	counters := make(chan counterResult, 1)
	probes := make(chan probeResult, 1)
	latencies := make(chan latencyResult, 1)
	geos := make(chan geoResult, 1)
	quotesCh := make(chan quoteResult, 1)
	topUSCh := make(chan topUSResult, 1)

	var generation atomic.Uint64
	var target atomic.Value
	target.Store("1.1.1.1")

	workers.Add(2)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(Heartbeat)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			gen := generation.Load()
			c, err := e.deps.ReadCounters(target.Load().(string))
			select {
			case counters <- counterResult{c, err, gen}:
			case <-ctx.Done():
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(Heartbeat)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			gen := generation.Load()
			address := target.Load().(string)
			p := e.deps.Ping(address)
			select {
			case probes <- probeResult{p, gen}:
			case <-ctx.Done():
				return
			}
			if !p.Success {
				if address == "1.1.1.1" {
					target.Store("8.8.8.8")
				} else {
					target.Store("1.1.1.1")
				}
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()

	var tracker monitor.Tracker
	tracker.Reset()
	var sampler monitor.Sampler
	quoteTracker := market.NewTracker()

	snapshot := Snapshot{
		Status:          monitor.Unknown,
		IntervalSeconds: seconds,
		MarketGroup:     market.ActiveGroup().ID,
		WatchError:      e.deps.WatchError,
	}
	display := time.NewTicker(time.Duration(seconds) * time.Second)
	defer display.Stop()
	latencyTicker := time.NewTicker(LatencyPoll)
	defer latencyTicker.Stop()
	poll := time.NewTicker(GeoPoll)
	defer poll.Stop()
	tunnelPoll := time.NewTicker(e.tunnelPoll)
	defer tunnelPoll.Stop()
	quoteTicker := time.NewTicker(time.Duration(QuotePollInterval(seconds)) * time.Second)
	defer quoteTicker.Stop()

	var limiter gate
	var pending, busy, urgent bool
	var geoGeneration uint64
	var refreshTimer *time.Timer
	var refreshC <-chan time.Time
	var debounceUntil time.Time
	var fetchCancel context.CancelFunc

	var quoteBusy bool
	var quoteEpoch uint64
	var quoteFailures int
	var quoteNextTry time.Time
	var quoteCancel context.CancelFunc
	var topUSBusy bool
	var topUSNextTry time.Time
	var lastHTTPSuccessAt time.Time
	var latencyBusy bool
	var latencyEpoch uint64
	var latencyCancel context.CancelFunc

	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		if fetchCancel != nil {
			fetchCancel()
		}
		if quoteCancel != nil {
			quoteCancel()
		}
		if latencyCancel != nil {
			latencyCancel()
		}
	}()

	emit := func() {
		now := time.Now()
		snapshot.At = now
		snapshot.Status = tracker.Status
		snapshot.Failures = tracker.Failures
		snapshot.Quotes = quoteTracker.Views(now, QuotePollInterval(snapshot.IntervalSeconds))
		publish(snapshot)
	}

	armGeo := func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		refreshC = nil
		if !pending || busy || (tracker.Status == monitor.Offline && !urgent && !snapshot.EgressActive) {
			return
		}
		now := time.Now()
		cooldown := e.geoCooldown
		if urgent {
			cooldown = GeoManualCooldown
		}
		wait := limiter.waitFor(now, cooldown)
		if debounce := debounceUntil.Sub(now); debounce > wait {
			wait = debounce
		}
		if refreshTimer == nil {
			refreshTimer = time.NewTimer(wait)
		} else {
			refreshTimer.Reset(wait)
		}
		refreshC = refreshTimer.C
	}
	requestGeo := func() { pending = true; armGeo() }

	triggerTopUS := func() {
		now := time.Now()
		if e.deps.FetchTopUSCompanies == nil || topUSBusy || now.Before(topUSNextTry) {
			return
		}
		topUSBusy = true
		topUSNextTry = now.Add(e.topUSInterval)
		go func() {
			rankCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			defer stop()
			symbols, err := e.deps.FetchTopUSCompanies(rankCtx)
			select {
			case topUSCh <- topUSResult{symbols, err}:
			case <-ctx.Done():
			}
		}()
	}

	resetQuoteFetch := func() {
		quoteEpoch++
		if quoteCancel != nil {
			quoteCancel()
			quoteCancel = nil
		}
		quoteBusy = false
		quoteFailures = 0
		quoteNextTry = time.Time{}
	}

	triggerQuoteFetch := func(force bool) {
		if e.deps.FetchQuotes == nil || quoteBusy {
			return
		}
		if snapshot.MarketGroup == market.GroupTop2US && len(market.GroupByID(market.GroupTop2US).Symbols) == 0 {
			return
		}
		now := time.Now()
		if !force && now.Before(quoteNextTry) {
			return
		}
		quoteBusy = true
		ep := quoteEpoch
		qCtx, qCancel := context.WithTimeout(ctx, quoteRequestLimit)
		quoteCancel = qCancel
		go func() {
			defer qCancel()
			batch, err := e.deps.FetchQuotes(qCtx)
			select {
			case quotesCh <- quoteResult{batch: batch, err: err, at: time.Now(), epoch: ep}:
			case <-ctx.Done():
			}
		}()
	}

	triggerLatency := func() {
		if e.deps.MeasureLatency == nil || latencyBusy {
			return
		}
		latencyBusy = true
		ep := latencyEpoch
		lCtx, stop := context.WithTimeout(ctx, 4*time.Second)
		latencyCancel = stop
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer stop()
			value := e.deps.MeasureLatency(lCtx)
			select {
			case latencies <- latencyResult{value, ep}:
			case <-ctx.Done():
			}
		}()
	}
	invalidateLatency := func() {
		latencyEpoch++
		if latencyCancel != nil {
			latencyCancel()
			latencyCancel = nil
		}
		latencyBusy = false
		snapshot.Latency = monitor.Latency{}
		triggerLatency()
	}

	emit()
	requestGeo()
	if snapshot.MarketGroup == market.GroupTop2US {
		triggerTopUS()
	}
	triggerQuoteFetch(true)
	triggerLatency()

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-counters:
			if r.epoch != generation.Load() {
				continue
			}
			if r.err != nil {
				sampler.Reset()
				snapshot.Rates = monitor.Rates{}
				snapshot.NIC = ""
				snapshot.NICError = r.err.Error()
			} else {
				snapshot.Rates = sampler.Sample(r.value)
				snapshot.NIC = r.value.Name
				snapshot.NICError = ""
			}
		case r := <-probes:
			if r.epoch != generation.Load() {
				continue
			}
			probeVal := r.value
			// 层 2：若探针偶发超时，但当前网卡有真实下行流量（>512B/s）或最近 4 秒内 HTTPS 请求刚成功返回，视为旁路在线
			if !probeVal.Success && probeVal.Uplink != monitor.UplinkLocalError {
				activeDown := snapshot.Rates.Valid && snapshot.Rates.Down >= 512
				recentHTTP := !lastHTTPSuccessAt.IsZero() && time.Since(lastHTTPSuccessAt) <= 4*time.Second
				if activeDown || recentHTTP {
					probeVal.Success = true
					probeVal.Uplink = monitor.UplinkConfirmed
					probeVal.Method = "evidence"
					probeVal.RTT = 0
				}
			}
			previous := tracker.Status
			recovered := tracker.Apply(probeVal)
			snapshot.Probe = probeVal
			if recovered && previous == monitor.Offline {
				requestGeo()
				quoteNextTry = time.Time{}
				triggerQuoteFetch(true)
			}
			if tracker.Status != previous {
				emit()
				armGeo()
			}
		case r := <-latencies:
			if r.epoch != latencyEpoch {
				continue
			}
			latencyBusy = false
			if latencyCancel != nil {
				latencyCancel()
				latencyCancel = nil
			}
			snapshot.Latency = r.value
			if r.value.Valid(time.Now()) {
				lastHTTPSuccessAt = time.Now()
			}
			emit()
		case <-latencyTicker.C:
			triggerLatency()
		case <-e.deps.NetworkEvents:
			invalidateLatency()
			// 层 4：收到系统路由或 IPv6 临时地址变动通知时，若当前已在线，平滑保持当前状态与国家码，仅后台静默复核出口
			if tracker.Status != monitor.Online {
				generation.Add(1)
				tracker.Reset()
				sampler.Reset()
				snapshot.Rates = monitor.Rates{}
				snapshot.Probe = monitor.Probe{Uplink: monitor.UplinkUnknown}
			}
			debounceUntil = time.Now().Add(EventDebounce)
			requestGeo()
			quoteNextTry = time.Time{}
			triggerQuoteFetch(true)
			emit()
		case event := <-e.deps.EgressEvents:
			if event.Changed || snapshot.EgressActive != event.Active {
				invalidateLatency()
			}
			snapshot.EgressActive = event.Active
			if event.Changed {
				debounceUntil = time.Now().Add(EventDebounce)
				requestGeo()
				quoteNextTry = time.Time{}
				triggerQuoteFetch(true)
			}
			emit()
		case <-e.Refresh:
			invalidateLatency()
			geoGeneration++
			snapshot.Geo = ipinfo.Info{}
			snapshot.GeoError = ""
			if fetchCancel != nil {
				fetchCancel()
			}
			urgent = true
			debounceUntil = time.Time{}
			requestGeo()
			quoteNextTry = time.Time{}
			if snapshot.MarketGroup == market.GroupTop2US {
				topUSNextTry = time.Time{}
				triggerTopUS()
			}
			triggerQuoteFetch(true)
			emit()
		case n := <-e.Interval:
			if ValidInterval(n) {
				snapshot.IntervalSeconds = n
				display.Reset(time.Duration(n) * time.Second)
				quoteTicker.Reset(time.Duration(QuotePollInterval(n)) * time.Second)
				emit()
			}
		case g := <-e.Group:
			if market.ValidGroup(g) {
				spec := market.SetActiveGroup(g)
				snapshot.MarketGroup = spec.ID
				quoteTracker.SetGroup(spec.ID)
				resetQuoteFetch()
				if spec.ID == market.GroupTop2US {
					triggerTopUS()
				}
				emit()
				triggerQuoteFetch(true)
			}
		case <-e.topUSRefresh:
			triggerTopUS()
		case ranking := <-topUSCh:
			topUSBusy = false
			if ranking.err == nil && ctx.Err() == nil && market.SetTopUSCompanies(ranking.symbols) {
				if snapshot.MarketGroup == market.GroupTop2US {
					quoteTracker.SetSymbols(ranking.symbols[:])
					resetQuoteFetch()
					triggerQuoteFetch(true)
				}
				emit()
			}
		case <-poll.C:
			requestGeo()
		case <-tunnelPoll.C:
			if snapshot.EgressActive {
				requestGeo()
			}
		case <-quoteTicker.C:
			if snapshot.MarketGroup == market.GroupTop2US {
				triggerTopUS()
			}
			triggerQuoteFetch(false)
		case <-refreshC:
			refreshC = nil
			if !pending || busy || (tracker.Status == monitor.Offline && !urgent && !snapshot.EgressActive) {
				continue
			}
			pending = false
			urgent = false
			busy = true
			snapshot.GeoRefreshing = true
			limiter.last = time.Now()
			gen := geoGeneration
			fetchCtx, lookupCancel := context.WithTimeout(ctx, 8*time.Second)
			fetchCancel = lookupCancel
			emit()
			go func() {
				defer lookupCancel()
				info, err := e.deps.FetchGeo(fetchCtx)
				select {
				case geos <- geoResult{info, err, gen}:
				case <-ctx.Done():
				}
			}()
		case r := <-geos:
			busy = false
			snapshot.GeoRefreshing = false
			if fetchCancel != nil {
				fetchCancel()
				fetchCancel = nil
			}
			if r.epoch == geoGeneration {
				if r.err != nil {
					snapshot.GeoError = r.err.Error()
				} else {
					lastHTTPSuccessAt = time.Now()
					hadErr := snapshot.GeoError != ""
					snapshot.Geo = r.value
					snapshot.GeoError = ""
					if hadErr {
						quoteNextTry = time.Time{}
						triggerQuoteFetch(true)
					}
				}
			}
			armGeo()
			emit()
		case qr := <-quotesCh:
			if qr.epoch != quoteEpoch {
				continue
			}
			quoteBusy = false
			if quoteCancel != nil {
				quoteCancel()
				quoteCancel = nil
			}
			if ctx.Err() != nil {
				return
			}
			if qr.err != nil {
				detail := qr.err.Error()
				var httpErr market.HTTPError
				backoff := time.Duration(0)
				quoteFailures++
				if errors.As(qr.err, &httpErr) && httpErr.StatusCode == 429 {
					backoff = 15 * time.Second
				} else {
					sec := quoteFailures * 2
					if sec > 6 {
						sec = 6
					}
					backoff = time.Duration(sec) * time.Second
				}
				quoteNextTry = qr.at.Add(backoff)
				quoteTracker.FailAll(fmt.Sprintf("行情请求失败 (%s)", detail))
			} else {
				lastHTTPSuccessAt = qr.at
				quoteFailures = 0
				quoteNextTry = time.Time{}
				quoteTracker.ApplyBatch(qr.batch, qr.at)
			}
			emit()
		case <-display.C:
			emit()
		}
	}
}
