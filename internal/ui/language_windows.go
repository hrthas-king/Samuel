//go:build windows && (amd64 || arm64)

package ui

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/locale"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

func (u *UI) Language() string {
	if language, ok := u.language.Load().(string); ok {
		return language
	}
	return locale.Normalize(u.cfg.Language)
}

func (u *UI) tr(text string) string { return snapshotText(u.Language(), text, u.latest.Load()) }

func (u *UI) setLanguage(language string) error {
	if !locale.Valid(language) {
		return fmt.Errorf("invalid language: %s", language)
	}
	if language == u.Language() {
		return nil
	}
	candidate := u.cfg
	candidate.Language = language
	if err := u.saveSettings(candidate); err != nil {
		return fmt.Errorf("保存语言设置失败: %w", err)
	}
	u.cfg = candidate
	u.language.Store(language)
	u.closeFluentMenu()
	u.hideTooltip()
	u.dismissChart()
	if snapshot := u.latest.Load(); snapshot != nil && u.hwnd != 0 {
		_ = u.update(*snapshot)
	}
	if u.hwnd != 0 {
		invalidateRect.Call(u.hwnd, 0, 0)
	}
	return nil
}

func ShowErrorLanguage(err error, language string) {
	ShowError(errors.New(locale.Text(language, err.Error())))
}
func (u *UI) showError(err error) { ShowErrorLanguage(err, u.Language()) }

func (u *UI) appendLanguageMenu(parent uintptr) {
	menu, _, _ := createMenu.Call()
	if menu == 0 {
		return
	}
	for _, option := range []struct {
		action      Action
		code, label string
	}{{ActionLanguageChinese, locale.Chinese, "简体中文"}, {ActionLanguageEnglish, locale.English, "English"}} {
		flags := uintptr(0)
		if option.code == u.Language() {
			flags = 8
		}
		text := winapi.UTF16(option.label)
		appendMenu.Call(menu, flags, uintptr(option.action), uintptr(unsafe.Pointer(text)))
	}
	title := winapi.UTF16(u.tr("语言 / Language"))
	appendMenu.Call(parent, 0x10, menu, uintptr(unsafe.Pointer(title)))
}
