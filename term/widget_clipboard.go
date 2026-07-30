package term

import (
	"log"
	"strings"
	"unicode/utf8"

	"github.com/go-gui-org/go-gui/gui"
)

// Bracketed-paste markers (DEC ?2004). Sent around clipboard payloads
// when the application has enabled the mode; stripped from incoming
// payloads unconditionally so a clipboard exit-marker can't break out.
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

// stripPasteEnd removes any embedded paste-end markers from s. Without
// stripping, a clipboard payload containing pasteEnd could exit
// bracketed-paste mode early and feed the rest as commands. C0 controls
// (CR, ^C, ...) are passed through, matching xterm — without bracketed
// paste enabled by the application the shell cannot distinguish pasted
// bytes from typed bytes anyway. ReplaceAll returns the original string
// when the marker is absent (common case), so no extra fast path needed.
func stripPasteEnd(s string) string {
	return strings.ReplaceAll(s, pasteEnd, "")
}

// cleanPaste caps s at maxPasteBytes and removes any embedded paste-end
// marker. Both paste entry points — SendInput and the clipboard path — share
// it so they can never disagree on what a sanitized payload is.
func cleanPaste(s string) string {
	return stripPasteEnd(truncatePaste(s, maxPasteBytes))
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
	return true
}
