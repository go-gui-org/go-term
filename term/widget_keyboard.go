package term

import (
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/mike-ward/go-gui/gui"
)

// Bracketed-paste markers (DEC ?2004). Sent around clipboard payloads
// when the application has enabled the mode; stripped from incoming
// payloads unconditionally so a clipboard exit-marker can't break out.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
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
func (t *Term) onChar(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
	if e.CharCode == 0 {
		return
	}
	if t.search.active {
		if utf8.RuneCountInString(t.search.query) < MaxGridDim {
			t.search.query += string(rune(e.CharCode))
			t.recompileSearchRE()
		}
		e.IsHandled = true
		t.bumpVersion()
		t.cmd.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
		return
	}
	t.snapToLive()
	r := rune(e.CharCode)

	// KKP flag 8: report all printable keys as CSI u escape codes.
	// The codepoint is the base (unshifted) form; Shift is in the modifier.
	kkpFlags := t.keyModes().kittyKeyFlags
	if kkpFlags&8 != 0 {
		cp := int(r)
		if r >= 'A' && r <= 'Z' && e.Modifiers.Has(gui.ModShift) {
			cp = int(r-'A') + 'a'
		}
		if seq := kittyKeySeq(cp, e.Modifiers, kkpFlags, false); seq != nil {
			t.writeBytes(seq)
			e.IsHandled = true
			return
		}
	}

	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	if n > 0 {
		t.writeBytes(buf[:n])
	}
	e.IsHandled = true
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
func (t *Term) onKeyDown(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	shift := e.Modifiers.Has(gui.ModShift)
	cmd := e.Modifiers.Has(gui.ModSuper)
	ctrl := e.Modifiers.Has(gui.ModCtrl)
	alt := e.Modifiers.Has(gui.ModAlt)
	modes := t.keyModes()

	// Search: Cmd+F opens the search bar.
	if e.KeyCode == gui.KeyF && cmd {
		t.search.active = true
		t.search.query = ""
		t.search.matches = nil
		t.search.idx = 0
		e.IsHandled = true
		t.bumpVersion()
		w.UpdateWindow()
		return
	}

	// Cmd+Up/Down: jump between OSC 133 prompt marks (shell integration).
	if cmd && !ctrl && !alt && (e.KeyCode == gui.KeyUp || e.KeyCode == gui.KeyDown) {
		t.jumpToMark(e.KeyCode == gui.KeyUp, w)
		e.IsHandled = true
		return
	}

	// While in search mode, intercept navigation and editing keys.
	if t.search.active {
		switch e.KeyCode {
		case gui.KeyEnter, gui.KeyKPEnter:
			t.searchJump(!shift, w)
		case gui.KeyBackspace:
			if len(t.search.query) > 0 {
				rr := []rune(t.search.query)
				t.search.query = string(rr[:len(rr)-1])
				t.recompileSearchRE()
				t.bumpVersion()
				w.UpdateWindow()
			}
		case gui.KeyEscape:
			t.search.active = false
			t.search.query = ""
			t.search.matches = nil
			t.bumpVersion()
			w.UpdateWindow()
		case gui.KeyR:
			if ctrl {
				t.search.regex = !t.search.regex
				t.recompileSearchRE()
				t.bumpVersion()
				w.UpdateWindow()
			}
		}
		e.IsHandled = true
		return
	}

	// Copy: Cmd+C (macOS) or Ctrl+Shift+C. Only suppress when there
	// is a non-empty selection so plain Ctrl+C still SIGINTs the child.
	if e.KeyCode == gui.KeyC && (cmd || (ctrl && shift)) {
		if t.copySelection(w) {
			e.IsHandled = true
			return
		}
		if cmd {
			// Cmd+C without selection is a no-op; never reaches pty.
			e.IsHandled = true
			return
		}
		// Ctrl+Shift+C without selection falls through to Ctrl+letter
		// (sends 0x03 = SIGINT) below.
	}

	// Paste: Cmd+V (macOS) or Ctrl+Shift+V. Always suppresses so the
	// 'v' character isn't sent in addition to the paste payload.
	if e.KeyCode == gui.KeyV && (cmd || (ctrl && shift)) {
		t.pasteFromClipboard(w)
		e.IsHandled = true
		return
	}

	switch e.KeyCode {
	case gui.KeyPageUp:
		inAlt := func() bool {
			t.grid.Mu.Lock()
			defer t.grid.Mu.Unlock()
			return t.grid.AltActive
		}()
		if shift || !inAlt {
			t.scrollByPage(+1, w)
			e.IsHandled = true
			return
		}
	case gui.KeyPageDown:
		inAlt := func() bool {
			t.grid.Mu.Lock()
			defer t.grid.Mu.Unlock()
			return t.grid.AltActive
		}()
		if shift || !inAlt {
			t.scrollByPage(-1, w)
			e.IsHandled = true
			return
		}
	case gui.KeyHome:
		if shift && !ctrl {
			t.scrollToTop(w)
			e.IsHandled = true
			return
		}
	case gui.KeyEnd:
		if shift && !ctrl {
			t.scrollToBottom(w)
			e.IsHandled = true
			return
		}
	}

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
		} else if modes.appCursor {
			out = []byte("\x1bOA")
		} else {
			out = []byte("\x1b[A")
		}
	case gui.KeyDown:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('B', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOB")
		} else {
			out = []byte("\x1b[B")
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
	if len(out) == 0 {
		return
	}
	t.snapToLive()
	t.writeBytes(out)
	e.IsHandled = true
}

// onKeyUp generates KKP key-release sequences (event-type 3) when flag bit 2 is set.
func (t *Term) onKeyUp(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
	modes := t.keyModes()
	if modes.kittyKeyFlags&2 == 0 {
		return
	}
	cp, ok := kittyKeyCodepoint(e.KeyCode)
	if !ok {
		return
	}
	if seq := kittyKeySeq(cp, e.Modifiers, modes.kittyKeyFlags, true); seq != nil {
		t.writeBytes(seq)
		e.IsHandled = true
	}
}
