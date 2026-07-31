package term

import "testing"

// vimg is a well-formed virtual placement; tests vary one field at a time.
func vimg(src string) virtualImage {
	return virtualImage{Src: src, WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2}
}

// A placement that could never be drawn is not stored: it would only cost the
// render scan a lookup every frame.
func TestAddVirtualImage_RejectsDegenerate(t *testing.T) {
	cases := []struct {
		name string
		id   uint32
		v    virtualImage
	}{
		{"zero id", 0, vimg("a.png")},
		{"no source", 1, virtualImage{WidthPx: 20, HeightPx: 20, Cols: 2, Rows: 2}},
		{"zero width px", 1, virtualImage{Src: "a.png", HeightPx: 20, Cols: 2, Rows: 2}},
		{"zero height px", 1, virtualImage{Src: "a.png", WidthPx: 20, Cols: 2, Rows: 2}},
		{"zero cols", 1, virtualImage{Src: "a.png", WidthPx: 20, HeightPx: 20, Rows: 2}},
		{"zero rows", 1, virtualImage{Src: "a.png", WidthPx: 20, HeightPx: 20, Cols: 2}},
		{"negative cols", 1, virtualImage{Src: "a.png", WidthPx: 20, HeightPx: 20,
			Cols: -4, Rows: 2}},
	}
	for _, tc := range cases {
		g := newGrid(10, 20)
		g.addVirtualImage(tc.id, tc.v)
		if len(g.virtualImages) != 0 {
			t.Errorf("%s: stored %d placements; want 0", tc.name, len(g.virtualImages))
		}
	}
}

// c=/r= are client-supplied, so an absurd footprint is clamped rather than
// stored — the tile bounds check in the render scan reads it every cell.
func TestAddVirtualImage_ClampsFootprint(t *testing.T) {
	g := newGrid(10, 20)
	v := vimg("a.png")
	v.Cols, v.Rows = MaxGridDim*100, MaxGridDim*100
	g.addVirtualImage(1, v)

	got := g.virtualImages[1]
	if got.Cols != MaxGridDim || got.Rows != MaxGridDim {
		t.Fatalf("footprint = %dx%d; want %dx%d",
			got.Cols, got.Rows, MaxGridDim, MaxGridDim)
	}
}

// One entry per image id, last write wins — a re-transmitted image replaces
// its own placement instead of accumulating.
func TestAddVirtualImage_ReplacesSameID(t *testing.T) {
	g := newGrid(10, 20)
	g.addVirtualImage(1, vimg("first.png"))
	g.addVirtualImage(1, vimg("second.png"))

	if len(g.virtualImages) != 1 {
		t.Fatalf("stored %d placements; want 1", len(g.virtualImages))
	}
	if got := g.virtualImages[1].Src; got != "second.png" {
		t.Fatalf("Src = %q; want second.png", got)
	}
}

// The store is bounded: a stream of a=p,U=1 with distinct i= values evicts
// oldest-first rather than growing without limit.
func TestAddVirtualImage_EvictsOldestAtCapacity(t *testing.T) {
	g := newGrid(10, 20)
	for i := 1; i <= maxVirtualImages+5; i++ {
		g.addVirtualImage(uint32(i), vimg("img.png"))
	}
	if len(g.virtualImages) > maxVirtualImages {
		t.Fatalf("stored %d placements; want at most %d",
			len(g.virtualImages), maxVirtualImages)
	}
	// The first five ids were the oldest, so they are the ones that went.
	for i := 1; i <= 5; i++ {
		if _, ok := g.virtualImages[uint32(i)]; ok {
			t.Errorf("id %d survived eviction; oldest should go first", i)
		}
	}
	if _, ok := g.virtualImages[uint32(maxVirtualImages+5)]; !ok {
		t.Error("the newest placement was evicted")
	}
}

func TestDeleteVirtualImage(t *testing.T) {
	g := newGrid(10, 20)
	g.addVirtualImage(1, vimg("a.png"))
	g.addVirtualImage(2, vimg("b.png"))

	g.deleteVirtualImage(1)
	if _, ok := g.virtualImages[1]; ok {
		t.Error("id 1 survived deleteVirtualImage")
	}
	if _, ok := g.virtualImages[2]; !ok {
		t.Error("deleteVirtualImage removed an unrelated placement")
	}
	// Deleting an id that was never stored is a no-op, not a panic.
	g.deleteVirtualImage(99)
}

// The file backing a virtual placement can be removed by store eviction; when
// it is, every placement drawing it goes too so the renderer never reaches for
// a dead path.
func TestDeleteGraphicsBySrc_DropsVirtual(t *testing.T) {
	g := newGrid(10, 20)
	g.addVirtualImage(1, vimg("gone.png"))
	g.addVirtualImage(2, vimg("kept.png"))

	g.deleteGraphicsBySrc("gone.png")

	if _, ok := g.virtualImages[1]; ok {
		t.Error("placement drawing the removed file survived")
	}
	if _, ok := g.virtualImages[2]; !ok {
		t.Error("placement drawing an unrelated file was dropped")
	}
}

// Store eviction asks whether anything still draws a file. A virtual
// placement has no rectangle but is a display all the same.
func TestGraphicsUseSrc_CountsVirtual(t *testing.T) {
	g := newGrid(10, 20)
	g.addVirtualImage(1, vimg("live.png"))

	if !g.graphicsUseSrc("live.png") {
		t.Error("graphicsUseSrc missed a virtual placement")
	}
	if g.graphicsUseSrc("other.png") {
		t.Error("graphicsUseSrc claimed an unreferenced file is in use")
	}
}

// RIS wipes the screen, so every placeholder cell that could name a virtual
// placement is gone as well.
func TestHardReset_ClearsVirtualImages(t *testing.T) {
	g := newGrid(10, 20)
	g.addVirtualImage(1, vimg("a.png"))

	g.HardReset()

	if len(g.virtualImages) != 0 {
		t.Fatalf("HardReset left %d virtual placements", len(g.virtualImages))
	}
}

func TestKGPCellRect(t *testing.T) {
	// Cells are 8x16; the image is 32x32 px.
	const (
		cellW float32 = 8
		cellH float32 = 16
	)
	cases := []struct {
		name               string
		cols, rows         int
		wPx, hPx           int
		cellW, cellH       float32
		wantCols, wantRows int
	}{
		{"neither given defers to the caller", 0, 0, 32, 32, cellW, cellH, 0, 0},
		{"both given are used verbatim", 6, 3, 32, 32, cellW, cellH, 6, 3},
		// 4 columns is 32 screen px wide; a square image is then 32 px tall,
		// which is 2 rows.
		{"rows from aspect ratio", 4, 0, 32, 32, cellW, cellH, 4, 2},
		// 2 rows is 32 screen px tall, so a square image is 32 px wide: 4 cols.
		{"cols from aspect ratio", 0, 2, 32, 32, cellW, cellH, 4, 2},
		// Rounding never collapses the derived axis to zero.
		{"tiny derived axis clamps to one", 1, 0, 1000, 1, cellW, cellH, 1, 1},
		{"unmeasured cells fall back to one", 4, 0, 32, 32, 0, 0, 4, 1},
		{"zero pixel size falls back to one", 0, 3, 0, 0, cellW, cellH, 1, 3},
		{"negative treated as unspecified", -5, -5, 32, 32, cellW, cellH, 0, 0},
	}
	for _, tc := range cases {
		cols, rows := kgpCellRect(tc.cols, tc.rows, tc.wPx, tc.hPx, tc.cellW, tc.cellH)
		if cols != tc.wantCols || rows != tc.wantRows {
			t.Errorf("%s: kgpCellRect = %dx%d; want %dx%d",
				tc.name, cols, rows, tc.wantCols, tc.wantRows)
		}
	}
}
