package workspace

import (
	"runtime"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// termCfg is the choke point that rejects a working directory it cannot
// trust. A relative dir would resolve against the process CWD — the shell
// would silently open somewhere the user never chose.
func TestTermCfg_RejectsRelativeDir(t *testing.T) {
	tab := &Tab{id: "tab-0"}
	hooks := paneHooks{
		onExit:  func(string) {},
		onFocus: func(string) {},
		onTitle: func(string, string) {},
		onInput: func(string, []byte, term.InputKind) {},
	}
	// What counts as absolute is platform-specific: Windows treats a rooted
	// but driveless "/abs/path" as relative to the *current drive*, so it is
	// correctly dropped there. A Windows shell reports OSC 7 as
	// file:///C:/... anyway, which arrives here with a drive letter.
	abs := "/abs/path"
	if runtime.GOOS == "windows" {
		abs = `C:\abs\path`
	}
	cases := []struct{ in, want string }{
		{"", ""},
		{"relative/path", ""},
		{"..", ""},
		{"~/src", ""}, // no shell expansion happens here
		{abs, abs},
	}
	for _, c := range cases {
		got := tab.termCfg(&gui.Window{}, Cfg{}, "tab-0-pane-0", c.in, hooks).Dir
		if got != c.want {
			t.Errorf("termCfg(dir=%q).Dir = %q, want %q", c.in, got, c.want)
		}
	}
}
