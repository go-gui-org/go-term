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

// actionAbout routes the app menu's About item to showAbout through OnAction.
// No command backs it: About has no shortcut, and nothing else invokes it.
const actionAbout = "help.about"

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

// installMenubar replaces the backend's default menubar. There are no custom
// menus: the two things a Help menu used to hold — the shortcut overlay and
// the config file — are Cmd+/ and Cmd+, respectively, which is where a
// terminal user reaches for them anyway. That leaves the app menu (About,
// Quit) and the Window menu.
//
// AboutActionID keeps About in its conventional app-menu slot while still
// routing to falcon's own dialog. The system NSAboutPanel is not usable here:
// it renders from Info.plist, so an unbundled `go build` would show the
// lowercase process name with no version and no icon.
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
		AppName:           appName,
		AboutActionID:     actionAbout,
		IncludeWindowMenu: true,
		OnAction:          func(id string) { a.onMenuAction(id, w) },
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

// aboutOKID names the dialog's only button, used both as the button ID and as
// the dialog's initial focus target.
const aboutOKID = "about/ok"

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
// Laid out like the macOS default About box — icon, app name, version — which
// needs DialogCustom, the only dialog type that renders CustomContent. That
// also means supplying the OK button (Escape still dismisses via the dialog's
// own key handler).
func showAbout(w *gui.Window) {
	if w.DialogIsVisible() {
		return
	}
	var content []gui.View
	if path := aboutIconFile(); path != "" {
		content = append(content, gui.Image(gui.ImageCfg{
			Src:     path,
			Width:   aboutIconSize,
			Height:  aboutIconSize,
			A11YCfg: gui.A11YCfg{A11YLabel: appName + " icon"},
		}))
	}
	// No app-name text: the icon artwork already spells out "Falcon".
	content = append(content, gui.Text(gui.TextCfg{
		Text:      "Version " + appVersion(),
		TextStyle: gui.DefaultDialogStyle.TextStyle,
	}))
	w.Dialog(gui.DialogCfg{
		DialogType: gui.DialogCustom,
		// Dialog() focuses FocusID on open; pointing it at the OK button is
		// what makes Enter/Space dismiss without a click.
		FocusID: aboutOKID,
		CustomContent: []gui.View{
			gui.Column(gui.ContainerCfg{
				Sizing:     gui.FillFit,
				HAlign:     gui.HAlignCenter,
				Padding:    gui.NoPadding,
				SizeBorder: gui.NoBorder,
				Spacing:    gui.Some(gui.SpacingMedium),
				Content:    content,
			}),
			gui.Row(gui.ContainerCfg{
				Sizing:     gui.FillFit,
				HAlign:     gui.HAlignCenter,
				Padding:    gui.NoPadding,
				SizeBorder: gui.NoBorder,
				Content: []gui.View{
					gui.Button(gui.ButtonCfg{
						ID:      aboutOKID,
						Content: []gui.View{gui.Text(gui.TextCfg{Text: "OK"})},
						OnClick: func(ctx gui.EventCtx) {
							ctx.Window.DialogDismiss()
						},
					}),
				},
			}),
		},
	})
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
