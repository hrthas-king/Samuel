//go:build darwin

package sentinel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

type Outcomes struct {
	SPX5       *float64   `json:"spx_return_5d"`
	SPX20      *float64   `json:"spx_return_20d"`
	SPX60      *float64   `json:"spx_return_60d"`
	NDX5       *float64   `json:"ndx_return_5d"`
	NDX20      *float64   `json:"ndx_return_20d"`
	NDX60      *float64   `json:"ndx_return_60d"`
	Calibrated *time.Time `json:"calibrated_at"`
}
type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`
	Style     string    `json:"strategy_style"`
	Raw       Input     `json:"raw_indicators"`
	Final     Result    `json:"report"`
	SPXClose  *float64  `json:"spx_close_at_signal"`
	NDXClose  *float64  `json:"ndx_close_at_signal"`
	SPXDate   string    `json:"spx_close_date"`
	NDXDate   string    `json:"ndx_close_date"`
	Outcomes
}
type Store struct{ Dir string }

func (s Store) save(in Input, r Result) error {
	spx, ndx := in.Market.SPX["close"], in.Market.NDX["close"]
	row := Snapshot{Timestamp: in.AsOf, Style: in.Style, Raw: in, Final: r, SPXClose: spx.Level, NDXClose: ndx.Level, SPXDate: spx.Date, NDXDate: ndx.Date}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	name := in.AsOf.UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(suffix[:]) + "-" + in.Style + ".json"
	return atomicJSON(filepath.Join(s.Dir, "snapshots", name), row)
}
func (s Store) paths() ([]string, error) {
	dir := filepath.Join(s.Dir, "snapshots")
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, f := range files {
		if f.Type().IsRegular() && strings.HasSuffix(f.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, f.Name()))
		}
	}
	return paths, nil
}
func readSnapshot(path string) (Snapshot, error) {
	var r Snapshot
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Timestamp.IsZero() || r.Final.Signal == "" || (r.Style != Left && r.Style != Right) {
		return r, errors.New("快照缺少时间、策略或信号")
	}
	return r, nil
}
func (s Store) previous(style string, at time.Time) (*Prior, []string) {
	paths, err := s.paths()
	if err != nil {
		return nil, []string{err.Error()}
	}
	var warnings []string
	for _, path := range slices.Backward(paths) {
		if !strings.HasSuffix(path, "-"+style+".json") {
			continue
		}
		row, err := readSnapshot(path)
		if err != nil {
			warnings = append(warnings, filepath.Base(path)+": "+err.Error())
			continue
		}
		if row.Timestamp.After(at) {
			continue
		}
		r := row.Final
		return &Prior{
			Timestamp: row.Timestamp, Date: r.Date, Temperature: r.Temperature, Signal: r.Signal,
			Index: r.Index, Style: r.Style, SPXClose: row.SPXClose, NDXClose: row.NDXClose,
			Scores: row.Raw.Scores, Final: r,
		}, warnings
	}
	return nil, warnings
}

// A per-operation advisory lock is released by the kernel if the helper exits.
// Concurrent runs of one strategy see their predecessor; calibration serializes
// only with calibration and never blocks a quote helper or the other strategy.
func (s Store) lock(ctx context.Context, name string) (func(), error) {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.Dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { file.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, fmt.Errorf("等待历史记录: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
