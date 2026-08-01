//go:build !windows

package term

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// appReads drains the application (slave) end on a single goroutine and hands
// each read to the test over a channel.
//
// One long-lived reader, not one per assertion: a per-call goroutine left
// blocked in Read by a "nothing should arrive" assertion would swallow the
// *next* report and the test would misattribute it as silence. Exits when
// teardown closes the pty.
func appReads(app *os.File) <-chan []byte {
	ch := make(chan []byte, 8)
	go func() {
		defer close(ch)
		for {
			buf := make([]byte, 64)
			n, err := app.Read(buf)
			if n > 0 {
				ch <- buf[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// readWithin returns the next read from the app end, or nil if none arrives
// within d.
func readWithin(ch <-chan []byte, d time.Duration) []byte {
	select {
	case b := <-ch:
		return b
	case <-time.After(d):
		return nil
	}
}

// TestSetThemeNotifiesColorScheme drives the mode-2031 notification end to end:
// a subscribed child gets CSI ? 997 on the pty when the theme's light/dark
// character flips, and gets nothing at all when it doesn't. The queueing path
// matters as much as the bytes — SetTheme runs on the main thread, so it must
// not write to the pty itself.
func TestSetThemeNotifiesColorScheme(t *testing.T) {
	tm, app, teardown := newReplyTestTerm(t)
	defer teardown()
	out := appReads(app)

	light := DefaultTheme
	light.DefaultBG = gui.RGB(253, 246, 227)

	// Unsubscribed: a flip must stay silent. An unsolicited report to an app
	// that never asked for one lands in the shell as garbage input.
	tm.SetTheme(light)
	if got := readWithin(out, 150*time.Millisecond); len(got) != 0 {
		t.Fatalf("unsubscribed Term emitted %q", got)
	}

	// Subscribe the way a real client does.
	if _, err := app.Write([]byte("\x1b[?2031h")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		tm.grid.Mu.Lock()
		on := tm.grid.ColorSchemeUpdates
		tm.grid.Mu.Unlock()
		if on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("?2031h never reached the grid")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// light → dark: one report, encoded the same way DSR ?996 answers.
	tm.SetTheme(DefaultTheme)
	got := readWithin(out, 2*time.Second)
	if !bytes.Equal(got, []byte("\x1b[?997;1n")) {
		t.Fatalf("report = %q, want %q", got, "\x1b[?997;1n")
	}

	// dark → dark: nothing changed that the notification can describe, and one
	// report per keystroke of a theme-cycling chord is noise in the child's
	// input stream.
	tm.SetTheme(GruvboxTheme)
	if got := readWithin(out, 150*time.Millisecond); len(got) != 0 {
		t.Fatalf("same-scheme switch emitted %q", got)
	}

	// dark → light: reported.
	tm.SetTheme(light)
	got = readWithin(out, 2*time.Second)
	if !bytes.Equal(got, []byte("\x1b[?997;2n")) {
		t.Fatalf("report = %q, want %q", got, "\x1b[?997;2n")
	}
}
