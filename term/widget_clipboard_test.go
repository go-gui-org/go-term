package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// ---------------------------------------------------------------------------
// pasteFromClipboard
// ---------------------------------------------------------------------------

func TestPasteFromClipboard_EmptyClipboard(t *testing.T) {
	buf := make([]byte, 0, 64)
	tm := &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "" })
	tm.pasteFromClipboard(w)
	if len(buf) != 0 {
		t.Errorf("empty clipboard should produce no output, got %q", buf)
	}
}

func TestPasteFromClipboard_PlainPaste(t *testing.T) {
	buf := make([]byte, 0, 64)
	tm := &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "hello world" })
	tm.pasteFromClipboard(w)
	if string(buf) != "hello world" {
		t.Errorf("got %q, want %q", buf, "hello world")
	}
}

func TestPasteFromClipboard_BracketedPaste(t *testing.T) {
	buf := make([]byte, 0, 64)
	tm := &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	tm.grid.BracketedPaste = true
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "hello" })
	tm.pasteFromClipboard(w)
	want := pasteStart + "hello" + pasteEnd
	if string(buf) != want {
		t.Errorf("got %q, want %q", buf, want)
	}
}

func TestPasteFromClipboard_StripsEmbeddedMarker(t *testing.T) {
	buf := make([]byte, 0, 128)
	tm := &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "a" + pasteEnd + "b" })
	tm.pasteFromClipboard(w)
	got := string(buf)
	if strings.Contains(got, pasteEnd) {
		t.Errorf("paste-end marker should be stripped, got %q", got)
	}
	if got != "ab" {
		t.Errorf("got %q, want %q", got, "ab")
	}
}

func TestPasteFromClipboard_TruncatesLongPaste(t *testing.T) {
	buf := make([]byte, 0, maxPasteBytes+100)
	tm := &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
	}
	// Create payload longer than maxPasteBytes.
	longStr := strings.Repeat("x", maxPasteBytes+1000)
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return longStr })
	tm.pasteFromClipboard(w)
	if len(buf) > maxPasteBytes {
		t.Errorf("paste truncated to %d bytes, got %d", maxPasteBytes, len(buf))
	}
}

func TestPasteFromClipboard_SnapsToLive(t *testing.T) {
	tm := &Term{
		grid: newGrid(4, 8),
		pw:   writerFunc(func([]byte) (int, error) { return 0, nil }),
	}
	tm.grid.ViewOffset = 50
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "test" })
	tm.pasteFromClipboard(w)
	if tm.grid.ViewOffset != 0 {
		t.Errorf("paste should snap to live, ViewOffset=%d", tm.grid.ViewOffset)
	}
}

// ---------------------------------------------------------------------------
// copySelection
// ---------------------------------------------------------------------------

func TestCopySelection_EmptySelection(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	cbText := ""
	w := &gui.Window{}
	w.SetClipboardFn(func(text string) { cbText = text })
	ok := tm.copySelection(w)
	if ok {
		t.Error("empty selection should return false")
	}
	if cbText != "" {
		t.Errorf("SetClipboard should not be called, got %q", cbText)
	}
}

func TestCopySelection_ActiveSelection(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.grid.Mu.Lock()
	tm.grid.SelAnchor = contentPos{Row: 0, Col: 0}
	tm.grid.SelHead = contentPos{Row: 0, Col: 3}
	tm.grid.SelActive = true
	tm.grid.Cells[0].Ch = 'a'
	tm.grid.Cells[1].Ch = 'b'
	tm.grid.Cells[2].Ch = 'c'
	tm.grid.Cells[3].Ch = 'd'
	tm.grid.Mu.Unlock()
	cbText := ""
	w := &gui.Window{}
	w.SetClipboardFn(func(text string) { cbText = text })
	ok := tm.copySelection(w)
	if !ok {
		t.Error("active selection should return true")
	}
	if cbText == "" {
		t.Error("SetClipboard should be called with selection text")
	}
}

// ---------------------------------------------------------------------------
// SendInput (the input-injection path workspace broadcast mirrors through)
// ---------------------------------------------------------------------------

// newPasteTerm builds a bare Term whose pty writes land in *buf.
func newPasteTerm(buf *[]byte) *Term {
	return &Term{
		grid: newGrid(4, 8),
		pw: writerFunc(func(b []byte) (int, error) {
			*buf = append(*buf, b...)
			return len(b), nil
		}),
	}
}

// An InputPaste must consult the *receiving* pane's ?2004 state, not the
// sender's — that is the whole reason broadcast re-encodes per pane instead of
// copying the source pane's already-wrapped bytes.
func TestSendInput_HonorsThisPanesBracketedMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bracketed bool
		want      string
	}{
		{"plain", false, "hello"},
		{"bracketed", true, pasteStart + "hello" + pasteEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf []byte
			tm := newPasteTerm(&buf)
			tm.grid.BracketedPaste = tc.bracketed
			tm.SendInput([]byte("hello"), InputPaste)
			if string(buf) != tc.want {
				t.Errorf("got %q, want %q", buf, tc.want)
			}
		})
	}
}

// An embedded paste-end marker must be stripped on the injection path too, or
// a broadcast payload could break a receiving pane out of bracketed paste and
// run the tail as commands.
func TestSendInput_StripsEmbeddedEndMarker(t *testing.T) {
	var buf []byte
	tm := newPasteTerm(&buf)
	tm.grid.BracketedPaste = true
	tm.SendInput([]byte("safe"+pasteEnd+"rm -rf /"), InputPaste)
	want := pasteStart + "saferm -rf /" + pasteEnd
	if string(buf) != want {
		t.Errorf("got %q, want %q", buf, want)
	}
}

// A payload that sanitizes away to nothing must send nothing — not an empty
// bracketed-paste pair, which some shells treat as a bare Enter.
func TestSendInput_EmptyPayloadWritesNothing(t *testing.T) {
	for _, text := range []string{"", pasteEnd, pasteEnd + pasteEnd} {
		var buf []byte
		tm := newPasteTerm(&buf)
		tm.grid.BracketedPaste = true
		tm.SendInput([]byte(text), InputPaste)
		if len(buf) != 0 {
			t.Errorf("SendInput(%q, InputPaste) wrote %q; want nothing", text, buf)
		}
	}
}

// InputKey bytes are already encoded for the sender and go through untouched —
// re-encoding them here is what would break a pane in a different cursor-key
// or Kitty-protocol mode.
func TestSendInput_KeysGoThroughVerbatim(t *testing.T) {
	var buf []byte
	tm := newPasteTerm(&buf)
	tm.grid.BracketedPaste = true // must not wrap a key sequence
	tm.SendInput([]byte("\x1b[A"), InputKey)
	if want := "\x1b[A"; string(buf) != want {
		t.Errorf("got %q, want %q", buf, want)
	}
}

func TestSendInput_EmptyKeysWriteNothing(t *testing.T) {
	var buf []byte
	tm := newPasteTerm(&buf)
	tm.SendInput(nil, InputKey)
	tm.SendInput([]byte{}, InputKey)
	if len(buf) != 0 {
		t.Errorf("empty key input wrote %q; want nothing", buf)
	}
}

// Both kinds snap the receiving pane back to live. Without this a pane the
// user had scrolled into its scrollback sits frozen while its shell runs the
// command that was just broadcast to it — the feature looks broken exactly
// when it is working.
func TestSendInput_SnapsToLive(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind InputKind
	}{
		{"key", InputKey},
		{"paste", InputPaste},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf []byte
			tm := newPasteTerm(&buf)
			tm.grid.ViewOffset = 50
			tm.SendInput([]byte("x"), tc.kind)
			if tm.grid.ViewOffset != 0 {
				t.Errorf("ViewOffset = %d after SendInput, want 0", tm.grid.ViewOffset)
			}
		})
	}
}

// The loop guard: a mirrored write must not re-enter the tap, or two panes
// broadcasting to each other would recurse forever.
func TestSendInput_DoesNotFireOnInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind InputKind
	}{
		{"key", InputKey},
		{"paste", InputPaste},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf []byte
			tm := newPasteTerm(&buf)
			fired := 0
			tm.cfg.OnInput = func([]byte, InputKind) { fired++ }
			tm.SendInput([]byte("hello"), tc.kind)
			if fired != 0 {
				t.Errorf("SendInput fired OnInput %d times; want 0", fired)
			}
		})
	}
}

// pasteFromClipboard taps with the *unwrapped* text: each receiving pane
// applies its own markers.
func TestPasteFromClipboard_TapsUnwrappedText(t *testing.T) {
	var buf []byte
	tm := newPasteTerm(&buf)
	tm.grid.BracketedPaste = true
	var got []byte
	var gotKind InputKind
	calls := 0
	tm.cfg.OnInput = func(p []byte, kind InputKind) {
		got = append([]byte(nil), p...)
		gotKind = kind
		calls++
	}
	w := &gui.Window{}
	w.SetClipboardGetFn(func() string { return "hello" })
	tm.pasteFromClipboard(w)

	if calls != 1 {
		t.Fatalf("OnInput fired %d times; want 1", calls)
	}
	if gotKind != InputPaste {
		t.Errorf("kind = %v, want InputPaste", gotKind)
	}
	if string(got) != "hello" {
		t.Errorf("tapped %q; want the unwrapped text %q", got, "hello")
	}
	// The local write still gets the wrapper.
	if want := pasteStart + "hello" + pasteEnd; string(buf) != want {
		t.Errorf("local write = %q, want %q", buf, want)
	}
}
