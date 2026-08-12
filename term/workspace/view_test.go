package workspace

// View-construction smoke tests. These do not assert on layout — that needs a
// real backend — but they do build the whole view tree in each state the
// workspace can be in. Layout code is where an out-of-range index or a nil
// pane panics the frame, and a panic here would take down the window for a
// user, so "it builds at all" is worth pinning down.

import (
	"math"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
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
			ws.splitPane(false)
		}},
		{"horizontal split", func(t *testing.T, ws *Workspace) {
			ws.splitPane(true)
		}},
		{"nested splits", func(t *testing.T, ws *Workspace) {
			ws.splitPane(false)
			ws.splitPane(true)
			ws.splitPane(false)
		}},
		{"multiple tabs", func(t *testing.T, ws *Workspace) {
			ws.addTab()
			ws.addTab()
		}},
		{"tabs and splits", func(t *testing.T, ws *Workspace) {
			ws.splitPane(false)
			ws.addTab()
			ws.splitPane(true)
		}},
		{"help overlay", func(t *testing.T, ws *Workspace) {
			ws.toggleHelp()
		}},
		{"help over a split", func(t *testing.T, ws *Workspace) {
			ws.splitPane(false)
			ws.toggleHelp()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newLiveWorkspace(t)
			tc.setup(t, ws)
			buildView(t, ws)
		})
	}
}

// The theme browser builds at every cursor position, including the ones
// reached by wrapping — an off-by-one in the index would panic the page.
func TestView_ThemeBrowserAtEveryIndex(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()

	for range 2*len(ws.cfg.Themes) + 1 {
		buildView(t, ws)
		ws.themeBrowserMove(1)
	}
	for range 2*len(ws.cfg.Themes) + 1 {
		buildView(t, ws)
		ws.themeBrowserMove(-1)
	}
}

// The browser must render with an empty match list too: no selection means no
// preview, and the row-window arithmetic has to cope with a zero-length list
// rather than indexing into it.
func TestView_ThemeBrowserWithNoMatches(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()
	ws.setThemeFilter("no theme is called this")

	buildView(t, ws)
}

// Paging jumps further than the visible window, which is where a row-window
// calculation that assumed the cursor moved by one would go out of bounds.
func TestView_ThemeBrowserPaging(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()

	for range 5 {
		ws.themeBrowserPage(1)
		buildView(t, ws)
	}
	for range 5 {
		ws.themeBrowserPage(-1)
		buildView(t, ws)
	}
}

// The browser has to render against the full bundled corpus, not just the
// three-theme fixture: the row window only actually engages when the list is
// longer than the screen.
func TestView_ThemeBrowserWithFullCorpus(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = append(
		[]term.NamedTheme{{Name: "Default", Theme: term.DefaultTheme}},
		term.BundledThemes()...,
	)
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.toggleThemeBrowser()

	buildView(t, ws)
	ws.themeBrowserMove(-1) // wrap to the very end of ~600 entries
	buildView(t, ws)
	ws.setThemeFilter("light:sol")
	buildView(t, ws)
}

// The view still builds after panes and tabs are torn down — the state most
// likely to leave a dangling reference behind.
func TestView_BuildsAfterTeardown(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	ws.addTab()
	buildView(t, ws)

	ws.closePane()
	buildView(t, ws)

	ws.closeTab()
	buildView(t, ws)

	// Down to the last tab: closing it yields a fresh replacement, which must
	// also render.
	ws.closeTab()
	buildView(t, ws)
}

// Toggling the overlays off again returns to a buildable plain view.
func TestView_OverlaysToggleBackOff(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))

	ws.toggleHelp()
	buildView(t, ws)
	ws.toggleHelp()
	buildView(t, ws)

	// Closing the browser must put the panes back in the tree — it replaces
	// them rather than floating over them, so a toggle that left `visible` set
	// would leave the terminal permanently hidden.
	ws.toggleThemeBrowser()
	buildView(t, ws)
	ws.toggleThemeBrowser()
	if ws.browser.visible {
		t.Fatal("browser still visible after toggling off")
	}
	buildView(t, ws)
}

// ---------------------------------------------------------------------------
// Row-window geometry
//
// browserVisibleRows takes its inputs from the window and the gui theme, so a
// degenerate value reaches it without passing through any validation of its
// own. Everything downstream slices the match list with the result.
// ---------------------------------------------------------------------------

func TestBrowserVisibleRows_DegenerateInputsStayInRange(t *testing.T) {
	t.Parallel()

	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	for _, tc := range []struct {
		name    string
		h, size float32
	}{
		{"NaN height", nan, 13},
		{"+Inf height", inf, 13},
		{"-Inf height", -inf, 13},
		{"zero height", 0, 13},
		{"negative height", -1000, 13},
		{"NaN size", 900, nan},
		{"zero size", 900, 0},
		{"negative size", 900, -5},
		{"subnormal size", 900, 1e-30},
		{"huge height", math.MaxFloat32, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := browserVisibleRows(tc.h, tc.size)
			if got < browserMinRows || got > browserMaxRows {
				t.Errorf("browserVisibleRows(%v, %v) = %d, want within [%d, %d]",
					tc.h, tc.size, got, browserMinRows, browserMaxRows)
			}
		})
	}
}

// A realistic window still gets a row count driven by the geometry rather than
// pinned to a clamp — otherwise the guards above would pass trivially.
func TestBrowserVisibleRows_TypicalWindow(t *testing.T) {
	t.Parallel()

	if got := browserVisibleRows(900, 13); got <= browserMinRows || got >= browserMaxRows {
		t.Errorf("browserVisibleRows(900, 13) = %d, want strictly between %d and %d",
			got, browserMinRows, browserMaxRows)
	}
}

// The browser must build against a window whose dimensions are not real
// numbers: View gets them from the platform, and a NaN would otherwise
// propagate into every Fixed size on the page.
func TestView_ThemeBrowserNonFiniteDimensions(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()

	for _, d := range []float32{float32(math.NaN()), float32(math.Inf(1)), 0, -400} {
		if v := ws.themeBrowserView(d, d); v == nil {
			t.Errorf("themeBrowserView(%v, %v) produced an empty view", d, d)
		}
	}
}
