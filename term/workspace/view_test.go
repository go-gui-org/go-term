package workspace

// View-construction smoke tests. These do not assert on layout — that needs a
// real backend — but they do build the whole view tree in each state the
// workspace can be in. Layout code is where an out-of-range index or a nil
// pane panics the frame, and a panic here would take down the window for a
// user, so "it builds at all" is worth pinning down.

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// buildView renders the workspace and fails on a nil result. A panic
// propagates and fails the test on its own.
func buildView(t *testing.T, ws *Workspace) {
	t.Helper()
	if v := ws.View(&gui.Window{}); v == nil {
		t.Fatal("View returned nil")
	}
}

// The view builds in every structural state: one pane, splits, several tabs,
// and combinations of the overlays.
func TestView_BuildsInEveryState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, ws *Workspace)
	}{
		{"single pane", func(t *testing.T, ws *Workspace) {}},
		{"vertical split", func(t *testing.T, ws *Workspace) {
			ws.SplitPane(false)
		}},
		{"horizontal split", func(t *testing.T, ws *Workspace) {
			ws.SplitPane(true)
		}},
		{"nested splits", func(t *testing.T, ws *Workspace) {
			ws.SplitPane(false)
			ws.SplitPane(true)
			ws.SplitPane(false)
		}},
		{"multiple tabs", func(t *testing.T, ws *Workspace) {
			ws.AddTab()
			ws.AddTab()
		}},
		{"tabs and splits", func(t *testing.T, ws *Workspace) {
			ws.SplitPane(false)
			ws.AddTab()
			ws.SplitPane(true)
		}},
		{"help overlay", func(t *testing.T, ws *Workspace) {
			ws.ToggleHelp()
		}},
		{"help over a split", func(t *testing.T, ws *Workspace) {
			ws.SplitPane(false)
			ws.ToggleHelp()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newLiveWorkspace(t)
			tc.setup(t, ws)
			buildView(t, ws)
		})
	}
}

// The theme picker builds at every highlight position, including the ones
// reached by wrapping — an off-by-one in the index would panic the panel.
func TestView_ThemePickerAtEveryIndex(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themePickerCfg(t))
	ws.ToggleThemePicker()

	for i := 0; i < 2*len(ws.cfg.Themes)+1; i++ {
		buildView(t, ws)
		ws.themePickerMoveDown()
	}
	for i := 0; i < 2*len(ws.cfg.Themes)+1; i++ {
		buildView(t, ws)
		ws.themePickerMoveUp()
	}
}

// The view still builds after panes and tabs are torn down — the state most
// likely to leave a dangling reference behind.
func TestView_BuildsAfterTeardown(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.AddTab()
	buildView(t, ws)

	ws.ClosePane()
	buildView(t, ws)

	ws.CloseTab()
	buildView(t, ws)

	// Down to the last tab: closing it yields a fresh replacement, which must
	// also render.
	ws.CloseTab()
	buildView(t, ws)
}

// Toggling the overlays off again returns to a buildable plain view.
func TestView_OverlaysToggleBackOff(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themePickerCfg(t))

	ws.ToggleHelp()
	buildView(t, ws)
	ws.ToggleHelp()
	buildView(t, ws)

	ws.ToggleThemePicker()
	buildView(t, ws)
	ws.ToggleThemePicker()
	buildView(t, ws)
}
