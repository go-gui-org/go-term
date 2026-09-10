package term

import (
	"slices"
	"testing"
)

// A notification title or body is child output. Both delivery paths must put
// "--" ahead of the positional arguments, or a body such as "--icon=/x" is
// parsed as an option and the child controls the notification's appearance.
func TestNotifyArgs_OptionInjection(t *testing.T) {
	hostile := "--icon=/etc/passwd"

	linux := notifySendArgs(hostile, "-u critical")
	if got := linux[0]; got != "--" {
		t.Errorf("notify-send argv[0] = %q; want %q", got, "--")
	}
	if !slices.Contains(linux, hostile) {
		t.Errorf("notify-send argv = %q; hostile title missing", linux)
	}

	mac := osascriptNotifyArgs(hostile, hostile)
	sep := slices.Index(mac, "--")
	if sep < 0 {
		t.Fatalf("osascript argv = %q; no -- separator", mac)
	}
	// Every value after the separator is a `run` argument, never an option.
	for _, a := range mac[:sep] {
		if a == hostile {
			t.Errorf("osascript argv = %q; hostile text sits in the option half", mac)
		}
	}
}

// Without a title only the body is positional, and "--" still leads.
func TestNotifyArgs_BodyOnly(t *testing.T) {
	linux := notifySendArgs("", "body")
	want := []string{"--", "body"}
	if !slices.Equal(linux, want) {
		t.Errorf("notify-send argv = %q; want %q", linux, want)
	}

	mac := osascriptNotifyArgs("", "body")
	if got := mac[len(mac)-2:]; !slices.Equal(got, want) {
		t.Errorf("osascript argv tail = %q; want %q", got, want)
	}
}

// With a title, osascript takes body first then title: the script reads them
// as item 1 and item 2 of argv.
func TestNotifyArgs_TitleOrder(t *testing.T) {
	mac := osascriptNotifyArgs("T", "B")
	want := []string{"--", "B", "T"}
	if got := mac[len(mac)-3:]; !slices.Equal(got, want) {
		t.Errorf("osascript argv tail = %q; want %q", got, want)
	}
}
