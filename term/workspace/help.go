package workspace

import (
	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Help-page geometry. The page takes over the content area like the theme
// browser and flows its sections into as few columns as fit the window: a
// single 330px column ran ~700px tall once the command registry grew past
// ~35 entries, which overflows a laptop viewport.
const (
	helpColW    = 330 // per-column content width, px
	helpColGap  = 28  // gap between columns, px
	helpMaxCols = 2

	// helpPageChrome is the vertical space the page spends outside the rows:
	// page padding plus the header and the footer, subtracted from the
	// height budget before the rows are measured.
	helpPageChrome = 90

	// helpHeaderExtra is the per-section cost above one text line: the
	// header's own padding plus the divider rule.
	helpHeaderExtra = 6

	// helpLineFactor converts a font size to a rendered line height. An
	// estimate — no DrawContext exists at view-build time — deliberately
	// rounded up so the column choice never under-reports height.
	helpLineFactor = 1.45
)

// helpScrollID keys the scroll state of the page body. Stable across
// rebuilds so the scroll position survives a re-render.
const helpScrollID = "goterm-help-scroll"

// helpSection is one titled block of the cheatsheet. Building the page as
// data first lets the column packer measure it before any view is built.
type helpSection struct {
	title string
	rows  []term.ShortcutInfo // reuses the existing Label/Keys pair
}

// toggleHelp shows or hides the keyboard-shortcut page and rebuilds
// the view. Bound to Cmd+/ (toggle) and Escape (close, when visible).
func (ws *Workspace) toggleHelp() {
	ws.helpVisible = !ws.helpVisible
	ws.refresh()
}

// closeHelp hides the page and rebuilds the view. Separate from toggleHelp
// so Escape cannot accidentally re-open it.
func (ws *Workspace) closeHelp() {
	ws.helpVisible = false
	ws.refresh()
}

// helpSections builds the cheatsheet content. The workspace section is
// generated from the live command registry; the terminal section from the
// active pane's effective bindings, so rebound shortcuts show their real
// chords rather than the built-in defaults. Neither is hand-maintained.
func (ws *Workspace) helpSections() []helpSection {
	wsRows := make([]term.ShortcutInfo, 0, len(ws.commands))
	for _, cmd := range ws.commands {
		// The tab 1–9 commands carry no Label and are skipped.
		if cmd.Label == "" || !cmd.Shortcut.IsSet() {
			continue
		}
		wsRows = append(wsRows, term.ShortcutInfo{
			Label: cmd.Label,
			Keys:  cmd.Shortcut.String(),
		})
	}

	// Fall back to the package defaults when no pane is live (e.g. the last
	// shell exited but the page is still up).
	termRows := term.Shortcuts()
	if active := ws.ActivePane(); active != nil {
		termRows = active.Shortcuts()
	}

	return []helpSection{
		{title: "Workspace", rows: wsRows},
		{title: "Terminal", rows: termRows},
	}
}

// sectionUnits is a section's height in text lines: one per row plus the
// header line. The divider is sub-line height and is folded into
// helpHeaderExtra instead.
func sectionUnits(s helpSection) int { return len(s.rows) + 1 }

// packSections distributes sections across cols, splitting a section's rows
// across a column boundary only when it alone overruns the target. The
// continuation repeats the title so a column never opens with orphaned rows.
//
// With today's two near-equal sections at cols == 2 this lands Workspace left
// and Terminal right without splitting either — the natural outcome of the
// balance rule, not a special case.
func packSections(secs []helpSection, cols int) [][]helpSection {
	if cols < 1 {
		cols = 1
	}
	total := 0
	for _, s := range secs {
		total += sectionUnits(s)
	}
	target := (total + cols - 1) / cols // ceil: the tallest column allowed

	out := make([][]helpSection, 0, cols)
	var cur []helpSection
	curUnits := 0

	for _, s := range secs {
		rows := s.rows
		for {
			// The last column is the remainder bucket: never split into a
			// column that does not exist.
			if len(out) == cols-1 {
				cur = append(cur, helpSection{title: s.title, rows: rows})
				curUnits += len(rows) + 1
				break
			}
			free := target - curUnits - 1 // -1 for this section's header line
			if free <= 0 {
				// No room for even a header here; start the next column.
				out = append(out, cur)
				cur, curUnits = nil, 0
				continue
			}
			if len(rows) <= free {
				cur = append(cur, helpSection{title: s.title, rows: rows})
				curUnits += len(rows) + 1
				break
			}
			// Section overruns the column: take what fits, carry the rest
			// into the next column under a repeated title.
			cur = append(cur, helpSection{title: s.title, rows: rows[:free]})
			rows = rows[free:]
			out = append(out, cur)
			cur, curUnits = nil, 0
		}
	}
	out = append(out, cur)
	for len(out) < cols {
		out = append(out, nil) // keep the result shape predictable
	}
	return out
}

// packedHeight estimates the rendered height of the tallest column.
func packedHeight(packed [][]helpSection, rowH float32) float32 {
	var maxH float32
	for _, col := range packed {
		var h float32
		for _, s := range col {
			h += float32(sectionUnits(s))*rowH + helpHeaderExtra
		}
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

// helpPanelWidth is the page body's content width for n columns.
func helpPanelWidth(n int) float32 {
	return float32(n)*helpColW + float32(n-1)*helpColGap
}

// helpColumns picks the fewest columns whose packed height fits the content
// area, bounded by what the page is wide enough to show. Returns the column
// count and the estimated content height, so the caller knows whether the
// body will scroll.
func helpColumns(secs []helpSection, ww, wh int, rowH float32) (int, float32) {
	hBudget := float32(wh) - helpPageChrome
	wBudget := float32(ww) - 2*browserPad

	// Widest column count the page can show, even if it still overflows
	// vertically — a too-narrow window falls back to one column and scrolls.
	// n == 1 always runs — the width guard below only applies from two
	// columns up — so the loop always assigns both, and seeding these with a
	// one-column packing would only pack that layout twice.
	best, bestH := 1, float32(0)
	for n := 1; n <= helpMaxCols; n++ {
		if n > 1 && helpPanelWidth(n) > wBudget {
			break
		}
		h := packedHeight(packSections(secs, n), rowH)
		best, bestH = n, h
		if h <= hBudget {
			break // fewest columns that fit: stop widening
		}
	}
	return best, bestH
}

// helpView builds the whole shortcuts page. Like the theme browser it takes
// over the content area outright, so the panes are removed from the tree
// rather than covered: no OnDraw runs for them and no PTY resize is
// published — reopening finds them unchanged.
func (ws *Workspace) helpView(w, h float32) gui.View {
	theme := gui.CurrentTheme()

	// The dimensions come from the window; a NaN or Inf would propagate
	// through every Fixed size below and out into the layout. Fall back to a
	// size that still renders a usable page rather than trying to draw an
	// undefined one.
	if !finiteF32(w) || w <= 0 {
		w = browserListMinW
	}
	if !finiteF32(h) || h <= 0 {
		h = browserListMinW
	}

	// M6 is the mono style at SizeTextTiny+1; derive the line height from the
	// same size so a themed font shifts the column choice with it.
	rowStyle := theme.TextStyleCodeTiny
	headStyle := theme.TextStyleCodeTiny
	headStyle.Typeface = glyph.TypefaceBold
	rowH := (theme.SizeTextTiny + 1) * helpLineFactor

	secs := ws.helpSections()
	cols, _ := helpColumns(secs, int(w), int(h), rowH)
	packed := packSections(secs, cols)

	colViews := make([]gui.View, 0, len(packed))
	for _, col := range packed {
		c := tight(gui.FixedFit)
		c.Width = helpColW
		c.Spacing = gui.SpacingTight
		views := make([]gui.View, 0, len(col)*2)
		for _, s := range col {
			views = append(views, ws.helpHeader(s.title, theme, headStyle))
			for _, r := range s.rows {
				views = append(views, ws.helpRow(r.Label, r.Keys, rowStyle))
			}
		}
		c.Content = views
		colViews = append(colViews, gui.Column(c))
	}

	colsRow := tight(gui.FitFit)
	colsRow.Spacing = gui.SpacingPx(helpColGap)
	colsRow.Content = colViews

	// The title centers over the columns, not the page: title and columns
	// live in one fixed-width block (exactly as wide as the columns) that
	// is itself centered in the page with equal fills.
	block := tight(gui.FixedFit)
	block.Width = helpPanelWidth(cols)
	block.Spacing = gui.SpacingSmall
	block.Content = []gui.View{
		ws.helpTitle(headStyle),
		gui.Row(colsRow),
	}
	if about := ws.helpAbout(theme); about != nil {
		block.Content = append(block.Content, about)
	}

	center := tight(gui.FillFit)
	center.Content = []gui.View{
		gui.Row(tight(gui.FillFill)),
		gui.Column(block),
		gui.Row(tight(gui.FillFill)),
	}

	// The body scrolls when the tallest column overflows the page (a short
	// window, or a future, much longer command list). The ID is stable so
	// the scroll position survives a rebuild.
	inner := tight(gui.FillFill)
	inner.ID = helpScrollID
	inner.Scrollable = true
	inner.ScrollMode = gui.ScrollVerticalOnly
	inner.Clip = true
	// Reserve the scrollbar's lane; the last column's key text runs to the
	// right edge, exactly where the thumb would otherwise land.
	inner.Padding = gui.NewPadding(0, scrollGutter(), 0, 0)
	inner.Content = []gui.View{gui.Row(center)}

	main := tight(gui.FillFill)
	main.Padding = gui.NewPadding(browserPad, browserPad, browserPad, browserPad)
	main.Spacing = gui.SpacingSmall
	main.Content = []gui.View{
		gui.Column(inner),
	}

	page := tight(gui.FixedFixed)
	page.Width = w
	page.Height = h
	page.Color = theme.ColorPanel
	page.Content = []gui.View{gui.Column(main), ws.helpFooter(theme)}
	return gui.Column(page)
}

// helpAbout renders the embedder's about lines below the shortcut columns:
// app name, version, build metadata, centered like the title. Nil when
// Cfg.About is empty, so the page shows no gap for it.
func (ws *Workspace) helpAbout(theme gui.Theme) gui.View {
	if len(ws.cfg.About) == 0 {
		return nil
	}
	style := theme.TextStyleSecondary
	style.Size = theme.SizeTextTiny
	style.Align = gui.TextAlignCenter
	lines := make([]gui.View, 0, len(ws.cfg.About))
	for _, ln := range ws.cfg.About {
		if ln == "" {
			continue
		}
		// Centered like the title: equal fills on both sides, since
		// Align alone has nothing to center within.
		row := tight(gui.FillFit)
		row.Content = []gui.View{
			gui.Row(tight(gui.FillFill)),
			gui.Text(gui.TextCfg{Text: ln, TextStyle: style}),
			gui.Row(tight(gui.FillFill)),
		}
		lines = append(lines, gui.Row(row))
	}
	if len(lines) == 0 {
		return nil
	}
	body := tight(gui.FillFit)
	body.Spacing = gui.NoSpacing
	body.Content = lines

	wrap := tight(gui.FillFit)
	wrap.Spacing = gui.SpacingSmall
	wrap.Content = []gui.View{
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1.2,
			Color:  theme.ColorBorder,
		}),
		gui.Column(body),
	}
	return gui.Column(wrap)
}

// helpFooter is the key-hint strip. The toggle chord is read from the live
// command table so a rebound toggle shows its real binding.
func (ws *Workspace) helpFooter(theme gui.Theme) gui.View {
	toggle := "toggle"
	for _, cmd := range ws.commands {
		if cmd.ID == "workspace.toggleHelp" && cmd.Shortcut.IsSet() {
			toggle = cmd.Shortcut.String() + " toggle"
			break
		}
	}
	bar := tight(gui.FillFit)
	bar.Padding = gui.NewPadding(8, browserPad, 8, browserPad)
	bar.Spacing = gui.SpacingMedium

	hint := func(s string) gui.View {
		return gui.Text(gui.TextCfg{Text: s, TextStyle: theme.TextStyleCodeTiny})
	}
	fill := tight(gui.FillFit)
	bar.Content = []gui.View{
		hint("Esc close"),
		hint(toggle),
		gui.Row(fill),
	}

	wrap := tight(gui.FillFit)
	wrap.Content = []gui.View{
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1,
			Color:  theme.ColorBorder,
		}),
		gui.Row(bar),
	}
	return gui.Column(wrap)
}

// helpTitle renders the page title: uppercase and centered, with no divider.
// Separate from helpHeader, which stays left-aligned with a divider for the
// section titles and the theme picker.
func (ws *Workspace) helpTitle(style gui.TextStyle) gui.View {
	style.Align = gui.TextAlignCenter
	titleRow := tight(gui.FillFit)
	titleRow.Padding = gui.NewPadding(3, 0, 1, 0)
	// Equal fills on both sides center the title: the text box hugs its
	// content, so Align alone has nothing to center within.
	titleRow.Content = []gui.View{
		gui.Row(tight(gui.FillFill)),
		gui.Text(gui.TextCfg{Text: "KEYBOARD SHORTCUTS", TextStyle: style}),
		gui.Row(tight(gui.FillFill)),
	}
	return gui.Row(titleRow)
}

// helpHeader renders a section label with a thin divider below. style is
// passed in so the help page can run tighter type than the theme picker,
// which shares this helper.
func (ws *Workspace) helpHeader(text string, theme gui.Theme, style gui.TextStyle) gui.View {
	headerRow := tight(gui.FillFit)
	headerRow.Padding = gui.NewPadding(3, 0, 1, 0)
	headerRow.Content = []gui.View{gui.Text(gui.TextCfg{Text: text, TextStyle: style})}

	col := tight(gui.FillFit)
	col.Spacing = gui.NoSpacing
	col.Content = []gui.View{
		gui.Row(headerRow),
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1.2,
			Color:  theme.ColorBorder,
		}),
	}
	return gui.Column(col)
}

// helpRow renders one "label … keys" line with the keys right-aligned.
func (ws *Workspace) helpRow(label, keys string, style gui.TextStyle) gui.View {
	fill := tight(gui.FillFit)
	row := tight(gui.FillFit)
	row.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: label, TextStyle: style}),
		gui.Row(fill),
		gui.Text(gui.TextCfg{Text: keys, TextStyle: style}),
	}
	return gui.Row(row)
}
