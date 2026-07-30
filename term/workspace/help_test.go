package workspace

import (
	"strconv"
	"testing"

	"github.com/go-gui-org/go-term/term"
)

// mkSection builds a section with n synthetic rows whose labels are unique
// across sections, so round-trip checks can detect a dropped or duplicated row.
func mkSection(title string, n int) helpSection {
	rows := make([]term.ShortcutInfo, n)
	for i := range rows {
		rows[i] = term.ShortcutInfo{Label: title + "-" + strconv.Itoa(i), Keys: "K" + strconv.Itoa(i)}
	}
	return helpSection{title: title, rows: rows}
}

// collect flattens a packed layout back to its row labels in visual order
// (column by column, top to bottom).
func collect(packed [][]helpSection) []string {
	var out []string
	for _, col := range packed {
		for _, s := range col {
			for _, r := range s.rows {
				out = append(out, r.Label)
			}
		}
	}
	return out
}

// Every row must survive packing at any column count — losing a shortcut from
// the cheatsheet is the one failure mode a user would never notice as a bug,
// only as a missing feature.
func TestPackSectionsPreservesAllRows(t *testing.T) {
	secs := []helpSection{mkSection("Workspace", 19), mkSection("Terminal", 18)}
	var want []string
	for _, s := range secs {
		for _, r := range s.rows {
			want = append(want, r.Label)
		}
	}

	for cols := 1; cols <= helpMaxCols; cols++ {
		got := collect(packSections(secs, cols))
		if len(got) != len(want) {
			t.Fatalf("cols=%d: got %d rows, want %d", cols, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("cols=%d: row %d = %q, want %q", cols, i, got[i], want[i])
			}
		}
	}
}

// packSections must always return exactly cols columns so the view builder can
// size the panel from the count it asked for.
func TestPackSectionsColumnCount(t *testing.T) {
	secs := []helpSection{mkSection("A", 3), mkSection("B", 2)}
	for cols := 1; cols <= helpMaxCols; cols++ {
		if got := len(packSections(secs, cols)); got != cols {
			t.Errorf("cols=%d: got %d columns", cols, got)
		}
	}
	// Degenerate input must not panic or return zero columns.
	if got := len(packSections(nil, 0)); got != 1 {
		t.Errorf("packSections(nil, 0): got %d columns, want 1", got)
	}
}

// The common case: two near-equal sections at two columns should land one per
// column, with neither split across the boundary.
func TestPackSectionsTwoEqualSectionsDoNotSplit(t *testing.T) {
	secs := []helpSection{mkSection("Workspace", 19), mkSection("Terminal", 18)}
	packed := packSections(secs, 2)
	for i, col := range packed {
		if len(col) != 1 {
			t.Fatalf("column %d holds %d sections, want 1", i, len(col))
		}
	}
	if packed[0][0].title != "Workspace" || packed[1][0].title != "Terminal" {
		t.Fatalf("wrong section order: %q, %q", packed[0][0].title, packed[1][0].title)
	}
	if n := len(packed[0][0].rows); n != 19 {
		t.Errorf("Workspace column holds %d rows, want 19", n)
	}
	if n := len(packed[1][0].rows); n != 18 {
		t.Errorf("Terminal column holds %d rows, want 18", n)
	}
}

// A section too tall for one column splits, and the continuation repeats the
// title so the second column doesn't open with orphaned rows.
func TestPackSectionsSplitRepeatsTitle(t *testing.T) {
	packed := packSections([]helpSection{mkSection("Solo", 40)}, 2)
	if len(packed[0]) != 1 || len(packed[1]) != 1 {
		t.Fatalf("expected one section piece per column, got %d and %d",
			len(packed[0]), len(packed[1]))
	}
	if packed[0][0].title != "Solo" || packed[1][0].title != "Solo" {
		t.Errorf("continuation lost its title: %q, %q",
			packed[0][0].title, packed[1][0].title)
	}
	if n := len(packed[0][0].rows) + len(packed[1][0].rows); n != 40 {
		t.Errorf("split lost rows: %d, want 40", n)
	}
}

func TestHelpColumns(t *testing.T) {
	secs := []helpSection{mkSection("Workspace", 19), mkSection("Terminal", 18)}
	const rowH = 14 // ≈ (SizeTextTiny+1) * helpLineFactor at default theme sizes

	tests := []struct {
		name     string
		ww, wh   int
		wantCols int
	}{
		// 37 rows + 2 headers ≈ 558px in one column. A full-height display
		// swallows that, so stay at one column — narrower and more scannable.
		{"tall display", 1440, 900, 1},
		// falcon's default window: 600px tall leaves a ~486px budget, so the
		// list widens to two columns rather than overflowing.
		{"default window", 900, 600, 2},
		// Too narrow for a second 330px column: stay at one and let the
		// scroll fallback handle the overflow.
		{"narrow", 600, 700, 1},
		// Short and wide: keeps widening until a column fits the height.
		{"short and wide", 1800, 360, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cols, _ := helpColumns(secs, tc.ww, tc.wh, rowH)
			if cols != tc.wantCols {
				t.Errorf("got %d columns, want %d", cols, tc.wantCols)
			}
			// A single column may exceed the width budget on a very narrow
			// window — there is no narrower option — but two or more never may.
			if cols > 1 && helpPanelWidth(cols) > float32(tc.ww)*helpWidthFrac {
				t.Errorf("panel width %.0f exceeds budget for ww=%d",
					helpPanelWidth(cols), tc.ww)
			}
			if cols < 1 || cols > helpMaxCols {
				t.Errorf("column count %d out of range", cols)
			}
		})
	}
}

// helpColumns must report the height of the layout it chose, since the caller
// uses it to decide whether to enable the scroll fallback.
// An empty section list shouldn't panic and must still satisfy the column-count
// contract so the view builder never receives zero columns.
func TestPackSectionsEmptySections(t *testing.T) {
	for cols := 0; cols <= helpMaxCols; cols++ {
		got := packSections(nil, cols)
		wantCols := cols
		if cols < 1 {
			wantCols = 1
		}
		if len(got) != wantCols {
			t.Errorf("packSections(nil, %d): got %d columns, want %d", cols, len(got), wantCols)
		}
	}
	got := packSections([]helpSection{}, 3)
	if len(got) != 3 {
		t.Errorf("packSections([], 3): got %d columns, want 3", len(got))
	}
}

func TestHelpColumnsReportsChosenHeight(t *testing.T) {
	secs := []helpSection{mkSection("Workspace", 19), mkSection("Terminal", 18)}
	const rowH = 14
	cols, h := helpColumns(secs, 900, 600, rowH)
	if want := packedHeight(packSections(secs, cols), rowH); h != want {
		t.Errorf("reported height %.1f, want %.1f for %d columns", h, want, cols)
	}
}
