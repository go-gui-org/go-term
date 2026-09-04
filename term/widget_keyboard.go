package term

import (
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/go-gui-org/go-gui/gui"
)

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
// query is empty.
func (t *Term) recompileSearchRE() {
	if t.search.regex && t.search.query != "" {
		t.search.re, t.search.reErr = regexp.Compile(t.search.query)
	} else {
		t.search.re = nil
		t.search.reErr = nil
	}
}

// onChar receives printable character input from the OS.
func (t *Term) onChar(ctx gui.EventCtx) {
	if ctx.Event.CharCode == 0 {
		return
	}
	// A chord holding Cmd/Ctrl/Alt produces no text: AppKit suppresses
	// insertText: for those, and onKeyDown owns them (shortcut handlers,
	// control bytes, KKP sequences). The X11 backend synthesizes a char
	// event for every printable keypress regardless of modifiers, so
	// without this gate a Super+Shift+V paste would also type 'V' and
	// Ctrl+C would send its control byte *and* the letter. Drop the
	// duplicate char; keep Shift, which is just the same letter's
	// uppercase form.
	if ctx.Event.Modifiers&(gui.ModCtrl|gui.ModAlt|gui.ModSuper) != 0 {
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
	if t.search.active {
		if utf8.RuneCountInString(t.search.query) < MaxGridDim {
			t.search.query += text
			t.recompileSearchRE()
		}
		ctx.Consume()
		t.bumpVersion()
		t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
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
		if r >= 'A' && r <= 'Z' && ctx.Event.Modifiers.Has(gui.ModShift) {
			cp = int(r-'A') + 'a'
		}
		if seq := kittyPrintableSeq(cp, r, ctx.Event.Modifiers, kkpFlags); seq != nil {
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

// kittyKeyCodepoint returns the KKP codepoint for k, or (0, false) when k has none.
// Modifier keys map to private-use-area codepoints; ASCII keys A–Z return 'a'–'z',
// 0–9 return '0'–'9'. KKP spec §7 table.
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
	case gui.KeyInsert:
		return 57348, true
	case gui.KeyDelete:
		return 57349, true
	case gui.KeyLeft:
		return 57350, true
	case gui.KeyRight:
		return 57351, true
	case gui.KeyUp:
		return 57352, true
	case gui.KeyDown:
		return 57353, true
	case gui.KeyPageUp:
		return 57354, true
	case gui.KeyPageDown:
		return 57355, true
	case gui.KeyHome:
		return 57356, true
	case gui.KeyEnd:
		return 57357, true
	case gui.KeyF1:
		return 57364, true
	case gui.KeyF2:
		return 57365, true
	case gui.KeyF3:
		return 57366, true
	case gui.KeyF4:
		return 57367, true
	case gui.KeyF5:
		return 57368, true
	case gui.KeyF6:
		return 57369, true
	case gui.KeyF7:
		return 57370, true
	case gui.KeyF8:
		return 57371, true
	case gui.KeyF9:
		return 57372, true
	case gui.KeyF10:
		return 57373, true
	case gui.KeyF11:
		return 57374, true
	case gui.KeyF12:
		return 57375, true
	default:
		if k >= gui.KeyA && k <= gui.KeyZ {
			return int('a') + int(k-gui.KeyA), true
		}
		if k >= gui.Key0 && k <= gui.Key9 {
			return int('0') + int(k-gui.Key0), true
		}
		return 0, false
	}
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

// funcKeySeq returns the xterm sequence for Insert and F1–F12, with optional
// modifier encoding. Alt is excluded: callers prepend ESC separately.
func funcKeySeq(k gui.KeyCode, shift, ctrl bool) []byte {
	mod := modParam(shift, false, ctrl)
	switch k {
	case gui.KeyInsert:
		return modTilde("2", mod)
	case gui.KeyF1:
		return modSS3('P', mod)
	case gui.KeyF2:
		return modSS3('Q', mod)
	case gui.KeyF3:
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
		w.UpdateWindow()
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
			w.UpdateWindow()
		// Backspace and Escape are text editing, not rebindable shortcuts.
		case e.KeyCode == gui.KeyBackspace:
			if len(t.search.query) > 0 {
				rr := []rune(t.search.query)
				t.search.query = string(rr[:len(rr)-1])
				t.recompileSearchRE()
				t.bumpVersion()
				w.UpdateWindow()
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
			w.UpdateWindow()
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
// byte sequence. Returns nil when the key has no terminal encoding.
// shift and ctrl are pre-computed by the caller (onKeyDown).
func (t *Term) encodeKeyEvent(e *gui.Event, w *gui.Window, shift, ctrl bool) []byte {
	alt := e.Modifiers.Has(gui.ModAlt)
	modes := t.keyModes()

	var out []byte
	switch e.KeyCode {
	case gui.KeyPageUp:
		out = []byte("\x1b[5~")
	case gui.KeyPageDown:
		out = []byte("\x1b[6~")
	case gui.KeyEnter, gui.KeyKPEnter:
		// Application keypad Enter takes priority; KKP applies to regular Enter.
		if modes.appKeypad && e.KeyCode == gui.KeyKPEnter {
			out = []byte("\x1bOM")
		} else if kkp := kittyKeySeq(13, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{'\r'}
		}
	case gui.KeyBackspace:
		if kkp := kittyKeySeq(127, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{0x7F}
		}
	case gui.KeyTab:
		if kkp := kittyKeySeq(9, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else if shift && !ctrl {
			out = []byte("\x1b[Z")
		} else {
			out = []byte{'\t'}
		}
	case gui.KeyEscape:
		if kkp := kittyKeySeq(27, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{0x1B}
		}
	case gui.KeyUp:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('A', mod)
		} else {
			out = arrowSeq('A', modes.appCursor)
		}
	case gui.KeyDown:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('B', mod)
		} else {
			out = arrowSeq('B', modes.appCursor)
		}
	case gui.KeyRight:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('C', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOC")
		} else {
			out = []byte("\x1b[C")
		}
	case gui.KeyLeft:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('D', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOD")
		} else {
			out = []byte("\x1b[D")
		}
	case gui.KeyHome:
		if mod := modParam(false, false, ctrl); mod != 0 {
			// Shift excluded from modifier: Shift+Home scrolls, Ctrl+Shift+Home emits Ctrl+Home.
			out = modSS3('H', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOH")
		} else {
			out = []byte("\x1b[H")
		}
	case gui.KeyEnd:
		if mod := modParam(false, false, ctrl); mod != 0 {
			// Shift excluded from modifier: Shift+End scrolls, Ctrl+Shift+End emits Ctrl+End.
			out = modSS3('F', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOF")
		} else {
			out = []byte("\x1b[F")
		}
	case gui.KeyDelete:
		out = []byte("\x1b[3~")
	case gui.KeyInsert,
		gui.KeyF1, gui.KeyF2, gui.KeyF3, gui.KeyF4,
		gui.KeyF5, gui.KeyF6, gui.KeyF7, gui.KeyF8,
		gui.KeyF9, gui.KeyF10, gui.KeyF11, gui.KeyF12:
		out = funcKeySeq(e.KeyCode, shift, ctrl)
	default:
		if modes.appKeypad {
			out = keypadSeq(e.KeyCode)
			if len(out) > 0 {
				break
			}
		}
		// Alt+letter → lowercase letter; ESC prefix applied below.
		// Handled here so onChar sees IsHandled=true and does not also
		// send the OS-translated glyph (e.g. macOS Alt+F → ƒ).
		if alt && !ctrl && e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			out = []byte{byte('a' + (e.KeyCode - gui.KeyA))}
			break
		}
		// Ctrl+letter → control byte, or KKP CSI u when active.
		if e.Modifiers.Has(gui.ModCtrl) &&
			e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			if kkp := kittyKeySeq(int('a')+int(e.KeyCode-gui.KeyA),
				e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
				out = kkp
			} else {
				out = []byte{byte(e.KeyCode-gui.KeyA) + 1}
			}
		}
	}
	// Alt/Meta key: prefix any outbound sequence with ESC.
	if alt && len(out) > 0 {
		out = append([]byte{0x1b}, out...)
	}
	return out
}

// onKeyUp generates KKP key-release sequences (event-type 3) when flag bit 2 is set.
func (t *Term) onKeyUp(ctx gui.EventCtx) {
	t.syncHoverForModifiers(ctx.Event.Modifiers, ctx.Window)
	modes := t.keyModes()
	if modes.kittyKeyFlags&2 == 0 {
		return
	}
	cp, ok := kittyKeyCodepoint(ctx.Event.KeyCode)
	if !ok {
		return
	}
	if seq := kittyKeySeq(cp, ctx.Event.Modifiers, modes.kittyKeyFlags, true); seq != nil {
		t.writeBytes(seq)
		ctx.Consume()
	}
}
