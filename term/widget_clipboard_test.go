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
