package session

import (
	"testing"
)

func TestPane_Title_ZeroValue(t *testing.T) {
	p := &Pane{}
	if s := p.Title(); s != "" {
		t.Errorf("Title() = %q, want empty string for zero-value Pane", s)
	}
}

func TestPane_Title_Stored(t *testing.T) {
	p := &Pane{}
	title := "hello world"
	p.title.Store(&title)
	if s := p.Title(); s != title {
		t.Errorf("Title() = %q, want %q", s, title)
	}
}

func TestPane_Title_Overwrite(t *testing.T) {
	p := &Pane{}
	first := "first"
	p.title.Store(&first)
	second := "second"
	p.title.Store(&second)
	if s := p.Title(); s != second {
		t.Errorf("Title() = %q after overwrite, want %q", s, second)
	}
}

func TestPane_Title_Concurrent(t *testing.T) {
	// Verify atomic access — store from one goroutine, load from another.
	p := &Pane{}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s := "title"
			p.title.Store(&s)
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = p.Title() // must not race
	}
	<-done
}

func TestSplitNode_NewPaneNode_NilPane(t *testing.T) {
	// NewPaneNode with nil pane creates a leaf with nil Pane — valid
	// for use as a placeholder, but the caller must ensure it's
	// populated before use.
	n := NewPaneNode(nil)
	if n == nil {
		t.Fatal("NewPaneNode returned nil")
	}
	if n.Type != NodeLeaf {
		t.Errorf("Type = %d, want NodeLeaf", n.Type)
	}
	// Accessing n.Pane directly would be nil — that's the caller's
	// responsibility.
	if n.Pane != nil {
		t.Error("NewPaneNode(nil).Pane should be nil")
	}
}
