package term

import (
	"log"
	"strings"
	"unicode/utf8"

	"github.com/go-gui-org/go-gui/gui"
)

// Bracketed-paste markers (DEC ?2004). Sent around clipboard payloads
// when the application has enabled the mode; both markers are stripped
// from incoming payloads (see stripPasteMarkers) so clipboard
// content cannot forge frame boundaries.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// maxPasteBytes caps clipboard payloads written to the pty. Multi-MB
// pastes can wedge the shell and stall the reader goroutine; truncate
// silently — nothing useful types thousands of lines at once.
const maxPasteBytes = 1 << 20

// truncatePaste caps s at max bytes, backing up to the start of any
// trailing partial UTF-8 sequence so the pty never receives a split
// rune. Returns s unchanged when already within budget.
func truncatePaste(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// stripPasteMarkers removes every paste-entry and paste-exit marker from s.
// Without it a clipboard carrying pasteEnd could exit bracketed-paste mode
// early and feed the rest as commands, and one carrying pasteStart would
// nest inside the wrapper pasteText adds, so a naive child parser splits the
// paste in two. C0 controls (CR, ^C, ...) are passed through, matching xterm
// — without bracketed paste enabled by the application the shell cannot
// distinguish pasted bytes from typed bytes anyway. Literal marker text in
// the clipboard is mangled, which beats handing the child a forged frame
// boundary.
//
// One strings.ReplaceAll pass is not enough: removing a marker can splice
// its neighbors into a new one ("ESC[201" + pasteEnd + "~" leaves a live
// pasteEnd). The output is built as a stack instead — each time its tail
// completes a marker, the marker is popped — so markers formed by earlier
// removals go too, in one linear pass.
func stripPasteMarkers(s string) string {
	// Common case: no marker, no copy. A removal can only happen once a
	// marker exists, so this check is exact.
	if !strings.Contains(s, pasteEnd) && !strings.Contains(s, pasteStart) {
		return s
	}
	const n = len(pasteEnd) // both markers are the same length
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, s[i])
		// Both markers end in '~', so only then can the tail complete one.
		if s[i] == '~' && len(out) >= n {
			if tail := string(out[len(out)-n:]); tail == pasteEnd || tail == pasteStart {
				out = out[:len(out)-n]
			}
		}
	}
	return string(out)
}

// cleanPaste caps s at maxPasteBytes and removes any embedded paste markers
// (entry and exit). Both paste entry points — SendInput and the clipboard
// path — share it so they can never disagree on what a sanitized payload is.
func cleanPaste(s string) string {
	return stripPasteMarkers(truncatePaste(s, maxPasteBytes))
}

// pasteText snaps to live, brackets clean according to this pane's own DEC
// ?2004 state, and writes it to the pty. clean must already be truncated and
// stripped — see cleanPaste.
func (t *Term) pasteText(clean string) {
	// Guarded here rather than at the callers so a payload that sanitizes
	// away to nothing — empty clipboard, or a string of nothing but paste-end
	// markers — can never reach the child as a bare ESC[200~ ESC[201~ pair.
	if clean == "" {
		return
	}
	t.snapToLive()
	// Read BracketedPaste under the lock, then release before calling
	// pw.Write — holding Mu across a blocking pty write can deadlock
	// when the slave-side input buffer is full and the reader goroutine
	// is waiting for the same lock to drain output.
	t.grid.Mu.Lock()
	bracketed := t.grid.BracketedPaste
	t.grid.Mu.Unlock()
	payload := clean
	if bracketed {
		payload = pasteStart + clean + pasteEnd
	}
	// One conversion shared by the recorder and the pty — payload can be a
	// megabyte, so the second copy is worth avoiding.
	out := []byte(payload)
	t.rec.Load().Input(out)
	if _, err := t.pw.Write(out); err != nil {
		log.Printf("term: pty paste: %v", err)
	}
}

// pasteFromClipboard reads the clipboard, strips paste-end markers, and
// writes the payload to the pty — wrapped in bracketed-paste markers
// when the application has enabled DEC ?2004.
func (t *Term) pasteFromClipboard(w *gui.Window) {
	clean := cleanPaste(w.GetClipboard())
	if clean == "" {
		return
	}
	t.pasteText(clean)
	// The tap gets the *unwrapped* text: a receiving pane may have a
	// different ?2004 state, so it must apply its own markers via Paste.
	if t.cfg.OnInput != nil {
		t.cfg.OnInput([]byte(clean), InputPaste)
	}
}

// copySelection writes the current selection to the system clipboard
// and returns true if anything was copied. A nil window (no clipboard to write
// to, as in tests) reports false rather than panicking — callers treat that
// the same as "nothing selected".
func (t *Term) copySelection(w *gui.Window) bool {
	if w == nil {
		return false
	}
	var text string
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		text = t.grid.SelectedText()
	}()
	if text == "" {
		return false
	}
	w.SetClipboard(text)
	// PRIMARY is the X11 select-to-copy buffer that middle-click pastes. It is
	// independent of CLIPBOARD, so writing it here costs the user nothing —
	// their explicit Cmd+C value stays put — and a no-op everywhere without
	// PRIMARY (macOS, Windows, web).
	w.SetPrimary(text)
	return true
}

// pasteFromPrimary pastes the X11 PRIMARY selection — the middle-click
// gesture. Falls back to the clipboard when PRIMARY is empty or unsupported,
// which is what makes the gesture useful on platforms that have no PRIMARY at
// all. Returns false when there was nothing to paste.
func (t *Term) pasteFromPrimary(w *gui.Window) bool {
	if w == nil {
		return false
	}
	clean := cleanPaste(w.GetPrimary())
	if clean == "" {
		clean = cleanPaste(w.GetClipboard())
	}
	if clean == "" {
		return false
	}
	t.pasteText(clean)
	// Same contract as pasteFromClipboard: the tap gets unwrapped text so a
	// receiving pane applies its own ?2004 state.
	if t.cfg.OnInput != nil {
		t.cfg.OnInput([]byte(clean), InputPaste)
	}
	return true
}
