//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
)

func runToday(ctx context.Context, f Fetcher, style string, model Model) (Result, error) {
	if style != Left && style != Right {
		return Result{}, errors.New("策略必须为 left_dca 或 right_confirm")
	}
	store := Store{f.Dir}
	unlock, err := store.lock(ctx, style)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	prev, warnings := store.previous(style, f.Now)
	type sources struct {
		h []Headline
		e []Event
		w []string
	}
	ready := make(chan sources, 1)
	go func() { h, e, w := f.catalysts(ctx); ready <- sources{h, e, w} }()
	data, sourcesUsed, issues := f.history(ctx, priceSymbols, fredSymbols, nyDate(f.Now.In(ny).AddDate(0, 0, -800)))
	asOf := nyDate(f.Now)
	sessions := datesFor(data, []string{"^GSPC", "^NDX"}, asOf)
	floor := fearDateFloor(f.Now, sessions)
	cnn, cnnWarning := f.cnnFearGreed(ctx, sessions, floor)
	c := <-ready
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	m, macro := marketIndicators(data, asOf), macroIndicators(data, asOf)
	warnings = append(warnings, m.discardStaleVIX(floor)...)
	m.CNN = cnn
	if cnn.Source != "" {
		sourcesUsed["CNN_FEAR_GREED"] = cnn.Source
	}
	warnings = append(warnings, issues...)
	warnings = append(warnings, c.w...)
	if cnnWarning != "" {
		warnings = append(warnings, cnnWarning)
	}
	if cnn.Level != nil && (cnn.Delta == nil || cnn.Percentile == nil) {
		warnings = append(warnings, "CNN 恐惧与贪婪指数缺少可对齐的历史，部分 20 日变化或一年分位为空")
	}
	for _, symbol := range priceSymbols {
		n := len(observedDates(data, symbol, asOf))
		if n > 0 && n < 260 {
			warnings = append(warnings, fmt.Sprintf("%s 仅有 %d 个有效日收盘，历史不足 260 个交易日", symbol, n))
		}
	}
	for _, group := range []struct {
		name string
		m    Measures
	}{{"SPX", m.SPX}, {"NDX", m.NDX}, {"市场", m.Measures}, {"宏观", macro.Measures}} {
		if group.name == "宏观" {
			available := false
			for _, key := range fredSymbols {
				if len(data[key]) > 0 {
					available = true
					break
				}
			}
			if !available {
				continue
			} // The source warning already identifies every missing series.
		}
		for _, w := range missingMetrics(group.m) {
			warnings = append(warnings, group.name+"缺失："+w)
		}
	}
	if m.VIXDeclining == nil {
		warnings = append(warnings, "VIX 连续两日回落条件缺少完整收盘价")
	}
	if m.Reclaimed == nil {
		warnings = append(warnings, "短均线收复条件数据不齐")
	}
	if m.Divergence == nil {
		warnings = append(warnings, "广度背离缺少可对齐的 RSP/SPY 数据")
	}
	if macro.Measures["hy_oas_pct"].Level != nil && macro.Measures["hy_oas_chg_20d_bps"].Level == nil {
		warnings = append(warnings, "信用利差缺少可对齐的 20 日变化")
	}
	if warnings == nil {
		warnings = []string{}
	}
	in := Input{AsOf: f.Now, Style: style, Scores: computeQuantBaseScore(m, macro), Baseline: indexPreference(m, macro),
		Previous: prev, Market: m, Macro: macro, News: c.h, Events: c.e, Warnings: warnings,
		Sources: sourcesUsed, Observations: observedInputs(data, asOf)}
	out := analyze(ctx, in, model)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return out, store.save(in, out)
}

const usage = "SamuelData sentinel run [--style left_dca|right_confirm] [--force-refresh] [--json]\nSamuelData sentinel latest [--style left_dca|right_confirm] [--json]\nSamuelData sentinel calibrate [--force-refresh] [--json]\nSamuelData sentinel settings get|set\nSamuelData sentinel models <agy|opencode|codex|claude> [path]"

func Command(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, usage)
		return nil
	}
	op := args[0]
	dir := filepath.Join(filepath.Dir(config.Path()), "us_market_sentinel")
	if op == "settings" {
		return settingsCommand(ctx, dir, args[1:], os.Stdin, stdout)
	}
	if op == "models" {
		return modelsCommand(ctx, dir, args[1:], stdout)
	}
	if op != "run" && op != "latest" && op != "calibrate" {
		return fmt.Errorf("无效子命令 %q\n%s", op, usage)
	}
	env, err := settings(dir)
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("sentinel "+op, flag.ContinueOnError)
	flags.SetOutput(stderr)
	useJSON := flags.Bool("json", false, "输出 JSON")
	force := flags.Bool("force-refresh", false, "重新获取数据")
	style := env["SAMUEL_SENTINEL_STYLE"]
	if style == "" {
		style = Left
	}
	if op == "run" || op == "latest" {
		flags.StringVar(&style, "style", style, "left_dca 或 right_confirm")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("多余的参数 %q", flags.Arg(0))
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if op == "latest" {
		if style != Left && style != Right {
			return errors.New("策略必须为 left_dca 或 right_confirm")
		}
		prev, _ := (Store{dir}).previous(style, time.Now().UTC().Add(time.Minute))
		if prev == nil {
			return nil
		}
		if *useJSON {
			return enc.Encode(prev.Final)
		}
		_, err = fmt.Fprint(stdout, Brief(prev.Final))
		return err
	}
	client, err := market.NewResearchClient()
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	f := Fetcher{Client: client, Dir: dir, FREDKey: env["FRED_API_KEY"], Now: time.Now().UTC(), Force: *force}
	if op == "calibrate" {
		c, err := calibrate(ctx, f)
		if err != nil {
			return err
		}
		if *useJSON {
			return enc.Encode(c)
		}
		_, err = fmt.Fprintln(stdout, calibrationBrief(c))
		return err
	}
	model := configuredModel(client, env)
	r, saveErr := runToday(ctx, f, style, model)
	if r.Signal == "" {
		return saveErr
	}
	for _, change := range r.Guards {
		fmt.Fprintln(stderr, change)
	}
	if *useJSON {
		err = enc.Encode(r)
	} else {
		_, err = fmt.Fprint(stdout, Brief(r))
	}
	return errors.Join(err, saveErr)
}
