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
	// Close kills the child and reaps it; the file.Close error is what
	// is returned. Either nil or "file already closed" is acceptable —
	// the contract is that it doesn't panic and is safe to call.
	_ = p.Close()
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
