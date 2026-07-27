# User config file

go-term reads an optional INI-style config file at startup. Everything in it
is optional; a missing file, an unknown section, or an unknown key is ignored,
and a malformed line is logged and skipped without discarding the rest of the
file. A broken config never prevents the terminal from starting.

The file is read by the **workspace** layer, so it applies to embedders that
use `term/workspace` (including the `falcon` example). A bare `term.Term`
embedder configures itself through `term.Cfg` in code.

## Location

In order of preference:

1. the path the embedder passes as `workspace.Cfg.ConfigPath`, if set
2. `$XDG_CONFIG_HOME/go-term/config`
3. `~/.config/go-term/config`
4. `os.UserConfigDir()/go-term/config`

## Format

```ini
# Comments start with '#'. Blank lines are ignored.
[section]
key = value
```

Whitespace around `=` is trimmed. Keys and values are capped at 128 bytes each.

## Reloading

`Cmd+Shift+,` (`Ctrl+Alt+,` on Windows) re-reads the file and applies it to
every open pane without restarting: font, theme, scrollback, bell, scrollbar
and every keybinding. `Cmd+,` is deliberately left free for a future settings
UI.

A setting **deleted** from the file reverts to the embedder's built-in default
on the next reload — the file is always re-applied to a pristine base rather
than layered onto the previously loaded values.

Reload leaves per-pane state that the config doesn't mention alone. The one
thing it does reset is font zoom (`Cmd+=` / `Cmd+-`), and only when the font
actually changed: the zoom is an absolute point size, so it would otherwise
outrank the new configured size until the next `Cmd+0`.

## `[font]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `family` | string | embedder's default | Font family name, as the font's own name table spells it |
| `size` | number | embedder's default | Point size, clamped to 4–72 |

```ini
[font]
family = JetBrainsMono NFM
size   = 12
```

The family must be the name the font reports, not its marketing name — the
pure-Go font discovery reads the name table, where JetBrains Mono Nerd Font
Mono appears as `JetBrainsMono NFM`.

## `[general]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `theme` | string | embedder's first theme | Color theme, by display name (case-insensitive) |
| `scrollback` | integer | `5000` | Scrollback rows. `0` restores the default; a negative value disables scrollback |
| `bell` | enum | `auto` | `auto`, `audible`, `visual`, `both`, `none` |
| `scrollbar` | number | `4` | Scrollbar thumb width in px. Negative hides the scrollbar |

```ini
[general]
theme      = Dracula
scrollback = 5000
bell       = auto
scrollbar  = 4
```

`theme` is matched by name against the themes the embedder registered; an
unknown name is logged and the default is kept. Only names are accepted —
nothing loads a theme from disk. `falcon` ships: Default, Dracula, Catppuccin
Mocha, Tokyo Night, Monokai, One Dark, Rosé Pine, Kanagawa, Ayu Dark,
Everforest, GitHub Dark, Gruvbox, Nord, Solarized Dark.

Bell modes: `auto` plays the system alert sound and falls back to a visual
flash where the platform has none; `audible` never flashes; `visual` never
beeps; `both` does both; `none` ignores BEL entirely.

Scrollback is clamped to at most 100000 rows.

## `[keybindings]`

Each entry rebinds one action. Keys are namespaced:

- `workspace.<command>` — window-level commands (tabs, panes, overlays)
- `term.<action>` — terminal-level actions (copy, find, scrollback, zoom)

A key with no prefix means `workspace.` — the form that predates the `term.`
namespace. New configs should use the explicit form.

```ini
[keybindings]
workspace.splitVertical = Cmd+D
workspace.newTab        = Cmd+T
term.copy               = Cmd+Shift+C
term.find               = Cmd+F
term.scroll-page-up     = none      # hand PageUp back to the child process
```

### Chord syntax

Modifiers first, then the key, joined with `+`:

`Cmd` (alias `Super`), `Ctrl`, `Alt` (alias `Opt`), `Shift`.

Key names: `A`–`Z`, `0`–`9`, `F1`–`F25`, `Space`, `Enter`/`Return`,
`Escape`/`Esc`, `Tab`, `Backspace`, `Delete`/`Del`, `Insert`, `Home`, `End`,
`PageUp`, `PageDown`, `Left`, `Right`, `Up`, `Down`, and the punctuation keys
`[` `]` `/` `;` `,` `.` `-` `=` `` ` `` (also spelled `LeftBracket`,
`RightBracket`, `Slash`, `Semicolon`, `Comma`, `Period`, `Minus`, `Equal`,
`GraveAccent`). Modifier and key names are case-insensitive.

The value `none` **unbinds** the action. For a `term.*` action that hands the
key to the child process, which is the point of unbinding — e.g. freeing
`Ctrl+R` so a shell's reverse-i-search works instead of toggling regex search.

### Collisions

A chord already claimed by another binding is rejected, logged, and the losing
entry keeps its default. This is checked **across both namespaces**: go-gui
dispatches global workspace commands before the focused terminal sees the key,
so a `term.*` binding shadowed by a `workspace.*` one would never fire —
silently, since the workspace command would just run in its place.

Rebinding both sides in the same file works fine; collisions are judged against
the final assignment, not the built-in defaults.

On Windows the Super (Windows) key is OS-reserved, so the built-in
`Cmd`-based defaults are remapped (`Cmd`→`Ctrl+Shift`, `Cmd+Shift`→`Ctrl+Alt`,
`Cmd+Ctrl`→`Ctrl+Alt+Shift`, `Cmd+Alt`→`Alt+Shift`). Bindings you write in the
config file are used **verbatim** — no remapping — so write the chord you
actually want to press.

### `workspace.*` commands

| Command | Default |
|---|---|
| `workspace.splitVertical` | `Cmd+D` |
| `workspace.splitHorizontal` | `Cmd+Shift+D` |
| `workspace.closePane` | `Cmd+Shift+W` |
| `workspace.nextPane` | `Cmd+]` |
| `workspace.prevPane` | `Cmd+[` |
| `workspace.resizeLeft` | `Cmd+Ctrl+Left` |
| `workspace.resizeRight` | `Cmd+Ctrl+Right` |
| `workspace.resizeUp` | `Cmd+Ctrl+Up` |
| `workspace.resizeDown` | `Cmd+Ctrl+Down` |
| `workspace.newTab` | `Cmd+T` |
| `workspace.closeTab` | `Cmd+Ctrl+W` |
| `workspace.moveTabLeft` | `Cmd+Alt+[` |
| `workspace.moveTabRight` | `Cmd+Alt+]` |
| `workspace.nextTab` | `Cmd+Shift+]` |
| `workspace.prevTab` | `Cmd+Shift+[` |
| `workspace.tab1` … `workspace.tab9` | `Cmd+1` … `Cmd+9` |
| `workspace.toggleRecording` | `Cmd+Shift+R` |
| `workspace.chooseTheme` | `Cmd+Shift+T` |
| `workspace.reloadConfig` | `Cmd+Shift+,` |
| `workspace.toggleHelp` | `Cmd+/` |
| `workspace.dismissOverlay` | `Escape` (only while an overlay is open) |
| `workspace.themePickerUp` / `Down` / `Confirm` | `Up` / `Down` / `Enter` (only while the theme picker is open) |

### `term.*` actions

| Action | Default |
|---|---|
| `term.copy` | `Cmd+C` / `Ctrl+Shift+C` |
| `term.paste` | `Cmd+V` / `Ctrl+Shift+V` |
| `term.find` | `Cmd+F` |
| `term.toggle-regex` | `Ctrl+R` (in Find) |
| `term.next-match` | `Enter` (in Find) |
| `term.prev-match` | `Shift+Enter` (in Find) |
| `term.prev-prompt` | `Cmd+Up` |
| `term.next-prompt` | `Cmd+Down` |
| `term.scroll-page-up` | `PageUp` |
| `term.scroll-page-down` | `PageDown` |
| `term.scroll-top` | `Shift+Home` |
| `term.scroll-bottom` | `Shift+End` |
| `term.font-inc` | `Cmd+=` |
| `term.font-dec` | `Cmd+-` |
| `term.font-reset` | `Cmd+0` |

An override replaces the action's whole default chord list with the single
chord you give, but inherits the action's Shift tolerance. Where Shift is a
keyboard artefact rather than meaningful — the font-zoom keys, copy/paste,
Find — a stray Shift still matches, so rebinding `term.find` to `Cmd+G` also
answers `Cmd+Shift+G`. Where Shift picks a direction (`term.next-match` vs
`term.prev-match`) it is matched exactly.

Several `term.*` actions only fire in context, and that gate is *not* part of
the binding: `Ctrl+C` still sends SIGINT when nothing is selected, the Find
keys only apply while the search bar is open, and `term.copy` only copies when
there is a selection.

#### Caveat: PageUp/PageDown on the alt screen

`term.scroll-page-up` / `term.scroll-page-down` are gated on the **literal**
Shift state, independent of which chord matched: while a full-screen app owns
the alt screen (vim, less, htop), plain PageUp/PageDown pass through to the
app and only `Shift+PageUp`/`Shift+PageDown` scroll go-term's scrollback. That
is the "hold Shift to talk to the terminal, not the app" idiom.

Consequence: rebinding these two actions to a chord that doesn't include Shift
means they won't reach scrollback while the alt screen is active. On the normal
screen they work as bound. `term.scroll-top` / `term.scroll-bottom` have no
such gate and rebind freely.

## Full example

```ini
[font]
family = JetBrainsMono NFM
size   = 13

[general]
theme      = Tokyo Night
scrollback = 20000
bell       = visual
scrollbar  = 6

[keybindings]
workspace.splitVertical   = Cmd+D
workspace.splitHorizontal = Cmd+Shift+D
term.find                 = Cmd+G
term.toggle-regex         = none
```
