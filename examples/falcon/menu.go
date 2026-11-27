package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/internal/opener"
)

// appName titles the macOS app menu ("Quit <appName>"). This is the
// application's name, not the library's — falcon is the app, go-term is the
// widget it embeds.
const appName = "Falcon"

const repoURL = "https://github.com/go-gui-org/go-term"

// cmdToggleHelp names the workspace command that shows or hides the
// keyboard-shortcut page. Must match the ID in
// term/workspace/command.go: the Help menu item carries this as its ID so a
// native menu click resolves through the command registry.
const cmdToggleHelp = "workspace.toggleHelp"

// helpShortcutsLabel is the Help menu item's text. It repeats the
// workspace.toggleHelp command label verbatim — keep the two in step (see
// term/workspace/command.go).
const helpShortcutsLabel = "Show / Hide Shortcuts"

// cmdOpenConfig is falcon's Settings command (Cmd+,). It opens the config
// file in the OS-default editor, creating a commented stub first when the
// file doesn't exist yet.
const cmdOpenConfig = "falcon.openConfig"

// settingsLabel is the Settings entry's text, shared by the command-palette
// row (and the help overlay, which lists it under its Cmd+, chord).
const settingsLabel = "Settings"

// appCommands returns falcon's own window command: Settings (Cmd+,). It rides
// on workspace.Cfg.ExtraCommands — see run() in main.go — so the workspace
// registers it, lists it in the command palette, and re-registers it on every
// config reload. Registering it separately here as well would collide on ID
// with that table.
//
// Cmd+, is the macOS Settings convention. It has to be a gui.Command rather
// than a native menu key equivalent: the native encoder only handles A-Z/0-9,
// so a comma there would be silently dropped — no hint, no dispatch.
func appCommands() []gui.Command {
	return []gui.Command{
		{
			ID:       cmdOpenConfig,
			Label:    settingsLabel,
			Shortcut: gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper},
			// Global so it fires before focus dispatch — the focused pane
			// would otherwise get first refusal on the chord, same as
			// every workspace command.
			Global:  true,
			Execute: func(_ *gui.Event, _ *gui.Window) { openConfigFile() },
		},
	}
}

// aboutInfo builds the about lines shown below the shortcut columns on the
// help page: app name and version, then the build date and commit when the
// toolchain embedded them. Every row after the first is optional: an
// unstamped build knows no revision and no commit date, and an empty row
// reading "Commit —" is worse than no row.
func aboutInfo() []string {
	lines := []string{appName + " " + aboutVersion()}
	if date := buildDate(); date != "" {
		lines = append(lines, "Built "+date)
	}
	if rev := buildCommit(); rev != "" {
		lines = append(lines, "Commit "+shortCommitRev(rev, buildDirty()))
	}
	return lines
}

// shortCommitRev abbreviates a full revision for the help page's Commit row.
// Split out of the about lines so tests can cover the rule without stubbing
// the toolchain's build info.
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

// installMenubar replaces the backend's default menubar. The one custom menu
// is Help: the keyboard-shortcut page (Cmd+/, the same chord the menu item
// fires through the workspace command registry). The app menu carries no
// About item (see OmitAboutItem below): the version lives on the help page,
// and the system NSAboutPanel is not usable here — it renders from
// Info.plist, so an unbundled `go build` would show the lowercase process
// name with no version and no icon.
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
		// No About item anywhere: the version lives on the help page.
		// This takes precedence over AboutActionID, which stays unset.
		OmitAboutItem:     true,
		IncludeWindowMenu: true,
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
				},
			},
		},
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
	return opener.Start(opener.Command(path))
}
