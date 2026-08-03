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

// hostTerminalEnvKeys lists variables that identify the terminal emulator the
// *parent* process was running under. They are stale the moment a child runs
// inside go-term, and children act on them: yazi picks its image protocol from
// TERM_PROGRAM, so a falcon launched from iTerm2 got the iTerm2 inline-image
// protocol while the same falcon launched from Ghostty got Kitty graphics —
// same terminal, different behavior, decided by who opened the window.
//
// Dropping them makes the child fall back to feature detection (DA1, the KGP
// query, XTVERSION), which reports what go-term actually implements. Callers
// that want to impersonate a specific emulator can still set these through
// cfg.Env, which is applied after the scrub.
var hostTerminalEnvKeys = [...]string{
	"TERM_PROGRAM",
	"TERM_PROGRAM_VERSION", // meaningless once TERM_PROGRAM is gone
	"ITERM_SESSION_ID",
}

// selfIdentityEnv names this terminal to children, replacing the host identity
// the scrub above removed. Scrubbing alone left TERM_PROGRAM unset, which reads
// as "no information" rather than "a terminal that is not the one that launched
// me" — and every value here is true, unlike the alternative of answering to
// some other emulator's name to inherit its capability profile.
//
// This does not, on its own, get pixel graphics out of tools that key off the
// name: none of them know "go-term" yet, and the ones that matter should be
// asking the terminal instead (chafa, for one, detects sixel from DA1 but
// detects the Kitty protocol only from TERM_PROGRAM/TERM, never sending the
// a=q query go-term already answers). Advertising an honest name is what makes
// growing that support possible; claiming a false one is not.
var selfIdentityEnv = [...]string{
	"TERM_PROGRAM=go-term",
	"TERM_PROGRAM_VERSION=" + termVersion,
}

// setTerminalIdentity drops the host emulator's identity variables and names
// this terminal in their place. The two halves belong together — a scrub
// without the replacement leaves TERM_PROGRAM unset — so every startPTY goes
// through here rather than pairing dropEnv with an append of its own.
func setTerminalIdentity(env []string) []string {
	return append(dropEnv(env, hostTerminalEnvKeys[:]), selfIdentityEnv[:]...)
}

// colorFGBGEnv builds the COLORFGBG value describing the theme the child is
// about to run inside: "0;15" for a light scheme, "15;0" for a dark one (the
// two numbers are foreground and background as palette indices, which is the
// form rxvt established and everything else copied).
//
// vim, less and a handful of prompt tools read it to pick a light or dark
// variant of their own colors. That matters because a truecolor SGR is not
// themeable — a child that emits an orange chosen for a dark background gets
// that exact orange on a light one — so the only lever go-term has on those
// colors is telling the child which way the terminal is painted before it
// picks them.
//
// Only accurate at spawn: the variable cannot be updated in a running child,
// which is what mode 2031 exists for. Tools that read neither are why
// Cfg.MinimumContrast exists.
func colorFGBGEnv(cfg Cfg) string {
	th := DefaultTheme // what a Term with no configured themes renders with
	if len(cfg.Themes) > 0 {
		th = cfg.Themes[0].Theme
	}
	if th.IsDark() {
		return "COLORFGBG=15;0"
	}
	return "COLORFGBG=0;15"
}

// dropEnv returns env without any entry naming one of keys. The input slice is
// never mutated: os.Environ() hands back a fresh slice, but cfg.Env may not.
func dropEnv(env []string, keys []string) []string {
	// Sized for the common case (nothing dropped) so the append loop never
	// grows the backing array.
	out := make([]string, 0, len(env))
	for _, e := range env {
		drop := false
		for _, k := range keys {
			if len(e) > len(k) && e[len(k)] == '=' && e[:len(k)] == k {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, e)
		}
	}
	return out
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
