//go:build windows && (amd64 || arm64)

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/ipinfo"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/network"
	"github.com/hrthas-king/Samuel/internal/startup"
	"github.com/hrthas-king/Samuel/internal/ui"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

var version = "dev"

func main() {
	diagnose := flag.Duration("diagnose", 0, "后台无窗口诊断持续时间，例如 10s")
	runFor := flag.Duration("run-for", 0, "GUI 冒烟测试后自动退出，例如 15s")
	output := flag.String("output", "", "诊断 JSON 输出路径（含公网 IP，请勿公开提交）")
	probe := flag.Bool("probe", false, "执行一次网络与行情探针并在终端输出诊断报告")
	dockLeft := flag.Bool("dock-left", false, "将悬浮条重置停靠到任务栏最左侧")
	showDiag := flag.Bool("diagnostics", false, "打开当前运行实例的诊断窗口")
	showVersion := flag.Bool("version", false, "显示版本")
	quit := flag.Bool("quit", false, "请求当前会话的 Samuel 正常退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("Samuel", version)
		return
	}
	if *quit {
		if err := winapi.RequestExit(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *dockLeft {
		if sent, err := winapi.SendWindowCommand(winapi.WMDockLeft); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		} else if sent {
			return
		}
	}
	if *showDiag {
		if sent, err := winapi.SendWindowCommand(winapi.WMDiag); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		} else if sent {
			return
		}
	}
	if *probe {
		if *diagnose == 0 {
			*diagnose = 3 * time.Second
		}
	}
	if *diagnose < 0 || *runFor < 0 {
		fmt.Fprintln(os.Stderr, "持续时间不能为负数")
		os.Exit(1)
	}

	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(8 << 20)
	debug.SetGCPercent(50)

	err := run(*diagnose, *runFor, *output, *probe, *dockLeft, *showDiag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if *diagnose == 0 && *runFor == 0 && !*probe {
			ui.ShowError(err)
		}
		os.Exit(1)
	}
}

func run(diagnose, runFor time.Duration, output string, probe, dockLeft, showDiag bool) error {
	if diagnose == 0 {
		release, exists, err := winapi.SingleInstance()
		if err != nil {
			return err
		}
		defer release()
		if exists {
			return nil
		}
	}
	pinger, err := network.NewPinger()
	if err != nil {
		return err
	}
	defer pinger.Close()

	watcher, watchErr := network.Watch()
	defer watcher.Close()
	watchError := ""
	if watchErr != nil {
		watchError = watchErr.Error()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := app.New(app.Dependencies{
		ReadCounters:   network.ReadCounters,
		Ping:           pinger.Ping,
		MeasureLatency: network.MeasureHTTPSLatency,
		FetchGeo:       ipinfo.Fetch,
		FetchQuotes:    market.Fetch,
		NetworkEvents:  watcher.Events,
		EgressEvents:   network.WatchEgress(ctx),
		WatchError:     watchError,
	})

	var latest atomic.Pointer[app.Snapshot]
	settings := config.Load()
	if err := market.SetCustomPairs(settings.CustomPairs); err != nil {
		return err
	}
	market.SetActiveGroup(settings.MarketGroup)
	if dockLeft {
		settings.Placement = "left"
		settings.X = nil
		settings.Y = nil
		_ = config.Save(settings)
	}

	started := time.Now()
	var guiMemory winapi.Memory

	if diagnose > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, diagnose)
		defer stop()
		engine.Run(ctx, settings.IntervalSeconds, func(s app.Snapshot) {
			latest.Store(&s)
		})
	} else {
		if runFor > 0 {
			var stop context.CancelFunc
			ctx, stop = context.WithTimeout(ctx, runFor)
			defer stop()
		}
		barUI := ui.NewUI(version, settings)
		menuClient := market.NewMenuClient()
		defer menuClient.Close()
		barUI.SetMenuQuoteFetcher(ctx, menuClient.Fetch)
		if enabled, err := startup.Enabled(); err == nil {
			barUI.SetAutostart(enabled)
		}
		done := make(chan struct{})
		startedEngine := false
		err = barUI.Run(ctx, func() {
			startedEngine = true
			if showDiag {
				_, _ = winapi.SendWindowCommand(winapi.WMDiag)
			}
			go func() {
				engine.Run(ctx, settings.IntervalSeconds, func(s app.Snapshot) {
					latest.Store(&s)
					barUI.Publish(s)
					if runFor > 0 {
						if mem, err := winapi.ProcessMemory(); err == nil {
							guiMemory = mem
						}
					}
				})
				close(done)
			}()
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case group := <-barUI.GroupChanges:
						sendLatestGroup(engine.Group, group)
					case action := <-barUI.Actions:
						switch action {
						case ui.ActionExit:
							cancel()
						case ui.ActionRefresh:
							select {
							case engine.Refresh <- struct{}{}:
							default:
							}
						case ui.ActionInterval1, ui.ActionInterval2, ui.ActionInterval5:
							n := map[ui.Action]int{ui.ActionInterval1: 1, ui.ActionInterval2: 2, ui.ActionInterval5: 5}[action]
							select {
							case engine.Interval <- n:
							default:
							}
						case ui.ActionAutostart:
							enabled, err := startup.Enabled()
							if err == nil {
								err = startup.Set(!enabled)
							}
							if err != nil {
								ui.ShowError(err)
							} else {
								barUI.SetAutostart(!enabled)
							}
						default:
							if grp, ok := ui.GroupForAction(action); ok {
								sendLatestGroup(engine.Group, grp)
							}
						}
					}
				}
			}()
		})
		cancel()
		if startedEngine {
			<-done
		}
		if err != nil {
			return err
		}
	}

	if probe && output == "" {
		if s := latest.Load(); s != nil {
			fmt.Print(ui.DiagnosticsText(version, *s))
		}
		return nil
	}

	if output != "" || diagnose > 0 {
		memory, err := winapi.ProcessMemory()
		if err != nil {
			return err
		}
		report := struct {
			Version        string        `json:"version"`
			GoVersion      string        `json:"go_version"`
			Arch           string        `json:"arch"`
			ElapsedSeconds float64       `json:"elapsed_seconds"`
			GUI            bool          `json:"gui_started"`
			Snapshot       *app.Snapshot `json:"snapshot"`
			Memory         winapi.Memory `json:"memory"`
			GUIMemory      winapi.Memory `json:"memory_with_gui"`
		}{
			Version:        version,
			GoVersion:      runtime.Version(),
			Arch:           runtime.GOARCH,
			ElapsedSeconds: time.Since(started).Seconds(),
			GUI:            diagnose == 0,
			Snapshot:       latest.Load(),
			Memory:         memory,
			GUIMemory:      guiMemory,
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		if output != "" {
			return os.WriteFile(output, data, 0600)
		}
		fmt.Println(string(data))
	}
	return nil
}

func sendLatestGroup(channel chan string, group string) {
	select {
	case channel <- group:
		return
	default:
	}
	select {
	case <-channel:
	default:
	}
	select {
	case channel <- group:
	default:
	}
}
