package term

import (
	"testing"
)

// TestConformance exercises VT/xterm behaviors that vttest would probe,
// using the same replay harness as TestEmulatorReplay. These tests verify
// parser+grid correctness for edge cases that are hard to model with
// parser-only unit assertions.
func TestConformance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		rows      int
		cols      int
		input     string
		wantLines []string
		wantR     int
		wantC     int
		wantReply []byte
		assert    func(t *testing.T, g *grid)
	}{
		// --- Auto-wrap (vttest wrap-around test) ---------------------------

		{
			// Writing past the right margin wraps to the next row; when the
			// cursor is already on the bottom row, a full-screen scroll occurs
			// and the oldest row is pushed to scrollback.
			name:  "auto_wrap_scroll",
			rows:  2,
			cols:  4,
			input: "123456789",

			// 1-4 fill row 0, wrap to row 1. 5-8 fill row 1, wrap at
			// bottom triggers scrollUpRegion: row 0 ← row 1 ("5678"),
			// row 1 blanked. '9' lands at (1,0).
			wantLines: []string{"5678", "9   "},
			wantR:     1,
			wantC:     1,
		},
		{
			// DECAWM off (mode 7 clear): characters beyond the right margin
			// overwrite the last column instead of wrapping.
			name:  "auto_wrap_disabled",
			rows:  2,
			cols:  4,
			input: "\x1b[?7lABCDE",

			// A-D fill row 0 cols 0-3. 'E' clamps cursor to col 3 and
			// overwrites 'D'. Row 1 remains untouched.
			wantLines: []string{"ABCE", "    "},
			wantR:     0,
			wantC:     3,
		},

		// --- Origin mode (vttest DECOM tests) ------------------------------

		{
			// With DECOM enabled, CUP home (\x1b[H) targets (Top,0) not (0,0).
			// SetScrollRegion(1,4) then DECOM on then CUP home.
			name:  "origin_mode_cup_home",
			rows:  5,
			cols:  8,
			input: "\x1b[2;5r\x1b[?6h\x1b[H",

			wantLines: []string{
				"        ", "        ", "        ", "        ", "        ",
			},
			wantR: 1, // Top of region, not absolute 0
			wantC: 0,
			assert: func(t *testing.T, g *grid) {
				if !g.OriginMode {
					t.Fatal("expected OriginMode to be enabled")
				}
			},
		},
		{
			// CUP row 99 with DECOM active is clamped to Bottom of region.
			name:  "origin_mode_cup_clamp",
			rows:  5,
			cols:  8,
			input: "\x1b[2;5r\x1b[?6h\x1b[99;1H",

			wantLines: []string{
				"        ", "        ", "        ", "        ", "        ",
			},
			wantR: 4, // Bottom of region
			wantC: 0,
		},
		{
			// DSR always reports absolute cursor position (1-indexed),
			// even when origin mode is active.
			name:  "origin_mode_dsr_absolute",
			rows:  5,
			cols:  8,
			input: "\x1b[2;5r\x1b[?6h\x1b[3;3H\x1b[6n",

			// CUP 3;3 with DECOM → absolute row 2+Top(1)=3, col 2.
			// DSR reports row=CursorR+1=4, col=CursorC+1=3.
			wantLines: []string{
				"        ", "        ", "        ", "        ", "        ",
			},
			wantR:     3,
			wantC:     2,
			wantReply: []byte("\x1b[4;3R"),
		},

		// --- Reverse index at region top (vttest RI test) ------------------

		{
			// RI at Top of scroll region invokes scrollDownRegion; rows
			// outside the region are unchanged.
			name: "reverse_index_at_region_top",
			rows: 5,
			cols: 4,
			// Fill rows 0-4 with A-E, set region to 2-4 (1-indexed),
			// move cursor to Top, then RI.
			input: "A\r\nB\r\nC\r\nD\r\nE\x1b[2;4r\x1b[2;1H\x1bM",

			// scrollDownRegion(1) in region 1-3: rows 1..2 shift down,
			// row 1 blanked. Row 0 ("A") and row 4 ("E") untouched.
			wantLines: []string{
				"A   ", "    ", "B   ", "C   ", "E   ",
			},
			wantR: 1, // cursor stays at Top after RI
			wantC: 0,
		},

		// --- Insert lines within scroll region -----------------------------

		{
			// IL within a scroll region shifts content down and blanks
			// the inserted rows; rows outside the region are untouched.
			name: "insert_lines_in_region",
			rows: 5,
			cols: 4,
			// Fill rows 0-4 with A-E, set region to 2-4, cursor at Top,
			// insert 2 lines.
			input: "A\r\nB\r\nC\r\nD\r\nE\x1b[2;4r\x1b[2;1H\x1b[2L",

			// IL(2) in region 1-3 shifts rows 2-3 content down, blanks
			// rows 1-2. "D" at old row 3 shifts off the region bottom.
			// Rows 0 and 4 outside region unchanged.
			wantLines: []string{
				"A   ", "    ", "    ", "B   ", "E   ",
			},
			wantR: 1, // IL sets CursorC=0, cursor stays at same row
			wantC: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runReplayCase(t, tc.rows, tc.cols, []byte(tc.input),
				tc.wantLines, tc.wantR, tc.wantC,
				"", "", tc.wantReply, tc.assert)
		})
	}
}
