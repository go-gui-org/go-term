package term

import (
	"os"
	"sync"
	"testing"
)

// fuzzGraphicsDir is one temp dir shared by all fuzz iterations of this
// process. The parser writes decoded images to disk itself (encodePNGFile,
// parser_dcs.go:540), so without this every successfully-decoded fuzz frame
// would land in os.TempDir(). One dir per process keeps the run's detritus
// in one place. Empty on MkdirTemp failure, in which case encodePNGFile
// falls back to os.TempDir().
var fuzzGraphicsDir = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "go-term-fuzz-")
	if err != nil {
		return ""
	}
	return dir
})

// fuzzParser wires a grid+parser the way production uses them — every handler
// installed (so reply/notification/download code executes instead of being
// skipped by nil checks), clipboard writes allowed, decoded images directed
// to the shared fuzz dir — feeds data across Feed calls so parser state
// straddles boundaries, then asserts the invariants a panic would miss.
// Grid size comes from the raw (unwrapped) fuzz bytes' first two bytes;
// see below.
func fuzzParser(t *testing.T, data []byte, wrap func([]byte) []byte) {
	t.Helper()
	rows, cols := 24, 80
	if len(data) > 1 && data[0] < 8 {
		// Tiny grids are the bounds stress: cursor/region/rect math hits
		// its extremes on a 1..4 × 1..8 grid, which 24×80 never does.
		rows = int(data[0])%4 + 1
		cols = int(data[1])%8 + 1
	}
	g := newGrid(rows, cols)
	p := newParser(g)
	p.SetGraphicsDir(fuzzGraphicsDir())
	// Handlers run under Mu, exactly as the widget wires them. The handlers
	// themselves are no-ops — the point is the parser-side string building
	// (DA1/DA2, CPR, DECRQM, XTVERSION, XTSMGRAPHICS, XTWINOPS, OSC replies)
	// and decode paths (OSC 52, OSC 1337 File=) they unlock.
	p.SetReplyHandler(func([]byte) {})
	p.SetTitleHandler(func(string) {})
	p.SetNotifyHandler(func(string, string) {})
	p.SetCommandHandler(func(byte, int16) {})
	p.SetClipboardHandler(func([]byte) {})
	p.SetClipboardWriteAllowed(true)
	p.SetDownloadHandler(func(string, []byte) {})

	g.Mu.Lock()
	defer g.Mu.Unlock()
	feedSplit(p, wrap(data))
	assertParserInvariants(t, g, p)
}

// feedSplit feeds buf across Feed calls so escape-sequence and UTF-8 state
// carries over between batches — the shape the PTY reader actually produces.
// Byte-by-byte for short inputs hits every inter-byte boundary (a truncated
// CSI final, a UTF-8 sequence split mid-rune); larger inputs split once.
func feedSplit(p *parser, buf []byte) {
	if len(buf) <= 64 {
		for _, b := range buf {
			p.Feed([]byte{b})
		}
		return
	}
	n := len(buf) / 2
	p.Feed(buf[:n])
	p.Feed(buf[n:])
}

// assertParserInvariants checks the post-conditions every Feed must leave
// behind. Out-of-bounds cursor/region state is silent corruption — no panic
// — unless checked here, so a no-panic fuzz harness would pass it forever.
func assertParserInvariants(t *testing.T, g *grid, p *parser) {
	t.Helper()
	if g.CursorR < 0 || g.CursorR >= g.Rows {
		t.Fatalf("cursor row out of bounds: %d of %d", g.CursorR, g.Rows)
	}
	if g.CursorC < 0 || g.CursorC > g.Cols {
		// CursorC == Cols is the deferred-wrap encoding (grid.settledCol
		// collapses it); anything past the right margin is corruption.
		t.Fatalf("cursor col out of bounds: %d of %d", g.CursorC, g.Cols)
	}
	if g.Top < 0 || g.Bottom >= g.Rows || g.Top > g.Bottom {
		t.Fatalf("scroll region invalid: [%d, %d] in %d rows",
			g.Top, g.Bottom, g.Rows)
	}
	if len(g.Cells) != g.Rows*g.Cols {
		t.Fatalf("cells %d, want %d", len(g.Cells), g.Rows*g.Cols)
	}
	if len(g.Dirty) != g.Rows || len(g.RowWrapped) != g.Rows {
		t.Fatalf("row tables dirty=%d wrapped=%d, want %d rows",
			len(g.Dirty), len(g.RowWrapped), g.Rows)
	}
	if len(p.params) > maxCSIParams || len(p.paramSub) > maxCSIParams {
		t.Fatalf("CSI param tables over cap: params=%d sub=%d",
			len(p.params), len(p.paramSub))
	}
	if p.state > stAPCEsc {
		t.Fatalf("parser state out of range: %d", p.state)
	}
}

// seeder is the slice of *testing.T used by loadFixtures — enough for seed
// setup, which runs with a *testing.F in fuzz mode.
type seeder interface {
	Helper()
	Fatalf(format string, args ...any)
	Skip(args ...any)
}

// fixtureSeeds expands the EmulatorReplay fixtures into fuzz seeds: each
// fixture's full input plus every prefix up to 256 bytes. The prefixes are
// the malformed/truncated class — a sequence cut off mid-parameter — that
// replay fixtures, which only ever contain complete sessions, never reach;
// coverage-guided mutation extends from there.
func fixtureSeeds(t seeder) [][]byte {
	t.Helper()
	var out [][]byte
	for _, f := range loadFixtures(t) {
		in := decodeFixtureInput(t, f)
		out = append(out, in)
		for i := 1; i <= min(len(in), 256); i++ {
			out = append(out, in[:i])
		}
	}
	return out
}

func FuzzParserFeed(f *testing.F) {
	seeds := append([][]byte{
		[]byte("hello world"),
		[]byte("\x1b[31mred\x1b[0m"),
		[]byte("\x1b[1;2H"),
		[]byte("\x1b]0;title\x07"),
		[]byte("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"),
		[]byte("\x1b_Gf=24,s=10,v=0;aGVsbG8=\x1b\\"),
		[]byte("\x1bPq\"p\x1b\\"),
		[]byte("\x1bP$qm\x1b\\"),                          // DECRQSS
		[]byte("\x1bP+q544e\x1b\\"),                       // XTGETTCAP
		[]byte("\x1b[?2026h\x1bP=1s\x1b\\\x1bP=2s\x1b\\"), // sync updates
		{},
	}, fixtureSeeds(f)...)
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzParser(t, data, func(b []byte) []byte { return b })
	})
}

func FuzzCSIDispatch(f *testing.F) {
	seeds := []string{
		"m", "H", "A", "B", "C", "D",
		"2J", "1J", "0J", "K", "1K", "2K",
		"?25h", "?25l", "?1049h", "?1049l",
		">0u", "=1u", "?u",
		"?1000h", "?1000l", "?1002h", "?1006h",
		"0;0H", "1;1r", "@", "L", "M", "P",
		"3g", " q", "0 q", "2 q",
		"!p", "$p", "?1003;1$p", // DECSTR, DECRQM
		"14t", "16t", "22t", "23t", // XTWINOPS
		">q", ">c", "?996n", "?6n", "5n", "6n", // XTVERSION, DA2, DSR
		"$r", "$t", "$v", "$x", "*x", "$z", "${", "$x", // rect ops
		"38;2;1;2;3m", "48;5;200m", "58;2;9;9;9m", // extended SGR
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	for _, s := range fixtureSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzParser(t, data, func(b []byte) []byte {
			buf := make([]byte, 0, len(b)+2)
			buf = append(buf, '\x1b', '[')
			return append(buf, b...)
		})
	})
}

func FuzzOSCDispatch(f *testing.F) {
	seeds := []string{
		"0;hello",
		"2;My Window Title",
		"4;1;#ff0000;200;rgb:00/00/ff",
		"4;21;?",
		"104",
		"104;1;2",
		"7;file:///Users/test",
		"8;;https://example.com",
		"52;c;dGVzdA==",
		"133;A",
		"1337;File=name=AAABAAAA;size=100",
		"1337;File=name=AAABAAAA;size=5;inline=0:aGVsbG8=",
		"1337;File=name=Li4vLi4vZXRjL3Bhc3N3ZA==;inline=0:aGVsbG8=",
		"1337;File=width=10;height=50%;preserveAspectRatio=0;inline=1:AAAA",
		"1337;File=width=auto;height=px;size=-1;inline=1:",
		"9;4;1;50",  // ConEmu progress
		"9;Message", // notification
		"777;notify;Title;Body",
		"22;hand",
		"10;?", "11;?", "12;?", // dynamic color queries
		"2;truncated", // plain title-set; the harness adds the BEL terminator
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	for _, s := range fixtureSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzParser(t, data, func(b []byte) []byte {
			buf := make([]byte, 0, len(b)+3)
			buf = append(buf, '\x1b', ']')
			buf = append(buf, b...)
			return append(buf, '\x07') // BEL terminator
		})
	})
}

func FuzzDCSDispatch(f *testing.F) {
	seeds := []string{
		"$qm", "$qr", "$q q", "$q\"q", "$q*x", // DECRQSS finals
		"+q", "+q54e", "+q544e", "+q544e;6b63757531", // XTGETTCAP
		"q", "q\"1;1;8;16;0;0", "q#0~", // sixel: params, minimal frame
		"=1s", "=2s", // sync begin/end (inert without ?2026 set outside the DCS)
		"P", "?", "+", "$", "garbage", // no final, no introducer
	}
	// The wrapper below wraps every input in ESC P … ESC \, so a mode-setting
	// CSI in a seed can never execute — it lands inside the DCS payload. The
	// live sync path (mode set, then =1s/=2s) is covered by FuzzParserFeed,
	// which feeds raw bytes; mutations of "=1s\x1b\\…" reach it here too.
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	for _, s := range fixtureSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzParser(t, data, func(b []byte) []byte {
			buf := make([]byte, 0, len(b)+4)
			buf = append(buf, '\x1b', 'P')
			buf = append(buf, b...)
			return append(buf, '\x1b', '\\') // ST terminator
		})
	})
}

func FuzzKittyAPC(f *testing.F) {
	seeds := []string{
		"Gf=24,s=10,v=0;aGVsbG8=",
		"Gf=32,s=20,v=0;c=1,r=1;",
		"Ga=d,I=1;",
		"Ga=p,I=1;",
		"Ga=D;",
		"G",                  // bare introducer, no action
		"Gf=w,width=10;AAAA", // malformed chunk
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	for _, s := range fixtureSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzParser(t, data, func(b []byte) []byte {
			buf := make([]byte, 0, len(b)+4)
			buf = append(buf, '\x1b', '_')
			buf = append(buf, b...)
			return append(buf, '\x1b', '\\') // ST terminator
		})
	})
}
