package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// newSettingsTerm builds a Term bare enough for the live setters: a grid, a
// scheduler that runs queued commands inline, and a configured text style.
func newSettingsTerm(cfg Cfg) *Term {
	t := &Term{
		grid:   newGrid(4, 8),
		cfg:    cfg,
		cmd:    syncScheduler{},
		parser: newParser(newGrid(4, 8)),
	}
	t.parser = newParser(t.grid)
	if s := t.style(); s.Size > 0 {
		t.fontSize = s.Size
	}
	return t
}

// TestSetTextStyle_ClearsZoomOverride is the reason SetTextStyle exists rather
// than callers writing cfg.TextStyle: t.fontSize is an absolute size derived
// from the *previous* base and outranks cfg.TextStyle.Size in style(). Without
// the reset, a pane zoomed to 20 pt would ignore a config reload that sets
// 12 pt until the user pressed Cmd+0.
func TestSetTextStyle_ClearsZoomOverride(t *testing.T) {
	tm := newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Family: "Menlo", Size: 12}})
	tm.AdjustFontSize(8) // zoom to 20 pt
	if tm.fontSize != 20 {
		t.Fatalf("setup: fontSize = %v, want 20", tm.fontSize)
	}

	tm.SetTextStyle(gui.TextStyle{Family: "Iosevka", Size: 12})

	if tm.fontSize != 0 {
		t.Errorf("fontSize = %v, want 0 (zoom cleared)", tm.fontSize)
	}
	got := tm.style()
	if got.Family != "Iosevka" || got.Size != 12 {
		t.Errorf("style = %+v, want Iosevka/12", got)
	}
}

// TestSetTextStyle_InvalidatesMetrics checks the cached cell width and glyph
// runs are dropped, so the next frame remeasures at the new family/size.
func TestSetTextStyle_InvalidatesMetrics(t *testing.T) {
	tm := newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Size: 12}})
	tm.cellW = 7
	tm.draw.runeCache = map[rune]string{'x': "x"}

	tm.SetTextStyle(gui.TextStyle{Size: 13})

	if tm.cellW != 0 {
		t.Errorf("cellW = %v, want 0 (remeasure pending)", tm.cellW)
	}
	if tm.draw.runeCache != nil {
		t.Error("stale glyph run cache survived the style change")
	}
}

// TestStyle_ClampsConfiguredSize covers a config file (or embedder) asking for
// a size outside the zoom bounds: the effective size is clamped rather than
// being handed to the text measurer as-is.
func TestStyle_ClampsConfiguredSize(t *testing.T) {
	tm := newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Size: 500}})
	if got := tm.style().Size; got != maxFontSize {
		t.Errorf("style().Size = %v, want %v", got, maxFontSize)
	}
	tm = newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Size: 1}})
	if got := tm.style().Size; got != minFontSize {
		t.Errorf("style().Size = %v, want %v", got, minFontSize)
	}
}

func TestSetScrollbackRows_SignConvention(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"zero restores default", 0, defaultScrollbackRows},
		{"positive sets cap", 100, 100},
		{"negative disables", -1, 0},
		{"over-large clamps", MaxScrollbackCap * 2, MaxScrollbackCap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := newSettingsTerm(Cfg{})
			tm.grid.ScrollbackCap = 42
			tm.SetScrollbackRows(tc.n)
			if tm.grid.ScrollbackCap != tc.want {
				t.Errorf("ScrollbackCap = %d, want %d", tm.grid.ScrollbackCap, tc.want)
			}
		})
	}
}

// TestSetScrollbackRows_TrimsExistingRows checks a shrink actually returns the
// memory instead of waiting for eviction, keeps the newest rows, and pulls a
// viewport scrolled past the new end back into range.
func TestSetScrollbackRows_TrimsExistingRows(t *testing.T) {
	tm := newSettingsTerm(Cfg{})
	tm.grid.ScrollbackCap = 10
	tm.grid.Scrollback.EnsureGeom(10, 8)
	for i := range 10 {
		row := make([]cell, 8)
		row[0].Ch = rune('a' + i)
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.ViewOffset = 10

	tm.SetScrollbackRows(3)

	if got := tm.grid.Scrollback.Len(); got != 3 {
		t.Fatalf("Scrollback.Len() = %d, want 3", got)
	}
	// Oldest rows are the ones dropped: 'h', 'i', 'j' survive.
	if got := tm.grid.Scrollback.Row(0)[0].Ch; got != 'h' {
		t.Errorf("oldest surviving row = %q, want 'h'", got)
	}
	if tm.grid.ViewOffset != 3 {
		t.Errorf("ViewOffset = %d, want 3 (clamped to the trimmed history)",
			tm.grid.ViewOffset)
	}
}

func TestSetScrollbackRows_DisableDropsHistory(t *testing.T) {
	tm := newSettingsTerm(Cfg{})
	tm.grid.ScrollbackCap = 10
	tm.grid.Scrollback.EnsureGeom(10, 8)
	tm.grid.Scrollback.Push(make([]cell, 8), false)

	tm.SetScrollbackRows(-1)

	if got := tm.grid.Scrollback.Len(); got != 0 {
		t.Errorf("Scrollback.Len() = %d, want 0", got)
	}
	if tm.grid.Scrollback.cells != nil {
		t.Error("backing array retained after disabling scrollback")
	}
}

// TestSetBellMode_TakesEffect drives a BEL through the parser to confirm the
// setter reaches ringBell, which reads the atomic rather than Cfg.
func TestSetBellMode_TakesEffect(t *testing.T) {
	tm := newBellTerm(BellVisual)
	tm.SetBellMode(BellNone)
	tm.applyChunk([]byte("\x07"), true)
	if tm.bell.flashUntil.Load() != 0 {
		t.Error("BellNone set at runtime still flashed")
	}
}

func TestSetScrollbarWidth(t *testing.T) {
	tm := newSettingsTerm(Cfg{})
	if got := tm.effectiveScrollbarWidth(); got != scrollbarWidth {
		t.Fatalf("setup: width = %v, want the built-in default %v", got, scrollbarWidth)
	}
	tm.SetScrollbarWidth(9)
	if got := tm.effectiveScrollbarWidth(); got != 9 {
		t.Errorf("width = %v, want 9", got)
	}
	tm.SetScrollbarWidth(-1)
	if got := tm.effectiveScrollbarWidth(); got != 0 {
		t.Errorf("width = %v, want 0 (hidden)", got)
	}
	tm.SetScrollbarWidth(0)
	if got := tm.effectiveScrollbarWidth(); got != scrollbarWidth {
		t.Errorf("width = %v, want the built-in default restored", got)
	}
}

func TestParseAction(t *testing.T) {
	if a, ok := ParseAction("term.copy"); !ok || a != ActionCopy {
		t.Errorf("ParseAction(term.copy) = %v, %v", a, ok)
	}
	for _, name := range []string{"copy", "term.nope", "", "workspace.newTab"} {
		if _, ok := ParseAction(name); ok {
			t.Errorf("ParseAction(%q) resolved, want failure", name)
		}
	}
}
