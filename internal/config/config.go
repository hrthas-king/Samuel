// Package config 管理用户界面与刷新设置（%LOCALAPPDATA%\PulseBar\config.json）。不持久化公网 IP 或流量历史。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hrthas-king/Samuel/internal/watchlist"
)

const (
	DefaultMarketGroup    = "voo_qqq"
	DefaultUIScalePercent = 100
	TrayIconApplication   = "application"
	TrayIconNetwork       = "network"
)

var UIScalePresets = []int{75, 85, 90, 100, 115, 130}

var validMarketGroups = map[string]bool{
	"voo_qqq":           true,
	"soxx_smh":          true,
	"top2_us":           true,
	"gold_oil":          true,
	"cn_indices":        true,
	"hk_indices":        true,
	"asia_indices":      true,
	"eu_indices":        true,
	"etf_159632_159655": true,
	"us_indices":        true,
	"cn_composite":      true,
}

type Config struct {
	CustomPairs     []watchlist.Pair `json:"custom_pairs,omitempty"`
	IntervalSeconds int              `json:"interval_seconds"`
	ShowNetDetail   bool             `json:"show_net_detail"`
	Locked          bool             `json:"locked"`
	UpIsRed         bool             `json:"up_is_red"`
	MarketGroup     string           `json:"market_group"`
	UIScalePercent  int              `json:"ui_scale_percent"`
	Placement       string           `json:"placement"`
	TrayIcon        string           `json:"tray_icon"`
	X               *int             `json:"x,omitempty"`
	Y               *int             `json:"y,omitempty"`
}

func ValidInterval(seconds int) bool {
	return seconds == 1 || seconds == 2 || seconds == 5
}

func ValidMarketGroup(group string) bool {
	return validMarketGroups[group]
}

func ValidTrayIcon(style string) bool {
	return style == TrayIconApplication || style == TrayIconNetwork
}

func ValidUIScalePercent(pct int) bool {
	for _, p := range UIScalePresets {
		if p == pct {
			return true
		}
	}
	return false
}

func Default() Config {
	return Config{
		IntervalSeconds: 2,
		ShowNetDetail:   false,
		Locked:          false,
		UpIsRed:         false,
		MarketGroup:     DefaultMarketGroup,
		UIScalePercent:  DefaultUIScalePercent,
		Placement:       "left",
		TrayIcon:        TrayIconApplication,
	}
}

func Path() string {
	if override := os.Getenv("SAMUEL_CONFIG"); override != "" {
		return override
	}
	// The legacy override and storage directory remain valid across the rename.
	if override := os.Getenv("PULSEBAR_CONFIG"); override != "" {
		return override
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "PulseBar", "config.json")
}

func Load() Config {
	return LoadFrom(Path())
}

func LoadFrom(path string) Config {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var saved Config
	if json.Unmarshal(data, &saved) != nil {
		return c
	}
	seen := make(map[string]bool)
	for _, pair := range saved.CustomPairs {
		p, err := watchlist.NewPair(pair.Code1, pair.Code2)
		if err == nil && !seen[p.ID()] {
			c.CustomPairs = append(c.CustomPairs, p)
			seen[p.ID()] = true
		}
	}
	if ValidInterval(saved.IntervalSeconds) {
		c.IntervalSeconds = saved.IntervalSeconds
	}
	if c.HasMarketGroup(saved.MarketGroup) {
		c.MarketGroup = saved.MarketGroup
	}
	if ValidUIScalePercent(saved.UIScalePercent) {
		c.UIScalePercent = saved.UIScalePercent
	}
	c.ShowNetDetail = saved.ShowNetDetail
	c.Locked = saved.Locked
	c.UpIsRed = saved.UpIsRed
	if ValidTrayIcon(saved.TrayIcon) {
		c.TrayIcon = saved.TrayIcon
	}
	if saved.Placement == "manual" && saved.X != nil && saved.Y != nil &&
		abs(*saved.X) < 100000 && abs(*saved.Y) < 100000 {
		x, y := *saved.X, *saved.Y
		c.Placement = "manual"
		c.X = &x
		c.Y = &y
	} else {
		c.Placement = "left"
		c.X = nil
		c.Y = nil
	}
	return c
}

func Save(c Config) error {
	return SaveTo(Path(), c)
}

func SaveTo(path string, c Config) error {
	if !ValidInterval(c.IntervalSeconds) {
		return fmt.Errorf("刷新间隔只能为 1、2、5 秒")
	}
	if c.TrayIcon == "" {
		c.TrayIcon = TrayIconApplication
	} else if !ValidTrayIcon(c.TrayIcon) {
		return fmt.Errorf("无效的托盘图标样式: %s", c.TrayIcon)
	}
	if c.MarketGroup == "" {
		c.MarketGroup = DefaultMarketGroup
	} else if !c.HasMarketGroup(c.MarketGroup) {
		return fmt.Errorf("无效的行情标的分组: %s", c.MarketGroup)
	}
	seen := make(map[string]bool)
	for _, pair := range c.CustomPairs {
		p, err := watchlist.NewPair(pair.Code1, pair.Code2)
		if err != nil || p != pair || seen[p.ID()] {
			return fmt.Errorf("自定义股票组合无效或重复")
		}
		seen[p.ID()] = true
	}
	if c.UIScalePercent == 0 {
		c.UIScalePercent = DefaultUIScalePercent
	} else if !ValidUIScalePercent(c.UIScalePercent) {
		return fmt.Errorf("无效的显示缩放比例: %d%%", c.UIScalePercent)
	}
	if c.Placement != "manual" || c.X == nil || c.Y == nil {
		c.Placement = "left"
		c.X = nil
		c.Y = nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), "config-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
