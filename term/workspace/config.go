package workspace

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// workspaceConfig holds the parsed human config file.
//
// Every setting is optional, so each one that has a meaningful zero value
// carries a companion "has" flag. Reload recomputes the effective config from
// the embedder's pristine Cfg every time, so a key deleted from the file must
// be distinguishable from a key set to zero — otherwise removing a line would
// leave the last-loaded value stuck until restart.
type workspaceConfig struct {
	keybindings map[string]string // binding key (namespaced or bare) → chord

	fontFamily string // [font] family
	theme      string // [general] theme, matched by name against Cfg.Themes

	fontSize   float32       // [font] size, points
	scrollbar  float32       // [general] scrollbar, px (negative hides)
	scrollback int           // [general] scrollback rows (negative disables)
	bell       term.BellMode // [general] bell

	middleClickPaste bool // [general] middle-click-paste

	hasFontSize         bool
	hasScrollback       bool
	hasScrollbar        bool
	hasBell             bool
	hasMiddleClickPaste bool
}

const (
	maxKeybindings = 256 // per-file keybinding entry cap
	maxKeyLen      = 128 // max bytes for a config key
	maxValLen      = 128 // max bytes for a config value
)

// maxFontSizeCfg bounds a [font] size entry before it reaches term. term
// clamps the effective size itself (see Term.style); this only rejects
// values so far out of range that they read as a typo rather than intent,
// and keeps the parse error visible in the log.
const maxFontSizeCfg = 1000

// parseConfig parses an INI-style config file. Collects per-line errors
// without aborting. The format: [section] headers; key = value pairs;
// # comments; blank lines ignored; whitespace around = trimmed.
func parseConfig(r io.Reader) (workspaceConfig, []error) {
	cfg := workspaceConfig{keybindings: make(map[string]string)}
	var errs []error
	section := ""
	sc := bufio.NewScanner(r)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			errs = append(errs, fmt.Errorf("line %d: no '='", lineNum))
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			errs = append(errs, fmt.Errorf("line %d: empty key", lineNum))
			continue
		}
		if len(key) > maxKeyLen || len(val) > maxValLen {
			errs = append(errs, fmt.Errorf("line %d: key or value too long", lineNum))
			continue
		}
		// Unknown sections and unknown keys within a known section are
		// ignored on purpose: a config written for a newer go-term must
		// still work here, and vice versa.
		switch section {
		case "keybindings":
			if len(cfg.keybindings) >= maxKeybindings {
				errs = append(errs, fmt.Errorf("line %d: keybinding limit (%d) reached", lineNum, maxKeybindings))
				continue
			}
			cfg.keybindings[key] = val
		case "font":
			if err := cfg.setFont(key, val); err != nil {
				errs = append(errs, fmt.Errorf("line %d: %w", lineNum, err))
			}
		case "general":
			if err := cfg.setGeneral(key, val); err != nil {
				errs = append(errs, fmt.Errorf("line %d: %w", lineNum, err))
			}
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, fmt.Errorf("scan: %w", err))
	}
	return cfg, errs
}

// setFont applies one key from the [font] section. A returned error means the
// value was malformed; the caller logs it and the setting keeps its default.
// An unrecognized key is not an error — see parseConfig.
func (c *workspaceConfig) setFont(key, val string) error {
	switch key {
	case "family":
		if val == "" {
			return fmt.Errorf("font family: empty")
		}
		c.fontFamily = val
	case "size":
		n, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("font size %q: not a number", val)
		}
		// NaN must be rejected explicitly: every comparison against it is
		// false, so the range test below would let it through, and nothing
		// downstream catches it either — Term.style()'s clamp is gated on
		// "Size > 0", which NaN also fails.
		if math.IsNaN(n) || n <= 0 || n > maxFontSizeCfg {
			return fmt.Errorf("font size %v: out of range", n)
		}
		c.fontSize, c.hasFontSize = float32(n), true
	}
	return nil
}

// setGeneral applies one key from the [general] section. Same error contract
// as setFont.
func (c *workspaceConfig) setGeneral(key, val string) error {
	switch key {
	case "theme":
		if val == "" {
			return fmt.Errorf("theme: empty")
		}
		c.theme = val
	case "scrollback":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("scrollback %q: not an integer", val)
		}
		// Sign convention and upper bound are term.Cfg.ScrollbackRows':
		// negative disables, positive clamps. Only the parse is done here.
		c.scrollback, c.hasScrollback = n, true
	case "scrollbar":
		n, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("scrollbar %q: not a number", val)
		}
		// Non-finite is rejected for the same reason as the font size: NaN
		// passes every range test, and -Inf slips past the upper bound
		// because negatives are legal here. Both are caught downstream by
		// effectiveScrollbarWidth, but they would still defeat the equality
		// early-out in SetScrollbarWidth on every reload.
		if math.IsNaN(n) || math.IsInf(n, 0) || n > maxScrollbarPx {
			return fmt.Errorf("scrollbar %v: out of range", n)
		}
		c.scrollbar, c.hasScrollbar = float32(n), true
	case "bell":
		m, ok := parseBellMode(val)
		if !ok {
			return fmt.Errorf("bell %q: want auto|audible|visual|both|none", val)
		}
		c.bell, c.hasBell = m, true
	case "middle-click-paste":
		b, ok := parseConfigBool(val)
		if !ok {
			return fmt.Errorf("middle-click-paste %q: want true|false", val)
		}
		c.middleClickPaste, c.hasMiddleClickPaste = b, true
	}
	return nil
}

// parseConfigBool accepts the spellings users actually write in an INI file.
// strconv.ParseBool covers 1/t/T/TRUE/true/True and their false counterparts
// but rejects yes/no/on/off, which read naturally for a toggle.
func parseConfigBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "on":
		return true, true
	case "no", "off":
		return false, true
	}
	b, err := strconv.ParseBool(s)
	return b, err == nil
}

// maxScrollbarPx bounds a [general] scrollbar entry. A thumb wider than this
// would eat the pane; negative values are legal (they hide the scrollbar).
const maxScrollbarPx = 100

// parseBellMode maps a config bell value to a term.BellMode.
func parseBellMode(s string) (term.BellMode, bool) {
	switch strings.ToLower(s) {
	case "auto":
		return term.BellAuto, true
	case "audible":
		return term.BellAudible, true
	case "visual":
		return term.BellVisual, true
	case "both":
		return term.BellBoth, true
	case "none", "off":
		return term.BellNone, true
	default:
		return term.BellAuto, false
	}
}

const maxShortcutLen = 64 // "Cmd+Ctrl+Alt+Shift+F25" is ~22 chars; 64 is generous

// parseShortcut parses a chord string like "Cmd+D", "Ctrl+Shift+[", "Tab".
// Returns the Shortcut and true on success; Shortcut{}, false otherwise.
// Canonical format: modifiers first (Cmd/Ctrl/Alt/Shift), then the key name,
// all joined with "+".
func parseShortcut(s string) (gui.Shortcut, bool) {
	if len(s) > maxShortcutLen {
		return gui.Shortcut{}, false
	}
	parts := strings.Split(s, "+")
	if len(parts) == 0 {
		return gui.Shortcut{}, false
	}
	var mods gui.Modifier
	for _, m := range parts[:len(parts)-1] {
		switch strings.ToLower(m) {
		case "cmd", "super":
			mods |= gui.ModSuper
		case "ctrl":
			mods |= gui.ModCtrl
		case "alt", "opt":
			mods |= gui.ModAlt
		case "shift":
			mods |= gui.ModShift
		default:
			return gui.Shortcut{}, false
		}
	}
	key, ok := parseKeyName(parts[len(parts)-1])
	if !ok {
		return gui.Shortcut{}, false
	}
	return gui.Shortcut{Key: key, Modifiers: mods}, true
}

// parseKeyName maps a key name to a gui.KeyCode. Accepts single letters
// (A-Z, a-z), digits (0-9), F1-F25, and named special keys.
func parseKeyName(name string) (gui.KeyCode, bool) {
	if len(name) == 1 {
		c := name[0]
		switch {
		case c >= 'A' && c <= 'Z':
			return gui.KeyA + gui.KeyCode(c-'A'), true
		case c >= 'a' && c <= 'z':
			return gui.KeyA + gui.KeyCode(c-'a'), true
		case c >= '0' && c <= '9':
			return gui.Key0 + gui.KeyCode(c-'0'), true
		}
	}
	// F1–F25.
	lower := strings.ToLower(name)
	if len(lower) >= 2 && lower[0] == 'f' {
		n, err := strconv.Atoi(lower[1:])
		if err == nil && n >= 1 && n <= 25 {
			return gui.KeyF1 + gui.KeyCode(n-1), true
		}
	}
	switch lower {
	case "space":
		return gui.KeySpace, true
	case "enter", "return":
		return gui.KeyEnter, true
	case "escape", "esc":
		return gui.KeyEscape, true
	case "tab":
		return gui.KeyTab, true
	case "backspace":
		return gui.KeyBackspace, true
	case "delete", "del":
		return gui.KeyDelete, true
	case "insert":
		return gui.KeyInsert, true
	case "home":
		return gui.KeyHome, true
	case "end":
		return gui.KeyEnd, true
	case "pageup":
		return gui.KeyPageUp, true
	case "pagedown":
		return gui.KeyPageDown, true
	case "left":
		return gui.KeyLeft, true
	case "right":
		return gui.KeyRight, true
	case "up":
		return gui.KeyUp, true
	case "down":
		return gui.KeyDown, true
	case "[", "leftbracket":
		return gui.KeyLeftBracket, true
	case "]", "rightbracket":
		return gui.KeyRightBracket, true
	case "/", "slash":
		return gui.KeySlash, true
	case ";", "semicolon":
		return gui.KeySemicolon, true
	case ",", "comma":
		return gui.KeyComma, true
	case ".", "period":
		return gui.KeyPeriod, true
	case "-", "minus":
		return gui.KeyMinus, true
	case "=", "equal":
		return gui.KeyEqual, true
	case "`", "graveaccent":
		return gui.KeyGraveAccent, true
	default:
		return gui.KeyInvalid, false
	}
}

// loadConfig reads the config file for cfg, returning parsed keybindings.
// A missing file is silently ignored (no error, empty keybindings).
func loadConfig(cfg Cfg) workspaceConfig {
	path := cfg.ConfigPath
	if path == "" {
		path = DefaultConfigPath()
		if path == "" {
			return workspaceConfig{}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("workspace: open config %s: %v", path, err)
		}
		return workspaceConfig{}
	}
	defer func() { _ = f.Close() }()
	parsed, errs := parseConfig(f)
	for _, e := range errs {
		log.Printf("workspace: config %s: %v", path, e)
	}
	return parsed
}

// termOpts are the per-Term settings the config file can override. They live
// on Cfg (unexported) rather than in a separate parameter so that a single
// "effective Cfg" object flows to every pane, the way Cfg.TextStyle already
// does — no second channel for tabs and panes to keep in sync.
type termOpts struct {
	// theme is nil when the config names no theme (or names an unknown one),
	// in which case panes keep term's own default: Cfg.Themes[0].
	theme *term.Theme

	keys       term.KeyMap
	scrollback int
	scrollbar  float32
	bell       term.BellMode

	// middleClickPaste defaults to the platform convention rather than to
	// false, so it is always set explicitly by applySettings.
	middleClickPaste bool
}

// defaultMiddleClickPaste is the middle-click-paste default when the config
// file says nothing. Unix has the PRIMARY selection and the muscle memory that
// goes with it; macOS and Windows have neither, and a stray middle click there
// pasting into a shell would be a surprise. This is the only place the policy
// lives — term/ itself just honors Cfg.MiddleClickPaste.
func defaultMiddleClickPaste() bool { return runtime.GOOS == "linux" }

// applySettings returns base with the config file's [font] and [general]
// values layered on top, plus the resolved term.* keybindings.
//
// base must be the embedder's *pristine* Cfg, never a previously-resolved one:
// a reload recomputes from scratch, so deleting a key from the file restores
// the embedder's default rather than leaving the last-loaded value in place.
func applySettings(base Cfg, fc workspaceConfig, keys term.KeyMap) Cfg {
	out := base
	if fc.fontFamily != "" {
		out.TextStyle.Family = fc.fontFamily
	}
	if fc.hasFontSize {
		out.TextStyle.Size = fc.fontSize
	}
	out.opts = termOpts{keys: keys}
	if fc.hasScrollback {
		out.opts.scrollback = fc.scrollback
	}
	if fc.hasScrollbar {
		out.opts.scrollbar = fc.scrollbar
	}
	if fc.hasBell {
		out.opts.bell = fc.bell
	}
	out.opts.middleClickPaste = defaultMiddleClickPaste()
	if fc.hasMiddleClickPaste {
		out.opts.middleClickPaste = fc.middleClickPaste
	}
	if fc.theme != "" {
		if th, ok := findTheme(base.Themes, fc.theme); ok {
			out.opts.theme = &th
		} else {
			log.Printf("workspace: unknown theme %q in config; keeping default", fc.theme)
		}
	}
	return out
}

// findTheme looks up a theme by display name, case-insensitively so a config
// file need not reproduce the exact capitalization of "Catppuccin Mocha".
func findTheme(themes []term.NamedTheme, name string) (term.Theme, bool) {
	for _, nt := range themes {
		if strings.EqualFold(nt.Name, name) {
			return nt.Theme, true
		}
	}
	return term.Theme{}, false
}

// termPrefix / workspacePrefix namespace a [keybindings] entry. A key with
// neither prefix is treated as workspace.*, which is what bare keys meant
// before the term.* namespace existed; only the explicit forms are documented.
const (
	termPrefix      = "term."
	workspacePrefix = "workspace."
)

// unbindValue is the chord value that removes a binding instead of moving it,
// so a key the terminal intercepts can be handed back to the child process.
const unbindValue = "none"

// applyKeybindingOverrides applies config-file keybinding overrides to cmds in
// place and returns the term.KeyMap for the term.* entries. Unknown names,
// unparseable chords, and shortcut collisions are logged; the affected entry
// keeps its default.
//
// Workspace entries are resolved first, then term entries are checked against
// the *resulting* command shortcuts. That order matters: go-gui's global
// command registry runs before the focused widget's onKeyDown, so a term.*
// binding shadowed by a workspace command would never fire — silently, since
// the workspace command would just run instead. Rejecting the collision keeps
// the term default working and puts the conflict in the log.
func applyKeybindingOverrides(cmds []gui.Command, kb map[string]string) term.KeyMap {
	if len(kb) == 0 {
		return nil
	}
	applyWorkspaceBindings(cmds, kb)
	return termBindings(cmds, kb)
}

// sortedBindingKeys returns the keys of kb that are (or are not) term.* names,
// in a stable order.
//
// The order matters wherever a collision is rejected: whichever entry is seen
// first wins, so ranging over the map directly would pick the winner — and
// emit the log line — differently on each run. Both binding passes reject
// collisions, so both sort.
func sortedBindingKeys(kb map[string]string, wantTerm bool) []string {
	keys := make([]string, 0, len(kb))
	for key := range kb {
		if strings.HasPrefix(key, termPrefix) == wantTerm {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// applyWorkspaceBindings rewrites the shortcuts of the workspace.* commands
// named in kb. Bare (unprefixed) keys are workspace commands too.
func applyWorkspaceBindings(cmds []gui.Command, kb map[string]string) {
	byID := make(map[string]int, len(cmds))
	for i, cmd := range cmds {
		byID[cmd.ID] = i
	}
	for _, key := range sortedBindingKeys(kb, false) {
		chord := kb[key]
		// Bare keys are workspace commands, so normalize both spellings to
		// the namespaced command ID.
		fullID := workspacePrefix + strings.TrimPrefix(key, workspacePrefix)
		idx, ok := byID[fullID]
		if !ok {
			log.Printf("workspace: unknown command %q in config", fullID)
			continue
		}
		if strings.EqualFold(chord, unbindValue) {
			cmds[idx].Shortcut = gui.Shortcut{}
			continue
		}
		sc, ok := parseShortcut(chord)
		if !ok {
			log.Printf("workspace: cannot parse shortcut %q for %q", chord, fullID)
			continue
		}
		// Detect collision with any other command's current shortcut.
		collision := false
		for i, cmd := range cmds {
			if i != idx && cmd.Shortcut.IsSet() && cmd.Shortcut == sc {
				log.Printf("workspace: shortcut %q for %q collides with %q; keeping default", chord, fullID, cmd.ID)
				collision = true
				break
			}
		}
		if !collision {
			cmds[idx].Shortcut = sc
		}
	}
}

// termBindings builds the term.KeyMap for the term.* entries in kb, rejecting
// any chord already claimed by a workspace command or by an earlier term
// entry. Returns nil when no term.* entry survives, which leaves every Term
// binding at its built-in default.
func termBindings(cmds []gui.Command, kb map[string]string) term.KeyMap {
	var km term.KeyMap
	keys := sortedBindingKeys(kb, true)

	taken := make(map[gui.Shortcut]string, len(cmds))
	for _, cmd := range cmds {
		if cmd.Shortcut.IsSet() {
			taken[cmd.Shortcut] = cmd.ID
		}
	}
	for _, key := range keys {
		action, ok := term.ParseAction(key)
		if !ok {
			log.Printf("workspace: unknown terminal action %q in config", key)
			continue
		}
		chord := kb[key]
		if strings.EqualFold(chord, unbindValue) {
			// The zero Shortcut is term's "unbind" sentinel: the key falls
			// through to the child process.
			if km == nil {
				km = make(term.KeyMap)
			}
			km[action] = gui.Shortcut{}
			continue
		}
		sc, ok := parseShortcut(chord)
		if !ok {
			log.Printf("workspace: cannot parse shortcut %q for %q", chord, key)
			continue
		}
		if owner, dup := taken[sc]; dup {
			log.Printf("workspace: shortcut %q for %q collides with %q; keeping default", chord, key, owner)
			continue
		}
		taken[sc] = key
		if km == nil {
			km = make(term.KeyMap)
		}
		km[action] = sc
	}
	return km
}
