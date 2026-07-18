package term

import (
	"bytes"
	"testing"
)

func TestParser_DA1_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[c"))
	if len(replies) != 1 || !bytes.Equal(replies[0], []byte("\x1b[?1;2;4c")) {
		t.Errorf("DA1 reply: %q", replies)
	}
}

func TestParser_DA1_ExplicitZero(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[0c"))
	if got != 1 {
		t.Errorf("CSI 0 c reply count=%d", got)
	}
}

func TestParser_DA1_NonZeroIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[1c"))
	if got != 0 {
		t.Errorf("CSI 1 c should not reply: %d", got)
	}
}

func TestParser_DA1_PrivateIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[?c"))
	if got != 0 {
		t.Errorf("CSI ? c should not reply: %d", got)
	}
}

func TestParser_CPRReply(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.CursorR, g.CursorC = 2, 5
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[6n"))
	if len(replies) != 1 || string(replies[0]) != "\x1b[3;6R" {
		t.Fatalf("CPR reply = %q", replies)
	}
}

func TestParser_DCS_UnknownSwallowed(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1bPignored\x1b\\X"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Fatalf("DCS leaked into grid: got %q want X", got)
	}
}

func TestParser_XTGETTCAP_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q544e;6b63757531\x1b\\"))
	want := "\x1bP1+r544e=787465726d2d323536636f6c6f72;6b63757531=1b5b41\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("XTGETTCAP = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_UnknownCapReturnsHexName(t *testing.T) {

	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q756e6b6e6f776e\x1b\\"))
	want := "\x1bP0+r756e6b6e6f776e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("unknown cap reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_InvalidHexReturnsErrorWithPart(t *testing.T) {

	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q54e\x1b\\"))
	want := "\x1bP0+r54e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("invalid hex reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_EmptyBodyReturnsError(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q\x1b\\"))
	want := "\x1bP0+r\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("empty body reply = %q, want %q", replies, want)
	}
}

func TestSplitSemis_CapsAtMax(t *testing.T) {
	// 41 fields separated by 40 semicolons — must be capped at maxXTGETTCAPParts
	var b []byte
	for i := range 41 {
		if i > 0 {
			b = append(b, ';')
		}
		b = append(b, 'x')
	}
	parts := splitSemis(b)
	if len(parts) > maxXTGETTCAPParts {
		t.Errorf("splitSemis returned %d parts, want ≤%d", len(parts), maxXTGETTCAPParts)
	}
}

func TestSplitSemis_ExactBoundary(t *testing.T) {
	// Exactly maxXTGETTCAPParts semicolons (cap+1 fields). The pre-append cap
	// check used to miss the tail append and return cap+1 parts.
	var b []byte
	for i := range maxXTGETTCAPParts + 1 {
		if i > 0 {
			b = append(b, ';')
		}
		b = append(b, 'x')
	}
	parts := splitSemis(b)
	if len(parts) != maxXTGETTCAPParts {
		t.Errorf("splitSemis returned %d parts, want exactly %d", len(parts), maxXTGETTCAPParts)
	}
}

func TestParser_XTGETTCAP_BooleanCapEmptyValue(t *testing.T) {
	// Boolean caps (e.g. "am" = 616d) advertise presence with an empty
	// value: the reply must be "1+r616d=" — name, '=', nothing after.
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q616d\x1b\\"))
	want := "\x1bP1+r616d=\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("boolean cap reply = %q, want %q", replies, want)
	}
}

func TestXTGETTCAPValue_AllCaps(t *testing.T) {
	tests := []struct {
		cap  string
		want string
		ok   bool
	}{
		// Terminal identity
		{"TN", "xterm-256color", true},
		{"name", "xterm-256color", true},

		// Boolean caps (empty = present)
		{"am", "", true},
		{"bce", "", true},
		{"km", "", true},
		{"xenl", "", true},

		// SGR / video attributes
		{"bold", "\x1b[1m", true},
		{"dim", "\x1b[2m", true},
		{"sitm", "\x1b[3m", true},
		{"ritm", "\x1b[23m", true},
		{"smso", "\x1b[7m", true},
		{"rmso", "\x1b[27m", true},
		{"smul", "\x1b[4m", true},
		{"rmul", "\x1b[24m", true},
		{"rev", "\x1b[7m", true},
		{"sgr0", "\x1b(B\x1b[m", true},
		{"sgr", "\x1b[0%?%p6%t;1%;%?%p5%t;2%;%?%p2%t;4%;" +
			"%?%p1%p3%|%t;7%;%?%p4%t;5%;%?%p7%t;8%;" +
			"m%?%p9%t\x1b(0%e\x1b(B%;", true},
		{"Smulx", "\x1b[4:%p1%dm", true},
		{"Setulc", "\x1b[58:2::%p1%{65536}%/%d:%p1%{256}%/%{256}%m%d:%p1%{256}%m%dm", true},

		// Character set
		{"smacs", "\x1b(0", true},
		{"rmacs", "\x1b(B", true},
		{"acsc", "``aaffggiijjkkllmmnnooppqqrrssttuuvvwwxxyyzz{{||}}~~", true},

		// Cursor control
		{"civis", "\x1b[?25l", true},
		{"cnorm", "\x1b[?12l\x1b[?25h", true},
		{"cvvis", "\x1b[?12;25h", true},
		{"Ss", "\x1b[%p1%d q", true},
		{"Se", "\x1b[2 q", true},

		// Screen management
		{"clear", "\x1b[H\x1b[2J", true},
		{"home", "\x1b[H", true},
		{"cup", "\x1b[%i%p1%d;%p2%dH", true},
		{"hpa", "\x1b[%i%p1%dG", true},
		{"vpa", "\x1b[%i%p1%dd", true},
		{"ed", "\x1b[J", true},
		{"el", "\x1b[K", true},
		{"el1", "\x1b[1K", true},
		{"E3", "\x1b[3J", true},
		{"csr", "\x1b[%i%p1%d;%p2%dr", true},

		// Cursor movement
		{"cub", "\x1b[%p1%dD", true},
		{"cub1", "\x08", true},
		{"cud", "\x1b[%p1%dB", true},
		{"cud1", "\n", true},
		{"cuf", "\x1b[%p1%dC", true},
		{"cuf1", "\x1b[C", true},
		{"cuu", "\x1b[%p1%dA", true},
		{"cuu1", "\x1b[A", true},

		// Character / line insertion and deletion
		{"dch", "\x1b[%p1%dP", true},
		{"dch1", "\x1b[P", true},
		{"dl", "\x1b[%p1%dM", true},
		{"dl1", "\x1b[M", true},
		{"ich", "\x1b[%p1%d@", true},
		{"ich1", "\x1b[@", true},
		{"il", "\x1b[%p1%dL", true},
		{"il1", "\x1b[L", true},
		{"indn", "\x1b[%p1%dS", true},
		{"rin", "\x1b[%p1%dT", true},

		// Scroll / index
		{"ind", "\n", true},
		{"ri", "\x1bM", true},

		// Color
		{"RGB", "8/8/8", true},
		{"Co", "256", true},
		{"colors", "256", true},
		{"pairs", "32767", true},
		{"op", "\x1b[39;49m", true},
		{"setab", "\x1b[%?%p1%{8}%<%t4%p1%d" +
			"%e%p1%{16}%<%t10%p1%{8}%-%d" +
			"%e48;5;%p1%d%;m", true},
		{"setaf", "\x1b[%?%p1%{8}%<%t3%p1%d" +
			"%e%p1%{16}%<%t9%p1%{8}%-%d" +
			"%e38;5;%p1%d%;m", true},
		{"setrgbb", "\x1b[48:2:%p1%d:%p2%d:%p3%dm", true},
		{"setrgbf", "\x1b[38:2:%p1%d:%p2%d:%p3%dm", true},

		// Modes
		{"smam", "\x1b[?7h", true},
		{"rmam", "\x1b[?7l", true},
		{"smcup", "\x1b[?1049h", true},
		{"rmcup", "\x1b[?1049l", true},
		{"smir", "\x1b[4h", true},
		{"rmir", "\x1b[4l", true},
		{"smkx", "\x1b[?1h\x1b=", true},
		{"rmkx", "\x1b[?1l\x1b>", true},

		// Tab stops
		{"ht", "\t", true},
		{"hts", "\x1bH", true},
		{"tbc", "\x1b[3g", true},
		{"it", "8", true},

		// Misc control
		{"bel", "\x07", true},
		{"cr", "\r", true},
		{"sc", "\x1b7", true},
		{"rc", "\x1b8", true},

		// Status line
		{"tsl", "\x1b]2;", true},
		{"fsl", "\x07", true},
		{"dsl", "\x1b]2;\x07", true},

		// Focus reporting
		{"fe", "\x1b[?1004h", true},
		{"fd", "\x1b[?1004l", true},
		{"kxIN", "\x1b[I", true},
		{"kxOUT", "\x1b[O", true},

		// Bracketed paste
		{"BE", "\x1b[?2004h", true},
		{"BD", "\x1b[?2004l", true},
		{"PE", "\x1b[201~", true},
		{"PS", "\x1b[200~", true},

		// Sync
		{"Sync", "\x1b[?2026%?%p1%{1}%=%th%el%;", true},

		// Mouse
		{"kmous", "\x1b[<", true},
		{"XM", "\x1b[?1006;1000%?%p1%{1}%=%th%el%;", true},
		{"xm", "\x1b[<%i%p3%d;%p1%d;%p2%d;%?%p4%tM%em%;", true},

		// Clipboard
		{"Ms", "\x1b]52;%p1%s;%p2%s\x07", true},

		// Terminal queries / reports
		{"RV", "\x1b[>c", true},
		{"XR", "\x1b[>0q", true},
		{"u6", "\x1b[%i%d;%dR", true},
		{"u7", "\x1b[6n", true},
		{"u8", "\x1b[?%[;0123456789]c", true},
		{"u9", "\x1b[c", true},
		{"xr", "\x1bP>|[ -~]+\x1b\\", true},

		// Keyboard: basic keys
		{"kbs", "\x7f", true},
		{"kcbt", "\x1b[Z", true},
		{"kent", "\x1bOM", true},
		{"kcuu1", "\x1b[A", true},
		{"kcud1", "\x1b[B", true},
		{"kcub1", "\x1b[D", true},
		{"kcuf1", "\x1b[C", true},
		{"khome", "\x1b[H", true},
		{"kend", "\x1b[F", true},
		{"kich1", "\x1b[2~", true},
		{"kdch1", "\x1b[3~", true},
		{"kpp", "\x1b[5~", true},
		{"knp", "\x1b[6~", true},
		{"kind", "\x1b[1;2B", true},
		{"kri", "\x1b[1;2A", true},

		// Keyboard: function keys
		{"kf1", "\x1bOP", true},
		{"kf2", "\x1bOQ", true},
		{"kf3", "\x1bOR", true},
		{"kf4", "\x1bOS", true},
		{"kf5", "\x1b[15~", true},
		{"kf6", "\x1b[17~", true},
		{"kf7", "\x1b[18~", true},
		{"kf8", "\x1b[19~", true},
		{"kf9", "\x1b[20~", true},
		{"kf10", "\x1b[21~", true},
		{"kf11", "\x1b[23~", true},
		{"kf12", "\x1b[24~", true},

		// Keyboard: modified arrow keys
		{"kUP", "\x1b[1;2A", true},
		{"kUP5", "\x1b[1;5A", true},
		{"kUP6", "\x1b[1;6A", true},
		{"kDN", "\x1b[1;2B", true},
		{"kDN5", "\x1b[1;5B", true},
		{"kDN6", "\x1b[1;6B", true},
		{"kLFT", "\x1b[1;2D", true},
		{"kLFT5", "\x1b[1;5D", true},
		{"kLFT6", "\x1b[1;6D", true},
		{"kRIT", "\x1b[1;2C", true},
		{"kRIT5", "\x1b[1;5C", true},
		{"kRIT6", "\x1b[1;6C", true},
		{"kHOM5", "\x1b[1;5H", true},
		{"kEND5", "\x1b[1;5F", true},

		// Keyboard: modified pgup/pgdn
		{"kNXT", "\x1b[6;2~", true},
		{"kPRV", "\x1b[5;2~", true},

		// Keyboard: modified insert/delete
		{"kIC", "\x1b[2;2~", true},
		{"kIC5", "\x1b[2;5~", true},
		{"kIC6", "\x1b[2;6~", true},
		{"kDC", "\x1b[3;2~", true},
		{"kDC5", "\x1b[3;5~", true},
		{"kDC6", "\x1b[3;6~", true},

		// Custom query caps
		{"query-os-name", "\x1b]0;?\x07", true},

		// Deliberately unadvertised: features the emulator does not
		// implement. Re-adding any of these requires implementing the
		// underlying sequence first (see comments in xtgettcapValue).
		{"ech", "", false},   // CSI X not in dispatchCSI
		{"ccc", "", false},   // OSC 4/104 palette ops unimplemented
		{"initc", "", false}, // OSC 4 unimplemented
		{"oc", "", false},    // OSC 104 unimplemented
		{"flash", "", false}, // DECSCNM (mode 5) unimplemented

		// Unknown caps
		{"nonexistent", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := xtgettcapValue(tt.cap)
		if ok != tt.ok {
			t.Errorf("xtgettcapValue(%q) ok=%v, want %v", tt.cap, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Errorf("xtgettcapValue(%q) = %q, want %q", tt.cap, got, tt.want)
		}
	}
}
