package term

import (
	"log"
	"strings"
	"unicode/utf8"

	"github.com/mike-ward/go-gui/gui"
)

// maxPasteBytes caps clipboard payloads written to the PTY. Multi-MB
// pastes can wedge the shell and stall the reader goroutine; truncate
// silently — nothing useful types thousands of lines at once.
const maxPasteBytes = 1 << 20

// truncatePaste caps s at max bytes, backing up to the start of any
// trailing partial UTF-8 sequence so the PTY never receives a split
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

// pasteFromClipboard reads the clipboard, strips paste-end markers, and
// writes the payload to the PTY — wrapped in bracketed-paste markers
// when the application has enabled DEC ?2004.
func (t *Term) pasteFromClipboard(w *gui.Window) {
	text := w.GetClipboard()
	if text == "" {
		return
	}
	text = truncatePaste(text, maxPasteBytes)
	t.snapToLive()
	clean := stripPasteEnd(text)
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	bracketed := t.grid.BracketedPaste
	payload := clean
	if bracketed {
		payload = pasteStart + clean + pasteEnd
	}
	if err := t.writeHost([]byte(payload)); err != nil {
		log.Printf("term: pty paste: %v", err)
	}
}

// copySelection writes the current selection to the system clipboard
// and returns true if anything was copied.
func (t *Term) copySelection(w *gui.Window) bool {
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
