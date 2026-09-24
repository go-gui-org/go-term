package term

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-gui-org/go-gui/gui"
)

// rightOptionComposes enables the macOS Option split: left Option is Meta,
// right Option types what the keyboard layout prints. WezTerm defaults to the
// same split. Both halves are needed: readline's M-f, M-b and M-. live on
// Option for US users, and on a German layout @ [ ] { } | exist only as
// Option chords. Other platforms have no such conflict (AltGr is its own
// modifier state on X11, so right Alt there is plain Alt). A var so tests run
// the same on every host.
var rightOptionComposes = runtime.GOOS == "darwin"

// keyState is keyboard state kept across events. Main thread only, like every
// key handler.
type keyState struct {
	// leftAlt and rightAlt track which Option keys are held, from the
	// modifier-key events. go-gui reports a single ModAlt bit for both.
	leftAlt, rightAlt bool
	// down has one bit per key code whose press went to the child. onKeyUp
	// sends a KKP release only for those, and clears the bit.
	down [keyDownWords]uint64
}

// keyDownWords sizes keyState.down: go-gui key codes stay below 384.
const keyDownWords = 6

// noteOption updates the held Option keys from a key event. An event with no
// Alt at all clears both: a release the pane never saw (focus moved while the
// key was held) must not leave it composing, or Meta, for good.
func (s *keyState) noteOption(k gui.KeyCode, mods gui.Modifier, down bool) {
	held := down && mods.Has(gui.ModAlt)
	if !mods.Has(gui.ModAlt) {
		s.leftAlt, s.rightAlt = false, false
	}
	switch k {
	case gui.KeyLeftAlt:
		s.leftAlt = held
	case gui.KeyRightAlt:
		s.rightAlt = held
	}
}

// optionComposes reports whether an Alt chord with mods is macOS right Option
// typing a layout character rather than Meta. Left Option held as well means
// Meta: holding it is deliberate. Ctrl or Cmd held means a shortcut, not text.
func (s *keyState) optionComposes(mods gui.Modifier) bool {
	return rightOptionComposes && s.rightAlt && !s.leftAlt &&
		mods&(gui.ModCtrl|gui.ModSuper) == 0
}

// markDown records that k's press went to the child.
func (s *keyState) markDown(k gui.KeyCode) {
	if i := int(k) / 64; i < keyDownWords {
		s.down[i] |= 1 << (uint(k) % 64)
	}
}

// takeDown reports whether k's press went to the child, and forgets it.
func (s *keyState) takeDown(k gui.KeyCode) bool {
	i := int(k) / 64
	if i >= keyDownWords {
		return false
	}
	bit := uint64(1) << (uint(k) % 64)
	was := s.down[i]&bit != 0
	s.down[i] &^= bit
	return was
}

// keyModes captures keyboard mode state read under grid.Mu and used
// in onKeyDown/onKeyUp without holding the lock.
type keyModes struct {
	appCursor     bool
	appKeypad     bool
	kittyKeyFlags uint32
}

func (t *Term) keyModes() keyModes {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return keyModes{
		appCursor:     t.grid.AppCursorKeys,
		appKeypad:     t.grid.AppKeypad,
		kittyKeyFlags: t.grid.KittyKeyFlags,
	}
}

// arrowSeq returns the unmodified cursor-key sequence for final byte
// 'A'..'D', in SS3 form under DECCKM (application cursor keys) and CSI form
// otherwise. Shared by the keyboard path and the alt-screen wheel, which
// synthesizes the same keys — an app that switched to DECCKM must see the
// form it asked for from both.
func arrowSeq(final byte, appCursor bool) []byte {
	if appCursor {
		return []byte{0x1B, 'O', final}
	}
	return []byte{0x1B, '[', final}
}

// isAltActive reports whether the alt screen is active, acquiring grid.Mu
// briefly. Used by scrollback handling in encodeKeyEvent.
func (t *Term) isAltActive() bool {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.AltActive
}

// recompileSearchRE compiles searchQuery into searchRE when regex mode is
// active. Clears searchRE and searchREErr when not in regex mode or when the
// query is empty. Go's regexp is RE2 (linear-time, no backtracking), so a
// hostile pattern cannot wedge the main thread the way a backtracker could;
// the query is length-capped at MaxGridDim regardless.
func (t *Term) recompileSearchRE() {
	if t.search.regex && t.search.query != "" {
		t.search.re, t.search.reErr = regexp.Compile(t.search.query)
	} else {
		t.search.re = nil
		t.search.reErr = nil
	}
}

// maxIMECommitBytes caps a single IME commit delivered to the pty. Real
// commits are tens of bytes; a rogue IME handing over megabytes on the main
// thread would stall pty writes, so truncate at a rune boundary instead.
// truncatePaste already implements exactly that policy — reuse it.
const maxIMECommitBytes = 1 << 14

// onChar receives printable character input from the OS.
func (t *Term) onChar(ctx gui.EventCtx) {
	if ctx.Event.CharCode == 0 {
		return
	}
	// A chord holding Cmd/Ctrl/Alt belongs to onKeyDown (shortcut handlers,
	// control bytes, Meta, KKP sequences). Backends still send a char event
	// for many of them: X11 for every printable keypress, and macOS for
	// Option chords (Option+F arrives as ƒ). Without this gate a Super+Shift+V
	// paste would also type 'V', Ctrl+C would send its control byte *and* the
	// letter, and Meta+f would be followed by ƒ. Drop the duplicate char; keep
	// Shift, which is just the same letter's uppercase form.
	//
	// The one exception is macOS right Option (see optionComposes): there the
	// char event is the text the user meant to type, @ or [ on a German
	// layout, and onKeyDown left the key alone for exactly this path. Alt is
	// then dropped from the modifiers, because the layout used it up to pick
	// the character; KKP must not report the key as an Alt chord.
	mods := ctx.Event.Modifiers
	if mods.Has(gui.ModAlt) && t.keys.optionComposes(mods) {
		mods &^= gui.ModAlt
	}
	if mods&(gui.ModCtrl|gui.ModAlt|gui.ModSuper) != 0 {
		ctx.Consume()
		return
	}
	// Hints: label characters arrive here for the same reason copy mode's
	// motions do, and are swallowed under the same rule — an unmatched label
	// letter must not reach the shell's command line.
	if t.hints.active {
		t.handleHintsChar(rune(ctx.Event.CharCode), ctx.Window)
		ctx.Consume()
		return
	}
	// Copy mode: bare printable keys (the vim motions) arrive here, not in
	// onKeyDown — on macOS an unmodified printable key produces only OnChar.
	// See the dispatch-split comment in widget_copymode.go. Always swallow,
	// matched or not: a leaked 'j' would land in the shell's command line. The
	// search bar, which copy mode can open, still needs its characters.
	if t.copy.active && !t.copy.searching {
		t.handleCopyModeChar(rune(ctx.Event.CharCode), ctx.Window)
		ctx.Event.IsHandled = true
		return
	}
	// An IME commit delivers the whole composed string in IMEText; CharCode
	// carries only its first rune, so writing CharCode alone truncates 日本語
	// to 日. For ordinary typing IMEText is that same single character, so the
	// two agree and the fast paths below stay on the single-rune branch.
	// IMEText is empty on backends that do not populate it — fall back then.
	text := ctx.Event.IMEText
	if text == "" {
		text = string(rune(ctx.Event.CharCode))
	}
	text = truncatePaste(text, maxIMECommitBytes)
	if t.search.active {
		// A single bulk commit must not overshoot the cap the per-keystroke
		// path enforces: trim the commit to the remaining rune budget.
		if n := utf8.RuneCountInString(t.search.query); n < MaxGridDim {
			if rs := []rune(text); len(rs) > MaxGridDim-n {
				text = string(rs[:MaxGridDim-n])
			}
			t.search.query += text
			t.recompileSearchRE()
		}
		ctx.Consume()
		t.bumpVersion()
		t.queueCommand(func(w *gui.Window) { w.InvalidateLayout() })
		return
	}
	t.snapToLive()
	r := rune(ctx.Event.CharCode)
	// A commit longer than one rune is composed text, not a keystroke. Skip
	// the KKP encoder for it: there is no single codepoint to report, and the
	// modifiers held during composition describe the IME's own keys, not the
	// text it produced.
	singleRune := utf8.RuneLen(r) == len(text)

	// KKP flag 8: report all printable keys as CSI u escape codes.
	// The codepoint is the base (unshifted) form; Shift is in the modifier.
	kkpFlags := t.keyModes().kittyKeyFlags
	if singleRune && kkpFlags&8 != 0 {
		cp := int(r)
		if r >= 'A' && r <= 'Z' && mods.Has(gui.ModShift) {
			cp = int(r-'A') + 'a'
		}
		if seq := kittyPrintableSeq(cp, r, mods, kkpFlags); seq != nil {
			t.writeBytes(seq)
			ctx.Consume()
			return
		}
	}

	// Keep the single-rune path allocation-free — it is every ordinary
	// keystroke. Only a real IME commit pays for the conversion.
	if singleRune {
		var buf [4]byte
		if n := utf8.EncodeRune(buf[:], r); n > 0 {
			t.writeBytes(buf[:n])
		}
	} else {
		t.writeBytes([]byte(text))
	}
	ctx.Consume()
}

// kittyModParam returns the KKP modifier parameter: 1 plus the sum of the
// modifier bits (shift 1, alt 2, ctrl 4, super 8).
func kittyModParam(mods gui.Modifier) int {
	mod := 1
	if mods.Has(gui.ModShift) {
		mod += 1
	}
	if mods.Has(gui.ModAlt) {
		mod += 2
	}
	if mods.Has(gui.ModCtrl) {
		mod += 4
	}
	if mods.Has(gui.ModSuper) {
		mod += 8
	}
	return mod
}

// kittyPrintableSeq encodes a printable keystroke under KKP flag 8 (report all
// keys as escape codes). base is the unshifted codepoint that belongs in the
// key field; produced is the character the OS actually generated.
//
// Two optional fields carry the shifted form, and without one of them the child
// cannot tell Shift+m from M — the shift state alone does not name a layout's
// uppercase. Flag 4 (report alternate keys) puts the shifted codepoint in the
// key field as base:shifted; flag 16 (report associated text) appends the
// produced text as a third parameter. fish asks for both, and replying with a
// bare "CSI 109;2u" made every shifted character vanish at its prompt.
//
// The modifier field is mandatory once the text field is present, even when it
// is the no-modifier 1, because the parameters are positional.
//
// Returns nil when flag 8 is off, so the caller falls back to writing the
// character itself.
func kittyPrintableSeq(
	base int, produced rune, mods gui.Modifier, flags uint32,
) []byte {
	if flags&8 == 0 || base <= 0 {
		return nil
	}
	mod := kittyModParam(mods)
	// Longest form is base:shifted;mod;text — five digits per codepoint plus
	// four separators, so one allocation covers every keystroke.
	b := make([]byte, 0, 24)
	b = append(b, 0x1b, '[')
	b = strconv.AppendInt(b, int64(base), 10)
	// Alternate key: only meaningful when shifting actually changed the
	// character. Shift+1 on a US layout reports '!' as its own base (onChar
	// has no key code to derive '1' from), so there is nothing to add there.
	if flags&4 != 0 && int(produced) != base {
		b = append(b, ':')
		b = strconv.AppendInt(b, int64(produced), 10)
	}
	// The text field reports what the key typed, so it carries text only.
	// Control characters name a key, not text, and KKP forbids them here;
	// onKeyDown owns Enter/Tab/Escape/Backspace, so this is a backstop
	// against a backend that routes one of them through onChar instead.
	wantText := flags&16 != 0 && produced >= 0x20 && produced != 0x7f
	if mod != 1 || wantText {
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(mod), 10)
	}
	if wantText {
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(produced), 10)
	}
	b = append(b, 'u')
	return b
}

// kittyKeySeq encodes a key in Kitty Keyboard Protocol format: CSI codepoint u
// or CSI codepoint ; modifiers u. Returns nil when flags == 0 (legacy mode).
// The modifier parameter follows the KKP spec: 1=none, 2=shift, 3=shift+alt,
// 5=ctrl, 6=shift+ctrl, 9=super, ... (1 + sum of modifier bits).
// When release is true, generates a key release sequence (event-type 3):
// CSI codepoint ; modifiers : 3 u. The modifier field is mandatory when
// event-type is present, even when mod==1 (no modifiers).
func kittyKeySeq(codepoint int, mods gui.Modifier, flags uint32, release bool) []byte {
	if flags == 0 || codepoint <= 0 {
		return nil
	}
	mod := kittyModParam(mods)
	b := []byte("\x1b[")
	b = strconv.AppendInt(b, int64(codepoint), 10)
	if mod != 1 || release {
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(mod), 10)
	}
	if release {
		b = append(b, ':', '3')
	}
	b = append(b, 'u')
	return b
}

// kittyKeyCodepoint returns the KKP codepoint for a key whose KKP form is
// CSI codepoint u, or (0, false) when it has none. Modifier keys map to the
// spec's private-use codepoints; keys that type text map to their unshifted US
// character (see textKeyBase). Cursor, editing and function keys are not here:
// KKP keeps their legacy CSI forms, so their releases are built by
// legacyFuncForm instead.
func kittyKeyCodepoint(k gui.KeyCode) (int, bool) {
	switch k {
	case gui.KeyLeftShift:
		return 57441, true
	case gui.KeyRightShift:
		return 57447, true
	case gui.KeyLeftControl:
		return 57442, true
	case gui.KeyRightControl:
		return 57448, true
	case gui.KeyLeftAlt:
		return 57443, true
	case gui.KeyRightAlt:
		return 57449, true
	case gui.KeyLeftSuper:
		return 57444, true
	case gui.KeyRightSuper:
		return 57450, true
	case gui.KeyEnter, gui.KeyKPEnter:
		return 13, true
	case gui.KeyBackspace:
		return 127, true
	case gui.KeyTab:
		return 9, true
	case gui.KeyEscape:
		return 27, true
	}
	if b, ok := textKeyBase(k); ok {
		return int(b), true
	}
	return 0, false
}

// isModifierKey reports whether k is a Shift, Ctrl, Alt or Super key itself.
// KKP reports these only under flag 8 (report all keys as escape codes).
func isModifierKey(k gui.KeyCode) bool {
	switch k {
	case gui.KeyLeftShift, gui.KeyRightShift,
		gui.KeyLeftControl, gui.KeyRightControl,
		gui.KeyLeftAlt, gui.KeyRightAlt,
		gui.KeyLeftSuper, gui.KeyRightSuper:
		return true
	}
	return false
}

// legacyFuncForm returns the CSI form KKP keeps for a cursor, editing or
// function key: CSI 1;mod final (ps "1") or CSI ps;mod ~ (final '~'). F3 is
// CSI 13~ rather than CSI 1;mod R, because CSI R is also the cursor position
// report and an app could not tell the two apart.
func legacyFuncForm(k gui.KeyCode) (ps string, final byte, ok bool) {
	switch k {
	case gui.KeyUp:
		return "1", 'A', true
	case gui.KeyDown:
		return "1", 'B', true
	case gui.KeyRight:
		return "1", 'C', true
	case gui.KeyLeft:
		return "1", 'D', true
	case gui.KeyHome:
		return "1", 'H', true
	case gui.KeyEnd:
		return "1", 'F', true
	case gui.KeyF1:
		return "1", 'P', true
	case gui.KeyF2:
		return "1", 'Q', true
	case gui.KeyF4:
		return "1", 'S', true
	case gui.KeyInsert:
		return "2", '~', true
	case gui.KeyDelete:
		return "3", '~', true
	case gui.KeyPageUp:
		return "5", '~', true
	case gui.KeyPageDown:
		return "6", '~', true
	case gui.KeyF3:
		return "13", '~', true
	case gui.KeyF5:
		return "15", '~', true
	case gui.KeyF6:
		return "17", '~', true
	case gui.KeyF7:
		return "18", '~', true
	case gui.KeyF8:
		return "19", '~', true
	case gui.KeyF9:
		return "20", '~', true
	case gui.KeyF10:
		return "21", '~', true
	case gui.KeyF11:
		return "23", '~', true
	case gui.KeyF12:
		return "24", '~', true
	}
	return "", 0, false
}

// kittyReleaseSeq encodes the KKP release (event type 3) of k, in the same
// form its press used so the app can pair the two. Returns nil for a key that
// has no release under flags:
//   - Enter, Tab and Backspace send legacy bytes on press unless flag 8 is
//     set, so they have no release without it either (KKP spec, "report event
//     types"): an app that crashed with flag 2 on must not break `reset`.
//   - Modifier keys are reported at all only under flag 8.
func kittyReleaseSeq(k gui.KeyCode, mods gui.Modifier, flags uint32) []byte {
	switch {
	case k == gui.KeyEnter || k == gui.KeyKPEnter || k == gui.KeyTab || k == gui.KeyBackspace,
		isModifierKey(k):
		if flags&8 == 0 {
			return nil
		}
	}
	if ps, final, ok := legacyFuncForm(k); ok {
		b := append([]byte("\x1b["), ps...)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(kittyModParam(mods)), 10)
		return append(b, ':', '3', final)
	}
	if cp, ok := kittyKeyCodepoint(k); ok {
		return kittyKeySeq(cp, mods, flags, true)
	}
	return nil
}

// textKeyBase returns the character a text-typing key prints with no
// modifiers on a US layout: 'a' for KeyA, '.' for KeyPeriod, ' ' for KeySpace.
// go-gui key codes name physical keys and equal that character, so this is a
// range check. Meta and control chords are built from it: once Ctrl or a
// Meta Option is held, the OS reports no layout character to use instead.
func textKeyBase(k gui.KeyCode) (byte, bool) {
	switch {
	case k >= gui.KeyA && k <= gui.KeyZ:
		return byte('a' + (k - gui.KeyA)), true
	case k >= gui.Key0 && k <= gui.Key9:
		return byte(k), true
	}
	switch k {
	case gui.KeySpace, gui.KeyApostrophe, gui.KeyComma, gui.KeyMinus,
		gui.KeyPeriod, gui.KeySlash, gui.KeySemicolon, gui.KeyEqual,
		gui.KeyLeftBracket, gui.KeyBackslash, gui.KeyRightBracket,
		gui.KeyGraveAccent:
		return byte(k), true
	}
	return 0, false
}

// usShifted returns what Shift turns a US-layout base character into. Meta
// needs it because Alt+Shift+. is M-> (end of history in readline), not M-.
func usShifted(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	const plain, shifted = "1234567890-=[]\\;',./`", "!@#$%^&*()_+{}|:\"<>?~"
	if i := strings.IndexByte(plain, b); i >= 0 {
		return shifted[i]
	}
	return b
}

// ctrlByte returns the C0 control byte a Ctrl chord on base sends, following
// xterm and the VT convention: letters are 1..26, Ctrl+Space and Ctrl+2 (@)
// are NUL, Ctrl+[ \ ] are ESC FS GS, Ctrl+6 (^) is RS, Ctrl+- and Ctrl+/ (_)
// are US, and Ctrl+3..8 repeat that row. Shift does not change the byte, so
// Ctrl+Shift+2 is also NUL. Returns false for keys with no control byte
// (Ctrl+1, Ctrl+.), which then send nothing.
func ctrlByte(base byte) (byte, bool) {
	if base >= 'a' && base <= 'z' {
		return base - 'a' + 1, true
	}
	switch base {
	case ' ', '2', '`':
		return 0x00, true
	case '[', '3':
		return 0x1b, true
	case '\\', '4':
		return 0x1c, true
	case ']', '5':
		return 0x1d, true
	case '6':
		return 0x1e, true
	case '-', '/', '7':
		return 0x1f, true
	case '8':
		return 0x7f, true
	}
	return 0, false
}

func keypadSeq(k gui.KeyCode) []byte {
	switch k {
	case gui.KeyKP0:
		return []byte("\x1bOp")
	case gui.KeyKP1:
		return []byte("\x1bOq")
	case gui.KeyKP2:
		return []byte("\x1bOr")
	case gui.KeyKP3:
		return []byte("\x1bOs")
	case gui.KeyKP4:
		return []byte("\x1bOt")
	case gui.KeyKP5:
		return []byte("\x1bOu")
	case gui.KeyKP6:
		return []byte("\x1bOv")
	case gui.KeyKP7:
		return []byte("\x1bOw")
	case gui.KeyKP8:
		return []byte("\x1bOx")
	case gui.KeyKP9:
		return []byte("\x1bOy")
	case gui.KeyKPDecimal:
		return []byte("\x1bOn")
	case gui.KeyKPDivide:
		return []byte("\x1bOo")
	case gui.KeyKPMultiply:
		return []byte("\x1bOj")
	case gui.KeyKPSubtract:
		return []byte("\x1bOm")
	case gui.KeyKPAdd:
		return []byte("\x1bOk")
	case gui.KeyKPEqual:
		return []byte("\x1bOX")
	default:
		return nil
	}
}

// modParam returns the xterm modifier parameter (2..8) for shift/alt/ctrl
// combinations, or 0 when no modifiers are active.
func modParam(shift, alt, ctrl bool) int {
	n := 1
	if shift {
		n++
	}
	if alt {
		n += 2
	}
	if ctrl {
		n += 4
	}
	if n == 1 {
		return 0
	}
	return n
}

// modTilde returns \x1b[Ps~ (no modifier) or \x1b[Ps;N~ (with modifier).
func modTilde(ps string, mod int) []byte {
	if mod == 0 {
		return []byte("\x1b[" + ps + "~")
	}
	b := append([]byte("\x1b["), ps...)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(mod), 10)
	b = append(b, '~')
	return b
}

// modSS3 returns \x1bOl (no modifier) or \x1b[1;Nl (with modifier).
func modSS3(letter byte, mod int) []byte {
	if mod == 0 {
		return []byte{0x1b, 'O', letter}
	}
	b := []byte("\x1b[1;")
	b = strconv.AppendInt(b, int64(mod), 10)
	b = append(b, letter)
	return b
}

// funcKeySeq returns the xterm sequence for Insert and F1–F12 with modifier
// parameter mod (0 for none; see modParam). Under KKP (kkp true) F3 takes its
// CSI 13~ form; see legacyFuncForm.
func funcKeySeq(k gui.KeyCode, mod int, kkp bool) []byte {
	switch k {
	case gui.KeyInsert:
		return modTilde("2", mod)
	case gui.KeyF1:
		return modSS3('P', mod)
	case gui.KeyF2:
		return modSS3('Q', mod)
	case gui.KeyF3:
		if kkp {
			return modTilde("13", mod)
		}
		return modSS3('R', mod)
	case gui.KeyF4:
		return modSS3('S', mod)
	case gui.KeyF5:
		return modTilde("15", mod)
	case gui.KeyF6:
		return modTilde("17", mod)
	case gui.KeyF7:
		return modTilde("18", mod)
	case gui.KeyF8:
		return modTilde("19", mod)
	case gui.KeyF9:
		return modTilde("20", mod)
	case gui.KeyF10:
		return modTilde("21", mod)
	case gui.KeyF11:
		return modTilde("23", mod)
	case gui.KeyF12:
		return modTilde("24", mod)
	}
	return nil
}

// onKeyDown receives non-character keys (arrows, Enter, Backspace,
// Ctrl+letter combinations, etc.) and emits the corresponding terminal
// byte sequence. Scrollback navigation keys (PgUp/PgDn, Shift+Home/End)
// move the viewport instead of writing to the pty; any other key snaps
// the viewport back to live.
func (t *Term) onKeyDown(ctx gui.EventCtx) {
	t.syncHoverForModifiers(ctx.Event.Modifiers, ctx.Window)
	t.keys.noteOption(ctx.Event.KeyCode, ctx.Event.Modifiers, true)
	// Hints first, ahead of copy mode: the entry chords must work from inside
	// copy mode (a link you scrolled back to find is exactly the one you want
	// to open), and while hints is up it owns the keyboard outright.
	verb, isHintChord := hintOpen, false
	switch {
	case t.binds(ActionHints, ctx.Event):
		isHintChord = true
	case t.binds(ActionHintsCopy, ctx.Event):
		verb, isHintChord = hintCopy, true
	}
	if isHintChord {
		t.toggleHints(verb, ctx.Window)
		ctx.Consume()
		return
	}
	if t.hints.active {
		// Bare printable chords belong to onChar, exactly as in copy mode.
		// Swallowed either way: a leaked label letter would land in the shell's
		// command line.
		if !producesChar(ctx.Event) {
			t.handleHintsKey(ctx.Event, ctx.Window)
		}
		ctx.Consume()
		return
	}
	// Copy mode next: while it is active it owns the keyboard, and its entry
	// chord must be seen even when a search bar is open.
	if t.binds(ActionCopyMode, ctx.Event) {
		if t.copy.active {
			t.exitCopyMode(ctx.Window)
		} else {
			t.enterCopyMode(ctx.Window)
		}
		ctx.Consume()
		return
	}
	// While copy mode has the search bar open, the search handlers run as
	// usual; finishCopySearch hands control back on Enter/Escape.
	if t.copy.active && !t.copy.searching {
		// Bare printable chords belong to onChar — on macOS they never reach
		// here at all, and on backends that deliver both events dispatching in
		// both places would double-apply every motion. Still swallowed, so
		// nothing leaks to the child either way.
		if !producesChar(ctx.Event) {
			t.handleCopyModeKey(ctx.Event, ctx.Window)
		}
		ctx.Consume()
		return
	}
	if t.handleSearchKey(ctx.Event, ctx.Window) {
		return
	}
	if t.handleClipboardKey(ctx.Event, ctx.Window) {
		return
	}
	shift := ctx.Event.Modifiers.Has(gui.ModShift)
	ctrl := ctx.Event.Modifiers.Has(gui.ModCtrl)
	if t.scrollbackIntercept(ctx.Event, ctx.Window, shift) {
		return
	}
	if t.handleDisplayKey(ctx.Event, ctx.Window) {
		return
	}
	// Every handler that keeps a key for itself has returned by now, so this
	// press belongs to the child: either encoded below, or typed by the char
	// event that follows. Recorded so onKeyUp sends only releases the child
	// can pair with a press.
	t.keys.markDown(ctx.Event.KeyCode)
	out := t.encodeKeyEvent(ctx.Event, ctx.Window, shift, ctrl)
	if len(out) == 0 {
		return
	}
	t.snapToLive()
	t.writeBytes(out)
	ctx.Consume()
}

// openSearchBar resets and opens the search bar. Shared by the keyboard path
// and direct action dispatch; both must start from the same clean state.
// Main-thread only.
func (t *Term) openSearchBar(w *gui.Window) {
	t.search.active = true
	t.search.query = ""
	t.search.matches = nil
	t.search.idx = 0
	t.bumpVersion()
	if w != nil {
		w.InvalidateLayout()
	}
}

// handleSearchKey handles the search bar lifecycle: Cmd+F opens it,
// Cmd+Up/Down jumps between prompt marks, and while active, editing and
// navigation keys are intercepted. Returns true when the event was consumed.
// bindingTable returns the effective shortcut table, seeding it with the
// defaults on first use. A Term built as a bare struct literal (as several
// tests do) has no table, and a zero-value Term should behave like a
// default-configured one rather than one with every shortcut disabled.
// Main-thread only, like every other binding access.
func (t *Term) bindingTable() map[Action]binding {
	if t.bindings == nil {
		t.bindings = mergeBindings(nil)
	}
	return t.bindings
}

// binds reports whether e matches any chord bound to action a. Matching is
// exact on the keyboard modifier bits, except that actions flagged
// shiftOptional ignore a stray Shift (see binding). An unbound action never
// matches, so its key falls through to the child process.
//
// This decides only *whether the chord matched*; each handler keeps its own
// conditional-passthrough logic (selection state, alt screen, and so on).
func (t *Term) binds(a Action, e *gui.Event) bool {
	return t.bindingTable()[a].matches(e.KeyCode, e.Modifiers)
}

func (t *Term) handleSearchKey(e *gui.Event, w *gui.Window) bool {
	// Primary+F opens the search bar (Cmd+F on macOS, Ctrl+Shift+F on Windows).
	if t.binds(ActionFind, e) {
		t.openSearchBar(w)
		e.IsHandled = true
		return true
	}

	// Primary+Up/Down: jump between OSC 133 prompt marks (shell integration).
	// prev is computed once — this is the keyboard hot path, and binds() walks
	// a map plus a chord list on every call.
	if prev := t.binds(ActionPrevPrompt, e); prev || t.binds(ActionNextPrompt, e) {
		t.jumpToMark(prev, w)
		e.IsHandled = true
		return true
	}

	// The other two mark-driven actions: jump to the newest failed command,
	// and select a command's output region.
	if t.binds(ActionJumpFailure, e) {
		t.jumpToFailure(w)
		e.IsHandled = true
		return true
	}
	if t.binds(ActionSelectOutput, e) {
		t.selectCommandOutput(w)
		e.IsHandled = true
		return true
	}
	if t.binds(ActionSelectAll, e) {
		t.selectAll(w)
		e.IsHandled = true
		return true
	}

	// While in search mode, intercept navigation and editing keys.
	if t.search.active {
		switch {
		// Prev before next reads naturally but is not load-bearing: neither
		// match action is shiftOptional (see defaultBindings), so matching is
		// exact on the modifier bits and Shift+Enter cannot satisfy the plain
		// Enter chord. What separates the two is the binding table, not the
		// case order.
		//
		// Opened from copy mode, Enter closes the bar and moves the copy
		// cursor to the match instead of only scrolling the viewport; Shift
		// reverses the direction, as it does outside copy mode.
		case t.binds(ActionPrevMatch, e):
			if t.copy.searching {
				t.copy.backward = !t.copy.backward
				t.finishCopySearch(true, w)
			} else {
				t.searchJump(false, w)
			}
		case t.binds(ActionNextMatch, e):
			if t.copy.searching {
				t.finishCopySearch(true, w)
			} else {
				t.searchJump(true, w)
			}
		case t.binds(ActionToggleRegex, e):
			t.search.regex = !t.search.regex
			t.recompileSearchRE()
			t.bumpVersion()
			if w != nil {
				w.InvalidateLayout()
			}
		// Backspace and Escape are text editing, not rebindable shortcuts.
		case e.KeyCode == gui.KeyBackspace:
			if len(t.search.query) > 0 {
				rr := []rune(t.search.query)
				t.search.query = string(rr[:len(rr)-1])
				t.recompileSearchRE()
				t.bumpVersion()
				if w != nil {
					w.InvalidateLayout()
				}
			}
		case e.KeyCode == gui.KeyEscape:
			t.search.active = false
			t.search.query = ""
			t.search.matches = nil
			if t.copy.searching {
				// Escape dismisses the bar but stays in copy mode; a second
				// Escape then leaves the mode.
				t.finishCopySearch(false, w)
				break
			}
			t.bumpVersion()
			if w != nil {
				w.InvalidateLayout()
			}
		}
		e.IsHandled = true
		return true
	}
	return false
}

// handleClipboardKey handles Cmd+C / Ctrl+Shift+C (copy) and Cmd+V /
// Ctrl+Shift+V (paste). Returns true when the event was consumed.
func (t *Term) handleClipboardKey(e *gui.Event, w *gui.Window) bool {
	// Copy: Cmd+C (macOS) or Ctrl+Shift+C. Only suppress when there
	// is a non-empty selection so plain Ctrl+C still SIGINTs the child.
	if t.binds(ActionCopy, e) {
		if t.copySelection(w) {
			e.IsHandled = true
			return true
		}
		if !encodesControlByte(e.Modifiers) {
			// Cmd+C without selection is a no-op; never reaches pty.
			e.IsHandled = true
			return true
		}
		// Ctrl+Shift+C without selection falls through to Ctrl+letter
		// (sends 0x03 = SIGINT) below.
	}

	// Paste: Cmd+V (macOS) or Ctrl+Shift+V. Always suppresses so the
	// 'v' character isn't sent in addition to the paste payload.
	if t.binds(ActionPaste, e) {
		t.pasteFromClipboard(w)
		e.IsHandled = true
		return true
	}
	return false
}

// encodesControlByte reports whether a chord with these modifiers would
// otherwise encode a Ctrl+letter control byte for the child. Copy uses it to
// decide whether an unproductive press (no selection) should be swallowed or
// passed through: Ctrl+Shift+C must still reach the child as SIGINT, while
// Cmd+C has no terminal encoding and is simply a no-op. Super wins when both
// are held, matching the macOS reading of the chord.
func encodesControlByte(m gui.Modifier) bool {
	return m.Has(gui.ModCtrl) && !m.Has(gui.ModSuper)
}

// handleDisplayKey intercepts Primary+= (increase font size), Primary+-
// (decrease font size), and Primary+0 (reset to default) before they reach the
// pty — Cmd+=/Cmd+-/Cmd+0 on macOS, Ctrl+Shift+=/-/0 on Windows. isPrimaryChord
// (not exact) tolerates the Shift used to type '+' on the '=' key. Returns true
// when the event was consumed.
func (t *Term) handleDisplayKey(e *gui.Event, w *gui.Window) bool {
	switch {
	case t.binds(ActionFontInc, e):
		t.AdjustFontSize(0.25)
	case t.binds(ActionFontDec, e):
		t.AdjustFontSize(-0.25)
	case t.binds(ActionFontReset, e):
		t.ResetFontSize()
	default:
		return false
	}
	e.IsHandled = true
	return true
}

// scrollbackIntercept handles the scrollback navigation keys when they should
// move the viewport rather than being encoded for the pty. Returns true when
// the key was consumed. shift is pre-computed by the caller (onKeyDown) so it
// isn't re-read from e.Modifiers.
//
// When the alt screen is active, only Shift+PageUp/PageDown scroll; plain
// PageUp/PageDown pass through so full-screen apps get their own paging. That
// check is on the literal Shift state rather than the binding, because it is
// the "hold Shift to talk to the terminal, not the app" idiom — rebinding
// these two actions to a non-Shift chord therefore won't reach them on the
// alt screen. Scroll-to-top/bottom have no such gate.
func (t *Term) scrollbackIntercept(e *gui.Event, w *gui.Window, shift bool) bool {
	switch {
	case t.binds(ActionScrollPageUp, e):
		if shift || !t.isAltActive() {
			t.scrollByPage(+1, w)
			e.IsHandled = true
			return true
		}
	case t.binds(ActionScrollPageDown, e):
		if shift || !t.isAltActive() {
			t.scrollByPage(-1, w)
			e.IsHandled = true
			return true
		}
	case t.binds(ActionScrollTop, e):
		t.scrollToTop(w)
		e.IsHandled = true
		return true
	case t.binds(ActionScrollBottom, e):
		t.scrollToBottom(w)
		e.IsHandled = true
		return true
	}
	return false
}

// encodeKeyEvent translates a key event into the corresponding terminal
// byte sequence. Returns nil when the key has no terminal encoding, or when
// onChar will type it (plain and Shift text keys, macOS right Option).
// shift and ctrl are pre-computed by the caller (onKeyDown).
//
// Alt has two encodings and each key uses exactly one of them. Cursor, editing
// and function keys carry it in the xterm modifier parameter (Alt+Left is
// CSI 1;3D). Everything else is Meta: an ESC prefix on the legacy bytes. Under
// KKP it is always the modifier parameter, and never also a prefix.
func (t *Term) encodeKeyEvent(e *gui.Event, w *gui.Window, shift, ctrl bool) []byte {
	mods := e.Modifiers
	alt := mods.Has(gui.ModAlt)
	modes := t.keyModes()
	flags := modes.kittyKeyFlags
	mod := modParam(shift, alt, ctrl)

	switch e.KeyCode {
	case gui.KeyPageUp:
		return modTilde("5", mod)
	case gui.KeyPageDown:
		return modTilde("6", mod)
	case gui.KeyDelete:
		return modTilde("3", mod)
	case gui.KeyEnter, gui.KeyKPEnter:
		// Application keypad Enter takes priority; KKP applies to regular Enter.
		if modes.appKeypad && e.KeyCode == gui.KeyKPEnter {
			return []byte("\x1bOM")
		}
		if kkpNamedKey(mods, flags) {
			return kittyKeySeq(13, mods, flags, false)
		}
		return meta(alt, '\r')
	case gui.KeyBackspace:
		if kkpNamedKey(mods, flags) {
			return kittyKeySeq(127, mods, flags, false)
		}
		return meta(alt, 0x7F)
	case gui.KeyTab:
		if kkpNamedKey(mods, flags) {
			return kittyKeySeq(9, mods, flags, false)
		}
		if shift && !ctrl {
			return []byte("\x1b[Z")
		}
		return meta(alt, '\t')
	case gui.KeyEscape:
		if flags != 0 {
			return kittyKeySeq(27, mods, flags, false)
		}
		return meta(alt, 0x1B)
	case gui.KeyUp, gui.KeyDown, gui.KeyRight, gui.KeyLeft:
		_, final, _ := legacyFuncForm(e.KeyCode)
		if mod != 0 {
			return modSS3(final, mod)
		}
		return arrowSeq(final, modes.appCursor)
	case gui.KeyHome, gui.KeyEnd:
		_, final, _ := legacyFuncForm(e.KeyCode)
		// Shift excluded from the modifier: Shift+Home/End scroll the
		// viewport, and Ctrl+Shift+Home emits Ctrl+Home.
		if m := modParam(false, alt, ctrl); m != 0 {
			return modSS3(final, m)
		}
		return arrowSeq(final, modes.appCursor)
	case gui.KeyInsert,
		gui.KeyF1, gui.KeyF2, gui.KeyF3, gui.KeyF4,
		gui.KeyF5, gui.KeyF6, gui.KeyF7, gui.KeyF8,
		gui.KeyF9, gui.KeyF10, gui.KeyF11, gui.KeyF12:
		return funcKeySeq(e.KeyCode, mod, flags != 0)
	}
	if isModifierKey(e.KeyCode) {
		// Modifier keys alone are reported only under KKP flag 8.
		if flags&8 == 0 {
			return nil
		}
		cp, _ := kittyKeyCodepoint(e.KeyCode)
		return kittyKeySeq(cp, mods, flags, false)
	}
	if modes.appKeypad {
		if seq := keypadSeq(e.KeyCode); len(seq) > 0 {
			if alt {
				return append([]byte{0x1b}, seq...)
			}
			return seq
		}
	}
	return t.encodeTextChord(e.KeyCode, mods, flags)
}

// encodeTextChord encodes a Ctrl and/or Alt chord on a key that types text
// (letters, digits, punctuation, Space). Plain and Shift presses return nil:
// the char event that follows types them, with the layout's own character.
// So does macOS right Option, which composes layout characters (@ on German
// Option+L) instead of acting as Meta; see optionComposes.
//
// Under KKP the chord is CSI base;mod u with the unshifted base. In legacy
// mode Ctrl picks a control byte (ctrlByte) and Alt adds the Meta ESC prefix
// to it, or to the character itself, shifted when Shift is held so that
// Alt+Shift+f (M-F) differs from Alt+f (M-f).
func (t *Term) encodeTextChord(k gui.KeyCode, mods gui.Modifier, flags uint32) []byte {
	base, ok := textKeyBase(k)
	if !ok {
		return nil
	}
	ctrl, alt := mods.Has(gui.ModCtrl), mods.Has(gui.ModAlt)
	if !ctrl && !alt {
		return nil
	}
	if alt && t.keys.optionComposes(mods) {
		return nil
	}
	if flags != 0 {
		return kittyKeySeq(int(base), mods, flags, false)
	}
	if ctrl {
		b, ok := ctrlByte(base)
		if !ok {
			return nil
		}
		return meta(alt, b)
	}
	if mods.Has(gui.ModShift) {
		base = usShifted(base)
	}
	return meta(true, base)
}

// kkpNamedKey reports whether Enter, Tab or Backspace take their KKP CSI u
// form. Unmodified, they keep their legacy bytes unless flag 8 (report all
// keys as escape codes) is set, so the user can still type `reset` at a shell
// after an app that pushed KKP flags crashed without popping them (KKP spec,
// "disambiguate escape codes").
func kkpNamedKey(mods gui.Modifier, flags uint32) bool {
	if flags == 0 {
		return false
	}
	return flags&8 != 0 || kittyModParam(mods) != 1
}

// meta returns b, with the Meta ESC prefix when alt is held.
func meta(alt bool, b byte) []byte {
	if alt {
		return []byte{0x1b, b}
	}
	return []byte{b}
}

// onKeyUp generates KKP key-release sequences (event type 3) when flag 2 is
// set, but only for a key whose press went to the child (see keyState.down):
// the search bar, copy mode, hints and shortcuts keep their presses, and a
// release the child never saw pressed is noise it cannot pair.
func (t *Term) onKeyUp(ctx gui.EventCtx) {
	t.syncHoverForModifiers(ctx.Event.Modifiers, ctx.Window)
	t.keys.noteOption(ctx.Event.KeyCode, ctx.Event.Modifiers, false)
	if !t.keys.takeDown(ctx.Event.KeyCode) {
		return
	}
	modes := t.keyModes()
	if modes.kittyKeyFlags&2 == 0 {
		return
	}
	if seq := kittyReleaseSeq(ctx.Event.KeyCode, ctx.Event.Modifiers, modes.kittyKeyFlags); seq != nil {
		t.writeBytes(seq)
		ctx.Consume()
	}
}
