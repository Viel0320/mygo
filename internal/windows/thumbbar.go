//go:build windows && (amd64 || arm64)

package windows

import (
	"unicode/utf16"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

// A window's thumbnail toolbar (ThumbBarWindow): the row of small buttons
// under the taskbar button's live preview. ThumbBarAddButtons fixes the
// buttons' count and order once and for all, and later changes go through
// ThumbBarUpdateButtons (updating tooltips, flags and any replaced icons),
// so the state and latest PNGs are kept on the window: Explorer sends
// TaskbarButtonCreated again when it restarts, and the toolbar is added
// again from the PNGs kept here, at the window's DPI.

// thumbApply and thumbClicked are the toolbar's apply and click dispatch,
// linked when the app first sets one (LinkThumbBars): applyTaskbar and the
// window proc run in every app, and only call them.
var (
	thumbApply   func(*window)
	thumbClicked func(*window, uint16)
)

// LinkThumbBars links the code of the thumbnail toolbar, when the app
// first sets one.
func LinkThumbBars() {
	thumbApply = (*window).applyThumbBar
	thumbClicked = (*window).thumbButtonClicked
}

// More ITaskbarList3 vtable indices and a thumbnail toolbar button's
// masks, flags and notification (ShObjIdl_core.h).
const (
	tbThumbBarAddButtons    = 15
	tbThumbBarUpdateButtons = 16

	thbIcon    = 0x2
	thbToolTip = 0x4
	thbFlags   = 0x8

	thbfDisabled = 0x1
	thbfHidden   = 0x8

	// thbnClicked arrives in the high word of WM_COMMAND's wParam, with
	// the button's id in its low word.
	thbnClicked = 0x1800
)

// thumbButton is THUMBBUTTON (ShObjIdl_core.h): Go places the fields where
// C does on 64-bit Windows, with the icon 8-aligned after the bitmap.
type thumbButton struct {
	mask   uint32
	id     uint32
	bitmap uint32
	_      uint32
	icon   uintptr
	tip    [260]uint16
	flags  uint32
	_      uint32
}

// setTip copies a tooltip into szTip, which holds 259 runes and a zero,
// replacing what was there.
func (b *thumbButton) setTip(tip string) {
	s := utf16.Encode([]rune(tip))
	if len(s) > 259 {
		s = s[:259]
	}
	clear(b.tip[:])
	copy(b.tip[:], s)
}

// setFlags puts the state of a button of the core in dwFlags.
func (b *thumbButton) setFlags(disabled, hidden bool) {
	b.flags = 0
	if disabled {
		b.flags = thbfDisabled
	}
	if hidden {
		b.flags |= thbfHidden
	}
}

// SetThumbBar defines or updates the window's thumbnail toolbar. The first
// call fixes the buttons' count and order; later ones update tooltips,
// flags and any non-empty icons, keeping stored PNGs where Icon is empty.
func (w *window) SetThumbBar(buttons []platform.ThumbButton) error {
	if w.thumb.defined {
		return w.updateThumbBar(buttons)
	}
	// Validate and build icons even if the taskbar is skipped or COM is not
	// ready, so a bad PNG cannot lock in the defined state.
	icons, err := w.makeThumbIcons(buttons, true)
	if err != nil {
		return err
	}
	defer destroyIcons(icons)
	w.thumb.buttons = append([]platform.ThumbButton(nil), buttons...)
	w.thumb.defined = true
	if !w.skipTaskbar {
		if tl := w.b.taskbar(); tl != 0 {
			w.sendThumbButtons(tl, tbThumbBarAddButtons, icons)
		}
	}
	return nil
}

// applyThumbBar adds the toolbar to the window's taskbar button, at the
// window's DPI. Called whenever Explorer recreates the button
// (TaskbarButtonCreated) or SetSkipTaskbar restores it.
func (w *window) applyThumbBar() {
	if len(w.thumb.buttons) == 0 || w.skipTaskbar {
		return
	}
	tl := w.b.taskbar()
	if tl == 0 {
		return
	}
	icons, err := w.makeThumbIcons(w.thumb.buttons, true)
	if err != nil {
		return
	}
	defer destroyIcons(icons)
	w.sendThumbButtons(tl, tbThumbBarAddButtons, icons)
}

// updateThumbBar updates tooltips, flags and any replaced icons on an
// existing toolbar, preserving previously saved PNGs where Icon is empty.
func (w *window) updateThumbBar(buttons []platform.ThumbButton) error {
	// Build HICONs only for replaced icons; a bad PNG fails atomically,
	// leaving the saved buttons untouched.
	icons, err := w.makeThumbIcons(buttons, false)
	if err != nil {
		return err
	}
	defer destroyIcons(icons)
	next := make([]platform.ThumbButton, len(buttons))
	for i, b := range buttons {
		if len(b.Icon) == 0 {
			// Keep the saved PNG so Explorer restarts and taskbar restores
			// can rebuild the icon.
			b.Icon = w.thumb.buttons[i].Icon
		}
		next[i] = b
	}
	w.thumb.buttons = next
	if !w.skipTaskbar {
		if tl := w.b.taskbar(); tl != 0 {
			w.sendThumbButtons(tl, tbThumbBarUpdateButtons, icons)
		}
	}
	return nil
}

// sendThumbButtons builds the THUMBBUTTON array from w.thumb.buttons and
// passes it to which, the ITaskbarList3 method AddButtons or UpdateButtons,
// on tl. Entries without an HICON leave thbIcon out so updates keep the
// existing icon.
func (w *window) sendThumbButtons(tl uintptr, which int, icons []uintptr) {
	bs := make([]thumbButton, len(w.thumb.buttons))
	for i, b := range w.thumb.buttons {
		bs[i] = thumbButton{mask: thbToolTip | thbFlags, id: uint32(b.ID)}
		if icons != nil && icons[i] != 0 {
			bs[i].mask |= thbIcon
			bs[i].icon = icons[i]
		}
		bs[i].setTip(b.ToolTip)
		bs[i].setFlags(b.Disabled, b.Hidden)
	}
	comCall(tl, which, w.hwnd, uintptr(len(bs)), uintptr(unsafe.Pointer(&bs[0])))
}

// makeThumbIcons builds button HICONs at the window's DPI from button PNGs.
// When requireIcon is false, buttons with an empty Icon are skipped and
// leave a zero slot; if no button has an icon to build, it returns nil.
// ThumbBarAddButtons and ThumbBarUpdateButtons copy each HICON, so the
// caller destroys the returned handles afterwards.
func (w *window) makeThumbIcons(buttons []platform.ThumbButton, requireIcon bool) ([]uintptr, error) {
	var icons []uintptr
	var size int
	for i, b := range buttons {
		if !requireIcon && len(b.Icon) == 0 {
			continue
		}
		if icons == nil {
			icons = make([]uintptr, len(buttons))
			size = iconSize(smCxIcon, 32, dpiOf(w.hwnd))
		}
		icon, err := iconFromPNG(b.Icon, size)
		if err != nil {
			destroyIcons(icons)
			return nil, err
		}
		icons[i] = icon
	}
	return icons, nil
}

// destroyIcons releases the HICONs the shell copied from.
func destroyIcons(icons []uintptr) {
	for _, icon := range icons {
		if icon != 0 {
			procDestroyIcon.Call(icon)
		}
	}
}

// thumbButtonClicked reports a click of the thumbnail toolbar button whose
// id the shell sent.
func (w *window) thumbButtonClicked(id uint16) {
	if h, ok := w.h.(platform.ThumbBarHandler); ok {
		h.ThumbButtonClicked(int(id))
	}
}
