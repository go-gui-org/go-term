package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// putPlaceholder writes one placeholder cell at grid (r, c) for the given
// image id, tile (row, col). Written directly rather than through the parser
// so the tests state the cell contents they mean.
func putPlaceholder(g *grid, r, c int, id uint32, row, col int) {
	text := string(kgpPlaceholderRune) +
		string(kgpDiacritics[row].r) + string(kgpDiacritics[col].r)
	g.Cells[r*g.Cols+c] = cell{
		Ch:        kgpPlaceholderRune,
		clusterID: g.internCluster(text),
		FG:        paletteColor(uint8(id)),
		BG:        defaultColor,
		ULColor:   defaultColor,
		Width:     1,
	}
}

// fillPlaceholderBlock writes a rows×cols block of placeholders whose top-left
// corner is grid cell (r0, c0) and which shows tiles (0,0) upward.
func fillPlaceholderBlock(g *grid, r0, c0, rows, cols int, id uint32) {
	for r := range rows {
		for c := range cols {
			putPlaceholder(g, r0+r, c0+c, id, r, c)
		}
	}
}

// drawPlaceholderTerm builds a Term whose grid holds a virtual placement of
// the given cell size, plus a DrawContext sized to the grid.
func drawPlaceholderTerm(t *testing.T, rows, cols int, v virtualImage) (*Term, *gui.DrawContext) {
	t.Helper()
	tm, dc := newDrawTerm(rows, cols, 10, 20)
	tm.grid.CursorVisible = false
	tm.grid.addVirtualImage(1, v)
	return tm, dc
}

// A complete placeholder block is one image draw covering the whole
// placement rectangle — not one per cell, and with no clip.
func TestDrawPlaceholders_FullBlockIsOneBlit(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 40, HeightPx: 40, Cols: 2, Rows: 2,
	})
	fillPlaceholderBlock(tm.grid, 3, 5, 2, 2, 1)

	tm.onDraw(dc)

	imgs := dc.Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d image draws; want 1: %+v", len(imgs), imgs)
	}
	im := imgs[0]
	if im.Src != "img.png" {
		t.Errorf("Src = %q; want img.png", im.Src)
	}
	// Placement origin is the block's top-left cell: (5*10, 3*20).
	if im.X != 50 || im.Y != 60 {
		t.Errorf("origin = (%v,%v); want (50,60)", im.X, im.Y)
	}
	// 2x2 cells is 20x40 px; a square image fits to 20x20 and stays square.
	if im.W != 20 || im.H != 20 {
		t.Errorf("size = %vx%v; want 20x20", im.W, im.H)
	}
	if im.Clipped {
		t.Error("a fully visible block should not need a clip")
	}
}

// The placement origin is extrapolated from the tiles that are visible, so a
// block whose top rows scrolled off the viewport still lines up: the image is
// drawn from above the viewport and the canvas clips it.
func TestDrawPlaceholders_PartialTopExtrapolatesOrigin(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 80, Cols: 2, Rows: 4,
	})
	// Only tile rows 2 and 3 are on screen, at the top of the viewport.
	for r := range 2 {
		for c := range 2 {
			putPlaceholder(tm.grid, r, c, 1, r+2, c)
		}
	}

	tm.onDraw(dc)

	imgs := dc.Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d image draws; want 1", len(imgs))
	}
	// Tile row 2 sits at screen row 0, so the placement starts two rows above
	// the viewport: y = -40.
	if imgs[0].Y != -40 {
		t.Errorf("origin Y = %v; want -40", imgs[0].Y)
	}
	if imgs[0].Clipped {
		t.Error("truncation at the viewport edge needs no clip; the canvas clips")
	}
}

// When the block is cut off inside the viewport — the app printed only part of
// it, or another pane covers the rest — the draw is clipped to the cells that
// actually hold placeholders, so the image cannot spill over neighbouring
// text.
func TestDrawPlaceholders_InteriorTruncationClips(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 40, HeightPx: 40, Cols: 4, Rows: 4,
	})
	// A 4x4 placement, but only its top-left 2x2 corner is printed, well
	// inside the viewport.
	fillPlaceholderBlock(tm.grid, 2, 2, 2, 2, 1)

	tm.onDraw(dc)

	imgs := dc.Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d image draws; want 1", len(imgs))
	}
	im := imgs[0]
	if !im.Clipped {
		t.Fatal("interior truncation must clip")
	}
	// The clip is the printed cells: columns 2..3, rows 2..3.
	if im.ClipX != 20 || im.ClipY != 40 || im.ClipW != 20 || im.ClipH != 40 {
		t.Errorf("clip = %v,%v %vx%v; want 20,40 20x40",
			im.ClipX, im.ClipY, im.ClipW, im.ClipH)
	}
	// The image itself still maps onto the whole 4x4 placement rectangle.
	if im.X != 20 || im.Y != 40 {
		t.Errorf("origin = (%v,%v); want (20,40)", im.X, im.Y)
	}
}

// Two separate blocks of the same image are two rectangles, not one.
func TestDrawPlaceholders_DisjointBlocks(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2,
	})
	fillPlaceholderBlock(tm.grid, 0, 0, 2, 2, 1)
	fillPlaceholderBlock(tm.grid, 5, 10, 2, 2, 1)

	tm.onDraw(dc)

	if got := len(dc.Images()); got != 2 {
		t.Fatalf("got %d image draws; want 2", got)
	}
}

// Placeholder cells naming an image the terminal never received draw nothing.
func TestDrawPlaceholders_UnknownImageDrawsNothing(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2,
	})
	fillPlaceholderBlock(tm.grid, 0, 0, 2, 2, 99)

	tm.onDraw(dc)

	if got := len(dc.Images()); got != 0 {
		t.Fatalf("got %d image draws for an unknown image; want 0", got)
	}
}

// Tiles past the placement's rows/columns show nothing: the spec says a
// mismatch displays only the part of the image that fits.
func TestDrawPlaceholders_TilesOutsidePlacementIgnored(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2,
	})
	// Print a 2x3 block for a 2x2 placement: the third column is out of range.
	fillPlaceholderBlock(tm.grid, 0, 0, 2, 3, 1)

	tm.onDraw(dc)

	imgs := dc.Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d image draws; want 1", len(imgs))
	}
	// Two columns wide, so the drawn rectangle is the placement's, not three
	// columns of it.
	if imgs[0].W != 20 {
		t.Errorf("width = %v; want 20 (the placement, not the extra column)", imgs[0].W)
	}
}

// The placeholder character itself is never painted — it would render as a
// private-use tofu box under the image.
func TestDrawPlaceholders_GlyphNotPainted(t *testing.T) {
	tm, dc := drawPlaceholderTerm(t, 10, 20, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2,
	})
	fillPlaceholderBlock(tm.grid, 0, 0, 2, 2, 1)

	tm.onDraw(dc)

	for _, txt := range dc.Texts() {
		for _, r := range txt.Text {
			if r == kgpPlaceholderRune {
				t.Fatalf("placeholder rune painted as text: %q", txt.Text)
			}
		}
	}
}

// A screen of deliberately non-coalescing placeholders — every other column,
// all showing the same tile, so neither the horizontal nor the vertical merge
// can fire — is capped rather than turned into one draw call per cell.
func TestDrawPlaceholders_RectCountCapped(t *testing.T) {
	const (
		rows = 120
		cols = 160
	)
	tm, dc := drawPlaceholderTerm(t, rows, cols, virtualImage{
		Src: "img.png", WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2,
	})
	for r := range rows {
		for c := 0; c < cols; c += 2 {
			putPlaceholder(tm.grid, r, c, 1, 0, 0)
		}
	}

	tm.onDraw(dc)

	got := len(dc.Images())
	if got > maxPlaceholderRects {
		t.Fatalf("got %d image draws; want at most %d", got, maxPlaceholderRects)
	}
	if got == 0 {
		t.Fatal("the cap suppressed every draw; rows within the cap must still paint")
	}
}

// Placeholder cells are an image, not text: copying a selection over them
// must not paste private-use characters into the clipboard.
func TestSelectedText_SkipsPlaceholders(t *testing.T) {
	g := newGrid(4, 10)
	fillPlaceholderBlock(g, 0, 0, 1, 4, 1)
	g.Cells[4] = cell{Ch: 'h', FG: defaultColor, BG: defaultColor,
		ULColor: defaultColor, Width: 1}

	g.SelActive = true
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 0, Col: 10}

	got := g.SelectedText()
	for _, r := range got {
		if r == kgpPlaceholderRune {
			t.Fatalf("selection copied a placeholder rune: %q", got)
		}
	}
	if got != "    h" {
		t.Fatalf("SelectedText = %q; want %q", got, "    h")
	}
}

// A selection over placeholders and nothing else copies an empty line rather
// than a run of spaces: the trailing-blank trim counts them as blank.
func TestSelectedText_PlaceholderOnlyRowIsBlank(t *testing.T) {
	g := newGrid(4, 10)
	fillPlaceholderBlock(g, 0, 0, 1, 4, 1)

	g.SelActive = true
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 0, Col: 10}

	if got := g.SelectedText(); got != "" {
		t.Fatalf("SelectedText = %q; want empty", got)
	}
}
