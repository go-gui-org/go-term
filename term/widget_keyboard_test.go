package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// newKeyboardTerm returns a Term whose pty writer captures encoded key
// bytes, making it suitable for encodeKeyEvent testing.
func newKeyboardTerm(rows, cols int) (*Term, *[]byte) {
	buf := make([]byte, 0, 64)
	t := &Term{
		grid: newGrid(rows, cols),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	t.mouse.hoverR.Store(-1)
	t.mouse.hoverC.Store(-1)
	return t, &buf
}

// --- encodeKeyEvent tests ---

func TestEncodeKeyEvent_ArrowKeys_Normal(t *testing.T) {
	term, buf := newKeyboardTerm(24, 80)
	cases := []struct {
		name string
		kc   gui.KeyCode
		want string
	}{
		{"Up", gui.KeyUp, "\x1b[A"},
		{"Down", gui.KeyDown, "\x1b[B"},
		{"Right", gui.KeyRight, "\x1b[C"},
		{"Left", gui.KeyLeft, "\x1b[D"},
	}
	for _, tc := range cases {
		e := &gui.Event{KeyCode: tc.kc}
		*buf = (*buf)[:0]
		got := term.encodeKeyEvent(e, nil, false, false)
		if string(got) != tc.want {
			t.Errorf("%s: encodeKeyEvent = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodeKeyEvent_ArrowKeys_AppCursor(t *testing.T) {
	term, buf := newKeyboardTerm(24, 80)
	term.grid.AppCursorKeys = true
	cases := []struct {
		name string
		kc   gui.KeyCode
		want string
	}{
		{"Up", gui.KeyUp, "\x1bOA"},
		{"Down", gui.KeyDown, "\x1bOB"},
		{"Right", gui.KeyRight, "\x1bOC"},
		{"Left", gui.KeyLeft, "\x1bOD"},
	}
	for _, tc := range cases {
		e := &gui.Event{KeyCode: tc.kc}
		*buf = (*buf)[:0]
		got := term.encodeKeyEvent(e, nil, false, false)
		if string(got) != tc.want {
			t.Errorf("%s: encodeKeyEvent = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodeKeyEvent_Enter(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyEnter}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\r" {
		t.Errorf("Enter: got %q, want %q", got, "\r")
	}
}

func TestEncodeKeyEvent_Backspace(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyBackspace}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x7f" {
		t.Errorf("Backspace: got %q, want %q", got, "\x7f")
	}
}

func TestEncodeKeyEvent_Tab(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyTab}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\t" {
		t.Errorf("Tab: got %q, want %q", got, "\t")
	}
}

func TestEncodeKeyEvent_Tab_Shift(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyTab, Modifiers: gui.ModShift}
	got := term.encodeKeyEvent(e, nil, true, false)
	if string(got) != "\x1b[Z" {
		t.Errorf("Shift+Tab: got %q, want %q", got, "\x1b[Z")
	}
}

func TestEncodeKeyEvent_Escape(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyEscape}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b" {
		t.Errorf("Escape: got %q, want %q", got, "\x1b")
	}
}

func TestEncodeKeyEvent_Delete(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyDelete}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b[3~" {
		t.Errorf("Delete: got %q, want %q", got, "\x1b[3~")
	}
}

func TestEncodeKeyEvent_Home_Normal(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyHome}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b[H" {
		t.Errorf("Home normal: got %q, want %q", got, "\x1b[H")
	}
}

func TestEncodeKeyEvent_Home_AppCursor(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AppCursorKeys = true
	e := &gui.Event{KeyCode: gui.KeyHome}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1bOH" {
		t.Errorf("Home appCursor: got %q, want %q", got, "\x1bOH")
	}
}

func TestEncodeKeyEvent_End_Normal(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyEnd}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b[F" {
		t.Errorf("End normal: got %q, want %q", got, "\x1b[F")
	}
}

func TestEncodeKeyEvent_PageUp_AltScreen(t *testing.T) {
	// Plain PageUp is intercepted by scrollback in normal mode. Only
	// test the encoding path when AltActive forces pass-through.
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageUp}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b[5~" {
		t.Errorf("PageUp alt screen: got %q, want %q", got, "\x1b[5~")
	}
}

func TestEncodeKeyEvent_PageDown_AltScreen(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageDown}
	got := term.encodeKeyEvent(e, nil, false, false)
	if string(got) != "\x1b[6~" {
		t.Errorf("PageDown alt screen: got %q, want %q", got, "\x1b[6~")
	}
}

func TestEncodeKeyEvent_CtrlLetter(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	cases := []struct {
		name string
		kc   gui.KeyCode
		want byte
	}{
		{"Ctrl+A", gui.KeyA, 1},
		{"Ctrl+B", gui.KeyB, 2},
		{"Ctrl+C", gui.KeyC, 3},
		{"Ctrl+Z", gui.KeyZ, 26},
	}
	for _, tc := range cases {
		e := &gui.Event{KeyCode: tc.kc, Modifiers: gui.ModCtrl}
		got := term.encodeKeyEvent(e, nil, false, true)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: got %v, want [%d]", tc.name, got, tc.want)
		}
	}
}

func TestEncodeKeyEvent_AltLetter(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyA, Modifiers: gui.ModAlt}
	got := term.encodeKeyEvent(e, nil, false, false)
	want := "\x1ba"
	if string(got) != want {
		t.Errorf("Alt+A: got %q, want %q", got, want)
	}
}

func TestEncodeKeyEvent_AltShiftArrow(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyUp, Modifiers: gui.ModAlt | gui.ModShift}
	got := term.encodeKeyEvent(e, nil, true, false)
	want := "\x1b\x1b[1;2A"
	if string(got) != want {
		t.Errorf("Alt+Shift+Up: got %q, want %q", got, want)
	}
}

func TestEncodeKeyEvent_FunctionKeys(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	cases := []struct {
		name string
		kc   gui.KeyCode
		want string
	}{
		{"F1", gui.KeyF1, "\x1bOP"},
		{"F2", gui.KeyF2, "\x1bOQ"},
		{"F3", gui.KeyF3, "\x1bOR"},
		{"F4", gui.KeyF4, "\x1bOS"},
		{"F5", gui.KeyF5, "\x1b[15~"},
		{"F6", gui.KeyF6, "\x1b[17~"},
		{"F7", gui.KeyF7, "\x1b[18~"},
		{"F8", gui.KeyF8, "\x1b[19~"},
		{"F9", gui.KeyF9, "\x1b[20~"},
		{"F10", gui.KeyF10, "\x1b[21~"},
		{"F11", gui.KeyF11, "\x1b[23~"},
		{"F12", gui.KeyF12, "\x1b[24~"},
	}
	for _, tc := range cases {
		e := &gui.Event{KeyCode: tc.kc}
		got := term.encodeKeyEvent(e, nil, false, false)
		if string(got) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodeKeyEvent_Keypad_AppKeypad(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AppKeypad = true
	cases := []struct {
		name string
		kc   gui.KeyCode
		want string
	}{
		{"KP0", gui.KeyKP0, "\x1bOp"},
		{"KP1", gui.KeyKP1, "\x1bOq"},
		{"KP2", gui.KeyKP2, "\x1bOr"},
		{"KP3", gui.KeyKP3, "\x1bOs"},
		{"KP4", gui.KeyKP4, "\x1bOt"},
		{"KP5", gui.KeyKP5, "\x1bOu"},
		{"KP6", gui.KeyKP6, "\x1bOv"},
		{"KP7", gui.KeyKP7, "\x1bOw"},
		{"KP8", gui.KeyKP8, "\x1bOx"},
		{"KP9", gui.KeyKP9, "\x1bOy"},
		{"KPDecimal", gui.KeyKPDecimal, "\x1bOn"},
		{"KPDivide", gui.KeyKPDivide, "\x1bOo"},
		{"KPMultiply", gui.KeyKPMultiply, "\x1bOj"},
		{"KPSubtract", gui.KeyKPSubtract, "\x1bOm"},
		{"KPAdd", gui.KeyKPAdd, "\x1bOk"},
	}
	for _, tc := range cases {
		e := &gui.Event{KeyCode: tc.kc}
		got := term.encodeKeyEvent(e, nil, false, false)
		if string(got) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodeKeyEvent_UnmappedKey_Nil(t *testing.T) {
	// A key with no terminal encoding returns nil.
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyCode(9999)} // nonexistent key
	got := term.encodeKeyEvent(e, nil, false, false)
	if got != nil {
		t.Errorf("unmapped key: expected nil, got %v", got)
	}
}

func TestEncodeKeyEvent_CtrlShiftLetter(t *testing.T) {
	// Ctrl+Shift+letter should encode as Ctrl+letter (shift has no special
	// meaning for control sequences).
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyA, Modifiers: gui.ModCtrl | gui.ModShift}
	got := term.encodeKeyEvent(e, nil, true, true)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("Ctrl+Shift+A: got %v, want [1]", got)
	}
}

// --- scrollbackIntercept tests ---

func TestScrollbackIntercept_PageUp_Scrolls(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	// Add enough scrollback to scroll.
	term.grid.ScrollbackCap = 100
	term.grid.Scrollback.Push([]cell{}, false)
	term.grid.ViewOffset = 1
	e := &gui.Event{KeyCode: gui.KeyPageUp}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if !intercepted {
		t.Error("PageUp should be intercepted when scrollback is available")
	}
	if !e.IsHandled {
		t.Error("PageUp should mark event handled")
	}
}

func TestScrollbackIntercept_PageDown_Scrolls(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.ScrollbackCap = 100
	term.grid.Scrollback.Push([]cell{}, false)
	term.grid.ViewOffset = 1
	e := &gui.Event{KeyCode: gui.KeyPageDown}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if !intercepted {
		t.Error("PageDown should be intercepted when scrolled back")
	}
}

func TestScrollbackIntercept_PageUp_AltScreen_PassesThrough(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageUp}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if intercepted {
		t.Error("plain PageUp should pass through in alt screen")
	}
}

func TestScrollbackIntercept_ShiftPageUp_AltScreen_Scrolls(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageUp, Modifiers: gui.ModShift}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if !intercepted {
		t.Error("Shift+PageUp should scroll even in alt screen")
	}
}

func TestScrollbackIntercept_ShiftHome_ScrollsToTop(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.ScrollbackCap = 100
	term.grid.Scrollback.Push([]cell{}, false)
	term.grid.ViewOffset = 1
	e := &gui.Event{KeyCode: gui.KeyHome, Modifiers: gui.ModShift}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if !intercepted {
		t.Error("Shift+Home should scroll to top")
	}
}

func TestScrollbackIntercept_Home_Normal_NotIntercepted(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyHome}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if intercepted {
		t.Error("plain Home should not be intercepted")
	}
}

func TestScrollbackIntercept_NotScrollKey(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyA}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift), e.Modifiers.Has(gui.ModCtrl))
	if intercepted {
		t.Error("letter A should not be intercepted")
	}
}

// --- keyModes tests ---

func TestKeyModes_Defaults(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	modes := term.keyModes()
	if modes.appCursor {
		t.Error("appCursor should default to false")
	}
	if modes.appKeypad {
		t.Error("appKeypad should default to false")
	}
	if modes.kittyKeyFlags != 0 {
		t.Error("kittyKeyFlags should default to 0")
	}
}

func TestKeyModes_KittyFlags(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.KittyKeyFlags = 3 // bits 0+1 set
	modes := term.keyModes()
	if modes.kittyKeyFlags != 3 {
		t.Errorf("kittyKeyFlags = %d, want 3", modes.kittyKeyFlags)
	}
}

// --- KKP encoding tests ---

func TestKittyKeySeq_Disabled_ReturnsNil(t *testing.T) {
	// With flags=0 (KKP disabled), kittyKeySeq returns nil for non-Escape keys.
	seq := kittyKeySeq(13, gui.ModCtrl, 0, false) // Ctrl+Enter
	if seq != nil {
		t.Errorf("KKP disabled: expected nil, got %q", seq)
	}
}

func TestKittyKeySeq_Enter_KKP(t *testing.T) {
	// With bit 0 set (disambiguate), Enter emits CSI u.
	seq := kittyKeySeq(13, 0, 1, false)
	if seq == nil {
		t.Fatal("KKP Enter should not be nil")
	}
	// Should be CSI 13 u (no modifiers)
	want := "\x1b[13u"
	if string(seq) != want {
		t.Errorf("KKP Enter: got %q, want %q", seq, want)
	}
}

func TestKittyKeySeq_CtrlC_KKP(t *testing.T) {
	// Ctrl+c codepoint 3, Ctrl modifier = 5.
	seq := kittyKeySeq(3, gui.ModCtrl, 1, false)
	if seq == nil {
		t.Fatal("KKP Ctrl+C should not be nil")
	}
	want := "\x1b[3;5u"
	if string(seq) != want {
		t.Errorf("KKP Ctrl+C: got %q, want %q", seq, want)
	}
}

func TestKittyKeySeq_ReleaseEvent(t *testing.T) {
	// Key release with bit 2 set: event-type 3. When mod==1,
	// the modifier field is still emitted (required with event-type).
	seq := kittyKeySeq(13, 0, 1|2, true) // Enter release
	if seq == nil {
		t.Fatal("KKP Enter release should not be nil")
	}
	want := "\x1b[13;1:3u"
	if string(seq) != want {
		t.Errorf("KKP Enter release: got %q, want %q", seq, want)
	}
}

func TestKittyKeyCodepoint_Valid(t *testing.T) {
	cases := []struct {
		kc gui.KeyCode
		cp int
	}{
		{gui.KeyEnter, 13},
		{gui.KeyBackspace, 127},
		{gui.KeyTab, 9},
		{gui.KeyEscape, 27},
		{gui.KeyF1, 57364}, // U+E00C (PUA F1)
	}
	for _, tc := range cases {
		cp, ok := kittyKeyCodepoint(tc.kc)
		if !ok {
			t.Errorf("kittyKeyCodepoint(%v) should be valid", tc.kc)
		}
		if cp != tc.cp {
			t.Errorf("kittyKeyCodepoint(%v) = %d, want %d", tc.kc, cp, tc.cp)
		}
	}
}

func TestKittyKeyCodepoint_Invalid(t *testing.T) {
	_, ok := kittyKeyCodepoint(gui.KeyCode(9999))
	if ok {
		t.Error("invalid keycode should not be valid")
	}
}

// --- modSS3 tests ---

func TestModSS3(t *testing.T) {
	seq := modSS3('A', 2) // Shift modifier
	want := "\x1b[1;2A"
	if string(seq) != want {
		t.Errorf("modSS3: got %q, want %q", seq, want)
	}
}

// --- keypadSeq tests ---

func TestKeypadSeq_NormalKeys_NotEncoded(t *testing.T) {
	// Regular keys (not keypad) should not be encoded by keypadSeq.
	seq := keypadSeq(gui.KeyA)
	if len(seq) > 0 {
		t.Errorf("regular key A should not be encoded by keypadSeq, got %q", seq)
	}
}

// --- onKeyUp tests ---

func TestOnKeyUp_KittyFlagsDisabled_NoOutput(t *testing.T) {
	term, buf := newKeyboardTerm(24, 80)
	// KittyKeyFlags & 2 == 0 → no release events.
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, nil)
	if len(*buf) > 0 {
		t.Errorf("onKeyUp with flags=0 should not write, wrote %q", *buf)
	}
}

func TestOnKeyUp_KittyFlagsRelease_EmitsSequence(t *testing.T) {
	term, buf := newKeyboardTerm(24, 80)
	term.grid.KittyKeyFlags = 1 | 2 // bits 0+1: disambiguate + release
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, nil)
	if len(*buf) == 0 {
		t.Fatal("onKeyUp with release flag should write sequence")
	}
	if !e.IsHandled {
		t.Error("onKeyUp should mark event handled")
	}
	want := "\x1b[13;1:3u"
	if string(*buf) != want {
		t.Errorf("onKeyUp Enter release: got %q, want %q", *buf, want)
	}
}
