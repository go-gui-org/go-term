package term

import (
	"math"
	"strings"
	"testing"
)

func TestClampWinsize(t *testing.T) {
	cases := []struct {
		in   int
		want uint16
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{0xFFFF, 0xFFFF},
		{0x10000, 0xFFFF},
		{math.MaxInt32, 0xFFFF},
	}
	for _, c := range cases {
		if got := clampWinsize(c.in); got != c.want {
			t.Errorf("clampWinsize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPTY_StartResizeClose(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{})
	if err != nil {
		t.Skipf("startPTY failed (no shell available?): %v", err)
	}
	if err := p.Resize(30, 100); err != nil {
		t.Errorf("Resize: %v", err)
	}
	// Close is idempotent best-effort teardown: always nil, safe to repeat.
	if err := p.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestHasLocaleEnv(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want bool
	}{
		{"empty", nil, false},
		{"unrelated", []string{"TERM=xterm", "PATH=/bin"}, false},
		{"lang", []string{"LANG=en_US.UTF-8"}, true},
		{"lc_ctype", []string{"LC_CTYPE=C"}, true},
		{"lc_all", []string{"LC_ALL=C.UTF-8"}, true},
		// A bare "KEY=" is POSIX-unset; macOS GUI launches hand these down.
		{"empty value", []string{"LANG=", "LC_ALL=", "LC_CTYPE="}, false},
		{"empty then set", []string{"LANG=", "LC_CTYPE=tr_TR.UTF-8"}, true},
		// Must match on the full key, not a prefix of a longer name.
		{"prefix only", []string{"LANGUAGE=en"}, false},
	}
	for _, c := range cases {
		if got := hasLocaleEnv(c.env); got != c.want {
			t.Errorf("%s: hasLocaleEnv(%q) = %v, want %v", c.name, c.env, got, c.want)
		}
	}
}

func TestNormalizeLocaleName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"en_US", "en_US"},
		{"en_US\n", "en_US"},
		{"  fr_CA  ", "fr_CA"},
		{"en-US", "en_US"},
		{"pt_BR@calendar=gregorian", "pt_BR"},
		{"es_419", "es_419"}, // CLDR numeric regions are real locales
		{"en", "en"},
		{"", ""},
		{"@calendar=gregorian", ""},
		// Anything that is not language[_REGION] is rejected so it can never
		// be pasted into the /usr/share/locale path.
		{"../../etc/passwd", ""},
		{"en_US.UTF-8", ""},
		{"en US", ""},
	}
	for _, c := range cases {
		if got := normalizeLocaleName(c.in); got != c.want {
			t.Errorf("normalizeLocaleName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// defaultUTF8Locale must always name a UTF-8 locale — a non-UTF-8 answer would
// leave ncurses apps mangling wide characters, the bug this exists to fix.
func TestDefaultUTF8Locale(t *testing.T) {
	got := defaultUTF8Locale()
	if !strings.HasSuffix(got, ".UTF-8") {
		t.Errorf("defaultUTF8Locale() = %q, want a .UTF-8 locale", got)
	}
	if normalizeLocaleName(strings.TrimSuffix(got, ".UTF-8")) == "" {
		t.Errorf("defaultUTF8Locale() = %q, base is not a valid locale name", got)
	}
}

func TestDropEnv(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"TERM_PROGRAM=iTerm.app",
		"TERM_PROGRAM_VERSION=3.5",
		"ITERM_SESSION_ID=w0t0p0",
		"TERM=xterm-256color",
		"TERM_PROGRAMMER=me", // prefix match must not catch this
		"TERM_PROGRAM",       // no '=' at all
	}
	got := dropEnv(in, hostTerminalEnvKeys[:])
	want := []string{"PATH=/bin", "TERM=xterm-256color", "TERM_PROGRAMMER=me", "TERM_PROGRAM"}
	if len(got) != len(want) {
		t.Fatalf("dropEnv = %q; want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dropEnv = %q; want %q", got, want)
		}
	}
	// The caller's slice must come through untouched — cfg.Env may be shared.
	if in[1] != "TERM_PROGRAM=iTerm.app" {
		t.Errorf("dropEnv mutated its input: %q", in)
	}
}

// envValue returns the last value for key in an env slice ("" when absent).
// It mirrors execve's last-wins rule, which is what makes it useful for
// asserting effective values; countKey asserts the stronger no-duplicates
// invariant the cross-platform code now guarantees.
func envValue(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			val = e[len(prefix):]
		}
	}
	return val
}

// countKey tallies entries naming key (exact-case: these tests run the Unix
// matching path; Windows case-insensitivity is a two-line branch in
// envKeysEqual with no logic worth forking a VM over).
func countKey(env []string, key string) int {
	n := 0
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && k == key {
			n++
		}
	}
	return n
}

func TestSetEnvEntry(t *testing.T) {
	// Replace in place: position kept, no duplicate left behind.
	got := setEnvEntry([]string{"A=1", "B=2"}, "A=9")
	if len(got) != 2 || got[0] != "A=9" || got[1] != "B=2" {
		t.Errorf("replace: got %q", got)
	}
	// Append when absent.
	got = setEnvEntry([]string{"A=1"}, "B=2")
	if len(got) != 2 || got[1] != "B=2" {
		t.Errorf("append: got %q", got)
	}
	// Bare words carry no key and pass through untouched.
	got = setEnvEntry([]string{"A=1"}, "JUSTAWORD")
	if len(got) != 2 || got[1] != "JUSTAWORD" {
		t.Errorf("bare word: got %q", got)
	}
	// A key the parent already duplicates collapses to one entry: os/exec
	// resolves duplicates last-wins, so a surviving later copy would beat
	// the override.
	got = setEnvEntry([]string{"A=1", "B=2", "A=3"}, "A=9")
	if len(got) != 2 || got[0] != "A=9" || got[1] != "B=2" {
		t.Errorf("duplicate: got %q", got)
	}
	// The input's elements are never modified.
	in := []string{"A=1"}
	_ = setEnvEntry(in, "A=9")
	if in[0] != "A=1" {
		t.Errorf("input mutated: %q", in)
	}
}

// The child environment must force this terminal's description even when the
// parent disagrees, and must never carry duplicate keys (Windows resolves
// those differently than Unix, so duplicates are a portability bug, not a
// style nit).
func TestBaseChildEnv(t *testing.T) {
	parent := []string{
		"TERM=dumb",
		"COLORTERM=8bit",
		"COLORFGBG=0;15",
		"TERM_PROGRAM=iTerm.app",
		"PATH=/bin",
	}
	got := baseChildEnv(Cfg{}, parent)
	if v := envValue(got, "TERM"); v != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", v)
	}
	if v := envValue(got, "COLORTERM"); v != "truecolor" {
		t.Errorf("COLORTERM = %q, want truecolor", v)
	}
	for _, k := range []string{"TERM", "COLORTERM", "COLORFGBG",
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION"} {
		if n := countKey(got, k); n != 1 {
			t.Errorf("%s appears %d times, want exactly 1", k, n)
		}
	}
	if v := envValue(got, "PATH"); v != "/bin" {
		t.Errorf("PATH = %q, want it passed through", v)
	}
}

// applyCfgEnv folds caller entries over the defaults in place: the override
// wins and no duplicate remains for the platform to disambiguate.
func TestApplyCfgEnvOverrides(t *testing.T) {
	got := applyCfgEnv([]string{"TERM=xterm-256color", "A=1"},
		[]string{"TERM=screen", "B=2"})
	if v := envValue(got, "TERM"); v != "screen" {
		t.Errorf("TERM = %q, want screen", v)
	}
	if n := countKey(got, "TERM"); n != 1 {
		t.Errorf("TERM appears %d times, want 1: %q", n, got)
	}
	if v := envValue(got, "B"); v != "2" {
		t.Errorf("B = %q, want 2", v)
	}
}

func TestCheckIdentity(t *testing.T) {
	for _, ok := range []string{"", "go-term", "Falcon", "a=b", "a\nb"} {
		if err := checkIdentity(ok); err != nil {
			t.Errorf("checkIdentity(%q) = %v, want nil", ok, err)
		}
	}
	if err := checkIdentity("a\x00b"); err == nil {
		t.Error("checkIdentity(NUL) = nil, want an error")
	}
}

// Close on a zero ptyDev must not panic: the reply-path tests construct
// ptyDev without a cmd, and Close used to dereference p.cmd unguarded.
func TestPTY_CloseNilCmd(t *testing.T) {
	p := &ptyDev{}
	if err := p.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
}

// TestColorFGBGEnv covers the one lever go-term has on a child's *own* color
// choices: telling it which way the terminal is painted before it makes them.
// The value is read at spawn and cannot be updated afterwards, so getting the
// startup theme's character right is the whole feature.
func TestColorFGBGEnv(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Cfg
		want string
	}{
		{
			name: "no_themes_uses_the_default",
			cfg:  Cfg{},
			want: "COLORFGBG=15;0",
		},
		{
			// The first entry is what applyTheme gives a new pane, so it is
			// the one the child is actually running inside.
			name: "first_theme_wins",
			cfg: Cfg{Themes: []NamedTheme{
				{Name: "Solarized Light", Theme: mustBundled(t, "iTerm2 Solarized Light")},
				{Name: "Default", Theme: DefaultTheme},
			}},
			want: "COLORFGBG=0;15",
		},
		{
			name: "dark_theme",
			cfg: Cfg{Themes: []NamedTheme{
				{Name: "Catppuccin Mocha", Theme: mustBundled(t, "Catppuccin Mocha")},
			}},
			want: "COLORFGBG=15;0",
		},
		{
			name: "light_theme",
			cfg: Cfg{Themes: []NamedTheme{
				{Name: "GitHub Light", Theme: mustBundled(t, "GitHub Light Default")},
			}},
			want: "COLORFGBG=0;15",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := colorFGBGEnv(tc.cfg); got != tc.want {
				t.Errorf("colorFGBGEnv = %q, want %q", got, tc.want)
			}
		})
	}
}
