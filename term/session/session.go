package session

import (
	"errors"
	"sync"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Session manages a tree of terminal panes within a single window. It owns
// the split-tree root, tracks the focused pane, and routes window-level
// events (focus, key intercepts) to the appropriate Term.
type Session struct {
	w       *gui.Window
	root    *SplitNode
	focused *Pane

	prevOnEvent func(*gui.Event, *gui.Window)
	mu          sync.Mutex
}

// NewSession creates a session with a single full-window pane running the
// shell configured by cfg. Installs a window-level event handler since all
// panes use NoWindowHandler.
func NewSession(w *gui.Window, cfg term.Cfg) (*Session, error) {
	if w == nil {
		return nil, errors.New("session: window must not be nil")
	}
	s := &Session{w: w}
	p, err := s.createPane(cfg)
	if err != nil {
		return nil, err
	}
	s.root = NewPaneNode(p)
	s.focused = p
	s.prevOnEvent = w.OnEvent
	w.OnEvent = s.onWindowEvent
	return s, nil
}

// createPane wraps NewPane with session-specific wiring: OnKeyIntercept for
// keyboard shortcuts (chaining any caller-provided handler) and
// OnFocusRequest for click-to-focus.
func (s *Session) createPane(cfg term.Cfg) (*Pane, error) {
	prevIntercept := cfg.OnKeyIntercept
	cfg.OnKeyIntercept = func(e *gui.Event) bool {
		if prevIntercept != nil && prevIntercept(e) {
			return true
		}
		return s.onKeyIntercept(e)
	}
	p, err := NewPane(s.w, cfg)
	if err != nil {
		return nil, err
	}
	p.Term.SetOnFocusRequest(func() { s.focusPane(p) })
	return p, nil
}

// focusPane switches focus to the given pane. No-op if already focused or
// nil. Safe to call from the main thread.
func (s *Session) focusPane(pane *Pane) {
	if pane == nil {
		return
	}
	s.mu.Lock()
	if s.focused == pane {
		s.mu.Unlock()
		return
	}
	prev := s.focused
	s.focused = pane
	s.mu.Unlock()

	if prev != nil {
		prev.Term.SetFocused(false)
	}
	pane.Term.SetFocused(true)
	s.syncFocus()
	s.rebuildView()
}

// syncFocus ensures the window's IDFocus matches the session's focused pane.
func (s *Session) syncFocus() {
	if s.w != nil && s.focused != nil {
		s.w.SetIDFocus(s.focused.Term.FocusID())
	}
}

// rebuildView triggers a full view-tree rebuild via the window.
func (s *Session) rebuildView() {
	if s.w != nil {
		s.w.UpdateView(s.View)
	}
}

// cycleFocus moves focus to the next (or previous) pane in depth-first leaf
// order. Wraps at the ends. No-op when there are fewer than 2 panes.
func (s *Session) cycleFocus(next bool) {
	s.mu.Lock()
	root := s.root
	current := s.focused
	s.mu.Unlock()
	if root == nil || current == nil {
		return
	}

	leaves := root.Leaves()
	if len(leaves) < 2 {
		return
	}

	idx := -1
	for i, p := range leaves {
		if p == current {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Current not in tree (e.g. orphaned by removal). Default
		// to the first pane rather than advancing from a fake index.
		s.focusPane(leaves[0])
		return
	}
	if next {
		idx = (idx + 1) % len(leaves)
	} else {
		idx = (idx - 1 + len(leaves)) % len(leaves)
	}
	s.focusPane(leaves[idx])
}

// onKeyIntercept is the OnKeyIntercept callback installed on every pane's
// Term. It consumes session-level keyboard shortcuts (Cmd+] / Cmd+[ for
// focus cycling) and lets everything else through to the PTY.
func (s *Session) onKeyIntercept(e *gui.Event) bool {
	if e == nil {
		return false
	}
	if !e.Modifiers.Has(gui.ModSuper) {
		return false
	}
	switch e.KeyCode {
	case gui.KeyRightBracket: // Cmd+]
		s.cycleFocus(true)
		e.IsHandled = true
		return true
	case gui.KeyLeftBracket: // Cmd+[
		s.cycleFocus(false)
		e.IsHandled = true
		return true
	default:
		return false
	}
}

// onWindowEvent routes window-level events to the focused pane for focus-
// reporting sequences and momentum cancellation, then chains to the previous
// window event handler.
func (s *Session) onWindowEvent(e *gui.Event, w *gui.Window) {
	s.mu.Lock()
	focused := s.focused
	s.mu.Unlock()

	if focused != nil {
		focused.Term.HandleWindowEvent(e)
	}
	if s.prevOnEvent != nil {
		s.prevOnEvent(e, w)
	}
}

// Close tears down all panes and restores the original window event handler.
// Safe to call more than once. Call from the main thread.
func (s *Session) Close() {
	if s.w != nil {
		s.w.OnEvent = s.prevOnEvent
	}
	s.mu.Lock()
	root := s.root
	s.mu.Unlock()
	if root != nil {
		for _, p := range root.Leaves() {
			_ = p.Close()
		}
	}
}
