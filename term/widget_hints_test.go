package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// hintsMods is the modifier both entry chords carry; the key picks the verb
// (U opens, Y copies).
var hintsMods = remapMod(gui.ModSuper | gui.ModShift)

// hintTerm builds a Term holding the given rows plus a window that captures
// clipboard writes, so the copy verb can be asserted without opening a browser.
func hintTerm(cols int, rows ...string) (*Term, *gui.Window, *string, *[]byte) {
	tm, buf := copyTerm(cols, rows...)
	var clip string
	w := &gui.Window{}
	w.SetClipboardFn(func(s string) { clip = s })
	return tm, w, &clip, buf
}

func TestHints_EnterAssignsLabels(t *testing.T) {
	tm, w, _, _ := hintTerm(60, "a https://one.example b https://two.example")
	key(tm, w, gui.KeyU, hintsMods)

	if !tm.hints.active {
		t.Fatal("hints mode did not activate")
	}
	if len(tm.hints.targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(tm.hints.targets))
	}
	// Two targets fit the alphabet, so labels are one character each and come
	// from the front of the home row.
	if got := tm.hints.labels; got[0] != "a" || got[1] != "s" {
		t.Errorf("labels = %v, want [a s]", got)
	}
}

func TestHints_NoTargetsDoesNotEnter(t *testing.T) {
	// A mode with nothing to pick can only be escaped from, so it must not open.
	tm, w, _, _ := hintTerm(40, "nothing linkable here")
	key(tm, w, gui.KeyU, hintsMods)
	if tm.hints.active {
		t.Error("hints mode activated with no targets")
	}
}

func TestHints_CopyVerbWritesClipboard(t *testing.T) {
	tm, w, clip, _ := hintTerm(40, "see https://copy.example ok")
	key(tm, w, gui.KeyY, hintsMods)
	if !tm.hints.active {
		t.Fatal("hints mode did not activate")
	}
	press(tm, w, 'a') // the only target's label

	if *clip != "https://copy.example" {
		t.Errorf("clipboard = %q, want the URL", *clip)
	}
	if tm.hints.active {
		t.Error("hints mode still active after commit")
	}
}

func TestHints_UppercaseLabelStillMatches(t *testing.T) {
	// Caps Lock or a held Shift must not silently drop the mode.
	tm, w, clip, _ := hintTerm(40, "see https://caps.example ok")
	key(tm, w, gui.KeyY, hintsMods)
	press(tm, w, 'A')
	if *clip != "https://caps.example" {
		t.Errorf("clipboard = %q, want the URL", *clip)
	}
}

func TestHints_UnmatchedCharExitsAndDoesNotReachPTY(t *testing.T) {
	// The regression that matters: a label letter that matches nothing must be
	// swallowed, not forwarded to the shell's command line.
	tm, w, _, buf := hintTerm(40, "see https://one.example ok")
	key(tm, w, gui.KeyU, hintsMods)
	*buf = (*buf)[:0]

	press(tm, w, 'z') // not a label — only "a" exists

	if tm.hints.active {
		t.Error("hints mode still active after an unmatched key")
	}
	if len(*buf) != 0 {
		t.Errorf("pty received %q, want nothing written", *buf)
	}
}

func TestHints_TypedCharsNeverReachPTY(t *testing.T) {
	// Same rule for a character that *is* a live prefix: it drives the label
	// matcher and stops there.
	rows := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, "https://example.com/"+string(rune('a'+i%26)))
	}
	tm, w, _, buf := hintTerm(40, rows...)
	key(tm, w, gui.KeyU, hintsMods)
	if len(tm.hints.targets) <= len(hintAlphabet) {
		t.Fatalf("got %d targets, want more than the alphabet for 2-char labels", len(tm.hints.targets))
	}
	*buf = (*buf)[:0]

	press(tm, w, 'a') // first half of a two-character label

	if !tm.hints.active {
		t.Error("hints mode exited on a valid prefix")
	}
	if len(*buf) != 0 {
		t.Errorf("pty received %q, want nothing written", *buf)
	}
}

func TestHints_EscapeExits(t *testing.T) {
	tm, w, _, buf := hintTerm(40, "see https://one.example ok")
	key(tm, w, gui.KeyU, hintsMods)
	*buf = (*buf)[:0]

	key(tm, w, gui.KeyEscape, 0)

	if tm.hints.active {
		t.Error("hints mode still active after Escape")
	}
	if len(*buf) != 0 {
		t.Errorf("pty received %q, want Escape swallowed", *buf)
	}
}

func TestHints_BackspaceUntypes(t *testing.T) {
	rows := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, "https://example.com/"+string(rune('a'+i%26)))
	}
	tm, w, _, _ := hintTerm(40, rows...)
	key(tm, w, gui.KeyU, hintsMods)

	press(tm, w, 'a')
	if len(tm.hints.typed) != 1 {
		t.Fatalf("typed = %v, want one rune", tm.hints.typed)
	}
	key(tm, w, gui.KeyBackspace, 0)
	if len(tm.hints.typed) != 0 {
		t.Errorf("typed = %v, want empty after Backspace", tm.hints.typed)
	}
	if !tm.hints.active {
		t.Error("Backspace exited hints mode")
	}
}

func TestHints_EntryChordTogglesOut(t *testing.T) {
	tm, w, _, _ := hintTerm(40, "see https://one.example ok")
	key(tm, w, gui.KeyU, hintsMods)
	if !tm.hints.active {
		t.Fatal("hints mode did not activate")
	}
	key(tm, w, gui.KeyU, hintsMods)
	if tm.hints.active {
		t.Error("entry chord did not toggle the mode back off")
	}
}

// The *other* entry chord switches the verb in place rather than dropping the
// labels — the user is already reading them, and relabelling the same links
// would be work for nothing.
func TestHints_OtherChordSwitchesVerb(t *testing.T) {
	tm, w, clip, _ := hintTerm(40, "see https://switch.example ok")
	key(tm, w, gui.KeyU, hintsMods)
	if !tm.hints.active || tm.hints.verb != hintOpen {
		t.Fatalf("hints did not open in the open verb: active=%v verb=%v", tm.hints.active, tm.hints.verb)
	}

	key(tm, w, gui.KeyY, hintsMods)
	if !tm.hints.active {
		t.Fatal("the copy chord dropped the mode instead of switching verb")
	}
	if tm.hints.verb != hintCopy {
		t.Fatalf("verb = %v, want hintCopy", tm.hints.verb)
	}

	// And the switched verb is what commits: the URL lands on the clipboard
	// rather than at the OS opener.
	press(tm, w, 'a')
	if *clip != "https://switch.example" {
		t.Errorf("clipboard = %q, want the URL", *clip)
	}
}

func TestHintLabels_ZeroTargets(t *testing.T) {
	// enterHints never calls this with n == 0, but the clamp arithmetic should
	// not depend on that.
	if got := hintLabels(0, nil); len(got) != 0 {
		t.Errorf("hintLabels(0) = %v, want empty", got)
	}
}

func TestHintLabels_ClampsAboveAlphabetSquare(t *testing.T) {
	// Two characters can only address len(alphabet)² targets; asking for more
	// must clamp rather than index past the alphabet.
	n := len(hintAlphabet) * len(hintAlphabet)
	if got := hintLabels(n+50, nil); len(got) != n {
		t.Errorf("got %d labels, want the clamped %d", len(got), n)
	}
}

func TestHints_OSC8TargetWins(t *testing.T) {
	// An OSC 8 link whose text is itself a URL commits the OSC 8 destination,
	// matching what Cmd+click resolves.
	tm, w, clip, _ := hintTerm(40, "see https://visible.example now")
	linkCells(tm.grid, 0, 4, 26, "https://real.example")

	key(tm, w, gui.KeyY, hintsMods)
	press(tm, w, 'a')

	if *clip != "https://real.example" {
		t.Errorf("clipboard = %q, want the OSC 8 destination", *clip)
	}
}

func TestHintLabels_WidthIsUniform(t *testing.T) {
	// A mixed-width label set would make a typed prefix ambiguous between
	// "complete" and "still narrowing", which needs a timeout to resolve.
	for _, n := range []int{1, len(hintAlphabet), len(hintAlphabet) + 1, 100} {
		labels := hintLabels(n, nil)
		if len(labels) != n {
			t.Fatalf("n=%d: got %d labels", n, len(labels))
		}
		want := len(labels[0])
		for i, l := range labels {
			if len(l) != want {
				t.Errorf("n=%d: label %d = %q, width %d, want %d", n, i, l, len(l), want)
			}
		}
	}
}

func TestHintLabels_Unique(t *testing.T) {
	labels := hintLabels(200, nil)
	seen := make(map[string]bool, len(labels))
	for _, l := range labels {
		if seen[l] {
			t.Fatalf("duplicate label %q", l)
		}
		seen[l] = true
	}
}

func TestHintLabelCol_PrefersBlankCellsLeft(t *testing.T) {
	// The label goes in the space before the link, so the URL's own first
	// characters stay readable — "attps://go.dev" is the bug this prevents.
	g := newGrid(3, 40)
	putRowAt(g, 0, "see https://go.dev now")
	sb := g.Scrollback.Len()
	sp := urlSpan{Row: sb, C0: 4, C1: 17}

	if got := hintLabelCol(g, sp, 1); got != 3 {
		t.Errorf("one-char label placed at col %d, want 3 (the space)", got)
	}
	// Two characters need two blanks; only one is free, so it falls back to
	// covering the link rather than eating the word before it.
	if got := hintLabelCol(g, sp, 2); got != 4 {
		t.Errorf("two-char label placed at col %d, want 4 (over the link)", got)
	}
}

func TestHintLabelCol_FallsBackAtLineStart(t *testing.T) {
	// A link in column 0 has nowhere to the left to go.
	g := newGrid(3, 40)
	putRowAt(g, 0, "https://go.dev")
	sb := g.Scrollback.Len()
	if got := hintLabelCol(g, urlSpan{Row: sb, C0: 0, C1: 13}, 1); got != 0 {
		t.Errorf("label placed at col %d, want 0", got)
	}
}
