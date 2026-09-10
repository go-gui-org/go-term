package workspace

import "github.com/go-gui-org/go-gui/gui"

// Chrome shared by the modal overlays — the help sheet and the command
// palette. Both are a dimming backdrop under a centered floating panel, and
// they were written twice: the same scrim color, the same z-order, the same
// window-edge guard, the same click-swallowing panel. Two overlays that look
// alike by accident drift apart on the first change to either, so the shape
// lives here and each overlay supplies only what is actually its own — the
// dismiss action, the padding, the content.

const (
	// overlayBackdropZ / overlayPanelZ keep a panel above its own backdrop.
	// The broadcast pill sits below both at 998, so an overlay covers it.
	overlayBackdropZ = 999
	overlayPanelZ    = 1000

	// overlayEdgePx is the window-border band where a click is read as the
	// start of a resize drag rather than as a dismiss. See overlayBackdrop.
	overlayEdgePx = float32(30)
)

// overlayScrim dims the panes behind an overlay: enough to push them back
// without hiding what the overlay is covering.
var overlayScrim = gui.RGBA(0, 0, 0, 120)

// overlayBackdrop is a window-sized translucent float behind a panel. It dims
// the panes and calls dismiss when clicked.
func overlayBackdrop(ww, wh int, dismiss func()) gui.View {
	b := tight(gui.FixedFixed)
	b.Width = float32(ww)
	b.Height = float32(wh)
	b.Float = true
	b.FloatAnchor = gui.FloatTopLeft
	b.FloatTieOff = gui.FloatTopLeft
	b.FloatZIndex = overlayBackdropZ
	b.Color = overlayScrim
	b.OnClick = func(ctx gui.EventCtx) {
		// Ignore clicks near the window edges — the platform (notably macOS)
		// dispatches MouseDown to the content view even when the user is
		// starting a window-resize drag at a corner or edge. Without this
		// guard the overlay would dismiss on the first touch of a resize
		// instead of on an intentional click-outside-to-dismiss.
		if nearWindowEdge(ctx, ww, wh) {
			return
		}
		dismiss()
		// The dismiss is the whole click; nothing behind the backdrop
		// should also see it.
		ctx.Consume()
	}
	return gui.Column(b)
}

// nearWindowEdge reports whether a click landed in the resize band around the
// window border.
func nearWindowEdge(ctx gui.EventCtx, ww, wh int) bool {
	return ctx.Event.MouseX < overlayEdgePx || ctx.Event.MouseX > float32(ww)-overlayEdgePx ||
		ctx.Event.MouseY < overlayEdgePx || ctx.Event.MouseY > float32(wh)-overlayEdgePx
}

// overlayPanel is the centered floating card an overlay draws its content in.
// The caller owns Padding, Spacing and Content; everything returned here is
// the part both overlays must keep identical.
func overlayPanel(theme gui.Theme) gui.ContainerCfg {
	panel := tight(gui.FitFit)
	panel.Float = true
	panel.FloatAnchor = gui.FloatMiddleCenter
	panel.FloatTieOff = gui.FloatMiddleCenter
	panel.FloatZIndex = overlayPanelZ
	panel.Color = theme.ColorPanel
	panel.ColorBorder = theme.ColorBorder
	panel.SizeBorder = gui.SomeF(1)
	panel.Radius = gui.SomeF(6)
	// Swallow clicks so they don't fall through to the backdrop, which would
	// dismiss the overlay when clicking inside it.
	panel.OnClick = func(ctx gui.EventCtx) {}
	return panel
}
