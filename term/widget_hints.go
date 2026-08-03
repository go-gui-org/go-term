package term

import (
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// hintAlphabet is the label alphabet, home row first so the common case is one
// keypress without moving the hands. Every rune must be a bare printable that
// reaches onChar; anything needing a modifier would never be typed as a label.
const hintAlphabet = "asdfghjklqwertyuiopzxcvbnm"

// hintVerb is what committing a hint does.
type hintVerb uint8

const (
	hintOpen hintVerb = iota // hand the URL to the OS opener
	hintCopy                 // put the URL on the clipboard
)

// hintState is hints mode's state. The zero value is inactive.
//
// labels is parallel to targets and is built once on entry: the draw path runs
// every frame the mode is up and must not allocate, so the strings a label pill
// renders are prepared here instead.
type hintState struct {
	active  bool
	verb    hintVerb
	targets []hintTarget
	labels  []string
	// typed is a string rather than a []rune so the draw path can prefix-match
	// against it directly; it runs every frame the mode is up, and a per-frame
	// string(runes) conversion there is an allocation for nothing. Labels are
	// ASCII, so byte length and rune length agree.
	typed string
}

// hintLabels fills dst with one label per target: single characters while the
// alphabet has room, two characters for everything beyond it.
//
// The width is uniform across a pass rather than per-label. A mix would mean a
// typed prefix could both complete one label and still be a prefix of another,
// which forces a disambiguation timeout — the one interaction a keyboard-driven
// picker cannot afford.
func hintLabels(n int, dst []string) []string {
	dst = dst[:0]
	a := hintAlphabet
	// Two characters address len(a)² targets. maxHintTargets is well under that,
	// but clamping here keeps the indexing below correct on its own terms rather
	// than by appeal to a constant in another file.
	if n > len(a)*len(a) {
		n = len(a) * len(a)
	}
	if n <= len(a) {
		for i := 0; i < n; i++ {
			dst = append(dst, a[i:i+1])
		}
		return dst
	}
	for i := 0; i < n; i++ {
		dst = append(dst, string([]byte{a[i/len(a)], a[i%len(a)]}))
	}
	return dst
}

// enterHints labels every link in the viewport and takes over the keyboard.
// No-op when nothing is linkable — a mode with no targets can only be escaped
// from, so entering it would be a trap. Main-thread only.
func (t *Term) enterHints(w *gui.Window, verb hintVerb) {
	targets := func() []hintTarget {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		return t.grid.hintTargets(t.hints.targets)
	}()
	if len(targets) == 0 {
		return
	}
	labels := hintLabels(len(targets), t.hints.labels)
	// labels is the shorter of the two if the alphabet ran out. Every other
	// path indexes labels by target, so trim rather than leave the two lengths
	// free to disagree.
	targets = targets[:len(labels)]
	t.hints = hintState{
		active:  true,
		verb:    verb,
		targets: targets,
		labels:  labels,
	}
	t.scheduleViewUpdate(w)
}

// exitHints leaves hints mode, keeping the slices for the next pass. Safe to
// call when inactive. Main-thread only.
func (t *Term) exitHints(w *gui.Window) {
	if !t.hints.active {
		return
	}
	t.hints.active = false
	t.hints.targets = t.hints.targets[:0]
	t.hints.typed = ""
	t.scheduleViewUpdate(w)
}

// handleHintsChar feeds one typed character to the label matcher. Every rune is
// consumed by the mode whether or not it matches; see the swallow rule in
// onChar. Main-thread only.
func (t *Term) handleHintsChar(r rune, w *gui.Window) {
	// Labels are lowercase ASCII; fold so a stuck Shift or Caps Lock still
	// picks a link rather than silently dropping the mode.
	t.hints.typed += string(toLowerASCII(r))
	prefix := t.hints.typed

	idx := -1
	n := 0
	for i, label := range t.hints.labels {
		if strings.HasPrefix(label, prefix) {
			idx = i
			n++
		}
	}
	switch {
	case n == 0:
		// Fail closed. A mode that ignores unmatched keys looks identical to a
		// hung terminal, and the keys are being swallowed either way.
		t.exitHints(w)
	case n == 1 && len(prefix) == len(t.hints.labels[idx]):
		t.commitHint(idx, w)
	default:
		// Still ambiguous: redraw so the overlay can show the narrowing.
		t.scheduleViewUpdate(w)
	}
}

// handleHintsKey handles the non-character keys hints mode understands:
// Escape leaves, Backspace un-types. Everything else is swallowed by the
// caller. Main-thread only.
func (t *Term) handleHintsKey(e *gui.Event, w *gui.Window) {
	switch e.KeyCode {
	case gui.KeyEscape:
		t.exitHints(w)
	case gui.KeyBackspace:
		// One byte is one typed character here: a rune that matched no label
		// exits the mode on the keystroke that produced it, so nothing
		// multi-byte can still be in typed by the time Backspace arrives.
		if n := len(t.hints.typed); n > 0 {
			t.hints.typed = t.hints.typed[:n-1]
			t.scheduleViewUpdate(w)
		}
	}
}

// commitHint acts on the chosen target and leaves the mode. Main-thread only.
func (t *Term) commitHint(idx int, w *gui.Window) {
	// Read both before exiting — exitHints owns the state from here on.
	url, verb := t.hints.targets[idx].url, t.hints.verb
	t.exitHints(w)
	switch verb {
	case hintCopy:
		if w != nil {
			w.SetClipboard(url)
			// Mirror copySelection: PRIMARY is independent of CLIPBOARD, so
			// writing both costs the user nothing and makes middle-click paste
			// work on X11.
			w.SetPrimary(url)
		}
	default:
		// openURL enforces the http/https/mailto allowlist, so a hostile OSC 8
		// destination is no more dangerous here than under Cmd+click.
		openURL(url)
	}
}

// toLowerASCII lowercases A–Z and leaves everything else alone. The label
// alphabet is ASCII, so full Unicode case folding would be dead weight.
func toLowerASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
