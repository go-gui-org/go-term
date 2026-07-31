package term

import (
	"bytes"
	"slices"
	"testing"
)

func TestEmulatorReplay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		rows        int
		cols        int
		input       string
		wantLines   []string
		wantCursorR int
		wantCursorC int
		wantTitle   string
		wantCwd     string
		wantReply   []byte
		assert      func(t *testing.T, g *grid)
	}{
		{
			name:        "cursor_move_and_erase_line",
			rows:        3,
			cols:        5,
			input:       "hello\x1b[2;3HX\x1b[K",
			wantLines:   []string{"hello", "  X  ", "     "},
			wantCursorR: 1,
			wantCursorC: 3,
		},
		{
			name:        "full_screen_scroll",
			rows:        3,
			cols:        4,
			input:       "1\r\n2\r\n3\r\n4",
			wantLines:   []string{"2   ", "3   ", "4   "},
			wantCursorR: 2,
			wantCursorC: 1,
		},
		{
			name:        "alt_screen_round_trip_restores_main",
			rows:        2,
			cols:        6,
			input:       "main\x1b7\x1b[?1049hALT\x1b[?1049l",
			wantLines:   []string{"main  ", "      "},
			wantCursorR: 0,
			wantCursorC: 4,
			assert: func(t *testing.T, g *grid) {
				t.Helper()
				if g.AltActive {
					t.Fatal("alt screen should be inactive after ?1049l")
				}
			},
		},
		{
			name:        "osc_and_da1_reply",
			rows:        1,
			cols:        8,
			input:       "\x1b]2;Build Output\x07\x1b]7;file://host/tmp\x07\x1b[c",
			wantLines:   []string{"        "},
			wantCursorR: 0,
			wantCursorC: 0,
			wantTitle:   "Build Output",
			wantCwd:     "file://host/tmp",
			wantReply:   []byte(da1Reply),
		},
		// The next four cases are vttest "Test of cursor movements" screen 5,
		// "cursor-control characters inside ESC sequences": a C0 control
		// arriving mid-sequence executes immediately and the sequence resumes
		// around it. vttest writes the same line three ways and expects them
		// to come out identical.
		{
			name:        "c0_backspace_inside_csi",
			rows:        1,
			cols:        8,
			input:       "A\x1b[2\x08CB\x1b[2\x08CC",
			wantLines:   []string{"A B C   "},
			wantCursorR: 0,
			wantCursorC: 5,
		},
		{
			name:        "c0_carriage_return_inside_csi",
			rows:        1,
			cols:        8,
			input:       "AB\x1b[\r4CC",
			wantLines:   []string{"AB  C   "},
			wantCursorR: 0,
			wantCursorC: 5,
		},
		{
			// VT is a line feed, so VT + CUU nets to no vertical movement.
			name:        "c0_vertical_tab_inside_csi",
			rows:        3,
			cols:        4,
			input:       "A\x1b[1\x0bAB",
			wantLines:   []string{"AB  ", "    ", "    "},
			wantCursorR: 0,
			wantCursorC: 2,
		},
		{
			// CAN is one of the two controls that cancel rather than execute;
			// the parameters collected so far are discarded with it.
			name:        "can_cancels_csi",
			rows:        1,
			cols:        5,
			input:       "\x1b[3\x18C X",
			wantLines:   []string{"C X  "},
			wantCursorR: 0,
			wantCursorC: 3,
		},
		{
			// The deferred-wrap state is encoded as CursorC == Cols. A
			// backspace out of it must land one column left of the right
			// margin, not on it — vttest's autowrap screens are built on
			// exactly this and drift by a column when it is wrong.
			name:        "backspace_from_pending_wrap",
			rows:        1,
			cols:        3,
			input:       "abc\x08X",
			wantLines:   []string{"aXc"},
			wantCursorR: 0,
			wantCursorC: 2,
		},
		{
			name:        "decaln_fills_screen_with_e",
			rows:        2,
			cols:        3,
			input:       "hi\x1b#8",
			wantLines:   []string{"EEE", "EEE"},
			wantCursorR: 0,
			wantCursorC: 0,
		},
		{
			// DECCOLM without ?40 is inert: an application that never asked
			// for column switching must not be able to narrow the pane.
			name:        "deccolm_ignored_without_mode_40",
			rows:        1,
			cols:        6,
			input:       "hello\x1b[?3l",
			wantLines:   []string{"hello "},
			wantCursorR: 0,
			wantCursorC: 5,
			assert: func(t *testing.T, g *grid) {
				t.Helper()
				if g.ColumnMode != 0 || g.Cols != 6 {
					t.Fatalf("width pinned without ?40: mode=%d cols=%d",
						g.ColumnMode, g.Cols)
				}
			},
		},
		{
			name:        "private_modes_toggle_state",
			rows:        1,
			cols:        4,
			input:       "\x1b[?2004h\x1b[?1004h\x1b[?1000;1006h\x1b[?2004l\x1b[?1004l\x1b[?1000;1006l",
			wantLines:   []string{"    "},
			wantCursorR: 0,
			wantCursorC: 0,
			assert: func(t *testing.T, g *grid) {
				t.Helper()
				if g.BracketedPaste || g.FocusReporting || g.MouseTrack || g.MouseSGR {
					t.Fatalf("modes should be cleared: paste=%v focus=%v mouse=%v sgr=%v",
						g.BracketedPaste, g.FocusReporting, g.MouseTrack, g.MouseSGR)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runReplayCase(t, tc.rows, tc.cols, []byte(tc.input),
				tc.wantLines, tc.wantCursorR, tc.wantCursorC,
				tc.wantTitle, tc.wantCwd, tc.wantReply, tc.assert)
		})
	}
}

// TestEmulatorReplayFixtures replays fixture files from testdata/.
// Each .json file is a serialised fixture (see fixture_test.go).
func TestEmulatorReplayFixtures(t *testing.T) {
	t.Parallel()

	for _, f := range loadFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			input := decodeFixtureInput(t, f)
			runReplayCase(t, f.Rows, f.Cols, input,
				f.WantLines, f.WantRow, f.WantCol,
				f.WantTitle, f.WantCwd, nil, nil)
		})
	}
}

// runReplayCase runs a single replay scenario against a fresh grid and
// parser, asserting the expected outputs.
func runReplayCase(t *testing.T, rows, cols int, input []byte,
	wantLines []string, wantR, wantC int,
	wantTitle, wantCwd string, wantReply []byte,
	assertFn func(t *testing.T, g *grid),
) {
	t.Helper()

	g := newGrid(rows, cols)
	p := newParser(g)

	var gotTitle string
	var gotReply []byte
	p.SetTitleHandler(func(s string) { gotTitle = s })
	p.SetReplyHandler(func(b []byte) { gotReply = append(gotReply, b...) })

	feed(t, g, p, input)

	if got := gridLines(g); !slices.Equal(got, wantLines) {
		t.Fatalf("grid = %#v, want %#v", got, wantLines)
	}
	if g.CursorR != wantR || g.CursorC != wantC {
		t.Fatalf("cursor = (%d,%d), want (%d,%d)",
			g.CursorR, g.CursorC, wantR, wantC)
	}
	if gotTitle != wantTitle {
		t.Fatalf("title = %q, want %q", gotTitle, wantTitle)
	}
	if g.Cwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", g.Cwd, wantCwd)
	}
	if !bytes.Equal(gotReply, wantReply) {
		t.Fatalf("reply = %q, want %q", gotReply, wantReply)
	}
	if assertFn != nil {
		assertFn(t, g)
	}
}
