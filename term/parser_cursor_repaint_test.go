package term

import (
	"testing"
	"time"
)

// Cursor appearance changes must dirty the cursor row, or applyChunk sees a
// clean grid and skips the repaint (widget_loops.go). A PTY read carrying only
// DECTCEM or DECSCUSR then leaves the cursor drawn in its previous state until
// unrelated output happens to force a frame — the cursor visibly stops tracking
// what the child asked for. ConPTY makes such chunks routine: it brackets its
// viewport repaints with ESC[?25l … ESC[?25h and splits them across writes.
func TestCursorStateChangeMarksDirty(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		{"DECTCEM hide", "\x1b[?25l"},
		{"DECTCEM show", "\x1b[?25h"},
		{"DECSCUSR bar", "\x1b[5 q"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newParserGrid(3, 10)
			if tc.seq == "\x1b[?25h" {
				p.Feed([]byte("\x1b[?25l"))
			}
			g.ClearDirty()
			p.Feed([]byte(tc.seq))
			if !g.HasDirtyRows() {
				t.Fatalf("%q left no dirty row: the frame is never scheduled", tc.seq)
			}
		})
	}
}

// A no-op DECTCEM (already visible) need not dirty anything.
func TestCursorVisibleNoOpStaysClean(t *testing.T) {
	g, p := newParserGrid(3, 10)
	g.ClearDirty()
	p.Feed([]byte("\x1b[?25h"))
	if g.HasDirtyRows() {
		t.Fatal("redundant ESC[?25h dirtied a row")
	}
}

// repaintTestTerm is a parser-backed Term with an inline command scheduler,
// enough to exercise applyChunk's repaint decision without a window.
func repaintTestTerm() *Term {
	g := newGrid(4, 80)
	tm := &Term{grid: g, parser: newParser(g), cmd: syncScheduler{}}
	tm.bellMode.Store(int32(BellNone))
	return tm
}

// A chunk that only moves the cursor must still schedule a frame. Erasing
// over blank cells is the case that exposes it: the child rewrites nothing
// (a space over a space is not a change), so the only effect of the read is
// the cursor stepping left. Gating the repaint on dirty rows alone leaves the
// cursor painted where it used to be until the user backspaces over a real
// character and the rewrite dirties the row — the cursor then jumps several
// columns at once to catch up.
func TestCursorOnlyMoveSchedulesFrame(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"BS", "\b"},
		{"CR", "\r"},
		{"HT", "\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := repaintTestTerm()
			tm.applyChunk([]byte("abcd"), true)
			// Stand in for the frame that paint would have produced.
			tm.grid.ClearDirty()
			tm.rephaseCursorBlink(tm.grid.CursorR, tm.grid.CursorC, time.Now())

			if !tm.applyChunk([]byte(tc.seq), true) {
				t.Fatalf("%q scheduled no frame: the cursor stays painted at its old column", tc.seq)
			}
		})
	}
}

// Output that moves nothing and changes nothing still schedules no frame.
func TestNoChangeSchedulesNoFrame(t *testing.T) {
	tm := repaintTestTerm()
	tm.applyChunk([]byte("abcd"), true)
	tm.grid.ClearDirty()
	tm.rephaseCursorBlink(tm.grid.CursorR, tm.grid.CursorC, time.Now())

	if tm.applyChunk([]byte("\x1b[?1000h"), true) {
		t.Fatal("a mode set that touches neither cells nor cursor forced a frame")
	}
}
