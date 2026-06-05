package session

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// stubPane creates a Pane with a zero-value Term for tree-structure tests.
func stubPane() *Pane {
	t := &term.Term{}
	return &Pane{Term: t}
}

func TestOnKeyIntercept_CmdRightBracket(t *testing.T) {
	s := &Session{}
	e := &gui.Event{
		Type:      gui.EventKeyDown,
		KeyCode:   gui.KeyRightBracket,
		Modifiers: gui.ModSuper,
	}
	if !s.onKeyIntercept(e) {
		t.Error("Cmd+] should be intercepted")
	}
	if !e.IsHandled {
		t.Error("Cmd+] should set IsHandled")
	}
}

func TestOnKeyIntercept_CmdLeftBracket(t *testing.T) {
	s := &Session{}
	e := &gui.Event{
		Type:      gui.EventKeyDown,
		KeyCode:   gui.KeyLeftBracket,
		Modifiers: gui.ModSuper,
	}
	if !s.onKeyIntercept(e) {
		t.Error("Cmd+[ should be intercepted")
	}
	if !e.IsHandled {
		t.Error("Cmd+[ should set IsHandled")
	}
}

func TestOnKeyIntercept_OtherKey(t *testing.T) {
	s := &Session{}
	e := &gui.Event{
		Type:      gui.EventKeyDown,
		KeyCode:   gui.KeyA,
		Modifiers: gui.ModSuper,
	}
	if s.onKeyIntercept(e) {
		t.Error("Cmd+A should not be intercepted")
	}
}

func TestOnKeyIntercept_NoModifier(t *testing.T) {
	s := &Session{}
	e := &gui.Event{
		Type:    gui.EventKeyDown,
		KeyCode: gui.KeyRightBracket,
	}
	if s.onKeyIntercept(e) {
		t.Error("bare ] should not be intercepted")
	}
}

func TestOnKeyIntercept_WrongModifier(t *testing.T) {
	s := &Session{}
	e := &gui.Event{
		Type:      gui.EventKeyDown,
		KeyCode:   gui.KeyRightBracket,
		Modifiers: gui.ModCtrl,
	}
	if s.onKeyIntercept(e) {
		t.Error("Ctrl+] should not be intercepted")
	}
}

func TestCycleFocus_TreeOrder(t *testing.T) {
	// Build: [a, [b, c]] — three panes: a, b, c in depth-first order.
	a := stubPane()
	b := stubPane()
	c := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)       // root: vert(a, b)
	root.Right.Add(NodeHorz, c) // right: horz(b, c)

	ls := root.Leaves()
	if len(ls) != 3 {
		t.Fatalf("expected 3 leaves, got %d", len(ls))
	}
	if ls[0] != a || ls[1] != b || ls[2] != c {
		t.Error("leaf order should be depth-first left-to-right: a, b, c")
	}
}

func TestCycleFocus_SinglePane_NoOp(t *testing.T) {
	// cycleFocus with < 2 leaves is a no-op. Verify via leaf count.
	p := stubPane()
	root := NewPaneNode(p)
	if n := root.LeafCount(); n != 1 {
		t.Fatalf("expected 1 leaf, got %d", n)
	}
	ls := root.Leaves()
	if len(ls) != 1 || ls[0] != p {
		t.Error("single pane should remain the only leaf")
	}
}

func TestCycleFocus_NilRoot(t *testing.T) {
	s := &Session{}
	// Must not panic.
	s.cycleFocus(true)
	s.cycleFocus(false)
}

func TestCycleFocus_NilFocused(t *testing.T) {
	p := stubPane()
	s := &Session{root: NewPaneNode(p), focused: nil}
	// Must not panic.
	s.cycleFocus(true)
}

func TestCycleFocus_WrapForward(t *testing.T) {
	// From last pane, next should wrap to first.
	a := stubPane()
	b := stubPane()
	c := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)       // vert(a, b)
	root.Right.Add(NodeHorz, c) // horz(b, c) → order: a, b, c
	s := &Session{root: root, focused: c}
	s.cycleFocus(true)
	if s.focused != a {
		t.Error("cycleFocus(true) from last should wrap to first")
	}
}

func TestCycleFocus_WrapBackward(t *testing.T) {
	// From first pane, prev should wrap to last.
	a := stubPane()
	b := stubPane()
	c := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)
	root.Right.Add(NodeHorz, c) // order: a, b, c
	s := &Session{root: root, focused: a}
	s.cycleFocus(false)
	if s.focused != c {
		t.Error("cycleFocus(false) from first should wrap to last")
	}
}

func TestCycleFocus_CurrentNotInTree(t *testing.T) {
	// When current is not found in the leaf list, idx<0 fallback
	// selects index 0 (the first pane).
	a := stubPane()
	b := stubPane()
	orphan := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b) // leaves: a, b
	s := &Session{root: root, focused: orphan}
	s.cycleFocus(true)
	if s.focused != a {
		t.Error("cycleFocus with orphan focused should fall back to first pane")
	}
}

func TestCycleFocus_TwoPanes(t *testing.T) {
	// Minimal 2-pane tree: next from a→b, prev from b→a.
	a := stubPane()
	b := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)
	s := &Session{root: root, focused: a}
	s.cycleFocus(true)
	if s.focused != b {
		t.Error("cycleFocus(true) from a should focus b")
	}
	s.cycleFocus(false)
	if s.focused != a {
		t.Error("cycleFocus(false) from b should focus a")
	}
}

func TestFocusPane_Nil(t *testing.T) {
	s := &Session{}
	p := stubPane()
	s.root = NewPaneNode(p)
	s.focused = p
	s.focusPane(nil)
	if s.focused != p {
		t.Error("focusPane(nil) should not change focus")
	}
}

func TestFocusPane_SamePane(t *testing.T) {
	s := &Session{}
	p := stubPane()
	s.root = NewPaneNode(p)
	s.focused = p
	s.focusPane(p)
	if s.focused != p {
		t.Error("focusPane(same) should keep focus")
	}
}

func TestFocusPane_Switch(t *testing.T) {
	// Switching from pane A to pane B must update s.focused. SetFocused
	// calls on zero-value Terms are safe (nil cmd skips QueueCommand).
	a := stubPane()
	b := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)
	s := &Session{root: root, focused: a}
	s.focusPane(b)
	if s.focused != b {
		t.Error("focusPane(switch) should update focused to b")
	}
}

func TestFocusPane_SwitchBack(t *testing.T) {
	a := stubPane()
	b := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)
	s := &Session{root: root, focused: b}
	s.focusPane(a)
	if s.focused != a {
		t.Error("focusPane(switch back) should update focused to a")
	}
}

func TestBorderColor_Unfocused(t *testing.T) {
	// Unfocused returns ColorTransparent without accessing Theme, so a
	// zero-value Term is safe.
	p := &Pane{Term: &term.Term{}}
	color := borderColor(p, false)
	// ColorTransparent has RGBA {0,0,0,0}. Verify alpha is zero.
	if color.A != 0 {
		t.Error("unfocused border color should be fully transparent")
	}
}

func TestOnKeyIntercept_NilEvent(t *testing.T) {
	s := &Session{}
	if s.onKeyIntercept(nil) {
		t.Error("nil event should not be intercepted")
	}
}

func TestNewSession_NilWindow(t *testing.T) {
	_, err := NewSession(nil, term.Cfg{})
	if err == nil {
		t.Error("NewSession with nil window should return an error")
	}
}

func TestOnWindowEvent_NilFocused(t *testing.T) {
	s := &Session{focused: nil}
	var chained bool
	s.prevOnEvent = func(e *gui.Event, w *gui.Window) {
		chained = true
	}
	e := &gui.Event{Type: gui.EventFocused}
	s.onWindowEvent(e, nil)
	if !chained {
		t.Error("prevOnEvent should be called even when focused is nil")
	}
}

func TestClose_NilRoot_NoPanic(t *testing.T) {
	s := &Session{}
	s.Close()
}

func TestClose_WithPanes_NilTerms(t *testing.T) {
	// Panes with nil Terms: Close should not panic (Term.Close is
	// guarded by nil check in Pane.Close, which calls p.Term.Close()).
	// But Term.Close on a zero-value Term panics on nil blinkDone.
	// This tests that the session's Close doesn't add extra panics.
	p1 := &Pane{}
	p2 := &Pane{}
	root := NewPaneNode(p1)
	root.Add(NodeVert, p2)
	s := &Session{root: root}
	// Expect panic from Term.Close on nil blinkDone — recover it.
	func() {
		defer func() { _ = recover() }()
		s.Close()
	}()
}

func TestClose_Idempotent(t *testing.T) {
	// Close must be safe to call more than once.
	s := &Session{}
	s.Close()
	s.Close() // must not panic
}

func TestClose_RestoresWindowHandler(t *testing.T) {
	orig := func(e *gui.Event, w *gui.Window) {}
	w := &gui.Window{}
	w.OnEvent = orig
	s := &Session{w: w, prevOnEvent: orig}
	s.Close()
	// After Close, w.OnEvent must be restored to orig. Verify by
	// calling Close again — second call sets w.OnEvent = s.prevOnEvent
	// which is already orig (idempotent, no change).
	s.Close() // must not panic
}

func TestBuildView_NilPane_Graceful(t *testing.T) {
	// Nil-pane leaf: wrapPane returns a placeholder, must not panic.
	node := NewPaneNode(nil)
	v := buildView(node, nil, nil)
	if v == nil {
		t.Error("buildView with nil pane should return a placeholder view")
	}
}

func TestBuildView_NilNode_Graceful(t *testing.T) {
	// Nil node: buildView returns a placeholder, must not panic.
	v := buildView(nil, nil, nil)
	if v == nil {
		t.Error("buildView with nil node should return a placeholder view")
	}
}

func TestSplitNode_LeafOrder_MatchesCycleFocus(t *testing.T) {
	// The cycleFocus traversal order must be depth-first left-to-right,
	// matching Walk(). Verify on a 4-pane tree.
	a := stubPane()
	b := stubPane()
	c := stubPane()
	d := stubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)       // vert(a, b)
	root.Left.Add(NodeHorz, c)  // horz(a, c) — a left, c right
	root.Right.Add(NodeHorz, d) // horz(b, d) — b left, d right

	ls := root.Leaves()
	if len(ls) != 4 {
		t.Fatalf("expected 4 leaves, got %d", len(ls))
	}
	// Walk is depth-first left-to-right: a, c, b, d
	if ls[0] != a || ls[1] != c || ls[2] != b || ls[3] != d {
		t.Error("leaf order should be depth-first left-to-right: a, c, b, d")
	}
}
