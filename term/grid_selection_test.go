package term

import "testing"

func TestGrid_SelectedText_RowRange(t *testing.T) {
	g := newGrid(3, 5)
	for c, r := range "hello" {
		g.At(0, c).Ch = r
	}
	for c, r := range "world" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 1, Col: 4}
	g.SelActive = true
	if got := g.SelectedText(); got != "hello\nworld" {
		t.Errorf("got %q, want %q", got, "hello\nworld")
	}
}

func TestGrid_SelectedText_TrailingBlankTrim(t *testing.T) {
	g := newGrid(2, 8)
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	for c, r := range "de" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 1, Col: 7}
	g.SelActive = true

	if got := g.SelectedText(); got != "abc\nde" {
		t.Errorf("got %q, want %q", got, "abc\nde")
	}
}

func TestGrid_SelectedText_ColumnRangeWithinRow(t *testing.T) {
	g := newGrid(1, 10)
	for c, r := range "abcdefghij" {
		g.At(0, c).Ch = r
	}
	g.SelAnchor = contentPos{Row: 0, Col: 3}
	g.SelHead = contentPos{Row: 0, Col: 6}
	g.SelActive = true
	if got := g.SelectedText(); got != "defg" {
		t.Errorf("got %q, want %q", got, "defg")
	}
}

func TestGrid_SelectedText_BackwardDragNormalized(t *testing.T) {
	g := newGrid(2, 4)
	for c, r := range "ab" {
		g.At(0, c).Ch = r
	}
	for c, r := range "cd" {
		g.At(1, c).Ch = r
	}

	g.SelAnchor = contentPos{Row: 1, Col: 1}
	g.SelHead = contentPos{Row: 0, Col: 0}
	g.SelActive = true
	if got := g.SelectedText(); got != "ab\ncd" {
		t.Errorf("got %q, want %q", got, "ab\ncd")
	}
}

func TestGrid_SelectedText_InactiveOrEmpty(t *testing.T) {
	g := newGrid(1, 3)
	if got := g.SelectedText(); got != "" {
		t.Errorf("inactive selection returned %q", got)
	}
	g.SelAnchor = contentPos{Row: 0, Col: 1}
	g.SelHead = contentPos{Row: 0, Col: 1}
	g.SelActive = true
	if got := g.SelectedText(); got != "" {
		t.Errorf("zero-width selection returned %q", got)
	}
}

func TestGrid_SelectedText_AcrossScrollbackBoundary(t *testing.T) {
	g := newGrid(2, 3)
	g.ScrollbackCap = 5

	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	g.scrollUpRegion(1)

	for c, r := range "xyz" {
		g.At(0, c).Ch = r
	}

	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 1, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("ViewOffset=0: got %q, want %q", got, "abc\nxyz")
	}

	g.ViewOffset = 1
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("ViewOffset=1: got %q, want %q", got, "abc\nxyz")
	}
}

func TestGrid_SelectedText_ClampsOutOfRangeCoords(t *testing.T) {

	g := newGrid(2, 3)
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	for c, r := range "xyz" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = contentPos{Row: -10, Col: -10}
	g.SelHead = contentPos{Row: 99, Col: 99}
	g.SelActive = true
	got := g.SelectedText()
	if got != "abc\nxyz" {
		t.Errorf("got %q, want %q", got, "abc\nxyz")
	}
}

func TestGrid_SelectedText_RowWithEmptySpan(t *testing.T) {

	g := newGrid(1, 3)
	g.At(0, 0).Ch = 'a'
	g.At(0, 1).Ch = 'b'
	g.At(0, 2).Ch = 'c'
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 0, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc" {
		t.Errorf("baseline: got %q want %q", got, "abc")
	}
}

func TestGrid_SelectedText_ContentCoords_IndependentOfViewOffset(t *testing.T) {

	g := newGrid(2, 3)
	g.ScrollbackCap = 5
	for c, ch := range "abc" {
		g.At(0, c).Ch = ch
	}
	g.scrollUpRegion(1)
	for c, ch := range "xyz" {
		g.At(0, c).Ch = ch
	}
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 1, Col: 2}
	g.SelActive = true

	for _, off := range []int{0, 1} {
		g.ViewOffset = off
		if got := g.SelectedText(); got != "abc\nxyz" {
			t.Errorf("ViewOffset=%d: got %q, want %q", off, got, "abc\nxyz")
		}
	}
}

func TestSelectedTextWide(t *testing.T) {
	g := newGrid(1, 10)
	g.CursorR, g.CursorC = 0, 0
	g.Put('🍣') // (0,0) and (0,1)

	g.SelActive = true
	g.SelAnchor = contentPos{0, 0}
	g.SelHead = contentPos{0, 1}

	text := g.SelectedText()
	if text != "🍣" {
		t.Errorf("expected SelectedText to be '🍣', but got %q (hex: %x)", text, text)
	}
}

func TestSelectedText_ContinuationCellOnly(t *testing.T) {
	g := newGrid(1, 10)
	g.CursorR, g.CursorC = 0, 0
	g.Put('🍣') // (0,0) and (0,1)

	// Select only the continuation cell
	g.SelActive = true
	g.SelAnchor = contentPos{0, 1}
	g.SelHead = contentPos{0, 1}

	text := g.SelectedText()
	if text != "" {
		t.Errorf("expected empty text for continuation-only selection, got %q", text)
	}
}
