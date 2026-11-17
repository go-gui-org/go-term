package term

// Draw-golden tests pin the onDraw render passes' recorded output: the text
// runs (text, color, typeface, origin), image calls (rect, placement
// identity), and overlay batches. The record-DC call list is the contract —
// no pixels, no GPU. Expectations below were written from observed output and
// each entry is explainable from the input bytes noted above it. When render
// output changes intentionally, update the inline expectations in the same
// PR and say which input bytes explain each changed entry.
//
// The scrolled-viewport golden is the safety net for the scrolled-render
// refactor: it must pass before AND after.

import (
	"strings"
	"testing"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
)

// feedDrawBytes feeds input through a fresh parser into the Term's grid, the
// way bytes arrive from the pty. The grid lock is held across the feed, as
// every other parser driver does.
func feedDrawBytes(tm *Term, input string) {
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	p := newParser(tm.grid)
	p.Feed([]byte(input))
}

// goldenRun is one expected dc.Text call: the coalesced run text, the resolved
// color, the weight/style-selected typeface, and the snapped cell origin.
type goldenRun struct {
	text     string
	color    gui.Color
	typeface glyph.Typeface
	x, y     float32
}

// checkRuns asserts the recorded Texts() match the golden list exactly —
// length, order, and every field. got must already be dc.Texts().
func checkRuns(t *testing.T, got []gui.DrawCanvasTextEntry, want []goldenRun) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d text runs, want %d: %q", len(got), len(want), textsOf(got))
	}
	for i, w := range want {
		g := got[i]
		if g.Text != w.text || g.Style.Color != w.color ||
			g.Style.Typeface != w.typeface || g.X != w.x || g.Y != w.y {
			t.Errorf("run %d = (%q, %+v, %v, %v, %v), want (%q, %+v, %v, %v, %v)",
				i, g.Text, g.Style.Color, g.Style.Typeface, g.X, g.Y,
				w.text, w.color, w.typeface, w.x, w.y)
		}
	}
}

func textsOf(entries []gui.DrawCanvasTextEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Text
	}
	return out
}

// Each SGR segment below becomes exactly one text run; the single spaces
// between them are plain-space cells with the run closed on both sides, so
// they start no run of their own. Columns: red 0-2, grn 4-6, pk 8-9,
// tc 11-12, it 14-15, un 17-18, iv 20-21, dm 23-24; x = col*10, y = 0.
func TestDrawGoldenText_SGRRuns(t *testing.T) {
	tm, dc := newDrawTerm(4, 40, 10, 20)
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	tm.grid.Mu.Unlock()
	feedDrawBytes(tm,
		"\x1b[31mred\x1b[0m "+ // palette red
			"\x1b[1;32mgrn\x1b[0m "+ // bold palette green
			"\x1b[38;5;200mpk\x1b[0m "+ // 256-color magenta
			"\x1b[38;2;10;20;30mtc\x1b[0m "+ // truecolor slate
			"\x1b[3mit\x1b[0m "+ // italic default fg
			"\x1b[4mun\x1b[0m "+ // underline default fg
			"\x1b[7miv\x1b[0m "+ // inverse: fg shows the default bg
			"\x1b[2mdm\x1b[0m") // dim: each channel halved
	tm.onDraw(dc)

	defFG := tm.grid.Theme.DefaultFG
	defBG := tm.grid.Theme.DefaultBG
	checkRuns(t, dc.Texts(), []goldenRun{
		{"red", gui.RGB(0xCD, 0x31, 0x31), glyph.TypefaceRegular, 0, 0},
		{"grn", gui.RGB(0x0D, 0xBC, 0x79), glyph.TypefaceBold, 40, 0},
		{"pk", gui.RGB(0xFF, 0x00, 0xD7), glyph.TypefaceRegular, 80, 0},
		{"tc", gui.RGB(0x0A, 0x14, 0x1E), glyph.TypefaceRegular, 110, 0},
		{"it", defFG, glyph.TypefaceItalic, 140, 0},
		{"un", defFG, glyph.TypefaceRegular, 170, 0},
		{"iv", defBG, glyph.TypefaceRegular, 200, 0},
		{"dm", gui.RGB(0x72, 0x72, 0x72), glyph.TypefaceRegular, 230, 0},
	})

	// Two batches share the default-fg color and merge in the record DC:
	// the underline decoration under "un" and the inverse cell background
	// behind "iv" (2 columns each, 2 triangles each = 4 triangles total).
	batches := dc.Batches()
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1 (merged underline + inverse bg): %v",
			len(batches), batches)
	}
	if batches[0].Color != defFG {
		t.Errorf("batch color = %+v, want default fg %+v", batches[0].Color, defFG)
	}
	if len(batches[0].Triangles) != 24 {
		t.Errorf("batch holds %d floats, want 24 (4 triangles)",
			len(batches[0].Triangles))
	}
}

// The minimum-contrast clamp must fire: bright-black (0x66) on the dark
// default background is below 4.5, so the painted run is the clamped gray,
// not the raw palette color — and every default-underline run like it keeps
// a ratio at or above the floor.
func TestDrawGoldenText_MinContrast(t *testing.T) {
	tm, dc := newDrawTerm(2, 10, 10, 20)
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	tm.grid.MinContrast = 4.5
	tm.grid.Mu.Unlock()
	feedDrawBytes(tm, "\x1b[38;5;8mcc")
	tm.onDraw(dc)

	raw := tm.grid.pal[8]
	checkRuns(t, dc.Texts(), []goldenRun{
		{"cc", gui.RGB(0x7E, 0x7E, 0x7E), glyph.TypefaceRegular, 0, 0},
	})
	if got := dc.Texts()[0].Style.Color; got == raw {
		t.Errorf("run color = raw palette %+v; the clamp did not fire", raw)
	}
	tm.grid.Mu.Lock()
	bg := tm.grid.bgOf(tm.grid.Cells[0])
	tm.grid.Mu.Unlock()
	if r := contrastRatio(dc.Texts()[0].Style.Color, bg); r < 4.5 {
		t.Errorf("clamped ratio %.2f, want >= 4.5", r)
	}
}

// Six fed lines on a 4-row grid with scrollback capped leave L0-L1 in
// scrollback and L2-L5 live. The live frame and the frame frozen two rows
// back must resolve the same content rows at the same origins — the case the
// scrolled-render refactor has to preserve.
func TestDrawGoldenText_ScrolledViewport(t *testing.T) {
	tm, _ := newDrawTerm(4, 10, 10, 20)
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	// Without a cap the scrolled rows are discarded and the offset case
	// below would pass vacuously against the live screen.
	tm.grid.ScrollbackCap = 200
	tm.grid.Mu.Unlock()
	feedDrawBytes(tm, "L0\r\nL1\r\nL2\r\nL3\r\nL4\r\nL5")

	defFG := tm.grid.Theme.DefaultFG
	live := []goldenRun{
		{"L2", defFG, glyph.TypefaceRegular, 0, 0},
		{"L3", defFG, glyph.TypefaceRegular, 0, 20},
		{"L4", defFG, glyph.TypefaceRegular, 0, 40},
		{"L5", defFG, glyph.TypefaceRegular, 0, 60},
	}
	scrolled := []goldenRun{
		{"L0", defFG, glyph.TypefaceRegular, 0, 0},
		{"L1", defFG, glyph.TypefaceRegular, 0, 20},
		{"L2", defFG, glyph.TypefaceRegular, 0, 40},
		{"L3", defFG, glyph.TypefaceRegular, 0, 60},
	}
	for _, tc := range []struct {
		name   string
		offset int
		want   []goldenRun
	}{
		{"live", 0, live},
		{"scrolled back two", 2, scrolled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm.grid.Mu.Lock()
			tm.grid.ViewOffset = tc.offset
			tm.grid.Mu.Unlock()
			// A fresh context per frame: DrawContext accumulates calls.
			dc := gui.NewDrawContext(100, 80, testTextMeasurer{cellW: 10, cellH: 20})
			tm.onDraw(dc)
			checkRuns(t, dc.Texts(), tc.want)
		})
	}
}

// End to end: a 24x6 sixel at 8x16 cell pixels covers 3 columns x 1 row, so
// the image call is the 30x20 cell rect at the origin. Text printed below the
// graphic coexists with it; text painted over its cells occludes it — sixel
// has no delete sequence, so painting over the cells is the application's
// eraser (see occludeGraphics), while the blanked cells emit no tofu.
func TestDrawGoldenGraphics_SixelImageCall(t *testing.T) {
	// feedSixelTerm builds a 4x20 draw term holding one 3x1 sixel graphic at
	// the origin and returns it for the caller to overprint (or not).
	feedSixelTerm := func(t *testing.T) (*Term, *gui.DrawContext) {
		t.Helper()
		tm, dc := newDrawTerm(4, 20, 10, 20)
		tm.grid.Mu.Lock()
		tm.grid.CursorVisible = false
		tm.grid.CellPxW, tm.grid.CellPxH = 8, 16
		p := newParser(tm.grid)
		p.SetGraphicsDir(t.TempDir())
		p.Feed([]byte("\x1bP" + sixelPayload("0;0;0", "#0!24~") + "\x1b\\"))
		tm.grid.Mu.Unlock()
		return tm, dc
	}
	defFG := DefaultTheme.DefaultFG

	t.Run("text below coexists", func(t *testing.T) {
		tm, dc := feedSixelTerm(t)
		// The cursor advances past the 1-row image, so this lands on row 1.
		feedDrawBytes(tm, "AB")
		tm.onDraw(dc)

		imgs := dc.Images()
		if len(imgs) != 1 {
			t.Fatalf("got %d image calls, want 1: %+v", len(imgs), imgs)
		}
		im := imgs[0]
		if im.X != 0 || im.Y != 0 || im.W != 30 || im.H != 20 {
			t.Errorf("rect = (%v,%v %vx%v), want (0,0 30x20)", im.X, im.Y, im.W, im.H)
		}
		if im.Clipped {
			t.Error("fully visible graphic must not clip")
		}
		if im.Src == "" || !strings.HasSuffix(im.Src, ".png") {
			t.Errorf("Src = %q, want the decoded sixel PNG path", im.Src)
		}
		checkRuns(t, dc.Texts(), []goldenRun{
			{"AB", defFG, glyph.TypefaceRegular, 0, 20},
		})
	})

	t.Run("overprint occludes", func(t *testing.T) {
		tm, dc := feedSixelTerm(t)
		// Home the cursor and paint over the graphic's cells: the image is
		// dropped, the text stays.
		feedDrawBytes(tm, "\x1b[1;1HAB")
		tm.onDraw(dc)

		if len(dc.Images()) != 0 {
			t.Errorf("got %d image calls after overprint, want 0: %+v",
				len(dc.Images()), dc.Images())
		}
		checkRuns(t, dc.Texts(), []goldenRun{
			{"AB", defFG, glyph.TypefaceRegular, 0, 0},
		})
	})
}

// A complete 2x2 placeholder block is one image draw covering the placement
// rectangle: 2x2 cells at 10x20 is 20x40 px, and the square 40x40 image fits
// to 20x20 anchored top-left. The placeholder cells themselves paint nothing.
func TestDrawGoldenGraphics_PlaceholderRect(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 40, HeightPx: 40, Cols: 2, Rows: 2,
	})
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	tm.grid.Mu.Unlock()
	fillPlaceholderBlock(tm.grid, 3, 5, 2, 2, 1)

	tm.onDraw(dc)

	imgs := dc.Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d image calls, want 1: %+v", len(imgs), imgs)
	}
	im := imgs[0]
	if im.Src != "img.png" {
		t.Errorf("Src = %q, want img.png", im.Src)
	}
	if im.X != 50 || im.Y != 60 || im.W != 20 || im.H != 20 {
		t.Errorf("rect = (%v,%v %vx%v), want (50,60 20x20)", im.X, im.Y, im.W, im.H)
	}
	if im.Clipped {
		t.Error("a fully visible block should not need a clip")
	}
	if im.ClipX != 0 || im.ClipY != 0 || im.ClipW != 0 || im.ClipH != 0 {
		t.Errorf("clip = %v,%v %vx%v, want zero with Clipped=false",
			im.ClipX, im.ClipY, im.ClipW, im.ClipH)
	}
	if len(dc.Texts()) != 0 {
		t.Errorf("placeholder cells painted as text: %q", textsOf(dc.Texts()))
	}
	if len(dc.Batches()) != 0 {
		t.Errorf("got %d batches, want none on a default-bg grid", len(dc.Batches()))
	}
}

// The jump-to-failure outline is four filled rects around prompt row 4
// through output row 5 (y 80-120), full text width (x 0-200), 2 px edges in
// the theme failure red at full alpha. Same-color rects merge into one batch
// of 8 triangles: left, right, top, bottom.
func TestDrawGoldenOverlay_FailBox(t *testing.T) {
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	// failBoxTerm never draws: give it cell metrics and a matching context.
	tm.cellW, tm.cellH = 10, 20
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = false
	tm.grid.Mu.Unlock()
	dc := gui.NewDrawContext(200, 200, testTextMeasurer{cellW: 10, cellH: 20})
	tm.onDraw(dc)

	batches := dc.Batches()
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1 (merged fail-box outline): %+v",
			len(batches), batches)
	}
	if batches[0].Color != withAlpha(tm.grid.ov.fail, 255) {
		t.Errorf("outline color = %+v, want failure red %+v",
			batches[0].Color, withAlpha(tm.grid.ov.fail, 255))
	}
	wantTris := []float32{
		0, 80, 2, 80, 2, 120, 0, 80, 2, 120, 0, 120, // left
		198, 80, 200, 80, 200, 120, 198, 80, 200, 120, 198, 120, // right
		0, 80, 200, 80, 200, 82, 0, 80, 200, 82, 0, 82, // top
		0, 118, 200, 118, 200, 120, 0, 118, 200, 120, 0, 120, // bottom
	}
	gotTris := batches[0].Triangles
	if len(gotTris) != len(wantTris) {
		t.Fatalf("outline holds %d floats, want %d: %v",
			len(gotTris), len(wantTris), gotTris)
	}
	for i, w := range wantTris {
		if gotTris[i] != w {
			t.Fatalf("triangle float %d = %v, want %v (full: %v)",
				i, gotTris[i], w, gotTris)
		}
	}

	// The boxed command's rows still paint as ordinary text underneath.
	checkRuns(t, dc.Texts(), []goldenRun{
		{"$ false", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 0},
		{"boom", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 20},
		{"$ true", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 40},
		{"ok", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 60},
		{"$ false", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 80},
		{"err", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 100},
		{"$", tm.grid.Theme.DefaultFG, glyph.TypefaceRegular, 0, 120},
	})
}
