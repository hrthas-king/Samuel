//go:build windows && (amd64 || arm64)

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/config"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/watchlist"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

const actionAddCustom Action = 2001
const wmCustomResult = 0x8003
const wmShowCustom = 0x8010

type customMenuCommand struct {
	id     string
	remove bool
}
type pairValidator func(context.Context, string, string) (watchlist.Pair, error)
type pairValidation struct {
	epoch uint64
	pair  watchlist.Pair
	err   error
}

func (u *UI) appendCustomMenu(parent uintptr, commands map[Action]customMenuCommand) {
	menu, _, _ := createMenu.Call()
	if menu == 0 {
		return
	}
	appendItem := func(m uintptr, id Action, label string, flags uintptr) {
		p := winapi.UTF16(strings.ReplaceAll(u.tr(label), "&", "&&"))
		appendMenu.Call(m, flags, uintptr(id), uintptr(unsafe.Pointer(p)))
	}
	appendItem(menu, actionAddCustom, "+ 新增组合", 0)
	for i, pair := range u.cfg.CustomPairs {
		selectID, deleteID := Action(0x10000+i*2), Action(0x10001+i*2)
		commands[selectID] = customMenuCommand{id: pair.ID()}
		commands[deleteID] = customMenuCommand{id: pair.ID(), remove: true}
		flags := uintptr(0)
		if u.cfg.MarketGroup == pair.ID() {
			flags |= 8
		}
		appendItem(menu, selectID, u.groupMenuLabel(u.menuGroup(pair.ID())), flags)
	}
	if len(u.cfg.CustomPairs) > 0 {
		// A second popup-menu column supplies a delete action beside each pair.
		// Pair rows remain normal commands, so one click selects immediately.
		appendItem(menu, 0, "操作", 0x20|2) // MF_MENUBARBREAK | MF_DISABLED
		for i := range u.cfg.CustomPairs {
			appendItem(menu, Action(0x10001+i*2), "删除该组合", 0)
		}
	}
	p := winapi.UTF16(u.tr("其他（自定义股票 / ETF）"))
	appendMenu.Call(parent, 0x10, menu, uintptr(unsafe.Pointer(p)))
}

func (u *UI) commitCustom(candidate config.Config, refresh bool) error {
	// Disk is the transaction boundary. A failed save must leave the displayed state intact.
	if err := u.saveSettings(candidate); err != nil {
		return fmt.Errorf("保存组合失败：%w", err)
	}
	if err := market.SetCustomPairs(candidate.CustomPairs); err != nil {
		return err
	}
	u.cfg = candidate
	if !refresh {
		return nil
	}
	u.desiredGroup.Store(candidate.MarketGroup)
	spec := market.SetActiveGroup(candidate.MarketGroup)
	if old := u.latest.Load(); old != nil {
		s := *old
		s.MarketGroup = spec.ID
		s.Quotes = nil
		u.latest.Store(&s)
	}
	if u.hwnd != 0 {
		invalidateRect.Call(u.hwnd, 0, 0)
	}
	select {
	case u.GroupChanges <- spec.ID:
		return nil
	default:
	}
	select {
	case <-u.GroupChanges:
	default:
	}
	select {
	case u.GroupChanges <- spec.ID:
	default:
	}
	return nil
}

func (u *UI) addCustomPair(pair watchlist.Pair) error {
	c, err := u.cfg.AddPair(pair)
	if err != nil {
		return err
	}
	return u.commitCustom(c, true)
}
func (u *UI) selectCustomPair(id string) error {
	if !u.cfg.HasMarketGroup(id) {
		return fmt.Errorf("该组合不存在")
	}
	c := u.cfg
	c.MarketGroup = id
	return u.commitCustom(c, true)
}
func (u *UI) deleteCustomPair(id string) error {
	c, err := u.cfg.RemovePair(id)
	if err != nil {
		return err
	}
	return u.commitCustom(c, c.MarketGroup != u.cfg.MarketGroup)
}

func (u *UI) submitCustomPair() {
	p := u.customPanel
	if p == nil || p.busy {
		return
	}
	a, b := panelText(p.first), panelText(p.second)
	if _, err := watchlist.NewPair(a, b); err != nil {
		p.setStatus(err.Error())
		return
	}
	p.busy = true
	enableWindow.Call(p.first, 0)
	enableWindow.Call(p.second, 0)
	p.setStatus("正在核实股票 / ETF代码…")
	enableWindow.Call(p.submit, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	u.customCancel = cancel
	epoch := u.customEpoch
	hwnd := u.hwnd
	go func() {
		defer cancel()
		pair, err := u.validatePair(ctx, a, b)
		if ctx.Err() == context.Canceled {
			return
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		select {
		case u.customResults <- pairValidation{epoch, pair, err}:
			if hwnd != 0 {
				postMessage.Call(hwnd, wmCustomResult, 0, 0)
			}
		default:
		}
	}()
}

func (u *UI) handleCustomResults() {
	for {
		select {
		case result := <-u.customResults:
			if u.customPanel == nil || result.epoch != u.customEpoch {
				continue
			}
			p := u.customPanel
			if u.customCancel != nil {
				u.customCancel()
				u.customCancel = nil
			}
			p.busy = false
			enableWindow.Call(p.first, 1)
			enableWindow.Call(p.second, 1)
			enableWindow.Call(p.submit, 1)
			err := result.err
			if err == nil {
				err = u.addCustomPair(result.pair)
			}
			if err != nil {
				p.setStatus(err.Error())
				setFocus.Call(p.first)
				continue
			}
			u.closeCustomPanel()
		default:
			return
		}
	}
}
