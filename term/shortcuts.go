package term

import "github.com/go-gui-org/go-gui/gui"

// ShortcutInfo describes one Term-level keyboard shortcut for display in a
// help / cheatsheet overlay.
//
// The Term handles these shortcuts imperatively in onKeyDown (see
// handleSearchKey, handleClipboardKey, scrollbackIntercept) because each
// needs conditional passthrough to the child process — e.g. plain Ctrl+C
// must still send SIGINT, and Cmd+C only copies when a selection exists.
// A declarative command registry can't own them without breaking that, so
// this list is the single display source. Keep it in sync with those
// handlers when bindings change.
type ShortcutInfo struct {
	Label string
	Keys  string // human-readable, platform-formatted (macOS glyphs on darwin)
}

// sc formats a single key+modifier combo using go-gui's platform-aware
// renderer (⌘C on darwin, Super+C on Linux, Ctrl+Shift+C on Windows). Modifiers
// are run through remapMod so the displayed combo matches what the handlers
// accept on each platform.
func sc(key gui.KeyCode, mods gui.Modifier) string {
	return gui.Shortcut{Key: key, Modifiers: remapMod(mods)}.String()
}

// scPair renders two combos for one action, collapsing to a single combo when
// the platform remap makes them identical (e.g. Super and Ctrl+Shift both
// render as Ctrl+Shift on Windows).
func scPair(key gui.KeyCode, a, b gui.Modifier) string {
	sa, sb := sc(key, a), sc(key, b)
	if sa == sb {
		return sa
	}
	return sa + " / " + sb
}

// Shortcuts returns the Term-level keyboard shortcuts in display order.
// Workspace-level shortcuts (tabs, panes, theme) live in the workspace
// command registry and are listed separately by the help overlay.
func Shortcuts() []ShortcutInfo {
	return []ShortcutInfo{
		{"Copy", scPair(gui.KeyC, gui.ModSuper, gui.ModCtrl|gui.ModShift)},
		{"Paste", scPair(gui.KeyV, gui.ModSuper, gui.ModCtrl|gui.ModShift)},
		{"Find", sc(gui.KeyF, gui.ModSuper)},
		{"Toggle regex (in Find)", sc(gui.KeyR, gui.ModCtrl)},
		{"Next match (in Find)", sc(gui.KeyEnter, 0)},
		{"Previous match (in Find)", sc(gui.KeyEnter, gui.ModShift)},
		{"Previous prompt mark", sc(gui.KeyUp, gui.ModSuper)},
		{"Next prompt mark", sc(gui.KeyDown, gui.ModSuper)},
		{"Scroll page up", sc(gui.KeyPageUp, gui.ModShift)},
		{"Scroll page down", sc(gui.KeyPageDown, gui.ModShift)},
		{"Scroll to top", sc(gui.KeyHome, gui.ModShift)},
		{"Scroll to bottom", sc(gui.KeyEnd, gui.ModShift)},
		{"Increase font size", sc(gui.KeyEqual, gui.ModSuper)},
		{"Decrease font size", sc(gui.KeyMinus, gui.ModSuper)},
	}
}
