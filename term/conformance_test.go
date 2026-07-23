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

		// --- REP (ncurses `rep` capability) --------------------------------

		{
			// REP repeats the preceding graphic character. ncurses emits it
			// for runs, so the repeated cells must land exactly as if the
			// character had been sent again — including the wrap.
			name:  "rep_repeats_and_wraps",
			rows:  2,
			cols:  4,
			input: "-\x1b[5b",

			// 1 written + 5 repeats = 6 cells: row 0 fills, row 1 takes two.
			wantLines: []string{"----", "--  "},
			wantR:     1,
			wantC:     2,
		},
		{
			// REP repeats only the graphic character — an intervening cursor
			// move does not change what is repeated, and SGR set after the
			// character applies to the repeats (they are fresh writes).
			name:  "rep_after_cursor_move",
			rows:  2,
			cols:  6,
			input: "X\x1b[2;3H\x1b[2b",

			wantLines: []string{"X     ", "  XX  "},
			wantR:     1,
			wantC:     4,
		},

		// --- Reset (terminfo rs1 / rs2, is2) -------------------------------

		{
			// RIS wipes the screen, homes the cursor and drops SGR state.
			// `reset` leads with this to recover a wedged terminal.
			name:  "ris_full_reset",
			rows:  2,
			cols:  4,
			input: "\x1b[7mab\r\ncd\x1bc",

			wantLines: []string{"    ", "    "},
			wantR:     0,
			wantC:     0,
			assert: func(t *testing.T, g *grid) {
				if g.CurAttrs != 0 {
					t.Errorf("attrs = %d after RIS, want 0", g.CurAttrs)
				}
			},
		},
		{
			// DECSTR resets modes and SGR but leaves the screen alone — an
			// app running `tput init` mid-session must not lose its output.
			name: "decstr_soft_reset_keeps_screen",
			rows: 2,
			cols: 4,
			// The scroll region is set first: DECSTBM homes the cursor, so
			// setting it after the text would hide whether DECSTR moved it.
			input: "\x1b[1;2r\x1b[4hab\x1b[?25l\x1b[!p",

			wantLines: []string{"ab  ", "    "},
			wantR:     0,
			wantC:     2,
			assert: func(t *testing.T, g *grid) {
				if g.InsertMode || !g.CursorVisible {
					t.Errorf("modes survived DECSTR: insert=%v visible=%v",
						g.InsertMode, g.CursorVisible)
				}
				if g.Top != 0 || g.Bottom != g.Rows-1 {
					t.Errorf("scroll region = %d..%d, want full screen",
						g.Top, g.Bottom)
				}
			},
		},

		// --- DECSCA + rectangular areas (vttest menu 8) --------------------

		{
			// A protected frame survives DECSED/DECSEL; the unprotected text
			// inside it does not. This is the shape DEC forms-mode apps use:
			// paint the labels protected, clear the fields selectively.
			name: "decsca_protected_frame_survives_selective_erase",
			rows: 3,
			cols: 8,
			// Row 1: protected "NAME:" then unprotected "bob".
			// Row 2: unprotected filler. Then DECSED 2 from home.
			input: "\x1b[1\"qNAME:\x1b[0\"qbob\r\n" +
				"filler\x1b[H\x1b[?2J",

			wantLines: []string{"NAME:   ", "        ", "        "},
			wantR:     0,
			wantC:     0,
		},
		{
			// Plain ED after the same setup takes the protected cells too.
			name: "ed_ignores_protection",
			rows: 2,
			cols: 8,
			input: "\x1b[1\"qNAME:\x1b[0\"qbob\r\n" +
				"\x1b[H\x1b[2J",

			wantLines: []string{"        ", "        "},
		},
		{
			// DECFRA paints a box of '#', DECERA punches a hole in it, and
			// DECCRA duplicates the remaining top row lower down.
			name:  "rect_fill_erase_copy",
			rows:  4,
			cols:  6,
			input: "\x1b[35;1;1;2;6$x\x1b[1;3;1;4$z\x1b[1;1;1;6;1;4;1;1$v",

			wantLines: []string{
				"##  ##",
				"######",
				"      ",
				"##  ##",
			},
		},
		{
			// DECSERA clears only the unprotected half of a filled row, and
			// DECCARA (rectangle extent) reverses video over a sub-span
			// without touching the characters.
			name:  "decsera_and_deccara",
			rows:  1,
			cols:  6,
			input: "\x1b[1\"qAB\x1b[0\"qCD\x1b[2*x\x1b[1;1;1;4${\x1b[1;1;1;2;7$r",

			wantLines: []string{"AB    "},
			wantC:     4, // the rect ops leave the cursor where ABCD ended
			assert: func(t *testing.T, g *grid) {
				if g.At(0, 0).Attrs&attrInverse == 0 || g.At(0, 1).Attrs&attrInverse == 0 {
					t.Error("DECCARA did not set inverse over the protected span")
				}
				if g.At(0, 2).Attrs&attrInverse != 0 {
					t.Error("DECCARA reached past the rectangle")
				}
				if g.At(0, 0).Ch != 'A' {
					t.Errorf("DECCARA changed the character: %q", g.At(0, 0).Ch)
				}
			},
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
