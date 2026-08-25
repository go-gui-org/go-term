package term

import (
	"math"
	"testing"
	"time"

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

// countingScheduler records how many commands were queued, so a test can
// assert that a setter asked for a repaint.
type countingScheduler struct{ n *int }

func (c countingScheduler) QueueCommand(fn func(*gui.Window)) {
	*c.n++
	fn(&gui.Window{})
}

// TestSetScrollbackRows_RequestsRepaint pins the repaint that shrinking needs:
// trimming can clamp ViewOffset, which moves the visible viewport, so nothing
// on screen matches the grid until a frame is drawn. Marking rows dirty is not
// enough — something has to schedule the frame.
func TestSetScrollbackRows_RequestsRepaint(t *testing.T) {
	var queued int
	tm := newSettingsTerm(Cfg{})
	tm.cmd = countingScheduler{n: &queued}

	tm.SetScrollbackRows(500)
	if queued == 0 {
		t.Error("a cap change queued no repaint")
	}

	queued = 0
	tm.SetScrollbackRows(500) // same value: nothing changed
	if queued != 0 {
		t.Errorf("a no-op change queued %d repaints, want 0", queued)
	}
}

// TestStyle_NonFiniteSize covers the config path that could deliver a NaN
// size. style()'s clamp used a "Size > 0" gate, which NaN fails, so the value
// reached text measurement unclamped. A non-finite size means "unset".
func TestStyle_NonFiniteSize(t *testing.T) {
	nan := float32(math.NaN())
	tm := newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Family: "Menlo", Size: nan}})
	if got := tm.style().Size; got != 0 {
		t.Errorf("style().Size = %v, want 0 (non-finite treated as unset)", got)
	}
}

// TestSetTextStyle_NormalizesNonFiniteSize checks the setter stores a finite
// size. Left as NaN, the "did it change?" equality below can never hold
// (NaN != NaN), so every later reload would force a remeasure and silently
// throw away the user's font zoom.
func TestSetTextStyle_NormalizesNonFiniteSize(t *testing.T) {
	tm := newSettingsTerm(Cfg{TextStyle: gui.TextStyle{Family: "Menlo", Size: 12}})
	tm.SetTextStyle(gui.TextStyle{Family: "Menlo", Size: float32(math.Inf(1))})
	if got := tm.cfg.TextStyle.Size; got != 0 {
		t.Errorf("cfg.TextStyle.Size = %v, want 0", got)
	}
	// Stored finite, so re-applying the same style is correctly a no-op —
	// with NaN in there the equality check could never hold.
	tm.fontSize = 9 // pretend the user zoomed
	tm.SetTextStyle(gui.TextStyle{Family: "Menlo", Size: float32(math.NaN())})
	if tm.fontSize != 0 {
		t.Error("a pending zoom must still be cleared when the style is re-applied")
	}
	tm.SetTextStyle(gui.TextStyle{Family: "Menlo", Size: float32(math.NaN())})
	if tm.cfg.TextStyle.Size != 0 {
		t.Errorf("cfg.TextStyle.Size = %v, want 0", tm.cfg.TextStyle.Size)
	}
}

// TestTermShortcuts_ZeroValueUsesDefaults matches (*Term).Shortcuts to the
// lazy seed in bindingTable: a Term with no binding table behaves as though it
// has the defaults, so it must also *report* the defaults rather than nothing.
func TestTermShortcuts_ZeroValueUsesDefaults(t *testing.T) {
	tm := &Term{}
	got, want := tm.Shortcuts(), Shortcuts()
	if len(got) != len(want) {
		t.Fatalf("zero-value Term reported %d shortcuts, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("shortcut %d = %+v, want %+v", i, got[i], want[i])
		}
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

// TestSetMinimumContrast covers the live setter a config reload drives. The
// memo behind the clamp is keyed by color pair and *not* by ratio, so entries
// computed under the old ratio are wrong rather than merely stale — dropping
// them is part of the setter's contract, not an optimization.
func TestSetMinimumContrast(t *testing.T) {
	var queued int
	tm := newSettingsTerm(Cfg{})
	tm.cmd = countingScheduler{n: &queued}

	// A starship orange on a Catppuccin Latte background: fails every floor
	// this test sets, and by a different amount for each.
	fg, bg := gui.RGB(255, 161, 1), gui.RGB(239, 241, 245)

	tm.SetMinimumContrast(3)
	if tm.grid.MinContrast != 3 {
		t.Fatalf("MinContrast = %v, want 3", tm.grid.MinContrast)
	}
	if queued == 0 {
		t.Error("a ratio change queued no repaint")
	}
	loose := tm.grid.applyMinContrast(fg, bg) // populates the memo slot

	queued = 0
	tm.SetMinimumContrast(3)
	if queued != 0 {
		t.Errorf("a no-op change queued %d repaints, want 0", queued)
	}

	tm.SetMinimumContrast(7)
	if strict := tm.grid.applyMinContrast(fg, bg); strict == loose {
		t.Error("memo survived the ratio change: still returning the 3.0 result")
	}

	// Non-finite is ignored rather than stored: NaN would compare false
	// against every threshold and silently disable the clamp.
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		tm.SetMinimumContrast(bad)
		if tm.grid.MinContrast != 7 {
			t.Fatalf("SetMinimumContrast(%v) stored %v, want 7 unchanged",
				bad, tm.grid.MinContrast)
		}
	}

	// 1.0 is a color against itself, so it is the documented "off".
	tm.SetMinimumContrast(1)
	if got := tm.grid.applyMinContrast(fg, bg); got != fg {
		t.Errorf("clamp still active at ratio 1: %v became %v", fg, got)
	}
}

// TestApplyContrastConfig covers the construction-time half of the same
// setting, which has to agree with the setter about what counts as "off".
func TestApplyContrastConfig(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"unset_stays_off", 0, 0},
		{"at_the_off_value", 1, 0},
		{"below_the_off_value", 0.5, 0},
		{"negative", -3, 0},
		{"a_real_ratio", 4.5, 4.5},
		{"nan_is_ignored", math.NaN(), 0},
		{"inf_is_ignored", math.Inf(1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(4, 8)
			applyContrastConfig(g, Cfg{MinimumContrast: tc.in})
			if g.MinContrast != tc.want {
				t.Errorf("MinContrast = %v, want %v", g.MinContrast, tc.want)
			}
		})
	}
}

// The three cursor setters write grid state, which is what the draw pass and
// blinkLoop read. Each is a default as well as a live change, so a later RIS
// returns to the value the setter installed.
func TestSetCursor_LiveAndDefault(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}

	tm.SetCursorStyle(CursorStyleUnderline)
	tm.SetCursorBlink(true)
	if tm.grid.cursorShape != CursorStyleUnderline || !tm.grid.CursorBlink {
		t.Fatalf("cursor = %v/blink %v, want underline/true",
			tm.grid.cursorShape, tm.grid.CursorBlink)
	}
	tm.grid.HardReset()
	if tm.grid.cursorShape != CursorStyleUnderline || !tm.grid.CursorBlink {
		t.Errorf("cursor after RIS = %v/blink %v, want the configured underline/true",
			tm.grid.cursorShape, tm.grid.CursorBlink)
	}

	// Out of range falls back to a block, matching what drawCursorShape
	// would have painted for an unknown value.
	tm.SetCursorStyle(CursorStyle(200))
	if tm.grid.cursorShape != CursorStyleBlock {
		t.Errorf("out-of-range style = %v, want block", tm.grid.cursorShape)
	}

	// The lock gates DECSCUSR without moving the current cursor.
	tm.SetCursorStyle(CursorStyleBar)
	tm.SetCursorLocked(true)
	if tm.grid.cursorShape != CursorStyleBar {
		t.Errorf("locking moved the cursor to %v", tm.grid.cursorShape)
	}
	tm.grid.ApplyDECSCUSR(1)
	if tm.grid.cursorShape != CursorStyleBar {
		t.Errorf("locked pane followed DECSCUSR to %v", tm.grid.cursorShape)
	}
	tm.SetCursorLocked(false)
	tm.grid.ApplyDECSCUSR(1)
	if tm.grid.cursorShape != CursorStyleBlock {
		t.Errorf("unlocked pane ignored DECSCUSR: %v", tm.grid.cursorShape)
	}
}

// SetCursorBlink restarts the blink phase: the cursor must be visible at the
// moment the setting changes rather than stuck in the hidden half of a cycle
// the child started. cursorBlinkOff reads cursorEpoch, so the epoch write is
// what makes the promise observable.
func TestSetCursorBlink_RestartsPhase(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.focused.Store(true)
	tm.winFocused.Store(true)

	// Age the epoch so the cursor sits in the hidden half of its cycle…
	tm.grid.CursorBlink = true
	tm.cursorEpoch = time.Now().Add(-cursorBlinkPeriod)
	if !tm.cursorBlinkOff(time.Now()) {
		t.Fatalf("precondition: cursor should be in its hidden half")
	}

	// …and the setter must bring it back into the visible half.
	tm.SetCursorBlink(true)
	if tm.cursorBlinkOff(time.Now()) {
		t.Error("cursor still in the hidden half right after SetCursorBlink(true)")
	}
}

// Setting a value the pane already has must not repaint: every setter in this
// file early-outs, and the version counter is how that is observable.
func TestSetCursor_IdempotentSettersDoNotBump(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.SetCursorStyle(CursorStyleBar)
	tm.SetCursorBlink(true)
	before := tm.drawVersion.Load()
	tm.SetCursorStyle(CursorStyleBar)
	tm.SetCursorBlink(true)
	if got := tm.drawVersion.Load(); got != before {
		t.Errorf("drawVersion moved %d → %d on a no-op set", before, got)
	}
}
