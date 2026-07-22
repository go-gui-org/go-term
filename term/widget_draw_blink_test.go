package term

import (
	"strings"
	"testing"
	"time"
)

func TestTextBlinkOff_AlternatesEachHalfCycle(t *testing.T) {
	base := time.Unix(0, 0)
	if textBlinkOff(base) {
		t.Error("first half-cycle should be visible")
	}
	if !textBlinkOff(base.Add(cursorBlinkPeriod)) {
		t.Error("second half-cycle should be hidden")
	}
	if textBlinkOff(base.Add(2 * cursorBlinkPeriod)) {
		t.Error("third half-cycle should be visible again")
	}
}

func TestMaskGlyph(t *testing.T) {
	tests := []struct {
		name     string
		attrs    uint8
		blinkOff bool
		hidden   bool
	}{
		{"plain", 0, false, false},
		{"plain during blink-off half", 0, true, false},
		{"conceal always hidden", attrConceal, false, true},
		{"blink visible half", attrBlink, false, false},
		{"blink hidden half", attrBlink, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := cell{Ch: 'x', Attrs: tc.attrs, Width: 1, clusterID: 7}
			got := maskGlyph(c, tc.blinkOff)
			if tc.hidden {
				if got.Ch != ' ' || got.clusterID != 0 {
					t.Errorf("glyph not masked: %q cluster=%d", got.Ch, got.clusterID)
				}
			} else if got.Ch != 'x' || got.clusterID != 7 {
				t.Errorf("glyph altered: %q cluster=%d", got.Ch, got.clusterID)
			}
			// Attributes and width always survive: the background rect,
			// underline and selection inversion still paint.
			if got.Attrs != tc.attrs || got.Width != 1 {
				t.Errorf("attrs/width changed: %d/%d", got.Attrs, got.Width)
			}
		})
	}
}

// SGR 8 exists so ncurses A_INVIS (password prompts) hides typed text; the
// glyph must never reach the canvas.
func TestOnDraw_ConcealedTextNotPainted(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorVisible = false // the cursor draws its own cell; test the fg pass
	for c, ch := range "hunter2" {
		g.Cells[c].Ch = ch
		g.Cells[c].Width = 1
		g.Cells[c].Attrs = attrConceal
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	for _, te := range dc.Texts() {
		if strings.ContainsAny(te.Text, "hunter2") {
			t.Errorf("concealed text painted: %q", te.Text)
		}
	}
}

// A block cursor redraws the glyph under it, so conceal has to be applied
// there too or the secret shows up wherever the cursor rests.
func TestOnDraw_ConcealedCellUnderCursorNotPainted(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.Cells[0].Ch = 'S'
	g.Cells[0].Width = 1
	g.Cells[0].Attrs = attrConceal
	g.CursorR, g.CursorC = 0, 0
	g.CursorVisible = true
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	for _, te := range dc.Texts() {
		if strings.Contains(te.Text, "S") {
			t.Errorf("concealed cell painted under cursor: %q", te.Text)
		}
	}
}

// The blink ticker only needs to force repaints while blinking text is on
// screen; the flag is what tells it so.
func TestOnDraw_TracksBlinkCells(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	tm.grid.Cells[0].Ch = 'a'
	tm.grid.Cells[0].Width = 1
	tm.grid.Cells[0].Attrs = attrBlink
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	if !tm.blinkCells.Load() {
		t.Fatal("blinkCells not set with blinking text on screen")
	}

	tm.grid.Mu.Lock()
	tm.grid.Cells[0].Attrs = 0
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	if tm.blinkCells.Load() {
		t.Error("blinkCells stayed set after the blinking text went away")
	}
}
