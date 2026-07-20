package term

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ptyIO is the PTY interface: platform-specific implementations
// satisfy this. Read, Write, Resize, Close, and PID cover the
// full lifecycle.
type ptyIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(int, int) error
	Close() error
	PID() int
}

// ptyDev wraps a pseudoterminal master and the child shell process. The
// concrete struct is platform-specific (pty_unix.go / pty_windows.go); both
// keep cmd and file fields so cross-platform tests can construct it directly.

// clampWinsize bounds rows/cols to the uint16 range expected by the
// kernel ioctl, with a sane lower bound so a degenerate caller can't
// hand the shell a 0-row terminal.
func clampWinsize(n int) uint16 {
	if n < 1 {
		return 1
	}
	if n > 0xFFFF {
		return 0xFFFF
	}
	return uint16(n)
}

// localeEnvKeys lists the variables that select the child's character-set
// locale, in POSIX precedence order: LC_ALL beats LC_CTYPE beats LANG.
var localeEnvKeys = [...]string{"LC_ALL", "LC_CTYPE", "LANG"}

// hasLocaleEnv reports whether env already pins the character-set locale.
// An entry with an empty value (`LANG=`) does not count — POSIX treats it as
// unset, and macOS GUI launches routinely hand down exactly that.
func hasLocaleEnv(env []string) bool {
	for _, key := range localeEnvKeys {
		prefix := key + "="
		for _, e := range env {
			if strings.HasPrefix(e, prefix) && len(e) > len(prefix) {
				return true
			}
		}
	}
	return false
}

// defaultUTF8Locale returns the locale name to hand a child that inherited no
// locale at all. Without one, libc falls back to the "C" locale and ncurses
// apps cannot encode wide characters — ttysolitaire's card suits, for
// instance, come out as mangled byte soup. Terminal.app and iTerm2 set this
// for the same reason.
func defaultUTF8Locale() string {
	if runtime.GOOS == "darwin" {
		if name := darwinUTF8Locale(); name != "" {
			return name
		}
		return "en_US.UTF-8"
	}
	// C.UTF-8 is built into glibc >= 2.35 and always available on musl, and
	// unlike en_US.UTF-8 it needs no generated locale archive. On a system
	// that lacks it libc falls back to "C" — the same behavior as today, so
	// this cannot regress anything.
	return "C.UTF-8"
}

// darwinUTF8Locale derives a UTF-8 locale name from the user's macOS region
// setting (AppleLocale, e.g. "en_US" or "pt_BR@calendar=gregorian"). Returns
// "" when the setting is missing or the derived locale is not installed.
func darwinUTF8Locale() string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		return ""
	}
	name := normalizeLocaleName(string(out))
	if name == "" {
		return ""
	}
	name += ".UTF-8"
	// macOS ships each locale as a directory under /usr/share/locale; a name
	// with no directory would leave setlocale falling back to "C".
	if _, err := os.Stat("/usr/share/locale/" + name); err != nil {
		return ""
	}
	return name
}

// normalizeLocaleName reduces an AppleLocale identifier to a POSIX locale
// base: trims whitespace, drops any "@keyword=value" suffix, and converts the
// BCP-47 hyphen ("en-US") to the POSIX underscore. Returns "" if what remains
// is not a plain language[_REGION] token.
func normalizeLocaleName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "-", "_")
	if s == "" {
		return ""
	}
	// Reject anything that could not be a locale directory name, so a
	// surprising `defaults` payload can never be pasted into a path.
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		default:
			return ""
		}
	}
	return s
}
