package term

import (
	"sync"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// A Term inside a container with an explicit ID must still take keyboard input
// (go-gui#1284). The ID opens a scope, so the pane's focus container resolves to
// "outer:term-N", not "term-N". View used to reassert focus with the bare leaf:
// that ID matched no shape, go-gui's focus repair moved focus to the first tab
// stop, and the next rebuild did it again. Keys never reached the pane.
//
// TabControl is the case the reporter hit; any ID-bearing ancestor shows it.
// Real frames are needed: the effective ID is stamped during generation, and
// the repair runs after arrange.
func TestTerm_View_FocusUnderScopedAncestor(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})

	// OnInput sees every byte the pane writes to the child, so it proves the
	// key reached onChar. Guarded: the pty reader goroutine runs alongside.
	var (
		mu  sync.Mutex
		got []byte
	)
	term, err := New(win, Cfg{
		OnInput: func(b []byte, _ InputKind) {
			mu.Lock()
			got = append(got, b...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	view := func(w *gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{
			ID:     "outer",
			Sizing: gui.FillFill,
			Content: []gui.View{
				// A focusable sibling ahead of the pane in tab order: the
				// focus repair lands here when the pane's focus ID is dead.
				gui.Button(gui.ButtonCfg{ID: "before"}),
				term.View(w),
			},
		})
	}
	// Two frames: the first stamps the effective ID, the second is where a
	// leaf reassert would lose focus again to the repair.
	win.TestRender(view)
	win.TestRender(nil)

	want := gui.ScopeID("outer", term.FocusID())
	if got := win.FocusID(); got != want {
		t.Fatalf("FocusID = %q, want %q", got, want)
	}
	if found := win.TestFindings(gui.DebugUnknownFocus); len(found) > 0 {
		t.Errorf("unknown focus findings: %v", found)
	}

	// A char event with no explicit focus step: it goes where the frame left
	// focus, the way a real keystroke does.
	win.EventFn(&gui.Event{Type: gui.EventChar, CharCode: 'x'})
	mu.Lock()
	defer mu.Unlock()
	if string(got) != "x" {
		t.Errorf("pane input = %q, want %q", got, "x")
	}
}

// winScheduler runs a queued command at once against one real window, so a
// test can see what the command did before any frame rebuilds.
type winScheduler struct{ w *gui.Window }

func (s winScheduler) QueueCommand(fn func(*gui.Window)) { fn(s.w) }

// SetFocused must also focus the effective ID (go-gui#1284). Its queued
// SetFocus used the bare leaf, which no shape claims under a scope. The next
// View reassert hid that only when a rebuild followed, so the command is run
// here with no frame after it.
func TestTerm_SetFocused_UsesEffectiveID(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	win.TestRender(func(w *gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{
			ID:      "outer",
			Sizing:  gui.FillFill,
			Content: []gui.View{term.View(w)},
		})
	})
	term.SetFocused(false)
	win.ClearFocus()

	term.cmd = winScheduler{w: win}
	term.SetFocused(true)
	want := gui.ScopeID("outer", term.FocusID())
	if got := win.FocusID(); got != want {
		t.Errorf("after SetFocused(true), FocusID = %q, want %q", got, want)
	}
}
