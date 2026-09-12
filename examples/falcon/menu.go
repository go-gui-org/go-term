package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/go-gui-org/go-gui/gui"
)

// appName titles the macOS app menu ("Quit <appName>") and the About dialog.
// This is the application's name, not the library's — falcon is the app,
// go-term is the widget it embeds.
const appName = "Falcon"

const repoURL = "https://github.com/go-gui-org/go-term"

// actionAbout routes the Help menu's About item to showAbout through OnAction.
// No command backs it: About has no shortcut, and nothing else invokes it.
const actionAbout = "help.about"

// cmdToggleHelp names the workspace command that shows or hides the
// keyboard-shortcut overlay. Must match the ID in
// term/workspace/command.go: the Help menu item carries this as its ID so a
// native menu click resolves through the command registry.
const cmdToggleHelp = "workspace.toggleHelp"

// helpShortcutsLabel is the Help menu item's text. It repeats the
// workspace.toggleHelp command label verbatim — keep the two in step (see
// term/workspace/command.go).
const helpShortcutsLabel = "Show / Hide Shortcuts"

// cmdOpenConfig is falcon's own window command (Cmd+,). It carries no menu
// item — the chord is the whole interface.
const cmdOpenConfig = "falcon.openConfig"

// registerCommands adds falcon's own window commands. Everything else in the
// registry comes from workspace.New; this runs after it so the workspace's
// installCommands (which unregisters only its own table) can't drop these on a
// config reload.
//
// Cmd+, is the macOS Settings convention. It has to be a gui.Command rather
// than a native menu key equivalent: the native encoder only handles A-Z/0-9,
// so a comma there would be silently dropped — no hint, no dispatch.
func registerCommands(w *gui.Window) {
	cmd := gui.Command{
		ID:       cmdOpenConfig,
		Label:    "Open Config File",
		Shortcut: gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper},
		// Global so it fires before focus dispatch — the focused pane would
		// otherwise get first refusal on the chord, same as every workspace
		// command.
		Global:  true,
		Execute: func(_ *gui.Event, _ *gui.Window) { openConfigFile() },
	}
	if err := w.RegisterCommand(cmd); err != nil {
		log.Printf("menu: register %s: %v", cmd.ID, err)
	}
}

// installMenubar replaces the backend's default menubar. The one custom menu
// is Help: the keyboard-shortcut overlay (Cmd+/, the same chord the menu item
// fires through the workspace command registry) and the About dialog, which
// OmitAboutItem moves out of the app menu.
//
// AboutActionID is deliberately unset: About is an explicit Help item routed
// through OnAction to falcon's own dialog. The system NSAboutPanel is not
// usable here: it renders from Info.plist, so an unbundled `go build` would
// show the lowercase process name with no version and no icon.
//
// No Edit menu: Cmd+C/Cmd+V are terminal shortcuts handled by term's binding
// table, and an auto-wired Edit menu would swallow them before they get there.
// IncludeWindowMenu restores the Close/Minimize/Zoom menu that installing a
// custom menubar would otherwise drop.
func (a *app) installMenubar(gapp *gui.App, w *gui.Window) {
	gapp.SetNativeMenubar(a.menubarCfg(w))
}

// menubarCfg builds the menubar config. Split out of installMenubar so a test
// can assert the fields: SetNativeMenubar needs a registered main window and
// a live platform backend, so it is a no-op under `go test`.
func (a *app) menubarCfg(w *gui.Window) gui.NativeMenubarCfg {
	return gui.NativeMenubarCfg{
		AppName: appName,
		// About lives in the Help menu below, so drop it from the app
		// menu. This takes precedence over AboutActionID, which stays
		// unset.
		OmitAboutItem:     true,
		IncludeWindowMenu: true,
		OnAction:          func(id string) { a.onMenuAction(id, w) },
		Menus: []gui.NativeMenuCfg{
			{
				Title: "Help",
				Items: []gui.NativeMenuItemCfg{
					{
						// ID is what the macOS backend hands back
						// on click, and App.SetNativeMenubar
						// resolves it via the command registry
						// first — so this fires
						// workspace.toggleHelp with no OnAction
						// handling. CommandID mirrors it for
						// backends that route on that field.
						// Shortcut paints the ⌘/ hint beside the
						// item; it duplicates the command's own
						// chord by construction, so the two
						// cannot drift.
						ID:        cmdToggleHelp,
						CommandID: cmdToggleHelp,
						Text:      helpShortcutsLabel,
						Shortcut:  gui.Shortcut{Key: gui.KeySlash, Modifiers: gui.ModSuper},
					},
					{Separator: true},
					{
						// No command backs About, so this ID
						// falls through to OnAction and
						// onMenuAction shows the dialog.
						ID:   actionAbout,
						Text: "About " + appName,
					},
				},
			},
		},
	}
}

// onMenuAction dispatches a menu click that no command handles. go-gui already
// queued this onto the main thread, so it can touch window state directly.
func (a *app) onMenuAction(id string, w *gui.Window) {
	switch id {
	case actionAbout:
		showAbout(w)
	}
}

// aboutIconSize is the on-screen edge length of the icon in the About dialog,
// matching the proportions the macOS about panel uses.
const aboutIconSize = 96

// About dialog widget IDs. aboutKeysID is the dialog's initial focus target
// and its Enter handler; the rest are the links along the bottom and the
// commit row.
const (
	aboutKeysID   = "about/keys"
	aboutDocsID   = "about/docs"
	aboutGitHubID = "about/github"
	aboutCommitID = "about/commit"
)

// aboutTagline is the description under the icon, matching what the README
// leads with. The line break is written in rather than left to the wrapper:
// the break belongs after "emulator", where the clause ends, and a width-
// driven wrap put it mid-clause at the panel's natural size.
const aboutTagline = "A fast, GPU-rendered terminal emulator\n" +
	"written in pure Go."

// docsURL is the config reference the Docs button opens. Points at main
// rather than the built tag: the docs are corrected between releases, and a
// user opening them wants the current text.
const docsURL = repoURL + "/blob/main/docs/config.md"

// aboutLabelWidth is the width of the label column in the version rows. Fixed
// so "Version", "Built", and "Commit" right-align into one edge, the way the
// macOS and Ghostty about panels set their metadata out.
const aboutLabelWidth = 76

// aboutIconFile materializes the embedded icon as a real file: gui.Image loads
// from a path, and data: URLs are only honored by the WASM backend. Written
// once per process into the user cache dir; returns "" on failure, in which
// case the dialog simply renders without the icon.
var aboutIconFile = sync.OnceValue(func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		log.Printf("about: no cache dir: %v", err)
		return ""
	}
	dir = filepath.Join(dir, "falcon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("about: create %s: %v", dir, err)
		return ""
	}
	path := filepath.Join(dir, "about-icon.png")
	// Rewritten every run rather than reused: the icon ships with the binary,
	// so a stale cached copy from an older build would silently win.
	if err := os.WriteFile(path, appIconPNG, 0o644); err != nil {
		log.Printf("about: write %s: %v", path, err)
		return ""
	}
	return path
})

// showAbout presents the in-app About dialog. Deliberately not the system
// NSAboutPanel: that panel reads its content from the bundle Info.plist, which
// a `go run` binary doesn't have, so it would show the process name and
// nothing else.
//
// Laid out like the Ghostty about panel — icon, tagline, a right-aligned
// label column of build metadata, then links out to the docs and the repo.
// That needs DialogCustom, the only dialog type that renders CustomContent.
//
// There is no OK button and no default button: the panel reports, it does
// not ask, so nothing here is worth a highlighted target. Escape dismisses
// through go-gui's own dialog key handler, and Enter dismisses through the
// invisible focus holder this builds around the content — see aboutKeys.
// Docs and GitHub stay reachable with Tab.
func showAbout(w *gui.Window) {
	if w.DialogIsVisible() {
		return
	}
	installAboutOutsideClick(w)
	w.Dialog(aboutDialogCfg(w.Theme()))
}

// The About panel's outside-click hook. aboutHookInstalled says the wrapper
// below is in place and aboutPrevOnEvent holds the handler it displaced.
// Without the flag a second open before the hook retired would capture the
// hook as its own predecessor and chain to itself forever. Read and written
// only from gui callbacks, which all run on the main thread.
var (
	aboutHookInstalled bool
	aboutPrevOnEvent   func(*gui.Event, *gui.Window)
)

// installAboutOutsideClick makes a click anywhere outside the panel close it.
//
// Window.OnEvent is go-gui's last-resort hook: it fires only for events no
// shape consumed. A click that lands on the terminal is exactly that — the
// dialog is modal, so go-gui routes the click into the dialog layer, where
// nothing contains the point and nothing handles it. Clicks on the panel's
// own controls never reach here, because those consume first.
//
// Every dismissal path calls retireAboutHook, so the terminal's own handler
// is back in charge as soon as the panel closes rather than paying for this
// dialog on every event for the rest of the session.
func installAboutOutsideClick(w *gui.Window) {
	if aboutHookInstalled {
		return
	}
	aboutHookInstalled = true
	aboutPrevOnEvent = w.OnEvent
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		prev := aboutPrevOnEvent
		if w.DialogIsVisible() && e.Type == gui.EventMouseDown {
			w.DialogDismiss()
			retireAboutHook(w)
			// Swallowed on purpose: the click spent itself closing
			// the panel, and passing it on would also send a mouse
			// report to the child that was hidden behind it.
			return
		}
		// Safety net for a dismissal that reached neither of the
		// panel's own exits (a future go-gui path, or a caller that
		// dismisses the dialog itself).
		if !w.DialogIsVisible() {
			retireAboutHook(w)
		}
		if prev != nil {
			prev(e, w)
		}
	}
}

// retireAboutHook puts the displaced handler back. Safe to call when no hook
// is installed, which is what lets every dismissal path call it blindly.
func retireAboutHook(w *gui.Window) {
	if !aboutHookInstalled {
		return
	}
	w.OnEvent = aboutPrevOnEvent
	aboutPrevOnEvent = nil
	aboutHookInstalled = false
}

// aboutDialogCfg builds the About dialog's config. Split out of showAbout so
// a test can assert the keyboard contract — the focus target, and that no
// button is defaulted — without a live window.
func aboutDialogCfg(theme gui.Theme) gui.DialogCfg {
	var content []gui.View
	if path := aboutIconFile(); path != "" {
		content = append(content, gui.Image(gui.ImageCfg{
			Src:     path,
			Width:   aboutIconSize,
			Height:  aboutIconSize,
			A11YCfg: gui.A11YCfg{A11YLabel: appName + " icon"},
		}))
	}
	// The name in the panel's largest bold face, the way the macOS and
	// Ghostty about panels title themselves. It repeats the wordmark in
	// the icon artwork on purpose: the icon is decoration, and a panel
	// whose only statement of what it is sits inside a picture reads as
	// unnamed — and says nothing at all to a screen reader.
	name := theme.B1
	name.Align = gui.TextAlignCenter
	// The tagline sets a step below body text: it is a caption for the
	// icon, and at body size it competed with the metadata rows for the
	// eye instead of introducing them.
	tagline := theme.TextStyleSecondary
	tagline.Size = theme.SizeTextSmall
	tagline.Align = gui.TextAlignCenter
	// Name and tagline ride in one block rather than as two items of the
	// outer stack: the tagline says what the name means, so they belong
	// closer to each other than to the icon above or the rows below.
	// Medium, not Small — at the title's 22px the smaller gap read as a
	// collision rather than as a pairing.
	content = append(content, gui.Column(gui.ContainerCfg{
		Sizing:     gui.FillFit,
		HAlign:     gui.HAlignCenter,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		Spacing:    gui.Some(gui.SpacingMedium),
		Content: []gui.View{
			gui.Text(gui.TextCfg{Text: appName, TextStyle: name}),
			gui.Text(gui.TextCfg{
				Text:      aboutTagline,
				TextStyle: tagline,
				Mode:      gui.TextModeWrap,
			}),
		},
	}))
	content = append(content, aboutMetadata(theme))
	content = append(content, gui.Row(gui.ContainerCfg{
		Sizing:     gui.FillFit,
		HAlign:     gui.HAlignCenter,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		Spacing:    gui.Some(gui.SpacingLarge),
		Content: []gui.View{
			aboutLinkButton(aboutDocsID, "Docs", docsURL),
			aboutLinkButton(aboutGitHubID, "GitHub", repoURL),
		},
	}))
	return gui.DialogCfg{
		DialogType: gui.DialogCustom,
		// Dialog() focuses FocusID on open. It points at the invisible key
		// holder rather than at Docs or GitHub: focusing a link would make
		// Enter open a browser, which is the opposite of dismissing.
		FocusID:       aboutKeysID,
		CustomContent: []gui.View{aboutKeys(content)},
		// go-gui's dialog root calls OnCancelNo when Escape dismisses.
		// Not a button callback here — the panel has no buttons — just
		// the notification that tells the outside-click hook to retire.
		OnCancelNo: retireAboutHook,
	}
}

// aboutKeys wraps the dialog body in a focusable container that dismisses on
// Enter. A plain container takes no focus ring and no fill, so it adds the
// key handling without adding a visible control — which is the point, since
// the dialog has no button worth defaulting to.
//
// Escape is not handled here: go-gui's dialog root already consumes it, and a
// second handler on the same key would only be a place for the two to drift.
func aboutKeys(content []gui.View) gui.View {
	return gui.Column(gui.ContainerCfg{
		ID:         aboutKeysID,
		Focusable:  true,
		Sizing:     gui.FillFit,
		HAlign:     gui.HAlignCenter,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		// Large, not Medium: the four blocks here are separate ideas
		// (what it is, how old it is, where to read more), and the
		// panel reads as a stack of them rather than a dense list.
		Spacing: gui.Some(gui.SpacingLarge),
		OnKeyDown: func(ctx gui.EventCtx) {
			switch ctx.Event.KeyCode {
			case gui.KeyEnter, gui.KeyKPEnter:
				ctx.Window.DialogDismiss()
				retireAboutHook(ctx.Window)
				ctx.Consume()
			}
		},
		Content: content,
	})
}

// aboutMetadata builds the Version/Built/Commit block. Every row is optional
// below the first: an unstamped build knows no revision and no commit date,
// and an empty row reading "Commit —" is worse than no row.
func aboutMetadata(theme gui.Theme) gui.View {
	// The values sit in the theme's mono face so the hash and the version
	// digits line up column-wise, at the dialog's own text size rather than
	// M3's slightly larger default.
	value := theme.M3
	value.Size = gui.DefaultDialogStyle.TextStyle.Size
	rows := []gui.View{
		aboutRow(theme, "Version", gui.Text(gui.TextCfg{
			Text:      aboutVersion(),
			TextStyle: value,
		})),
	}
	if date := buildDate(); date != "" {
		rows = append(rows, aboutRow(theme, "Built", gui.Text(gui.TextCfg{
			Text:      date,
			TextStyle: value,
		})))
	}
	if rev := buildCommit(); rev != "" {
		rows = append(rows, aboutRow(theme, "Commit",
			aboutCommitLink(theme, value, rev)))
	}
	return gui.Column(gui.ContainerCfg{
		Sizing:     gui.FitFit,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		Spacing:    gui.Some(gui.SpacingTight),
		Content:    rows,
	})
}

// aboutRow pairs one right-aligned label with its value view.
func aboutRow(theme gui.Theme, label string, value gui.View) gui.View {
	style := theme.TextStyleSecondary
	style.Align = gui.TextAlignRight
	return gui.Row(gui.ContainerCfg{
		Sizing:     gui.FitFit,
		VAlign:     gui.VAlignMiddle,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		Spacing:    gui.Some(gui.SpacingSmall),
		Content: []gui.View{
			gui.Text(gui.TextCfg{
				Text:      label,
				TextStyle: style,
				MinWidth:  aboutLabelWidth,
			}),
			value,
		},
	})
}

// shortCommitRev abbreviates a full revision for the About dialog's Commit
// row. Split out of aboutCommitLink so tests can cover the rule without
// stubbing the toolchain's build info.
func shortCommitRev(rev string, dirty bool) string {
	if rev == "" {
		return ""
	}
	short := rev
	if len(short) > commitLen {
		short = short[:commitLen]
	}
	if dirty {
		short += "-dirty"
	}
	return short
}

// aboutCommitLink renders the short revision as a button that opens the
// commit on GitHub. A ghost button rather than colored text: it carries the
// hit target, the focus ring, and the keyboard activation that a bare Text
// has none of, while showing no chrome until hovered.
//
// A dirty tree appends "-dirty" to the label but keeps the link, which still
// points at the revision the working tree was based on.
func aboutCommitLink(theme gui.Theme, value gui.TextStyle, rev string) gui.View {
	short := shortCommitRev(rev, buildDirty())
	value.Color = theme.ColorAccent
	url := repoURL + "/commit/" + rev
	return gui.Button(gui.ButtonCfg{
		ID:      aboutCommitID,
		Variant: gui.ButtonGhost,
		Padding: gui.NoPadding,
		Content: []gui.View{gui.Text(gui.TextCfg{
			Text:      short,
			TextStyle: value,
		})},
		A11YCfg: gui.A11YCfg{A11YLabel: "Commit " + short + ", opens GitHub"},
		OnClick: func(gui.EventCtx) { openURL(url) },
	})
}

// aboutLinkButton is one of the dialog's outbound links. The dialog stays
// open behind the browser: the user asked to read something, not to dismiss
// the panel.
func aboutLinkButton(id, label, url string) gui.View {
	return gui.Button(gui.ButtonCfg{
		ID:      id,
		Content: []gui.View{gui.Text(gui.TextCfg{Text: label})},
		OnClick: func(gui.EventCtx) { openURL(url) },
	})
}

// openURL hands a URL to the platform browser, logging rather than surfacing
// a failure: a link that does nothing is a small loss, and an error dialog
// stacked on the About dialog is a bigger one.
func openURL(url string) {
	if err := openPath(url); err != nil {
		log.Printf("about: open %s: %v", url, err)
	}
}

// openConfigFile opens the user's config file in the OS-default editor,
// creating a commented stub first when the file doesn't exist yet — opening a
// nonexistent path just fails silently on every platform, which reads as a
// dead menu item.
func openConfigFile() {
	path := defaultConfigPath()
	if path == "" {
		log.Printf("menu: no config directory available")
		return
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeConfigStub(path); err != nil {
			log.Printf("menu: create config %s: %v", path, err)
			return
		}
	}
	if err := openPath(path); err != nil {
		log.Printf("menu: open config %s: %v", path, err)
	}
}

// configStub is the starter file written when no config exists. Every setting
// go-term reads appears here, and every key line is commented out, so the file
// doubles as the key reference and changes nothing until the user edits it.
// Section headers stay uncommented: an empty section is a no-op, and a key
// uncommented without its header would be parsed under no section and dropped
// without a word (see parseConfig).
// Values shown are the defaults; keep them in step with docs/config.md.
const configStub = `# go-term configuration.
# See ` + repoURL + `/blob/main/docs/config.md for the full key reference.
#
# Every setting below is commented out and shows its default value.
# Remove the leading "#" from a key to change it. The section headers are
# already live — a key only counts when it sits under its own header.

# Font family (as the font's own name table spells it) and point size.
# Both default to whatever the application picked; these are examples.

[font]
# family = Menlo
# size   = 14

[general]

# Color theme, by display name (case-insensitive). Cmd+Shift+T lists them.
# theme = Default

# Scrollback rows. 0 restores the default; a negative value disables it.
# scrollback = 5000

# Bell handling: auto, audible, visual, both, none.
# bell = auto

# Scrollbar thumb width in px. A negative value hides the scrollbar.
# scrollbar = 4

# Force text to reach a WCAG contrast ratio against its background. Worth
# turning on with a light theme: apps that emit 24-bit color (eza, starship)
# pick it for a dark background, and no theme setting can reach those.
# 1 is off, 3 fixes the worst colors, 4.5 is the WCAG floor for body text.
# minimum-contrast = 1

# Paste with the middle mouse button. On by default for Linux only.
# middle-click-paste = off

# Notify when a command that ran this long finishes while you are looking
# elsewhere. Needs shell integration; 0 disables. "30s" and "2m" work too.
# notify-after = 0

# Cursor shape and blink. These are what a pane starts with and what "reset"
# returns to; an app can still change them (vim switches to a bar for insert
# mode) until cursor-lock is on, which makes go-term ignore those requests.
# Cursor shapes: block, underline, bar.
# cursor-style = block
# cursor-blink = off
# cursor-lock  = off

# Environment variables for every child process. These are applied last, so
# they override the terminal's own — including TERM_PROGRAM, which is what
# yazi and superfile key their image protocol off. go-term implements the
# Kitty protocol, so naming an emulator that does too upgrades their previews
# from sixel to full-color images.

[env]
# TERM_PROGRAM = Ghostty

# Keyboard shortcuts. Each entry rebinds one action: "workspace.<command>"
# for window-level commands, "term.<action>" for pane-level ones. Cmd+/ shows
# the full list with its current bindings. Set a binding to "none" to hand
# the chord back to the child process.

[keybindings]
# workspace.splitVertical = Cmd+D
# workspace.newTab        = Cmd+T
# term.copy               = Cmd+Shift+C
# term.find               = Cmd+F
# term.scroll-page-up     = none
`

func writeConfigStub(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(configStub), 0o644)
}

// openPath hands a file to the platform's default handler.
func openPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
