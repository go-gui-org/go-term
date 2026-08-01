package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// replyTo feeds input to a fresh grid+parser and returns the concatenated
// reply bytes plus the grid, which is what every case below needs to inspect.
func replyTo(t *testing.T, rows, cols int, input string, prep func(g *grid)) (string, *grid) {
	t.Helper()
	g := newGrid(rows, cols)
	if prep != nil {
		prep(g)
	}
	p := newParser(g)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte(input))
	return string(reply), g
}

// --- Mode 2031 + DSR ?996 -------------------------------------------------

func TestThemeIsDark(t *testing.T) {
	t.Parallel()

	// Every shipped theme is a dark theme; if one ever isn't, the DSR ?996
	// answer for it changes and this is where that shows up.
	for _, th := range []struct {
		name  string
		theme Theme
	}{
		{"Default", DefaultTheme}, {"Gruvbox", GruvboxTheme}, {"Nord", NordTheme},
		{"SolarizedDark", SolarizedDarkTheme}, {"Dracula", DraculaTheme},
		{"CatppuccinMocha", CatppuccinMochaTheme}, {"TokyoNight", TokyoNightTheme},
		{"Monokai", MonokaiTheme}, {"OneDark", OneDarkTheme}, {"RosePine", RosePineTheme},
		{"Kanagawa", KanagawaTheme}, {"AyuDark", AyuDarkTheme},
		{"Everforest", EverforestTheme}, {"GitHubDark", GitHubDarkTheme},
	} {
		if !themeIsDark(th.theme.DefaultBG) {
			t.Errorf("%s: DefaultBG %v reports light", th.name, th.theme.DefaultBG)
		}
	}

	cases := []struct {
		name string
		c    gui.Color
		dark bool
	}{
		{"black", gui.RGB(0, 0, 0), true},
		{"white", gui.RGB(255, 255, 255), false},
		{"solarized_light_base3", gui.RGB(253, 246, 227), false},
		// Green dominates Rec.601, so equal channel values are not the
		// threshold: 128,128,128 is light, but 128 of red alone is dark.
		{"mid_gray", gui.RGB(128, 128, 128), false},
		{"dark_gray", gui.RGB(120, 120, 120), true},
		{"saturated_red", gui.RGB(128, 0, 0), true},
	}
	for _, tc := range cases {
		if got := themeIsDark(tc.c); got != tc.dark {
			t.Errorf("%s: themeIsDark(%v) = %v, want %v", tc.name, tc.c, got, tc.dark)
		}
	}
}

func TestDSR996ReportsColorScheme(t *testing.T) {
	t.Parallel()

	light := DefaultTheme
	light.DefaultBG = gui.RGB(253, 246, 227)

	cases := []struct {
		name  string
		prep  func(g *grid)
		input string
		want  string
	}{
		{"dark_theme", nil, "\x1b[?996n", "\x1b[?997;1n"},
		{"light_theme", func(g *grid) { g.setTheme(light) }, "\x1b[?996n", "\x1b[?997;2n"},
		{
			// OSC 11 writes straight into Theme.DefaultBG, so a child that
			// repainted the background is answered about the background it set.
			name:  "osc11_override_flips_answer",
			input: "\x1b]11;#fdf6e3\x1b\\\x1b[?996n",
			want:  "\x1b[?997;2n",
		},
		{
			// DECSCNM is a render toggle, not a scheme change — reporting a flip
			// for it would re-theme every subscribed app twice per `tput flash`.
			name:  "decscnm_does_not_flip",
			input: "\x1b[?5h\x1b[?996n",
			want:  "\x1b[?997;1n",
		},
		{"decxcpr_still_works", nil, "\x1b[?6n", "\x1b[?1;1R"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := replyTo(t, 4, 8, tc.input, tc.prep)
			if got != tc.want {
				t.Fatalf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMode2031(t *testing.T) {
	t.Parallel()

	// DECRQM before, after set, and after reset — 0 would mean "unrecognized",
	// which is what a client falls back on.
	got, g := replyTo(t, 4, 8,
		"\x1b[?2031$p\x1b[?2031h\x1b[?2031$p\x1b[?2031l\x1b[?2031$p", nil)
	want := "\x1b[?2031;2$y\x1b[?2031;1$y\x1b[?2031;2$y"
	if got != want {
		t.Fatalf("DECRQM replies = %q, want %q", got, want)
	}
	if g.ColorSchemeUpdates {
		t.Fatal("ColorSchemeUpdates still set after ?2031l")
	}

	// Setting the mode must not itself emit a report: the client primes its
	// state with DSR ?996, and an unsolicited byte here would land in the shell.
	if got, _ := replyTo(t, 4, 8, "\x1b[?2031h", nil); got != "" {
		t.Fatalf("?2031h emitted %q, want nothing", got)
	}

	// RIS drops the subscription along with every other host-set mode.
	_, g = replyTo(t, 4, 8, "\x1b[?2031h\x1bc", nil)
	if g.ColorSchemeUpdates {
		t.Fatal("RIS left ColorSchemeUpdates set")
	}
}

// --- OSC 22 ----------------------------------------------------------------

func TestOSC22PointerShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  pointerShape
	}{
		{"\x1b]22;text\x07", pointerIBeam},
		{"\x1b]22;xterm\x07", pointerIBeam},
		{"\x1b]22;pointer\x07", pointerPointingHand},
		{"\x1b]22;crosshair\x07", pointerCrosshair},
		{"\x1b]22;default\x07", pointerArrow},
		{"\x1b]22;fleur\x07", pointerAll},
		{"\x1b]22;ns-resize\x07", pointerNS},
		{"\x1b]22;sb_h_double_arrow\x07", pointerEW},
		{"\x1b]22;nesw-resize\x07", pointerNESW},
		{"\x1b]22;se-resize\x07", pointerNWSE},
		{"\x1b]22;not-allowed\x07", pointerNotAllowed},
		{"\x1b]22;X_cursor\x07", pointerNotAllowed}, // case-insensitive
		// Empty payload is the documented reset.
		{"\x1b]22;crosshair\x07\x1b]22;\x07", pointerDefault},
	}
	for _, tc := range cases {
		_, g := replyTo(t, 4, 8, tc.input, nil)
		if g.PointerShape != tc.want {
			t.Errorf("%q: shape = %d, want %d", tc.input, g.PointerShape, tc.want)
		}
	}

	// An unknown name leaves the previous, honored request alone rather than
	// silently revoking it.
	_, g := replyTo(t, 4, 8, "\x1b]22;crosshair\x07\x1b]22;gumby\x07", nil)
	if g.PointerShape != pointerCrosshair {
		t.Fatalf("unknown name clobbered shape: got %d", g.PointerShape)
	}

	// RIS is the only way out of a shape left by a killed application.
	_, g = replyTo(t, 4, 8, "\x1b]22;crosshair\x07\x1bc", nil)
	if g.PointerShape != pointerDefault {
		t.Fatalf("RIS left shape = %d", g.PointerShape)
	}
}

// --- OSC 9;4 ---------------------------------------------------------------

func TestOSC94Progress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantState uint8
		wantVal   uint8
	}{
		{"normal", "\x1b]9;4;1;50\x07", 1, 50},
		{"error_with_value", "\x1b]9;4;2;80\x07", 2, 80},
		{"error_without_value", "\x1b]9;4;2\x07", 2, 0},
		{"indeterminate_ignores_value", "\x1b]9;4;3;40\x07", 3, 0},
		{"paused", "\x1b]9;4;4;10\x07", 4, 10},
		{"remove_clears_value", "\x1b]9;4;1;50\x07\x1b]9;4;0\x07", 0, 0},
		{"over_100_clamps", "\x1b]9;4;1;250\x07", 1, 100},
		{"garbage_state_clears", "\x1b]9;4;9;50\x07", 0, 0},
		{"garbage_value_is_zero", "\x1b]9;4;1;abc\x07", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, g := replyTo(t, 4, 8, tc.input, nil)
			if g.ProgressState != tc.wantState || g.ProgressValue != tc.wantVal {
				t.Fatalf("state=%d value=%d, want state=%d value=%d",
					g.ProgressState, g.ProgressValue, tc.wantState, tc.wantVal)
			}
		})
	}

	// Progress dirties no row, so OverlayVersion is the only thing that gets
	// the frame repainted — and a repeated identical report must not bump it,
	// or a chatty job invalidates the tessellation cache every read.
	g := newGrid(4, 8)
	p := newParser(g)
	feed(t, g, p, []byte("\x1b]9;4;1;50\x07"))
	if g.OverlayVersion == 0 {
		t.Fatal("OverlayVersion not bumped by a progress change")
	}
	if g.HasDirtyRows() {
		t.Fatal("progress marked rows dirty; OverlayVersion exists to avoid that")
	}
	ver := g.OverlayVersion
	feed(t, g, p, []byte("\x1b]9;4;1;50\x07"))
	if g.OverlayVersion != ver {
		t.Fatal("identical progress report bumped OverlayVersion")
	}

	// RIS clears a bar left behind by a killed job.
	_, g = replyTo(t, 4, 8, "\x1b]9;4;1;50\x07\x1bc", nil)
	if g.ProgressState != 0 || g.ProgressValue != 0 {
		t.Fatalf("RIS left progress state=%d value=%d", g.ProgressState, g.ProgressValue)
	}
}

// TestOSC9NotificationSplit is the regression this phase fixes: OSC 9;4 used to
// fall through to the notification handler, so `cargo build` popped a desktop
// notification whose body read "4;1;50".
func TestOSC9NotificationSplit(t *testing.T) {
	t.Parallel()

	var bodies []string
	g := newGrid(4, 8)
	p := newParser(g)
	p.SetNotifyHandler(func(_, body string) { bodies = append(bodies, body) })
	feed(t, g, p, []byte("\x1b]9;4;1;50\x07\x1b]9;build done\x07"))

	if len(bodies) != 1 || bodies[0] != "build done" {
		t.Fatalf("notifications = %q, want exactly [\"build done\"]", bodies)
	}
}

// --- XTSMGRAPHICS ----------------------------------------------------------

func TestXTSMGRAPHICS(t *testing.T) {
	t.Parallel()

	// CellPxW/H are 0 until the widget's first frame measures them, so set them
	// to make the "current geometry" answer deterministic.
	measured := func(g *grid) { g.CellPxW, g.CellPxH = 8, 16 }

	cases := []struct {
		name  string
		prep  func(g *grid)
		input string
		want  string
	}{
		{"color_registers_current", nil, "\x1b[?1;1S", "\x1b[?1;0;256S"},
		{"color_registers_max", nil, "\x1b[?1;4S", "\x1b[?1;0;256S"},
		{"color_registers_reset", nil, "\x1b[?1;2S", "\x1b[?1;0;256S"},
		{
			// 80 cols × 8px, 24 rows × 16px — the text area, not the decoder
			// cap: an image larger than the pane cannot be shown in full.
			name: "geometry_current_is_text_area", prep: measured,
			input: "\x1b[?2;1S", want: "\x1b[?2;0;640;384S",
		},
		{"geometry_max_is_decoder_cap", measured, "\x1b[?2;4S", "\x1b[?2;0;4096;4096S"},
		{"geometry_unmeasured_reports_zero", nil, "\x1b[?2;1S", "\x1b[?2;0;0;0S"},
		// Setting cannot be honored — both limits are compile-time constants.
		{"set_reports_failure", nil, "\x1b[?1;3;16S", "\x1b[?1;3S"},
		{"regis_unsupported", nil, "\x1b[?3;1S", "\x1b[?3;1S"},
		{"unknown_item", nil, "\x1b[?9;1S", "\x1b[?9;1S"},
		{"unknown_action", nil, "\x1b[?1;9S", "\x1b[?1;2S"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := replyTo(t, 24, 80, tc.input, tc.prep)
			if got != tc.want {
				t.Fatalf("reply = %q, want %q", got, tc.want)
			}
		})
	}

	// Non-private CSI S is still SU (scroll up) — the private leader is what
	// distinguishes them, and conflating the two would scroll on every query.
	_, g := replyTo(t, 3, 4, "a\r\nb\r\nc\x1b[1S", nil)
	if got := gridLines(g)[0]; got != "b   " {
		t.Fatalf("CSI 1 S did not scroll: first line = %q", got)
	}
}

// TestPhase49SequencesLeaveNoGlyphs is the cheap check the testdata fixture
// cannot make: fixtures have no field for reply bytes, so the query forms are
// excluded from protocol_odds.json. A mis-lexed sequence — CSI ? 1 ; 1 S read
// as text, say — shows up as junk between "before" and "after".
func TestPhase49SequencesLeaveNoGlyphs(t *testing.T) {
	t.Parallel()

	_, g := replyTo(t, 2, 20, "before"+
		"\x1b[?2031h\x1b[?996n\x1b[?2031$p"+
		"\x1b]22;crosshair\x07\x1b]9;4;1;50\x07"+
		"\x1b[?1;1S\x1b[?2;4S\x1b[?3;1S\x1b[?1;3;16S"+
		"\r\nafter", nil)

	want := []string{"before              ", "after               "}
	for i, line := range gridLines(g) {
		if line != want[i] {
			t.Fatalf("row %d = %q, want %q", i, line, want[i])
		}
	}
}
