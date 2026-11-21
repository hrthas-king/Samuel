//go:build darwin

package sentinel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Snapshot) pending() bool {
	return (s.SPXClose != nil && (s.SPX5 == nil || s.SPX20 == nil || s.SPX60 == nil)) ||
		(s.NDXClose != nil && (s.NDX5 == nil || s.NDX20 == nil || s.NDX60 == nil))
}
func fillReturns(s *Snapshot, data Dataset, sessions []string, now time.Time) bool {
	// Use the first actual index session strictly after issuance, including
	// weekend and after-close signals. A missing candle still occupies its
	// session; only its own horizon remains pending.
	signalDay := nyDate(s.Timestamp)
	start := sort.Search(len(sessions), func(i int) bool { return sessions[i] > signalDay })
	changed := false
	for _, idx := range []struct {
		symbol, anchor string
		close          *float64
		targets        [3]**float64
	}{
		{"^GSPC", s.SPXDate, s.SPXClose, [3]**float64{&s.SPX5, &s.SPX20, &s.SPX60}},
		{"^NDX", s.NDXDate, s.NDXClose, [3]**float64{&s.NDX5, &s.NDX20, &s.NDX60}},
	} {
		series := data[idx.symbol]
		if !above(idx.close, 0) || series[idx.anchor] == nil || idx.anchor > signalDay {
			continue
		}
		for i, n := range []int{5, 20, 60} {
			pos := start + n - 1
			if *idx.targets[i] != nil || pos >= len(sessions) || sessions[pos] > nyDate(now) {
				continue
			}
			p := series[sessions[pos]]
			if !above(p, 0) {
				continue
			}
			*idx.targets[i] = delta(p, idx.close, 100, true)
			changed = true
		}
	}
	if changed {
		stamp := now.UTC()
		s.Calibrated = &stamp
	}
	return changed
}

type HorizonStat struct {
	Samples int      `json:"samples"`
	WinRate *float64 `json:"win_rate_pct"`
	Mean    *float64 `json:"mean_return_pct"`
}
type GroupStats struct {
	Signal  string                 `json:"action_signal"`
	Samples int                    `json:"snapshots"`
	SPX     map[string]HorizonStat `json:"spx"`
	NDX     map[string]HorizonStat `json:"ndx"`
}
type Calibration struct {
	Total    int          `json:"snapshots"`
	Updated  int          `json:"updated_snapshots"`
	Groups   []GroupStats `json:"groups"`
	Warnings []string     `json:"missing_data_warnings"`
}
type tally struct {
	n, wins int
	sum     float64
}

func (a *tally) add(v *float64) {
	if v == nil {
		return
	}
	a.n++
	a.sum += *v
	if *v > 0 {
		a.wins++
	}
}
func (a tally) stat() HorizonStat {
	if a.n == 0 {
		return HorizonStat{}
	}
	return HorizonStat{a.n, number(100 * float64(a.wins) / float64(a.n)), number(a.sum / float64(a.n))}
}
func summarize(rows []Snapshot) []GroupStats {
	type bucket struct {
		n        int
		spx, ndx [3]tally
	}
	buckets := map[string]*bucket{}
	for _, s := range rows {
		sig := s.Final.Signal
		b := buckets[sig]
		if b == nil {
			b = &bucket{}
			buckets[sig] = b
		}
		b.n++
		for i, p := range []*float64{s.SPX5, s.SPX20, s.SPX60} {
			b.spx[i].add(p)
		}
		for i, p := range []*float64{s.NDX5, s.NDX20, s.NDX60} {
			b.ndx[i].add(p)
		}
	}
	out := []GroupStats{}
	for sig, b := range buckets {
		g := GroupStats{sig, b.n, map[string]HorizonStat{}, map[string]HorizonStat{}}
		for i, k := range []string{"5d", "20d", "60d"} {
			g.SPX[k] = b.spx[i].stat()
			g.NDX[k] = b.ndx[i].stat()
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal < out[j].Signal })
	return out
}
func calibrate(ctx context.Context, f Fetcher) (Calibration, error) {
	out := Calibration{Groups: []GroupStats{}, Warnings: []string{}}
	store := Store{f.Dir}
	unlock, err := store.lock(ctx, "calibration")
	if err != nil {
		return out, err
	}
	defer unlock()
	paths, err := store.paths()
	if err != nil {
		return out, err
	}
	var rows []Snapshot
	var valid []string
	start := ""
	for _, path := range paths {
		r, err := readSnapshot(path)
		if err != nil {
			out.Warnings = append(out.Warnings, filepath.Base(path)+": "+err.Error())
			continue
		}
		rows = append(rows, r)
		valid = append(valid, path)
		if !r.pending() || nyDate(r.Timestamp) >= nyDate(f.Now) {
			continue
		}
		for _, date := range []string{r.SPXDate, r.NDXDate} {
			if date != "" && (start == "" || date < start) {
				start = date
			}
		}
	}
	out.Total = len(rows)
	if start != "" {
		data, _, warnings := f.history(ctx, []string{"^GSPC", "^NDX"}, nil, start)
		out.Warnings = append(out.Warnings, warnings...)
		sessions := datesFor(data, []string{"^GSPC", "^NDX"}, nyDate(f.Now))
		for i := range rows {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			r := rows[i]
			if !r.pending() || !fillReturns(&r, data, sessions, f.Now) {
				continue
			}
			if err := atomicJSON(valid[i], r); err != nil {
				out.Warnings = append(out.Warnings, filepath.Base(valid[i])+": "+err.Error())
				continue
			}
			rows[i] = r
			out.Updated++
		}
	}
	out.Groups = summarize(rows)
	return out, nil
}
func calibrationBrief(c Calibration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "历史信号校准 · %d 个快照，本次回填 %d 个\n", c.Total, c.Updated)
	if len(c.Groups) == 0 {
		b.WriteString("暂无历史快照。运行一次研判即可开始记录。\n")
	}
	for _, g := range c.Groups {
		fmt.Fprintf(&b, "\n[%s]  %d 个快照\n", g.Signal, g.Samples)
		for _, idx := range []struct {
			name string
			s    map[string]HorizonStat
		}{{"SPX", g.SPX}, {"NDX", g.NDX}} {
			s := idx.s["20d"]
			fmt.Fprintf(&b, "%s  20 日到期 %d 个 · 胜率 %s%% · 平均收益 %s%%\n", idx.name, s.Samples, val(s.WinRate), val(s.Mean))
		}
	}
	for _, w := range c.Warnings {
		fmt.Fprintf(&b, "\n%s", w)
	}
	return b.String()
}
