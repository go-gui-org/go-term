package term

import (
	"reflect"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// viewAcceptsTextInput reads ContainerCfg.AcceptsTextInput back out of the
// View that Term.View returns. go-gui keeps the cfg unexported and offers no
// getter, so reflect reads it. Bool() is allowed on an unexported field; only
// Interface() is not.
func viewAcceptsTextInput(t *testing.T, v gui.View) bool {
	t.Helper()
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	f := rv.FieldByName("cfg").FieldByName("AcceptsTextInput")
	if !f.IsValid() {
		t.Fatalf("View %T has no cfg.AcceptsTextInput; go-gui layout changed", v)
	}
	return f.Bool()
}

// A focused pane must opt in to the platform input method. go-term draws on a
// DrawCanvas and holds focus on a container with no TextChunk, so go-gui starts
// the IME only when that container sets AcceptsTextInput (go-gui#999). Without
// it, CJK and other composing input methods cannot type into the terminal.
func TestTerm_View_FocusedAcceptsTextInput(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	if !viewAcceptsTextInput(t, term.View(win)) {
		t.Error("focused pane: AcceptsTextInput = false, want true")
	}

	// An unfocused pane is not focusable and holds no focus ID, so it must
	// not claim the input method either.
	term.SetFocused(false)
	if viewAcceptsTextInput(t, term.View(win)) {
		t.Error("unfocused pane: AcceptsTextInput = true, want false")
	}
}
