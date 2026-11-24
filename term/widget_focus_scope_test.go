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

// focusRig is a Term beside a text Input under one ID-bearing container: the
// shape of the #323 report (sidebar inputs next to a TabControl of panes).
// keys collects what reached the child through the pane; text is the Input's
// value as its OnTextChanged reports it.
type focusRig struct {
	win  *gui.Window
	term *Term
	mu   sync.Mutex
	keys []byte
	text string
}

func newFocusRig(t *testing.T) *focusRig {
	t.Helper()
	r := &focusRig{win: gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})}
	term, err := New(r.win, Cfg{
		OnInput: func(b []byte, _ InputKind) {
			r.mu.Lock()
			r.keys = append(r.keys, b...)
			r.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	r.term = term
	view := func(w *gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{
			ID:     "outer",
			Sizing: gui.FillFill,
			Content: []gui.View{
				gui.Input(gui.InputCfg{
					ID:     "in",
					Text:   r.text,
					Sizing: gui.FillFit,
					OnTextChanged: func(s string, ctx gui.EventCtx) {
						ctx.Consume()
						r.text = s
					},
				}),
				term.View(w),
			},
		})
	}
	// Two frames: the first stamps the effective ID, the second lets the pane
	// see that it holds focus.
	r.win.TestRender(view)
	r.win.TestRender(nil)
	if got, want := r.win.FocusID(), r.paneID(); got != want {
		t.Fatalf("setup: FocusID = %q, want pane %q", got, want)
	}
	return r
}

func (r *focusRig) paneID() string  { return gui.ScopeID("outer", r.term.FocusID()) }
func (r *focusRig) inputID() string { return gui.ScopeID("outer", "in") }

// frames runs n rebuilds, the way every keystroke and redraw does.
func (r *focusRig) frames(n int) {
	for range n {
		r.win.TestRender(nil)
	}
}

func (r *focusRig) typeChar(c rune) {
	r.win.EventFn(&gui.Event{Type: gui.EventChar, CharCode: uint32(c)})
}

func (r *focusRig) paneKeys() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.keys)
}

// #323: a click on an Input beside a focused pane must leave focus on the Input.
// View reasserted the pane's focus on every rebuild whenever the window's focus
// ID was not the pane's, so the very next frame took focus back and the Input
// never got a keystroke.
func TestTerm_View_YieldsFocusToClickedInput(t *testing.T) {
	r := newFocusRig(t)
	if err := r.win.TestClick(r.inputID()); err != nil {
		t.Fatalf("TestClick input: %v", err)
	}
	r.frames(3)
	if got, want := r.win.FocusID(), r.inputID(); got != want {
		t.Fatalf("after clicking input and rebuilding, FocusID = %q, want %q", got, want)
	}
	r.typeChar('x')
	r.frames(1)
	if r.text != "x" {
		t.Errorf("input text = %q, want %q", r.text, "x")
	}
	if got := r.paneKeys(); got != "" {
		t.Errorf("pane received %q, want nothing", got)
	}
	// A pane that gave the keyboard away must look like it: dimmed cursor, no
	// blink, completion notifications on.
	if r.term.ownsKeys() {
		t.Error("ownsKeys = true after yielding focus to the input")
	}
}

// A click back on the pane takes focus back: go-gui's press-focus does it, so
// the pane's column must stay focusable while it has yielded.
func TestTerm_View_ReclaimsFocusOnPaneClick(t *testing.T) {
	r := newFocusRig(t)
	if err := r.win.TestClick(r.inputID()); err != nil {
		t.Fatalf("TestClick input: %v", err)
	}
	r.frames(2)
	if err := r.win.TestClick(r.paneID()); err != nil {
		t.Fatalf("TestClick pane: %v", err)
	}
	r.frames(2)
	if got, want := r.win.FocusID(), r.paneID(); got != want {
		t.Fatalf("after clicking pane, FocusID = %q, want %q", got, want)
	}
	if !r.term.ownsKeys() {
		t.Error("ownsKeys = false after the pane took focus back")
	}
	r.typeChar('y')
	if got := r.paneKeys(); got != "y" {
		t.Errorf("pane received %q, want %q", got, "y")
	}
}

// An embedder that focuses the pane explicitly (a tab switch) overrides a
// yield: SetFocused(false) then (true) must bring keys back without a click.
func TestTerm_SetFocused_OverridesYield(t *testing.T) {
	r := newFocusRig(t)
	if err := r.win.TestClick(r.inputID()); err != nil {
		t.Fatalf("TestClick input: %v", err)
	}
	r.frames(2)
	r.term.cmd = winScheduler{w: r.win}
	r.term.SetFocused(false)
	r.term.SetFocused(true)
	r.frames(2)
	if got, want := r.win.FocusID(), r.paneID(); got != want {
		t.Fatalf("after SetFocused(true), FocusID = %q, want %q", got, want)
	}
	if !r.term.ownsKeys() {
		t.Error("ownsKeys = false after SetFocused(true)")
	}
}

// Focus cleared to nothing (SetView wipes it) is not a yield: the pane must
// take it back on the next rebuild, with no click.
func TestTerm_View_ReclaimsAfterClearedFocus(t *testing.T) {
	r := newFocusRig(t)
	r.win.ClearFocus()
	r.frames(2)
	if got, want := r.win.FocusID(), r.paneID(); got != want {
		t.Fatalf("after ClearFocus, FocusID = %q, want %q", got, want)
	}
}
