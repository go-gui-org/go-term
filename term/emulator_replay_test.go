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
			wantReply:   da1Reply,
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
