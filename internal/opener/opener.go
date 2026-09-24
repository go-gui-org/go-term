// Package opener hands a URL or file path to the operating system's default
// handler. It is a leaf package: it imports nothing from the repo, so term (OSC 8
// links) and falcon (opening the config file) share one copy of the platform
// choice and the child reaping.
//
// It does no validation. A caller passing untrusted input (terminal output) must
// check it first; see term's openURLCommand.
package opener

import (
	"os/exec"
	"runtime"
)

// Command returns the command that opens target with the default handler. The
// target is always one argv element of a non-shell program, so it is never
// parsed by a shell. On Windows that is why rundll32 is used: cmd /c start would
// let '&' or quotes in target escape into cmd.exe.
func Command(target string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return exec.Command("xdg-open", target)
	}
}

// Start starts cmd and waits for it in the background. Without the wait, every
// opener that exits stays in the process table as a defunct (zombie) entry
// until go-term exits.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
