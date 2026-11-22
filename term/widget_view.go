package term

import "github.com/go-gui-org/go-gui/gui"

// View returns the go-gui view tree for this terminal. Usable as a
// gui.Window SetView generator: w.SetView(t.View).
//
// The tree is built when go-gui generates the node, not when View is called.
// The focus reassert needs the pane's effective ID, and only generation knows
// the ID scope the pane lands in. A caller that builds a parent's Content
// eagerly (tm.View(w) inside a Column literal, as Workspace does) calls View
// before go-gui enters that parent, so resolving there would use the wrong
// scope (go-gui#1284).
//
// No allocation and no write: the node is the Term itself under another
// method set, so View stays safe to call from any goroutine.
func (t *Term) View(_ *gui.Window) gui.View {
	return (*termView)(t)
}

// termView defers Term's view build to layout generation. See [Term.View].
// A defined type over Term rather than a wrapper struct, so converting a
// *Term to it costs nothing.
type termView Term

// GenerateLayout builds the pane's tree under the scope go-gui is generating.
func (v *termView) GenerateLayout(w *gui.Window) gui.Layout {
	return gui.GenerateViewLayout((*Term)(v).columnView(w), w)
}

// columnView builds the pane's container. termView.GenerateLayout calls it
// during generation, where w.EffID resolves against the pane's scope. Tests
// call it directly to inspect the ContainerCfg; there the scope is empty and
// the effective ID is the leaf.
func (t *Term) columnView(w *gui.Window) gui.View {
	// Detect IME composition state changes and bump version to redraw.
	// Composition state is window-global, but only the focused pane may
	// render it — otherwise every Term in a split paints the same preedit
	// strip at its own cursor. An unfocused pane must also *clear* state it
	// cached before losing focus (focus can change mid-composition), so read
	// zero values rather than returning early: the change detection below
	// then clears the cache and repaints exactly once.
	var (
		composing  bool
		compText   string
		compCursor int
	)
	if t.focused.Load() && w != nil {
		composing = w.IMEComposing()
		compText = w.IMECompText()
		compCursor = w.IMECompCursor()
	}
	if composing != t.ime.composing || compText != t.ime.compText || compCursor != t.ime.compCursor {
		t.ime.composing = composing
		t.ime.compText = compText
		t.ime.compCursor = compCursor
		t.bumpVersion()
	}

	// Snapshot theme default-bg under the lock so a concurrent SetTheme
	// does not race with this read. The rest of View() is lock-free.
	// defaultBG (not Theme.DefaultBG) so DECSCNM flips the canvas fill too —
	// fillRun skips runs matching it, so the two must agree.
	t.grid.Mu.Lock()
	bgColor := t.grid.defaultBG()
	t.grid.Mu.Unlock()
	canvas := gui.DrawCanvas(gui.DrawCanvasCfg{
		ID:      t.canvasID,
		Version: t.drawVersion.Load(),
		// VersionFn lets a render-only frame repaint the canvas. Version is
		// stamped here at view time, which such a frame skips, so only the
		// live load reaches the cache; blinkTick depends on it. Safe because
		// OnDraw reads the same live Term state rather than values this view
		// captured, and an atomic load is cheap enough for the window lock.
		VersionFn: t.drawVersion.Load,
		Sizing:    gui.FillFill,
		// Clip is mandatory, not cosmetic. Smooth scrolling draws the
		// partial top row at y = -cellH + ViewSubPx — i.e. *above* the
		// canvas — and shifts the bottom row past dc.Height by ViewSubPx.
		// go-gui only scissors a DrawCanvas when Clip is set, so without
		// this those rows paint over whatever is adjacent: the workspace
		// tab bar above, or the neighboring pane in a split.
		Clip:          true,
		OnDraw:        t.onDraw,
		OnMouseScroll: t.onMouseScroll,
		// OnAnyClick, not OnClick: a canvas's OnClick runs on a left press
		// only. The child needs right and middle presses too (right-click
		// menus, middle-button reports), and middle-click paste needs the
		// middle press. onClick sorts the buttons itself.
		OnAnyClick:  t.onClick,
		OnMouseMove: t.onMouseMove,
		OnMouseUp:   t.onMouseUp,
	})
	colCfg := gui.ContainerCfg{
		Padding:     gui.PadAll(gridPadding),
		Spacing:     gui.NoSpacing,
		Color:       bgColor,
		OnChar:      t.onChar,
		OnKeyDown:   t.onKeyDown,
		OnKeyUp:     t.onKeyUp,
		AmendLayout: t.onAmendLayout,
		Content:     []gui.View{canvas},
	}
	if t.focused.Load() {
		colCfg.ID = t.focusID
		colCfg.Focusable = true
		// The pane draws on a DrawCanvas, so go-gui sees no TextChunk and would
		// not start the platform input method for it. This flag opts in
		// (go-gui#999): CJK composition then works, preedit reaches drawIME
		// through Window.IMECompText, and the commit arrives in onChar.
		colCfg.AcceptsTextInput = true
		// SetView → clearViewStateLocked clears the window's
		// focus ID. Reassert after every full layout rebuild so
		// keystrokes reach onChar/onKeyDown without requiring a
		// prior click. Skip while a modal dialog is up: go-gui routes
		// keys to the dialog layer, and re-asserting here would steal
		// focus back to the terminal, breaking Tab/Esc/Enter in the
		// dialog. DialogDismiss restores focus to this pane on close.
		//
		// Only assert when the ID is not already ours: SetFocus takes the
		// window lock and re-activates the platform input context, and View()
		// runs on every layout rebuild — i.e. every keystroke. Re-asserting
		// unconditionally is what surfaced go-gui#157, where SetFocus was not
		// idempotent and cleared the IME preedit mid-composition.
		//
		// Compared and set with the effective ID. The bare leaf matched no
		// shape under an ID-bearing ancestor (a TabControl): the guard fired on
		// every rebuild, focus went to a dead ID, and go-gui's repair moved it
		// to the first tab stop, so keys never reached the pane (go-gui#1284).
		t.effFocusID = w.EffID(t.focusID)
		if !w.DialogIsVisible() && w.FocusID() != t.effFocusID {
			w.SetFocus(t.effFocusID)
		}
	}
	// FillFill without explicit Width/Height: the Term may be embedded
	// in a multi-pane layout where the parent container dictates
	// dimensions. Using w.WindowSize() here would overflow the pane.
	// Theme selection is the embedder's business (e.g. Workspace's theme
	// browser) — the pane generates a plain Column so the embedder controls
	// the wrapping.
	colCfg.Sizing = gui.FillFill
	return gui.Column(colCfg)
}
