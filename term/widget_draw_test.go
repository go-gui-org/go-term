package term

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// ---------------------------------------------------------------------------
// Early-exit guards
// ---------------------------------------------------------------------------

func TestOnDraw_ZeroCellMetricReturnsEarly(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{grid: g, cellW: 0, cellH: 0}
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)
	// nil textMeasure → TextWidth returns 0, so cellW stays 0.
	dc := gui.NewDrawContext(800, 480, nil)
	tm.onDraw(dc)
	if len(dc.Texts()) != 0 || len(dc.Batches()) != 0 {
		t.Error("zero cell metrics: expected no output")
	}
}

func TestOnDraw_NaNCanvasWidthReturnsEarly(t *testing.T) {
	tm, dc := newDrawTerm(24, 80, 10, 20)
	dc.Width = float32(math.NaN())
	tm.onDraw(dc)
	if len(dc.Texts()) != 0 || len(dc.Batches()) != 0 {
		t.Error("NaN canvas: expected no output")
	}
}

// ---------------------------------------------------------------------------
// Background pass
// ---------------------------------------------------------------------------

func TestOnDraw_DefaultBGSkipsBackgroundPass(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorVisible = false
	g.CursorR = -1
	g.CursorC = -1
	// Use defaultCell() so BG=DefaultColor (not ANSI 0=black).
	for c := range g.Cols {
		g.Cells[c] = defaultCell()
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	if len(dc.Texts()) != 0 {
		t.Errorf("default bg grid: expected no text, got %d: %v", len(dc.Texts()), dc.Texts())
	}
	if len(dc.Batches()) != 0 {
		t.Errorf("default bg grid: expected no batches, got %d: %v", len(dc.Batches()), dc.Batches())
	}
}

func TestOnDraw_BackgroundNonDefaultSingleCell(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.Cells[0].BG = rgbColor(255, 0, 0)
	// Set all cells to spaces for clean text pass.
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	// One batch for the red cell at (0,0); rest is DefaultBG skipped.
	if len(batches) < 1 {
		t.Fatal("expected at least 1 batch for non-default bg")
	}
	found := false
	for _, b := range batches {
		if b.Color == gui.RGB(255, 0, 0) {
			found = true
			if len(b.Triangles) < 6 {
				t.Error("red batch should have at least 6 floats (2 triangles)")
			}
			break
		}
	}
	if !found {
		t.Error("no batch found with red color")
	}
}

func TestOnDraw_BackgroundMultipleRuns(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	// Row 0: cols 0-1 red, 2-4 blue, 5-7 default
	for c := range 2 {
		g.Cells[c].BG = rgbColor(255, 0, 0)
	}
	for c := 2; c < 5; c++ {
		g.Cells[c].BG = rgbColor(0, 0, 255)
	}
	// Set all chars to space.
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	redCount, blueCount := 0, 0
	for _, b := range batches {
		if b.Color == gui.RGB(255, 0, 0) {
			redCount++
		}
		if b.Color == gui.RGB(0, 0, 255) {
			blueCount++
		}
	}
	if redCount != 1 {
		t.Errorf("expected 1 red batch, got %d", redCount)
	}
	if blueCount != 1 {
		t.Errorf("expected 1 blue batch, got %d", blueCount)
	}
}

func TestOnDraw_BackgroundPartialTopRow(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.ViewSubPx = 10
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	// With ViewSubPx > 0, partialTopRow is called. Scrollback is empty,
	// so partialRow is nil and no row -1 batches appear. Verify OnDraw
	// completes without panic — partialRow nil-path is exercised.
}

// ---------------------------------------------------------------------------
// Foreground pass
// ---------------------------------------------------------------------------

func TestOnDraw_TextSingleRun(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	for c := range 5 {
		g.Cells[c].Ch = rune('a' + c) // a,b,c,d,e
		g.Cells[c].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	texts := dc.Texts()
	if len(texts) < 1 {
		t.Fatal("expected at least 1 text entry")
	}
	// All 5 chars same style → one coalesced run.
	found := false
	for _, te := range texts {
		if strings.Contains(te.Text, "abcde") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected coalesced 'abcde', got texts: %v", texts)
	}
}

func TestOnDraw_TextColorChangeBreaksRun(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorVisible = false // suppress cursor
	g.Cells[0].Ch = 'a'
	g.Cells[0].Width = 1
	g.Cells[0].FG = rgbColor(255, 0, 0) // red
	g.Cells[1].Ch = 'b'
	g.Cells[1].Width = 1
	g.Cells[1].FG = rgbColor(0, 0, 255) // blue
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	texts := dc.Texts()
	// Should have at least 2 Text entries: one for 'a', one for 'b'
	countA, countB := 0, 0
	for _, te := range texts {
		if strings.Contains(te.Text, "a") {
			countA++
		}
		if strings.Contains(te.Text, "b") {
			countB++
		}
	}
	if countA != 1 || countB != 1 {
		t.Errorf("expected 1 'a' and 1 'b' text entry, got %d/%d (all: %v)",
			countA, countB, texts)
	}
}

func TestOnDraw_WideCharEmitsSeparate(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.Cells[0].Ch = 'W'
	g.Cells[0].Width = 2 // wide char
	g.Cells[1].Ch = 0    // continuation cell
	g.Cells[1].Width = 0 //
	g.Cells[2].Ch = 'x'
	g.Cells[2].Width = 1
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	texts := dc.Texts()
	if len(texts) < 1 {
		t.Fatal("expected at least 1 text entry")
	}
	// Wide char 'W' should be in its own Text entry.
	// Continuation cell skipped. 'x' in a separate entry.
	wFound, xFound := false, false
	for _, te := range texts {
		if strings.Contains(te.Text, "W") {
			wFound = true
		}
		if strings.Contains(te.Text, "x") {
			xFound = true
		}
	}
	if !wFound {
		t.Error("wide char 'W' not found in text entries")
	}
	if !xFound {
		t.Error("'x' not found in text entries")
	}
}

func TestOnDraw_PlainSpacesNotEmitted(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorVisible = false // suppress cursor output
	g.CursorR = -1          // move cursor out of viewport
	g.CursorC = -1
	for c := range g.Cols {
		g.Cells[c].Ch = ' '
		g.Cells[c].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	// flushRun trims trailing spaces, so plain-space runs produce no text.
	for _, te := range dc.Texts() {
		if strings.TrimSpace(te.Text) == "" && len(te.Text) > 0 {
			t.Errorf("unexpected whitespace-only text entry: %q", te.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Cursor rendering
// ---------------------------------------------------------------------------

func TestOnDraw_CursorBlock(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorR = 0
	g.CursorC = 0
	g.CursorVisible = true
	g.cursorShape = cursorBlock
	g.Cells[0].Ch = 'X'
	g.Cells[0].Width = 1
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	// Block cursor: FilledRect at (0,0,10,20) with fg(cell) color,
	// plus Text entry with inverted color.
	if len(batches) < 1 {
		t.Fatal("expected cursor batch")
	}
	// The cursor batch should be at x=0, y=0, w=10, h=20.
	hasCursor := false
	for _, b := range batches {
		if len(b.Triangles) >= 6 && b.Triangles[0] == 0 && b.Triangles[1] == 0 {
			hasCursor = true
			break
		}
	}
	if !hasCursor {
		t.Error("no cursor batch found at (0,0)")
	}
}

func TestOnDraw_CursorUnderline(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorR = 0
	g.CursorC = 0
	g.CursorVisible = true
	g.cursorShape = cursorUnderline
	g.Cells[0].Ch = 'X'
	g.Cells[0].Width = 1
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	// Underline cursor: FilledRect at bottom of cell.
	// cellH=20 → h = 20/8 = 2.5 → min 2 = 2.5, so h=2.5.
	// y = 0 + 20 - 2.5 = 17.5.
	found := false
	for _, b := range batches {
		if len(b.Triangles) < 6 {
			continue
		}
		y0 := b.Triangles[1]
		if y0 > 17 && y0 < 18 {
			found = true
			break
		}
	}
	if !found {
		t.Error("no underline cursor batch at y≈17.5 found")
	}
}

func TestOnDraw_CursorBarShape(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorR = 0
	g.CursorC = 0
	g.CursorVisible = true
	g.cursorShape = cursorBar
	g.Cells[0].Ch = 'X'
	g.Cells[0].Width = 1
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	// Bar cursor: narrow vertical rect, w = max(2, cellW/6) = max(2, 10/6) = 2.
	found := false
	for _, b := range batches {
		if len(b.Triangles) < 6 {
			continue
		}
		// Bar rect should be at (0,0) with w=2.
		if b.Triangles[0] == 0 && b.Triangles[1] == 0 {
			// Width = Triangles[2] - Triangles[0] = w
			w := b.Triangles[2] - b.Triangles[0]
			if w == 2 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no bar cursor batch with w=2 found")
	}
}

func TestOnDraw_CursorSuppressScrolledBack(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.ViewOffset = 10
	g.CursorR = 0
	g.CursorC = 0
	g.CursorVisible = true
	g.Cells[0].Ch = 'X'
	g.Cells[0].Width = 1
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	// Cursor guard requires ViewOffset == 0. With ViewOffset=10, no cursor
	// rect or inverted text should appear. Verify OnDraw completes without
	// panic — the scrolled-back fast path correctly skips cursor rendering.
}

func TestOnDraw_CursorBlinkOffSupressed(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorR = 0
	g.CursorC = 0
	g.CursorVisible = true
	g.CursorBlink = true
	g.Cells[0].Ch = 'X'
	g.Cells[0].Width = 1
	tm.grid.Mu.Unlock()
	// Advance epoch into the silent half of the blink cycle so
	// cursorBlinkOff returns true.
	tm.cursorEpoch = time.Now().Add(-cursorBlinkPeriod)
	tm.onDraw(dc)
	// Cursor guard: cursorBlinkOff=true → no cursor rect or inverted text.
	// Verify OnDraw completes without panic in the blink-off path.
}

// ---------------------------------------------------------------------------
// Search bar
// ---------------------------------------------------------------------------

func TestOnDraw_SearchBarActive(t *testing.T) {
	tm, dc := newDrawTerm(4, 40, 10, 20)
	tm.grid.Mu.Lock()
	tm.search.active = true
	tm.search.query = "hello"
	// Set cells to spaces.
	g := tm.grid
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	texts := dc.Texts()
	found := false
	for _, te := range texts {
		if strings.Contains(te.Text, "Find (^R=regex): hello") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("search bar text not found in: %v", texts)
	}
}

func TestOnDraw_SearchBarNoMatch(t *testing.T) {
	tm, dc := newDrawTerm(4, 40, 10, 20)
	tm.grid.Mu.Lock()
	tm.search.active = true
	tm.search.query = "NONEXISTENT"
	tm.search.matches = nil // no matches
	g := tm.grid
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	// Red background when no match.
	found := false
	for _, b := range dc.Batches() {
		if b.Color == gui.RGB(90, 20, 20) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected red search bar background for no-match")
	}
}

func TestOnDraw_SearchBarRegexInvalid(t *testing.T) {
	tm, dc := newDrawTerm(4, 40, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.CursorVisible = false
	tm.search.active = true
	tm.search.query = "bad["
	tm.search.regex = true
	tm.search.reErr = errors.New("parse error")
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	// Label format: "/re/ " + query + " [invalid]▌"
	// query = "bad[" → "/re/ bad[ [invalid]▌"
	found := false
	for _, te := range dc.Texts() {
		if strings.Contains(te.Text, "/re/ bad[") && strings.Contains(te.Text, "[invalid]") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected invalid regex label in: %v", dc.Texts())
	}
}

// ---------------------------------------------------------------------------
// Scrollbar
// ---------------------------------------------------------------------------

func TestOnDraw_ScrollbarVisibleWhenScrolledBack(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	// Initialize scrollback ring with capacity.
	g.Scrollback.SetGeom(100, g.Cols)
	row := make([]cell, g.Cols)
	for i := range row {
		row[i] = cell{Ch: ' ', Width: 1}
	}
	for range 60 {
		g.Scrollback.Push(row, false)
	}
	g.ViewOffset = 50
	tm.scrollbar.until = time.Now().Add(time.Second)
	g.CursorVisible = false
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	batches := dc.Batches()
	found := false
	for _, b := range batches {
		if b.Color == gui.RGBA(128, 128, 128, 120) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected scrollbar thumb batch with semi-transparent gray")
	}
}

func TestOnDraw_ScrollbarHiddenLive(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	g.ViewOffset = 0
	tm.scrollbar.until = time.Time{} // expired
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.onDraw(dc)
	for _, b := range dc.Batches() {
		if b.Color == gui.RGBA(128, 128, 128, 120) {
			t.Error("scrollbar should be hidden at live viewport")
		}
	}
}

// ---------------------------------------------------------------------------
// Bell flash
// ---------------------------------------------------------------------------

func TestOnDraw_BellFlash(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	for i := range g.Cells {
		g.Cells[i].Ch = ' '
		g.Cells[i].Width = 1
	}
	tm.grid.Mu.Unlock()
	tm.bell.flashUntil = time.Now().Add(time.Second)
	tm.onDraw(dc)
	found := false
	for _, b := range dc.Batches() {
		if b.Color == gui.RGBA(255, 255, 255, 40) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected bell flash overlay")
	}
}

// ---------------------------------------------------------------------------
// Draw primitives (direct calls, no OnDraw)
// ---------------------------------------------------------------------------

func TestDrawCursor_CustomColor(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.grid.Mu.Lock()
	tm.grid.CursorColor = 0xFF0000 // red
	tm.grid.Mu.Unlock()
	cell := cell{Ch: 'X', Width: 1}
	tm.drawCursorShape(dc, 0, 0, cell, cursorBlock, gui.TextStyle{})
	batches := dc.Batches()
	found := false
	for _, b := range batches {
		if b.Color == gui.RGB(255, 0, 0) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected cursor fill with custom CursorColor=red")
	}
}

func TestFillRun_DefaultBGSkipped(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	before := len(dc.Batches())
	tm.fillRun(dc, 0, 0, 5, tm.grid.Theme.DefaultBG, 0)
	if len(dc.Batches()) != before {
		t.Error("fillRun should skip DefaultBG")
	}
}

func TestFillRun_NonDefaultBG(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.fillRun(dc, 0, 2, 5, gui.RGB(0, 255, 0), 0)
	batches := dc.Batches()
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	b := batches[0]
	if b.Color != gui.RGB(0, 255, 0) {
		t.Error("wrong color")
	}
	// x=2*10=20, y=0, w=3*10=30, h=20
	if b.Triangles[0] != 20 || b.Triangles[1] != 0 {
		t.Errorf("expected (20,0) got (%f,%f)", b.Triangles[0], b.Triangles[1])
	}
	if b.Triangles[2] != 50 || b.Triangles[3] != 0 {
		t.Errorf("expected (50,0) got (%f,%f)", b.Triangles[2], b.Triangles[3])
	}
}

func TestEmitCell_Plain(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	cell := cell{Ch: 'Z', Width: 1}
	k := runKey{color: gui.RGB(255, 255, 255)}
	tm.emitCell(dc, 0, 0, cell, k, gui.TextStyle{})
	texts := dc.Texts()
	if len(texts) != 1 {
		t.Fatalf("expected 1 text entry, got %d", len(texts))
	}
	if texts[0].Text != "Z" {
		t.Errorf("expected 'Z', got %q", texts[0].Text)
	}
}

func TestEmitCell_WithUnderline(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	cell := cell{Ch: 'U', Width: 1}
	k := runKey{
		color:   gui.RGB(255, 255, 255),
		ulStyle: ulSingle,
		ulColor: gui.RGB(255, 255, 255),
	}
	tm.emitCell(dc, 0, 0, cell, k, gui.TextStyle{})
	if len(dc.Texts()) != 1 {
		t.Error("expected 1 text entry for underline cell")
	}
	// Underline decor emits an additional FilledRect batch.
	if len(dc.Batches()) < 1 {
		t.Error("expected underline decor batch")
	}
}

func TestDrawUnderlineDecor_Single(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	// cellH=20 → thick = 20/14 ≈ 1.43, baseY = 20 - 2*1.43 - 1 ≈ 16.14
	tm.drawUnderlineDecor(dc, 0, 0, 50, ulSingle, gui.RGB(255, 255, 255))
	if len(dc.Batches()) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(dc.Batches()))
	}
	b := dc.Batches()[0]
	baseY := float32(0) + tm.cellH - 2*(tm.cellH/14) - 1
	y := b.Triangles[1]
	if y < baseY-0.001 || y > baseY+0.001 {
		t.Errorf("expected y≈%f, got y=%f", baseY, y)
	}
}

func TestDrawUnderlineDecor_Double(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.drawUnderlineDecor(dc, 0, 0, 50, ulDouble, gui.RGB(255, 255, 255))
	// Double: two rect bars with same color → merged into one batch.
	// At least 2 triangles per rect, 4 rects → valid if we have triangle data.
	if len(dc.Batches()) < 1 {
		t.Fatal("expected at least 1 batch for double underline")
	}
}

func TestDrawUnderlineDecor_Curly(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.drawUnderlineDecor(dc, 0, 0, 50, ulCurly, gui.RGB(255, 255, 255))
	// Curly: alternating up/down segments. Should produce at least 1 batch.
	if len(dc.Batches()) < 1 {
		t.Error("expected batches for curly underline")
	}
}

func TestDrawUnderlineDecor_Dotted(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.drawUnderlineDecor(dc, 0, 0, 50, ulDotted, gui.RGB(255, 255, 255))
	// Dotted: small rectangles with gaps. Should produce batches.
	if len(dc.Batches()) < 1 {
		t.Error("expected batches for dotted underline")
	}
}

func TestDrawUnderlineDecor_Dashed(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, 10, 20)
	tm.drawUnderlineDecor(dc, 0, 0, 50, ulDashed, gui.RGB(255, 255, 255))
	if len(dc.Batches()) < 1 {
		t.Error("expected batches for dashed underline")
	}
}
