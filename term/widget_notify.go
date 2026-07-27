package term

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// registerNotifyHandler wires the OSC 9 / OSC 777 notification path.
// Extracted so tests can reuse the same handler without copy-paste drift.
func (t *Term) registerNotifyHandler() {
	t.parser.SetNotifyHandler(func(title, body string) {
		if !t.notifyBusy.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer t.notifyBusy.Store(false)
			t.notify(title, body)
		}()
	})
}

// notify delivers a desktop notification through the host's OnNotify hook, or
// the built-in notifier when unset. Blocks (the built-in path may exec), so
// callers must already be off the reader goroutine.
func (t *Term) notify(title, body string) {
	if fn := t.cfg.OnNotify; fn != nil {
		fn(title, body)
		return
	}
	t.notif.Notify(title, body)
}

// onParserTitle is the OSC 0/1/2 handler. Runs on the reader goroutine
// while grid.Mu is held — must not touch *gui.Window state directly,
// hence the QueueCommand hop.
func (t *Term) onParserTitle(title string) {
	fn := t.cfg.OnTitle
	t.queueCommand(func(w *gui.Window) {
		if fn != nil {
			fn(title)
			return
		}
		w.SetTitle(title)
	})
}

// onParserReply buffers parser-originated bytes (e.g. DA1 reply) in
// pendingReplies; it does not write them. Called from inside parser.Feed,
// which runs under grid.Mu on the reader goroutine. applyChunk hands the batch
// to writeLoop (via enqueueReplies) once grid.Mu is released, and writeLoop
// does the blocking pty.Write on its own goroutine — so a full slave-side input
// buffer (shell not reading) never stalls the reader, which must keep draining
// the master, nor onDraw, which waits on grid.Mu.
func (t *Term) onParserReply(b []byte) {
	if len(b) == 0 {
		return
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	t.pendingReplies = append(t.pendingReplies, cp)
}

// sendDesktopNotify fires a native OS notification. Blocks briefly
// (subprocess exec), so always call from a goroutine. Null bytes are
// removed to prevent truncation of C-string args at the syscall boundary.
func sendDesktopNotify(title, body string) {
	clean := func(s string) string { return strings.ReplaceAll(s, "\x00", "") }
	switch runtime.GOOS {
	case "darwin":
		// Pass title/body as argv to avoid AppleScript string-literal injection.
		stmt := `display notification (item 1 of argv)`
		args := []string{"-e", "on run argv", "-e", stmt, "-e", "end run", clean(body)}
		if title != "" {
			stmt = `display notification (item 1 of argv) with title (item 2 of argv)`
			args = []string{"-e", "on run argv", "-e", stmt, "-e", "end run", clean(body), clean(title)}
		}
		exec.Command("osascript", args...).Run() //nolint:errcheck
	case "linux":
		args := []string{clean(body)}
		if title != "" {
			args = []string{clean(title), clean(body)}
		}
		exec.Command("notify-send", args...).Run() //nolint:errcheck
	case "windows":
		// WinRT toast via PowerShell — no extra dependency. Title and body
		// reach the script only through environment variables, never as
		// interpolated source, so hostile content cannot inject PowerShell.
		// The PowerShell AppUserModelID is borrowed so the toast surfaces
		// in Action Center without registering our own AUMID.
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-Command", winToastScript)
		cmd.Env = append(os.Environ(),
			"GOTERM_NOTIFY_TITLE="+clean(title),
			"GOTERM_NOTIFY_BODY="+clean(body))
		cmd.Run() //nolint:errcheck
	}
}

// winToastScript builds and shows a Windows toast from GOTERM_NOTIFY_TITLE /
// GOTERM_NOTIFY_BODY. Show() hands the toast to the platform and returns, so
// no sleep is needed to keep it alive. Errors are swallowed by the caller.
const winToastScript = `
$ErrorActionPreference = 'Stop'
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
$null = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$tpl = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$texts = $tpl.GetElementsByTagName('text')
$null = $texts.Item(0).AppendChild($tpl.CreateTextNode([string]$env:GOTERM_NOTIFY_TITLE))
$null = $texts.Item(1).AppendChild($tpl.CreateTextNode([string]$env:GOTERM_NOTIFY_BODY))
$toast = [Windows.UI.Notifications.ToastNotification]::new($tpl)
$aumid = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($aumid).Show($toast)
`
