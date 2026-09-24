package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// keyDown and keyUp drive the real handlers with no window, as the other
// keyboard tests do. They return the bytes the child saw for that one event.
func keyDown(tm *Term, buf *[]byte, k gui.KeyCode, mods gui.Modifier) string {
	*buf = (*buf)[:0]
	tm.onKeyDown(gui.EventCtx{Event: &gui.Event{KeyCode: k, Modifiers: mods}})
	return string(*buf)
}

func keyUp(tm *Term, buf *[]byte, k gui.KeyCode, mods gui.Modifier) string {
	*buf = (*buf)[:0]
	tm.onKeyUp(gui.EventCtx{Event: &gui.Event{KeyCode: k, Modifiers: mods}})
	return string(*buf)
}

func char(tm *Term, buf *[]byte, r rune, mods gui.Modifier) string {
	*buf = (*buf)[:0]
	tm.onChar(gui.EventCtx{Event: &gui.Event{CharCode: uint32(r), Modifiers: mods}})
	return string(*buf)
}

// withRightOptionComposes sets the macOS right-Option rule for one test, so the
// tests behave the same on every host OS.
func withRightOptionComposes(t *testing.T, on bool) {
	t.Helper()
	orig := rightOptionComposes
	t.Cleanup(func() { rightOptionComposes = orig })
	rightOptionComposes = on
}

// A German Mac types @ with Option+L and [ with Option+5. Right Option now
// types what the layout prints: the key-down sends nothing, and the char event
// that follows reaches the child. Regression: both keys were lost (Option+L
// became ESC l, and Option+5 sent nothing at all).
func TestRightOption_TypesLayoutCharacter(t *testing.T) {
	withRightOptionComposes(t, true)
	tm, buf := newKeyboardTerm(24, 80)
	keyDown(tm, buf, gui.KeyRightAlt, gui.ModAlt)
	for _, c := range []struct {
		k gui.KeyCode
		r rune
	}{{gui.KeyL, '@'}, {gui.Key5, '['}, {gui.KeyF, 'ƒ'}} {
		if got := keyDown(tm, buf, c.k, gui.ModAlt); got != "" {
			t.Errorf("right Option key-down %v sent %q, want nothing", c.k, got)
		}
		if got := char(tm, buf, c.r, gui.ModAlt); got != string(c.r) {
			t.Errorf("right Option char %q: child saw %q", c.r, got)
		}
	}
	// Right Option only changes keys that type text. An arrow is still Alt.
	if got := keyDown(tm, buf, gui.KeyLeft, gui.ModAlt); got != "\x1b[1;3D" {
		t.Errorf("right Option+Left = %q, want CSI 1;3D", got)
	}
}

// Left Option stays Meta. The char event macOS sends after it (ƒ for
// Option+F) must not reach the child as well.
func TestLeftOption_IsMeta(t *testing.T) {
	withRightOptionComposes(t, true)
	tm, buf := newKeyboardTerm(24, 80)
	keyDown(tm, buf, gui.KeyLeftAlt, gui.ModAlt)
	if got := keyDown(tm, buf, gui.KeyF, gui.ModAlt); got != "\x1bf" {
		t.Errorf("left Option+F = %q, want ESC f", got)
	}
	if got := char(tm, buf, 'ƒ', gui.ModAlt); got != "" {
		t.Errorf("left Option char ƒ reached the child as %q", got)
	}
	// Both Option keys held: Meta wins, since left Option is held on purpose.
	keyDown(tm, buf, gui.KeyRightAlt, gui.ModAlt)
	if got := keyDown(tm, buf, gui.KeyB, gui.ModAlt); got != "\x1bb" {
		t.Errorf("both Options+B = %q, want ESC b", got)
	}
}

// A release that macOS lost (focus moved while right Option was held) must
// not leave the pane typing layout characters forever. Any key event with no
// Alt at all clears the tracked Option state.
func TestRightOption_StateClearsWithoutAlt(t *testing.T) {
	withRightOptionComposes(t, true)
	tm, buf := newKeyboardTerm(24, 80)
	keyDown(tm, buf, gui.KeyRightAlt, gui.ModAlt)
	keyDown(tm, buf, gui.KeyA, 0) // Option was released elsewhere
	if got := keyDown(tm, buf, gui.KeyF, gui.ModAlt); got != "\x1bf" {
		t.Errorf("Alt+F after lost release = %q, want ESC f", got)
	}
}

// Off macOS the right Alt key is plain Alt (AltGr arrives as its own
// modifier state), so it stays Meta.
func TestRightAlt_MetaWhenNotComposing(t *testing.T) {
	withRightOptionComposes(t, false)
	tm, buf := newKeyboardTerm(24, 80)
	keyDown(tm, buf, gui.KeyRightAlt, gui.ModAlt)
	if got := keyDown(tm, buf, gui.KeyF, gui.ModAlt); got != "\x1bf" {
		t.Errorf("right Alt+F = %q, want ESC f", got)
	}
}

// Meta applies to every key that types text, not only letters, and keeps
// Shift. Regression: Alt+. (readline yank-last-arg) and Alt+1 sent nothing,
// and Alt+Shift+F sent ESC f, the same as Alt+F.
func TestEncodeKeyEvent_MetaTextKeys(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	cases := []struct {
		k    gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyPeriod, gui.ModAlt, "\x1b."},
		{gui.KeyPeriod, gui.ModAlt | gui.ModShift, "\x1b>"},
		{gui.Key1, gui.ModAlt, "\x1b1"},
		{gui.KeyF, gui.ModAlt | gui.ModShift, "\x1bF"},
		{gui.KeySpace, gui.ModAlt, "\x1b "},
		{gui.KeyC, gui.ModAlt | gui.ModCtrl, "\x1b\x03"},
	}
	for _, c := range cases {
		e := &gui.Event{KeyCode: c.k, Modifiers: c.mods}
		got := string(tm.encodeKeyEvent(e, nil, c.mods.Has(gui.ModShift), c.mods.Has(gui.ModCtrl)))
		if got != c.want {
			t.Errorf("key %v mods %v = %q, want %q", c.k, c.mods, got, c.want)
		}
	}
}

// Control chords beyond A–Z. Regression: Ctrl+\ (SIGQUIT), Ctrl+Space (NUL),
// Ctrl+] and Ctrl+_ sent nothing.
func TestEncodeKeyEvent_ControlPunctuation(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	cases := []struct {
		k     gui.KeyCode
		shift bool
		want  byte
	}{
		{gui.KeySpace, false, 0x00},
		{gui.Key2, true, 0x00}, // Ctrl+@
		{gui.KeyLeftBracket, false, 0x1b},
		{gui.KeyBackslash, false, 0x1c},
		{gui.KeyRightBracket, false, 0x1d},
		{gui.Key6, true, 0x1e}, // Ctrl+^
		{gui.KeyMinus, true, 0x1f},
		{gui.KeySlash, false, 0x1f},
	}
	for _, c := range cases {
		mods := gui.ModCtrl
		if c.shift {
			mods |= gui.ModShift
		}
		got := tm.encodeKeyEvent(&gui.Event{KeyCode: c.k, Modifiers: mods}, nil, c.shift, true)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("Ctrl+%v (shift %v) = %q, want %#x", c.k, c.shift, got, c.want)
		}
	}
}

// Modified editing and paging keys use xterm's CSI Ps;mod~ form, and Alt is a
// modifier parameter on cursor and function keys, not an ESC prefix.
// Regression: Ctrl+Delete sent plain Delete, Ctrl+PageUp plain PageUp, and
// Alt+Left ESC CSI D.
func TestEncodeKeyEvent_ModifiedFunctionalKeys(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	cases := []struct {
		k    gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyDelete, gui.ModCtrl, "\x1b[3;5~"},
		{gui.KeyDelete, 0, "\x1b[3~"},
		{gui.KeyPageUp, gui.ModCtrl, "\x1b[5;5~"},
		{gui.KeyPageDown, gui.ModCtrl | gui.ModShift, "\x1b[6;6~"},
		{gui.KeyLeft, gui.ModAlt, "\x1b[1;3D"},
		{gui.KeyUp, gui.ModAlt | gui.ModShift, "\x1b[1;4A"},
		{gui.KeyHome, gui.ModAlt, "\x1b[1;3H"},
		{gui.KeyF5, gui.ModAlt, "\x1b[15;3~"},
		{gui.KeyF1, gui.ModAlt, "\x1b[1;3P"},
		{gui.KeyInsert, gui.ModAlt, "\x1b[2;3~"},
	}
	for _, c := range cases {
		e := &gui.Event{KeyCode: c.k, Modifiers: c.mods}
		got := string(tm.encodeKeyEvent(e, nil, c.mods.Has(gui.ModShift), c.mods.Has(gui.ModCtrl)))
		if got != c.want {
			t.Errorf("key %v mods %v = %q, want %q", c.k, c.mods, got, c.want)
		}
	}
}

// Under KKP, Alt is in the modifier parameter and never also an ESC prefix,
// and Alt chords on text keys are CSI u. Enter, Tab and Backspace keep their
// legacy bytes unless modified or flag 8 is set, so `reset` still works after
// an app that pushed flag 1 crashes. Regression: Alt+Enter sent
// ESC CSI 13;3u, and plain Enter under flag 1 sent CSI 13u.
func TestEncodeKeyEvent_KKP(t *testing.T) {
	cases := []struct {
		flags uint32
		k     gui.KeyCode
		mods  gui.Modifier
		want  string
	}{
		{1, gui.KeyEnter, 0, "\r"},
		{1, gui.KeyTab, 0, "\t"},
		{1, gui.KeyBackspace, 0, "\x7f"},
		{1, gui.KeyEnter, gui.ModShift, "\x1b[13;2u"},
		{1, gui.KeyEnter, gui.ModAlt, "\x1b[13;3u"},
		{1, gui.KeyEscape, 0, "\x1b[27u"},
		{1, gui.KeyEscape, gui.ModAlt, "\x1b[27;3u"},
		{1, gui.KeyA, gui.ModAlt, "\x1b[97;3u"},
		{1, gui.KeyC, gui.ModCtrl | gui.ModAlt, "\x1b[99;7u"},
		{1, gui.KeyBackslash, gui.ModCtrl, "\x1b[92;5u"},
		{1, gui.KeyUp, gui.ModAlt, "\x1b[1;3A"},
		{1 | 8, gui.KeyEnter, 0, "\x1b[13u"},
		{1 | 8, gui.KeyBackspace, 0, "\x1b[127u"},
	}
	for _, c := range cases {
		tm, _ := newKeyboardTerm(24, 80)
		tm.grid.KittyKeyFlags = c.flags
		e := &gui.Event{KeyCode: c.k, Modifiers: c.mods}
		got := string(tm.encodeKeyEvent(e, nil, c.mods.Has(gui.ModShift), c.mods.Has(gui.ModCtrl)))
		if got != c.want {
			t.Errorf("flags %d key %v mods %v = %q, want %q", c.flags, c.k, c.mods, got, c.want)
		}
	}
}

// A release is sent only for a key whose press went to the child. Search,
// copy mode and shortcuts keep their presses, so their releases stay home
// too. Regression: Escape closing the search bar leaked CSI 27;1:3u.
func TestOnKeyUp_NoReleaseForKeptPress(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.grid.KittyKeyFlags = 1 | 2
	tm.search.active = true
	keyDown(tm, buf, gui.KeyEscape, 0)
	if got := keyUp(tm, buf, gui.KeyEscape, 0); got != "" {
		t.Errorf("release of search's Escape = %q, want nothing", got)
	}

	tm.copy.active = true
	keyDown(tm, buf, gui.KeyJ, 0)
	if got := keyUp(tm, buf, gui.KeyJ, 0); got != "" {
		t.Errorf("release of copy mode's j = %q, want nothing", got)
	}

	// A release with no press seen at all (the press went to another pane).
	tm.copy.active = false
	if got := keyUp(tm, buf, gui.KeyA, 0); got != "" {
		t.Errorf("release with no press = %q, want nothing", got)
	}
}

// Releases use the same form as the press, so the app can pair them: CSI u
// keys get :3 before u, cursor and function keys keep their legacy final.
// Regression: an Up release was CSI 57352;1:3u against a CSI A press.
func TestOnKeyUp_ReleaseMatchesPressForm(t *testing.T) {
	cases := []struct {
		k    gui.KeyCode
		want string
	}{
		{gui.KeyA, "\x1b[97;1:3u"},
		{gui.KeyEscape, "\x1b[27;1:3u"},
		{gui.KeyUp, "\x1b[1;1:3A"},
		{gui.KeyEnd, "\x1b[1;1:3F"},
		{gui.KeyPageUp, "\x1b[5;1:3~"},
		{gui.KeyDelete, "\x1b[3;1:3~"},
		{gui.KeyF1, "\x1b[1;1:3P"},
		{gui.KeyF3, "\x1b[13;1:3~"},
		{gui.KeyF12, "\x1b[24;1:3~"},
	}
	for _, c := range cases {
		tm, buf := newKeyboardTerm(24, 80)
		tm.grid.KittyKeyFlags = 1 | 2
		tm.grid.AltActive = true // plain PageUp goes to the child, not scrollback
		keyDown(tm, buf, c.k, 0)
		if got := keyUp(tm, buf, c.k, 0); got != c.want {
			t.Errorf("release of %v = %q, want %q", c.k, got, c.want)
		}
		if got := keyUp(tm, buf, c.k, 0); got != "" {
			t.Errorf("second release of %v = %q, want nothing", c.k, got)
		}
	}
}

// Enter, Tab and Backspace have no release events unless flag 8 is set, and
// modifier keys are reported, press and release, only under flag 8.
func TestOnKeyUp_Flag8Keys(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.grid.KittyKeyFlags = 1 | 2
	keyDown(tm, buf, gui.KeyEnter, 0)
	if got := keyUp(tm, buf, gui.KeyEnter, 0); got != "" {
		t.Errorf("Enter release without flag 8 = %q, want nothing", got)
	}
	if got := keyDown(tm, buf, gui.KeyLeftShift, gui.ModShift); got != "" {
		t.Errorf("Shift press without flag 8 = %q, want nothing", got)
	}
	if got := keyUp(tm, buf, gui.KeyLeftShift, 0); got != "" {
		t.Errorf("Shift release without flag 8 = %q, want nothing", got)
	}

	tm.grid.KittyKeyFlags = 1 | 2 | 8
	keyDown(tm, buf, gui.KeyEnter, 0)
	if got := keyUp(tm, buf, gui.KeyEnter, 0); got != "\x1b[13;1:3u" {
		t.Errorf("Enter release with flag 8 = %q, want CSI 13;1:3u", got)
	}
	if got := keyDown(tm, buf, gui.KeyLeftShift, gui.ModShift); got != "\x1b[57441;2u" {
		t.Errorf("Shift press with flag 8 = %q, want CSI 57441;2u", got)
	}
	if got := keyUp(tm, buf, gui.KeyLeftShift, 0); got != "\x1b[57441;1:3u" {
		t.Errorf("Shift release with flag 8 = %q, want CSI 57441;1:3u", got)
	}
}
