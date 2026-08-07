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
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
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
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
	if !intercepted {
		t.Error("PageDown should be intercepted when scrolled back")
	}
}

func TestScrollbackIntercept_PageUp_AltScreen_PassesThrough(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageUp}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
	if intercepted {
		t.Error("plain PageUp should pass through in alt screen")
	}
}

func TestScrollbackIntercept_ShiftPageUp_AltScreen_Scrolls(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	term.grid.AltActive = true
	e := &gui.Event{KeyCode: gui.KeyPageUp, Modifiers: gui.ModShift}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
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
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
	if !intercepted {
		t.Error("Shift+Home should scroll to top")
	}
}

func TestScrollbackIntercept_Home_Normal_NotIntercepted(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyHome}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
	if intercepted {
		t.Error("plain Home should not be intercepted")
	}
}

func TestScrollbackIntercept_NotScrollKey(t *testing.T) {
	term, _ := newKeyboardTerm(24, 80)
	e := &gui.Event{KeyCode: gui.KeyA}
	intercepted := term.scrollbackIntercept(e, nil, e.Modifiers.Has(gui.ModShift))
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
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: nil})
	if len(*buf) > 0 {
		t.Errorf("onKeyUp with flags=0 should not write, wrote %q", *buf)
	}
}

func TestOnKeyUp_KittyFlagsRelease_EmitsSequence(t *testing.T) {
	term, buf := newKeyboardTerm(24, 80)
	term.grid.KittyKeyFlags = 1 | 2 // bits 0+1: disambiguate + release
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: nil})
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

// --- Cfg.OnInput tap ---
//
// The tap is what lets a pane manager mirror input to sibling panes
// (workspace broadcast). Its value depends entirely on covering exactly the
// user-input paths and nothing else: a mouse report or a mirrored write
// replayed into another pane would be wrong or would loop.

// typed keys reach the tap, through both onChar (printable) and onKeyDown
// (control chords), and the tap sees the same bytes the pty did.
func TestOnInput_FiresForTypedKeys(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	var tapped []byte
	kinds := make([]InputKind, 0, 2)
	tm.cfg.OnInput = func(p []byte, kind InputKind) {
		tapped = append(tapped, p...)
		kinds = append(kinds, kind)
	}

	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'x'}, Window: nil})
	tm.onKeyDown(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: gui.KeyC, Modifiers: gui.ModCtrl}, Window: nil})

	if len(kinds) != 2 {
		t.Fatalf("OnInput fired %d times; want 2", len(kinds))
	}
	for i, k := range kinds {
		if k != InputKey {
			t.Errorf("call %d: kind = %v, want InputKey", i, k)
		}
	}
	if string(tapped) != string(*buf) {
		t.Errorf("tap saw %q; pty saw %q", tapped, *buf)
	}
	if want := "x\x03"; string(tapped) != want {
		t.Errorf("tapped %q, want %q", tapped, want)
	}
}

// Term.Write is the path broadcast mirrors *through*. If it fired the tap,
// two panes in a broadcasting tab would write to each other without end.
func TestOnInput_NotFiredByWrite(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	fired := 0
	tm.cfg.OnInput = func([]byte, InputKind) { fired++ }
	if _, err := tm.Write([]byte("echo hi\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if fired != 0 {
		t.Errorf("Write fired OnInput %d times; want 0", fired)
	}
}

// Mouse reports describe *this* pane's viewport — coordinates from one pane
// are meaningless in another, so they must stay out of the tap.
func TestOnInput_NotFiredByMouseReport(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	fired := 0
	tm.cfg.OnInput = func([]byte, InputKind) { fired++ }

	tm.grid.Mu.Lock()
	tm.grid.MouseSGR = true
	tm.grid.MouseTrackBtn = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR, tm.mouse.lastC = -1, -1
	tm.onMouseMove(gui.EventCtx{Layout: nil, Event: &gui.Event{MouseX: 10, MouseY: 25}, Window: &gui.Window{}})

	if len(*buf) == 0 {
		t.Fatal("expected a mouse report on the pty")
	}
	if fired != 0 {
		t.Errorf("mouse report fired OnInput %d times; want 0", fired)
	}
}

// A nil OnInput is the common case (standalone Term) and must not panic.
func TestOnInput_NilIsSafe(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'x'}, Window: nil})
}

// --- IME commit tests ---
//
// An IME commit arrives as a single EventChar whose CharCode holds only the
// first rune of the composed string; the whole commit is in IMEText (go-gui
// decodes it that way in gui/backend/metal/events.go). Reading CharCode alone
// truncated 日本語 to 日, which is issue #134.

// A multi-rune commit reaches the pty whole.
func TestOnChar_IMECommitWritesFullText(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32('日'), IMEText: "日本語"}, Window: nil})
	if got, want := string(*buf), "日本語"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}
}

// Ordinary typing sets IMEText to the same single character. The two agree,
// so the single-rune path must behave exactly as before.
func TestOnChar_SingleRuneUnaffectedByIMEText(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'x', IMEText: "x"}, Window: nil})
	if got, want := string(*buf), "x"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}
}

// Backends that never populate IMEText must still deliver the keystroke.
func TestOnChar_EmptyIMETextFallsBackToCharCode(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32('é')}, Window: nil})
	if got, want := string(*buf), "é"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}
}

// The X11 backend synthesizes a char event for every printable keypress,
// even chords onKeyDown already consumed (Super+Shift+V paste, Ctrl+C
// SIGINT, Alt+letter). AppKit suppresses those chars, so on Linux they
// must be dropped here or the paste payload picks up a trailing 'V'.
func TestOnChar_ModifierChordWritesNothing(t *testing.T) {
	mods := []gui.Modifier{
		gui.ModSuper,
		gui.ModSuper | gui.ModShift,
		gui.ModCtrl,
		gui.ModCtrl | gui.ModShift,
		gui.ModAlt,
		gui.ModAlt | gui.ModShift,
		gui.ModSuper | gui.ModCtrl | gui.ModAlt | gui.ModShift,
	}
	for _, m := range mods {
		tm, buf := newKeyboardTerm(24, 80)
		tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'V', Modifiers: m}, Window: nil})
		if len(*buf) != 0 {
			t.Errorf("mods %v: pty saw %q, want nothing", m, *buf)
		}
	}
}

// The modifier gate must not swallow ordinary typing: Shift is just the
// same letter's uppercase form, and plain chars pass untouched.
func TestOnChar_PlainAndShiftedCharsStillType(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'v'}, Window: nil})
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'V', Modifiers: gui.ModShift}, Window: nil})
	if got, want := string(*buf), "vV"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}
}

// The reported Linux bug end-to-end: X11 delivers a KeyDown for the paste
// chord and a separate Char for the key's letter. The keydown pastes; the
// char must be dropped rather than typed after the payload. Chords are
// remapped so the canonical paste chord matches on every platform
// (remapMod folds the macOS Super form into Windows' Ctrl+Shift).
func TestOnChar_PasteChordCharDropped(t *testing.T) {
	for _, mods := range []gui.Modifier{
		remapMod(gui.ModSuper),
		remapMod(gui.ModCtrlShift),
	} {
		tm, buf := newKeyboardTerm(24, 80)
		w := &gui.Window{}
		w.SetClipboardGetFn(func() string { return "hello" })

		tm.onKeyDown(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: gui.KeyV, Modifiers: mods}, Window: w})
		tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'V', Modifiers: mods | gui.ModShift}, Window: nil})

		if got, want := string(*buf), "hello"; got != want {
			t.Errorf("mods %v: pty saw %q, want %q (no trailing letter)", mods, got, want)
		}
	}
}

// KKP flag 8 reports keystrokes as CSI u escape codes. Composed text is not a
// keystroke and has no single codepoint to report, so the commit must go
// through as plain UTF-8 rather than as an escape sequence for its first rune.
func TestOnChar_IMECommitBypassesKittyEncoding(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.KittyKeyFlags = 8
	tm.grid.Mu.Unlock()

	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32('日'), IMEText: "日本語"}, Window: nil})
	if got, want := string(*buf), "日本語"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}

	// A single-rune event still takes the KKP path.
	*buf = (*buf)[:0]
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'a', IMEText: "a"}, Window: nil})
	if got, want := string(*buf), "\x1b[97u"; got != want {
		t.Errorf("pty saw %q, want %q", got, want)
	}
}

// The search bar takes the whole commit too — searching for CJK text is the
// case that needs IME most.
func TestOnChar_IMECommitReachesSearchBar(t *testing.T) {
	tm, _ := newKeyboardTerm(24, 80)
	tm.cmd = &gui.Window{} // the search path schedules a redraw
	tm.search.active = true
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32('日'), IMEText: "日本語"}, Window: nil})
	if got, want := tm.search.query, "日本語"; got != want {
		t.Errorf("search query = %q, want %q", got, want)
	}
}
