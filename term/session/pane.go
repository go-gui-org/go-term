package session

import (
	"sync/atomic"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Pane owns a single terminal emulator bound to a window. Created with
// NoWindowHandler: true so the session layer controls event dispatch and
// focus routing.
type Pane struct {
	Term *term.Term

	title atomic.Pointer[string] // *string set by OnTitle callback
}

// Title returns the last OSC 0/1/2 title captured via OnTitle. Safe for any
// goroutine — the underlying store is atomic.
func (p *Pane) Title() string {
	if s := p.title.Load(); s != nil {
		return *s
	}
	return ""
}

// NewPane creates a pane with NoWindowHandler: true and wires the OnTitle
// handler to capture tab titles per-pane. The original OnTitle (if any) is
// chained, so the application layer still receives title updates.
func NewPane(w *gui.Window, cfg term.Cfg) (*Pane, error) {
	cfg.NoWindowHandler = true
	p := &Pane{}
	prevTitle := cfg.OnTitle
	cfg.OnTitle = func(title string) {
		p.title.Store(&title)
		if prevTitle != nil {
			prevTitle(title)
		}
	}
	t, err := term.New(w, cfg)
	if err != nil {
		return nil, err
	}
	p.Term = t
	return p, nil
}

// Close tears down the underlying Term. Idempotent; safe to call more than
// once.
func (p *Pane) Close() error { return p.Term.Close() }
