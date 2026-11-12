//go:build windows && (amd64 || arm64)

package ui

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/app"
	"github.com/hrthas-king/Samuel/internal/market"
	"github.com/hrthas-king/Samuel/internal/winapi"
)

var (
	fluentMenuCount  = user32.NewProc("GetMenuItemCount")
	fluentMenuInfo   = user32.NewProc("GetMenuItemInfoW")
	fluentAdjustRect = user32.NewProc("AdjustWindowRectEx")
	fluentWindows    sync.Map
	fluentProcedure  uintptr
)

func init() { fluentProcedure = syscall.NewCallback(fluentMenuProc) }

type menuItemInfo struct {
	Size, Mask, Type, State, ID                         uint32
	Submenu, CheckedBitmap, UncheckedBitmap, Data, Text uintptr
	Length                                              uint32
	Bitmap                                              uintptr
}

type fluentEntry struct {
	label                       string
	groupID                     string
	action, remove              Action
	enabled, checked, separator bool
	children                    []fluentEntry
}

// Copy the existing menu definitions, preserving action IDs without retaining native handles.
func readFluentMenu(menu uintptr, commands map[Action]customMenuCommand) []fluentEntry {
	count, _, _ := fluentMenuCount.Call(menu)
	if int32(count) < 0 {
		return nil
	}
	var entries []fluentEntry
	for i := uintptr(0); i < count; i++ {
		var text [512]uint16
		info := menuItemInfo{Size: uint32(unsafe.Sizeof(menuItemInfo{})), Mask: 0x1 | 0x2 | 0x4 | 0x40 | 0x100, Text: uintptr(unsafe.Pointer(&text[0])), Length: 511}
		if ok, _, _ := fluentMenuInfo.Call(menu, i, 1, uintptr(unsafe.Pointer(&info))); ok == 0 {
			continue
		}
		if command, ok := commands[Action(info.ID)]; ok && command.remove {
			continue
		}
		if info.Type&0x20 != 0 {
			continue
		} // The native fallback's separate delete column.
		e := fluentEntry{label: strings.ReplaceAll(syscall.UTF16ToString(text[:]), "&&", "&"), action: Action(info.ID), enabled: info.State&3 == 0, checked: info.State&8 != 0, separator: info.Type&0x800 != 0}
		e.label, _, _ = strings.Cut(e.label, "\t")
		if id, ok := GroupForAction(e.action); ok {
			e.groupID = id
		} else if command, ok := commands[e.action]; ok && !command.remove {
			e.groupID = command.id
		} else if e.checked && e.action == 0 {
			current := market.ActiveGroup()
			if e.label == current.MenuLabel {
				e.groupID = current.ID
			}
		}
		if info.Submenu != 0 {
			e.action = 0
			e.children = readFluentMenu(info.Submenu, commands)
		}
		if command, ok := commands[e.action]; ok {
			for action, candidate := range commands {
				if candidate.remove && candidate.id == command.id {
					e.remove = action
					break
				}
			}
		}
		entries = append(entries, e)
	}
	return entries
}

type fluentMenu struct {
	u                                      *UI
	visual                                 *fluentVisuals
	commands                               map[Action]customMenuCommand
	windows                                []*fluentPopup
	closed, focusEstablished, outsideArmed bool
	lastFetch                              time.Time
	lastMain                               *app.Snapshot
	lastQuotes                             *menuQuoteSnapshot
}
type fluentPopup struct {
	menu                           *fluentMenu
	hwnd                           uintptr
	entries                        []fluentEntry
	bounds                         rect
	width, height, content, offset int32
	hover, childIndex              int
	hoverSince                     time.Time
	hoverPending                   bool
	hoverSwitchIndex               int
	hoverSwitchDeadline            time.Time
}

func (u *UI) closeFluentMenu() {
	if u.flyout != nil {
		u.flyout.close()
	}
}
func (s *fluentMenu) close() {
	if s.closed {
		return
	}
	s.closed = true
	s.closeAfter(0)
	s.visual.close()
	if s.u.flyout == s {
		s.u.flyout = nil
	}
}
func (s *fluentMenu) closeAfter(n int) {
	for len(s.windows) > n {
		last := s.windows[len(s.windows)-1]
		s.windows = s.windows[:len(s.windows)-1]
		fluentWindows.Delete(last.hwnd)
		destroyWindow.Call(last.hwnd)
	}
}
func (s *fluentMenu) choose(action Action) {
	s.close()
	s.u.handleMenuSelection(uintptr(action), s.commands)
}

func (u *UI) showFluentMenu(menu uintptr, at point, commands map[Action]customMenuCommand) bool {
	u.closeFluentMenu()
	u.closeCustomPanel()
	entries := readFluentMenu(menu, commands)
	if len(entries) == 0 {
		return false
	}
	s := &fluentMenu{u: u, commands: commands, visual: newFluentVisuals(u.dpiScale, systemFluentColors())}
	u.flyout = s
	if !s.open(entries, at, nil) {
		s.close()
		return false
	}
	root := s.windows[0]
	showWindow.Call(root.hwnd, 5)
	setForeground.Call(root.hwnd)
	setFocus.Call(root.hwnd)
	active, _, _ := panelForeground.Call()
	s.focusEstablished = active == root.hwnd
	panelSetTimer.Call(root.hwnd, 1, 40, 0)
	return true
}

func (p *fluentPopup) rowHeight(e fluentEntry) int32 {
	if e.separator {
		return p.menu.visual.px(9)
	}
	if !e.enabled {
		return p.menu.visual.px(25)
	}
	return p.menu.visual.px(34)
}
func (p *fluentPopup) rowBounds(index int) rect {
	v := p.menu.visual
	top := v.px(6) - p.offset
	for i, e := range p.entries {
		h := p.rowHeight(e)
		if i == index {
			return rect{v.px(5), top, p.width - v.px(5), top + h}
		}
		top += h
	}
	return rect{}
}
func (p *fluentPopup) hit(at point) int {
	if at.X < 0 || at.X >= p.width {
		return -1
	}
	for i, e := range p.entries {
		r := p.rowBounds(i)
		if e.enabled && !e.separator && at.Y >= r.Top && at.Y < r.Bottom {
			return i
		}
	}
	return -1
}

func (s *fluentMenu) open(entries []fluentEntry, at point, parent *fluentPopup) bool {
	v := s.visual
	p := &fluentPopup{menu: s, entries: entries, hover: -1, childIndex: -1, width: v.px(300)}
	dc, _, _ := fluentGetDC.Call(0)
	old, _, _ := selectObject.Call(dc, v.font)
	for _, e := range entries {
		text := winapi.UTF16(e.label)
		var size textSize
		getTextExtentPoint32.Call(dc, uintptr(unsafe.Pointer(text)), uintptr(len(syscall.StringToUTF16(e.label))-1), uintptr(unsafe.Pointer(&size)))
		extra := int32(0)
		if e.groupID != "" {
			extra = v.px(184)
			if e.remove != 0 {
				extra += v.px(14)
			}
		}
		p.width = max(p.width, min(v.px(680), size.CX+v.px(84)+extra))
		p.content += p.rowHeight(e)
	}
	selectObject.Call(dc, old)
	fluentReleaseDC.Call(0, dc)
	p.content += v.px(12)
	work := popupWorkArea(at)
	ex := uintptr(0x8 | 0x80)
	if parent != nil {
		ex |= 0x08000000
	}
	style := uintptr(0x80000000 | 0x00800000)
	var frame rect
	fluentAdjustRect.Call(uintptr(unsafe.Pointer(&frame)), style, 0, ex)
	frameWidth, frameHeight := frame.Right-frame.Left, frame.Bottom-frame.Top
	p.width = min(p.width, work.Right-work.Left-v.px(16)-frameWidth)
	p.height = min(p.content, work.Bottom-work.Top-v.px(16)-frameHeight)
	outerWidth, outerHeight := p.width+frameWidth, p.height+frameHeight
	x, y := at.X, at.Y
	if parent != nil && x+outerWidth > work.Right-v.px(8) {
		x = parent.bounds.Left - outerWidth + v.px(3)
	}
	x = max(work.Left+v.px(8), min(x, work.Right-outerWidth-v.px(8)))
	y = max(work.Top+v.px(8), min(y, work.Bottom-outerHeight-v.px(8)))
	p.bounds = rect{x, y, x + outerWidth, y + outerHeight}
	instance := s.u.instance
	if instance == 0 {
		instance, _, _ = getModule.Call(0)
	}
	class := winapi.UTF16("PulseBar.FluentMenu")
	cursor, _, _ := loadCursor.Call(0, 32512)
	wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Style: 0x20000, Procedure: fluentProcedure, Instance: instance, Cursor: cursor, ClassName: class}
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 && err != syscall.Errno(1410) {
		return false
	}
	title := winapi.UTF16("Samuel 菜单")
	hwnd, _, _ := createWindow.Call(ex, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), style, uintptr(x), uintptr(y), uintptr(outerWidth), uintptr(outerHeight), s.u.hwnd, 0, instance, 0)
	if hwnd == 0 {
		return false
	}
	p.hwnd = hwnd
	var client rect
	fluentGetClient.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	p.width, p.height = client.Right, client.Bottom
	fluentWindows.Store(hwnd, p)
	s.windows = append(s.windows, p)
	applyFluentFrame(hwnd, v.palette)
	if parent != nil {
		showWindow.Call(hwnd, 4)
	}
	return true
}

func (p *fluentPopup) openChild(index int) {
	if index < 0 || index >= len(p.entries) || len(p.entries[index].children) == 0 {
		return
	}
	s := p.menu
	depth := -1
	for i, w := range s.windows {
		if w == p {
			depth = i
			break
		}
	}
	if depth < 0 {
		return
	}
	if p.childIndex == index && len(s.windows) > depth+1 {
		return
	}
	s.closeAfter(depth + 1)
	p.childIndex = index
	r := p.rowBounds(index)
	s.open(p.entries[index].children, point{p.bounds.Right - s.visual.px(3), p.bounds.Top + r.Top}, p)
}
func (p *fluentPopup) setHover(index int) {
	p.hoverSwitchDeadline = time.Time{}
	if index == p.hover {
		return
	}
	p.hover = index
	p.hoverPending = false
	p.hoverSince = time.Now()
	for i, w := range p.menu.windows {
		if w == p {
			p.menu.closeAfter(i + 1)
			break
		}
	}
	p.childIndex = -1
	invalidateRect.Call(p.hwnd, 0, 0)
}

// Give the pointer time to cross the parent on its way into a cascade on either side.
func (p *fluentPopup) pointerMove(at point, now time.Time) {
	s := p.menu
	depth := -1
	for i, w := range s.windows {
		if w == p {
			depth = i
			break
		}
		w.hoverSwitchDeadline = time.Time{}
	}
	if depth < 0 {
		return
	}
	index := p.hit(at)
	if len(s.windows) > depth+1 && index != p.hover {
		if p.hoverSwitchDeadline.IsZero() || index != p.hoverSwitchIndex {
			p.hoverSwitchIndex = index
			p.hoverSwitchDeadline = now.Add(300 * time.Millisecond)
		}
		return
	}
	p.setHover(index)
	p.hoverPending = true
}

func (s *fluentMenu) advanceHover(now time.Time, cursor point) {
	hovered := -1
	for i, w := range s.windows {
		if within(w.bounds, cursor) {
			hovered = i
		}
	}
	for i := 0; i < hovered; i++ {
		s.windows[i].hoverSwitchDeadline = time.Time{}
	}
	for i := 0; i < len(s.windows); i++ {
		w := s.windows[i]
		if hovered == i && !w.hoverSwitchDeadline.IsZero() && !now.Before(w.hoverSwitchDeadline) {
			index := w.hoverSwitchIndex
			w.setHover(index)
			w.openChild(index)
		}
		if w.hoverPending && w.hover >= 0 && now.Sub(w.hoverSince) >= 180*time.Millisecond {
			w.hoverPending = false
			w.openChild(w.hover)
		}
	}
}
func (p *fluentPopup) scroll(pixels int32) {
	next := max(int32(0), min(p.offset+pixels, p.content-p.height))
	if next == p.offset {
		return
	}
	p.offset = next
	p.setHover(-1)
	invalidateRect.Call(p.hwnd, 0, 0)
}
func (p *fluentPopup) move(key uintptr) {
	step := 1
	if key == 38 || key == 35 {
		step = -1
	}
	n := len(p.entries)
	next := p.hover
	if key == 36 {
		next = -1
	} else if key == 35 {
		next = n
	}
	for i := 0; i < n; i++ {
		next = (next + step + n) % n
		if p.entries[next].enabled && !p.entries[next].separator {
			p.setHover(next)
			break
		}
	}
	if p.hover < 0 {
		return
	}
	r := p.rowBounds(p.hover)
	if r.Top < p.menu.visual.px(5) {
		p.offset = max(0, p.offset+r.Top-p.menu.visual.px(5))
	}
	if r.Bottom > p.height-p.menu.visual.px(5) {
		p.offset = min(p.content-p.height, p.offset+r.Bottom-p.height+p.menu.visual.px(5))
	}
	invalidateRect.Call(p.hwnd, 0, 0)
}

func (s *fluentMenu) tick() {
	if s.closed || len(s.windows) == 0 {
		return
	}
	now := time.Now()
	if s.lastFetch.IsZero() || now.Sub(s.lastFetch) >= 5*time.Second {
		s.u.requestMenuQuotes()
		s.lastFetch = now
	}
	main, quotes := s.u.latest.Load(), s.u.menuQuotes.Load()
	if main != s.lastMain || quotes != s.lastQuotes {
		s.lastMain, s.lastQuotes = main, quotes
		for _, window := range s.windows {
			invalidateRect.Call(window.hwnd, 0, 0)
		}
	}
	active, _, _ := panelForeground.Call()
	insideFocus := false
	for _, w := range s.windows {
		if active == w.hwnd {
			insideFocus = true
		}
	}
	if insideFocus {
		s.focusEstablished = true
	} else if s.focusEstablished {
		s.close()
		return
	}
	var cursor point
	getCursor.Call(uintptr(unsafe.Pointer(&cursor)))
	left, _, _ := getAsyncKeyState.Call(1)
	right, _, _ := getAsyncKeyState.Call(2)
	pressed := (left|right)&0x8000 != 0
	inside := false
	for _, w := range s.windows {
		if within(w.bounds, cursor) {
			inside = true
			break
		}
	}
	if !pressed {
		s.outsideArmed = true
	} else if s.outsideArmed && !inside {
		s.close()
		return
	}
	s.advanceHover(time.Now(), cursor)
}

func fluentIcon(e fluentEntry) string {
	if e.checked {
		return "\ue73e"
	}
	switch e.action {
	case ActionRefresh:
		return "\ue72c"
	case ActionToggleNetDetail:
		return "\ue839"
	case ActionToggleLock:
		return "\ue72e"
	case ActionDockLeft:
		return "\ue707"
	case ActionToggleColor:
		return "\ue790"
	case ActionDiagnostics:
		return "\ue9d9"
	case ActionAutostart:
		return "\ue7e8"
	case ActionExit:
		return "\ue8bb"
	case actionAddCustom:
		return "\ue710"
	}
	if strings.HasPrefix(e.label, "More") {
		return "\ue80a"
	}
	if strings.HasPrefix(e.label, "显示大小") || strings.HasPrefix(e.label, "Display size") {
		return "\ue8a7"
	}
	if strings.HasPrefix(e.label, "托盘图标") || strings.HasPrefix(e.label, "Tray icon") {
		return "\ue91b"
	}
	if strings.Contains(e.label, "间隔") || strings.HasPrefix(e.label, "Refresh interval") {
		return "\ue823"
	}
	if strings.HasPrefix(e.label, "其他") || strings.HasPrefix(e.label, "Other") {
		return "\ue713"
	}
	if strings.HasPrefix(e.label, "语言") || strings.HasPrefix(e.label, "Language") {
		return "\ue775"
	}
	return ""
}

func (p *fluentPopup) draw(dc uintptr) {
	v, c := p.menu.visual, p.menu.visual.palette
	area := rect{0, 0, p.width, p.height}
	fillRect.Call(dc, uintptr(unsafe.Pointer(&area)), v.background)
	fluentBox(dc, rect{0, 0, p.width, p.height}, v.px(10), c.background, c.border)
	for i, e := range p.entries {
		r := p.rowBounds(i)
		if r.Bottom <= 0 || r.Top >= p.height {
			continue
		}
		if e.separator {
			y := (r.Top + r.Bottom) / 2
			fluentBox(dc, rect{v.px(12), y, p.width - v.px(12), y + 1}, 0, c.border, c.border)
			continue
		}
		if i == p.hover {
			fluentBox(dc, rect{r.Left, r.Top + v.px(2), r.Right, r.Bottom - v.px(2)}, v.px(7), c.hover, c.hover)
		}
		textColor, font := c.text, v.font
		left := v.px(40)
		if !e.enabled {
			textColor, font, left = c.muted, v.small, v.px(16)
		}
		if strings.HasPrefix(e.label, "Samuel") {
			font, textColor = v.heading, c.text
		}
		right := r.Right - v.px(30)
		label := e.label
		if e.groupID != "" {
			if e.remove != 0 {
				right = r.Right - v.px(44)
			}
			group := p.menu.u.menuGroup(e.groupID)
			if group.ID == market.GroupTop2US {
				label = groupLabel(p.menu.u.Language(), group)
			}
			cells := p.menu.u.groupPercents(group, time.Now())
			start := right - v.px(172)
			fluentText(dc, cells[0].text, rect{start, r.Top, start + v.px(80), r.Bottom}, font, p.menu.u.menuPercentColor(cells[0], c), 2)
			fluentText(dc, "/", rect{start + v.px(80), r.Top, start + v.px(92), r.Bottom}, font, c.muted, 1)
			fluentText(dc, cells[1].text, rect{start + v.px(92), r.Top, right, r.Bottom}, font, p.menu.u.menuPercentColor(cells[1], c), 2)
			right = start - v.px(12)
		}
		fluentText(dc, label, rect{left, r.Top, right, r.Bottom}, font, textColor, 0)
		if icon := fluentIcon(e); icon != "" && e.enabled {
			fluentText(dc, icon, rect{v.px(14), r.Top, v.px(30), r.Bottom}, v.icons, textColor, 1)
		}
		if len(e.children) > 0 {
			fluentText(dc, "\ue76c", rect{r.Right - v.px(25), r.Top, r.Right - v.px(9), r.Bottom}, v.icons, textColor, 1)
		}
		if e.remove != 0 {
			fluentText(dc, "\ue74d", rect{r.Right - v.px(27), r.Top, r.Right - v.px(7), r.Bottom}, v.icons, c.muted, 1)
		}
	}
}

func fluentMenuProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	value, ok := fluentWindows.Load(hwnd)
	if ok {
		p := value.(*fluentPopup)
		s := p.menu
		switch msg {
		case 0x318:
			p.draw(wp)
			return 0
		case 0xf:
			var paint paintStruct
			dc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
			p.draw(dc)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
			return 0
		case 0x14:
			return 1
		case 0x200:
			at := point{int32(int16(lp & 0xffff)), int32(int16((lp >> 16) & 0xffff))}
			p.pointerMove(at, time.Now())
			return 0
		case 0x202:
			at := point{int32(int16(lp & 0xffff)), int32(int16((lp >> 16) & 0xffff))}
			i := p.hit(at)
			if i >= 0 {
				e := p.entries[i]
				if e.remove != 0 && at.X >= p.width-s.visual.px(38) {
					s.choose(e.remove)
				} else if len(e.children) > 0 {
					p.openChild(i)
				} else {
					s.choose(e.action)
				}
			}
			return 0
		case 0x20a:
			at := point{int32(int16(lp & 0xffff)), int32(int16(lp >> 16))}
			target := p
			for _, candidate := range s.windows {
				if within(candidate.bounds, at) {
					target = candidate
				}
			}
			target.scroll(-int32(int16(wp>>16)) * s.visual.px(34) / 40)
			return 0
		case 0x100, 0x104:
			if len(s.windows) == 0 {
				return 0
			}
			for _, w := range s.windows {
				w.hoverSwitchDeadline = time.Time{}
				w.hoverPending = false
			}
			current := s.windows[len(s.windows)-1]
			switch wp {
			case 27, 18:
				s.close()
			case 37:
				if len(s.windows) > 1 {
					s.closeAfter(len(s.windows) - 1)
				} else {
					s.close()
				}
			case 38, 40, 36, 35, 9:
				current.move(wp)
			case 39, 13, 32:
				if current.hover < 0 {
					current.move(40)
				}
				if current.hover >= 0 {
					e := current.entries[current.hover]
					if len(e.children) > 0 {
						current.openChild(current.hover)
						if len(s.windows) > 0 {
							s.windows[len(s.windows)-1].move(40)
						}
					} else if wp != 39 {
						s.choose(e.action)
					}
				}
			}
			return 0
		case 0x113:
			s.tick()
			return 0
		case 0x1a, 0x31a, 0x10:
			s.close()
			return 0
		}
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}
