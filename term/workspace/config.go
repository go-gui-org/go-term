package workspace

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// workspaceConfig holds the parsed human config file.
type workspaceConfig struct {
	keybindings map[string]string // command suffix → chord string
}

const (
	maxKeybindings = 256 // per-file keybinding entry cap
	maxKeyLen      = 128 // max bytes for a config key
	maxValLen      = 128 // max bytes for a config value
)

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
		if section == "keybindings" {
			if len(cfg.keybindings) >= maxKeybindings {
				errs = append(errs, fmt.Errorf("line %d: keybinding limit (%d) reached", lineNum, maxKeybindings))
				continue
			}
			cfg.keybindings[key] = val
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, fmt.Errorf("scan: %w", err))
	}
	return cfg, errs
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
		dir, err := configDir()
		if err != nil {
			return workspaceConfig{}
		}
		path = filepath.Join(dir, "config")
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

// applyKeybindingOverrides applies config-file keybinding overrides to cmds
// in place. Entries in kb map command suffix → chord string. Unknown command
// names, unparseable chords, and shortcut collisions are logged; defaults
// are kept for those entries.
func applyKeybindingOverrides(cmds []gui.Command, kb map[string]string) {
	if len(kb) == 0 {
		return
	}
	byID := make(map[string]int, len(cmds))
	for i, cmd := range cmds {
		byID[cmd.ID] = i
	}
	for suffix, chord := range kb {
		fullID := "workspace." + suffix
		idx, ok := byID[fullID]
		if !ok {
			log.Printf("workspace: unknown command %q in config", fullID)
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
			if i != idx && cmd.Shortcut == sc {
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
