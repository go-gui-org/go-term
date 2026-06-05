package session

import (
	"github.com/go-gui-org/go-gui/gui"
)

// emptyView returns a neutral FillFill placeholder for nil/empty trees.
func emptyView() gui.View {
	return gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill,
		Color:  gui.CurrentTheme().ContainerStyle.Color,
	})
}

// View returns the go-gui view tree for this session. Each leaf pane is
// wrapped in a container with a focus border; splits use Row/Column to
// arrange children. Call from the main thread via w.UpdateView(s.View).
func (s *Session) View(w *gui.Window) gui.View {
	s.mu.Lock()
	root := s.root
	focused := s.focused
	s.mu.Unlock()
	if root == nil {
		return emptyView()
	}
	return buildView(root, focused, w)
}

// buildView recursively builds the go-gui view tree from a SplitNode.
func buildView(node *SplitNode, focused *Pane, w *gui.Window) gui.View {
	if node == nil {
		return emptyView()
	}
	switch node.Type {
	case NodeLeaf:
		return wrapPane(node.Pane, node.Pane == focused, w)
	case NodeHorz:
		return gui.Row(gui.ContainerCfg{
			Sizing: gui.FillFill,
			Content: []gui.View{
				buildView(node.Left, focused, w),
				buildView(node.Right, focused, w),
			},
		})
	case NodeVert:
		return gui.Column(gui.ContainerCfg{
			Sizing: gui.FillFill,
			Content: []gui.View{
				buildView(node.Left, focused, w),
				buildView(node.Right, focused, w),
			},
		})
	}
	panic("session: unreachable node type")
}

// wrapPane wraps a single pane in a container with a focus border. The
// border is accent-colored when the pane has focus and transparent otherwise.
// IDFocus is set only on the focused pane so go-gui routes keystrokes to it
// and auto-focus-on-click covers border/padding clicks.
func wrapPane(pane *Pane, isFocused bool, w *gui.Window) gui.View {
	if pane == nil || pane.Term == nil {
		return emptyView()
	}
	cfg := gui.ContainerCfg{
		SizeBorder:  gui.SomeF(2),
		ColorBorder: borderColor(pane, isFocused),
		Padding:     gui.Some(gui.PaddingOne),
		Sizing:      gui.FillFill,
		Content:     []gui.View{pane.Term.View(w)},
	}
	if isFocused {
		cfg.IDFocus = pane.Term.FocusID()
	}
	return gui.Column(cfg)
}

// borderColor returns the border color for a pane wrapper. Focused panes
// get the terminal's default foreground at 65% opacity; unfocused panes
// get a transparent border (invisible).
func borderColor(pane *Pane, focused bool) gui.Color {
	if !focused {
		return gui.ColorTransparent
	}
	th := pane.Term.Theme()
	return th.DefaultFG.WithOpacity(0.65)
}
