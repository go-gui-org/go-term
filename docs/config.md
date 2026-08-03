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
and every keybinding.

`Cmd+,` opens this file in the OS-default editor, creating a commented stub
first if it doesn't exist yet. That binding belongs to falcon, not to
`term/workspace`, so it isn't rebindable through the `[keys]` section below.

A setting **deleted** from the file reverts to the embedder's built-in default
on the next reload — the file is always re-applied to a pristine base rather
than layered onto the previously loaded values.

Reload leaves per-pane state that the config doesn't mention alone. The one
thing it does reset is font zoom (`Cmd+=` / `Cmd+-`), and only when the font
actually changed: the zoom is an absolute point size, so it would otherwise
outrank the new configured size until the next `Cmd+0`.

## `[font]`

| Key      | Type   | Default            | Meaning                                                  |
| -------- | ------ | ------------------ | -------------------------------------------------------- |
| `family` | string | embedder's default | Font family name, as the font's own name table spells it |
| `size`   | number | embedder's default | Point size, clamped to 4–72                              |

```ini
[font]
family = JetBrainsMono NFM
size   = 12
```

The family must be the name the font reports, not its marketing name — the
pure-Go font discovery reads the name table, where JetBrains Mono Nerd Font
Mono appears as `JetBrainsMono NFM`.

## `[general]`

| Key                  | Type    | Default                | Meaning                                                                         |
| -------------------- | ------- | ---------------------- | ------------------------------------------------------------------------------- |
| `theme`              | string  | embedder's first theme | Color theme, by display name (case-insensitive)                                 |
| `scrollback`         | integer | `5000`                 | Scrollback rows. `0` restores the default; a negative value disables scrollback |
| `bell`               | enum    | `auto`                 | `auto`, `audible`, `visual`, `both`, `none`                                     |
| `scrollbar`          | number  | `4`                    | Scrollbar thumb width in px. Negative hides the scrollbar                       |
| `minimum-contrast`   | number  | `1` (off)              | WCAG contrast ratio, `1`–`21`, that text is forced to reach against its cell background |
| `middle-click-paste` | boolean | on for Linux, else off | Paste with the middle mouse button — see [Selection and mouse](#selection-and-mouse) |
| `notify-after`       | duration | `0` (off)             | Notify when a command that ran this long finishes while you are looking elsewhere — see [Shell integration](#shell-integration) |

```ini
[general]
theme              = Dracula
scrollback         = 5000
bell               = auto
scrollbar          = 4
minimum-contrast   = 1
middle-click-paste = true
```

Booleans accept `true`/`false`, `yes`/`no`, `on`/`off`, and `1`/`0`.

`theme` is matched by name against the themes the embedder registered; an
unknown name is logged and the default is kept. Only names are accepted —
nothing loads a theme from disk.

`falcon` registers go-term's own `Default` plus the whole bundled corpus: 602
themes, 473 dark and 129 light. **Press `Cmd+Shift+T` to browse them** with a
live preview and a filter — that is the intended way to choose one, and the
names are listed in [themes.md](themes.md) if you would rather set it here.

Theme names that go-term shipped before the corpus (`Tokyo Night`, `One Dark`,
`Solarized Dark`, `Gruvbox`, …) still resolve, to their closest corpus
equivalent. Existing config files and saved workspaces keep working.

Picking a light theme also switches `falcon`'s window chrome (tab bar,
borders) to light, and tells any child app subscribed to mode 2031 that the
color scheme changed — so a `neovim` or `delta` that follows the terminal
re-themes itself along with it. `COLORFGBG` is set at spawn from the startup
theme's character, which is how `vim`, `less` and some prompts decide the same
question without subscribing to anything. It cannot be updated in a running
child, so a shell started under a dark theme still reports dark after a switch.

### Minimum contrast

`minimum-contrast` is the answer to the one thing a theme cannot fix. A
24-bit color sequence is not themeable: `eza`, `starship` and most `ls` color
schemes emit colors chosen against a dark background, and on a light theme they
arrive exactly as sent. On a measured Catppuccin Latte session, 77% of the
visible glyph pixels sat below a 3:1 contrast ratio, and every one of them came
from a truecolor sequence rather than from the theme's palette.

Setting a ratio forces any foreground that falls below it to be pushed toward
white or black — whichever direction has room against that cell's background —
until it clears. The color is blended rather than replaced, so a red that fails
by a little stays recognizably red. Useful values:

| Value | Effect                                                              |
| ----- | ------------------------------------------------------------------- |
| `1`   | Off (the default). A color against itself is 1:1                     |
| `3`   | Fixes the worst dark-tuned colors, leaves most palettes alone        |
| `4.5` | The WCAG floor for body text                                         |
| `7`   | WCAG AAA. Expect most colors to be visibly adjusted                  |

The clamp is render-only: the grid keeps the color the app sent, so copy,
search and session recordings are unaffected. It costs about 11 ns per cell
when on and nothing measurable when off.

Bell modes: `auto` plays the system alert sound and falls back to a visual
flash where the platform has none; `audible` never flashes; `visual` never
beeps; `both` does both; `none` ignores BEL entirely.

Scrollback is clamped to at most 100000 rows.

## Shell integration

A handful of features need to know where your prompt is, where a command's
output starts, and whether it succeeded. Nothing in a terminal can work that
out by looking at the text — the shell has to say so, by printing OSC 133
marks around each command. Until you install the hooks below, these do
nothing:

| Feature                              | Shortcut                              |
| ------------------------------------ | ------------------------------------- |
| Jump to the previous / next prompt   | <kbd>Cmd</kbd>+<kbd>Up</kbd> / <kbd>Down</kbd> |
| Jump to the last failed command      | <kbd>Cmd</kbd>+<kbd>Shift</kbd>+<kbd>E</kbd> |
| Select a command's whole output      | <kbd>Cmd</kbd>+<kbd>Shift</kbd>+<kbd>O</kbd> |
| Failure ticks in the scrollbar       | —                                     |
| `notify-after` (below)               | —                                     |

Open a new pane in the current directory also depends on the OSC 7 report the
same hooks emit.

### Installing

Add one line to your shell's rc file, pointing at this repository's copy of
the script:

```bash
# ~/.bashrc
source /path/to/go-term/scripts/shell-integration/goterm.bash
```

```zsh
# ~/.zshrc
source /path/to/go-term/scripts/shell-integration/goterm.zsh
```

```fish
# ~/.config/fish/config.fish
source /path/to/go-term/scripts/shell-integration/goterm.fish
```

Then start a new shell. The scripts are safe to source twice, append to any
`precmd`/`preexec` chain you already have rather than replacing it, and emit
nothing that other terminals mind — iTerm2, kitty, and WezTerm read the same
marks, so one rc file works everywhere.

Two notes on what the scripts do and do not do:

- **fish 4.0 and newer need nothing.** They emit the whole set natively, so
  the fish script installs nothing there and exists only for fish 3.x.
- **bash can only preserve a `DEBUG` trap it can see.** If you set your own
  `trap … DEBUG`, load [bash-preexec] before this script — it is detected and
  used, and then nothing fights over the trap. A sourced file cannot read the
  outer `DEBUG` trap, so without bash-preexec ours replaces yours.

[bash-preexec]: https://github.com/rcaloras/bash-preexec

### Notify on long-running commands

With the marks flowing, `notify-after` fires a desktop notification when a
command that ran at least that long finishes while you are looking somewhere
else — the window is in the background, or the pane is in a tab you are not
on. A command that finishes in the pane you are watching never notifies,
because you just saw it happen.

```ini
[general]
notify-after = 30      # seconds; "30s" and "2m" work too, 0 disables
```

The notification names the command (`cargo build --release`) and reports the
duration, plus the exit status when the command failed. Values below one
second are raised to one second, and anything over an hour is rejected as a
unit mix-up.

### Tab activity indicators

A tab you are not looking at shows a marker to the left of its title:

| Marker | Meaning                                                       |
| ------ | ------------------------------------------------------------- |
| `●`    | The pane produced output                                      |
| `○`    | It produced output, then went quiet for 10 seconds            |
| `!`    | The pane rang the bell                                        |

Switching to the tab clears whichever marker it was showing. These need no
shell integration — they follow raw output, not marks.

## Selection and mouse

Mouse gestures are fixed, not rebindable — they are pointer behavior rather
than commands, and every terminal spells them the same way.

| Gesture              | Effect                                                                    |
| -------------------- | ------------------------------------------------------------------------- |
| Drag                 | Select by character                                                       |
| Double click         | Select the word under the pointer; drag to extend word by word            |
| Triple click         | Select the whole logical line; drag to extend line by line                |
| <kbd>Alt</kbd>+drag  | Rectangular (block) selection — one column band across every row it spans |
| <kbd>Shift</kbd>+click | Extend the existing selection to the click point                        |
| Middle click         | Paste (see `middle-click-paste` above)                                    |
| <kbd>Cmd</kbd>/<kbd>Ctrl</kbd>+click | Open the URL under the pointer                            |

Releasing a selection copies it, so there is no separate copy step for the
mouse. On X11 the selection is also published as PRIMARY, the buffer
middle-click pastes — independent of the clipboard, so <kbd>Cmd</kbd>+C and a
mouse selection can hold two different values at once.

A word is a run of non-blank characters with no punctuation class, so
double-clicking grabs a whole path, URL, or `--flag=value` rather than
stopping at every `/` or `-`. Double-clicking whitespace selects the
whitespace run. A logical line follows soft wraps, so triple-clicking one row
of a wrapped paragraph selects all of it.

Clicking a fourth time returns to character selection, cycling the
granularities. Two clicks count as a double only when the second lands within
500 ms and within half a cell of the first.

### Middle-click paste

`middle-click-paste` defaults on for Linux, which has the PRIMARY selection
and the muscle memory that goes with it, and off for macOS and Windows, where
neither exists and a stray middle click pasting into a shell would surprise.
Set it explicitly to override in either direction:

```ini
[general]
middle-click-paste = true
```

Where there is no PRIMARY, the gesture pastes the clipboard instead. An
application that has enabled mouse reporting always receives the middle button
itself — no paste is injected behind its back.

### The wheel on the alt screen

Pagers such as `less` and `man` take the alternate screen but never enable
mouse reporting, so there is nothing to scroll and nothing to report. The
wheel there sends <kbd>↑</kbd>/<kbd>↓</kbd> instead, one key per row of
scroll distance, matching kitty, iTerm2, and Ghostty. Full-screen applications
that *do* enable mouse reporting (vim with `mouse=a`, tmux) receive real wheel
events unchanged.

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

| Command                                        | Default                                                       |
| ---------------------------------------------- | ------------------------------------------------------------- |
| `workspace.splitVertical`                      | `Cmd+D`                                                       |
| `workspace.splitHorizontal`                    | `Cmd+Shift+D`                                                 |
| `workspace.closePane`                          | `Cmd+Shift+W`                                                 |
| `workspace.nextPane`                           | `Cmd+]`                                                       |
| `workspace.prevPane`                           | `Cmd+[`                                                       |
| `workspace.resizeLeft`                         | `Cmd+Ctrl+Left`                                               |
| `workspace.resizeRight`                        | `Cmd+Ctrl+Right`                                              |
| `workspace.resizeUp`                           | `Cmd+Ctrl+Up`                                                 |
| `workspace.resizeDown`                         | `Cmd+Ctrl+Down`                                               |
| `workspace.newTab`                             | `Cmd+T`                                                       |
| `workspace.closeTab`                           | `Cmd+Ctrl+W`                                                  |
| `workspace.moveTabLeft`                        | `Cmd+Alt+[`                                                   |
| `workspace.moveTabRight`                       | `Cmd+Alt+]`                                                   |
| `workspace.nextTab`                            | `Cmd+Shift+]`                                                 |
| `workspace.prevTab`                            | `Cmd+Shift+[`                                                 |
| `workspace.tab1` … `workspace.tab9`            | `Cmd+1` … `Cmd+9`                                             |
| `workspace.toggleRecording`                    | `Cmd+Shift+R`                                                 |
| `workspace.toggleBroadcast`                    | `Cmd+Shift+I`                                                 |
| `workspace.chooseTheme`                        | `Cmd+Shift+T`                                                 |
| `workspace.reloadConfig`                       | `Cmd+Shift+,`                                                 |
| `workspace.toggleHelp`                         | `Cmd+/`                                                       |
| `workspace.dismissOverlay`                     | `Escape` (only while an overlay is open)                      |
| `workspace.themeBrowserUp` / `Down`            | `Up` / `Down` (only while the theme browser is open)          |
| `workspace.themeBrowserPageUp` / `PageDown`    | `PageUp` / `PageDown` (only while the theme browser is open)  |
| `workspace.themeBrowserConfirm`                | `Enter` (only while the theme browser is open)                |

#### Broadcast input

`workspace.toggleBroadcast` mirrors what you type into **every live pane in the
active tab** — the multiplexer feature for driving several hosts in lockstep
(`tmux setw synchronize-panes`, iTerm2 "Broadcast Input"). While it is on, the
split dividers turn amber and a `⌁ BROADCAST` badge sits at the bottom of the
window.

- Scope is one tab. Panes in other tabs never receive broadcast input.
- Typed keys and pastes are mirrored. Mouse reporting, selection, copy and
  scrolling stay per-pane.
- A paste is re-encoded for each receiving pane according to that pane's own
  bracketed-paste (`?2004`) state. Typed keys are mirrored as encoded by the
  pane you typed in, so panes sitting in different cursor-key or Kitty-keyboard
  modes may see the wrong bytes for special keys.
- Receiving panes snap back to the live view, exactly as they would if you had
  typed into them directly — a pane scrolled into its scrollback won't sit
  frozen while its shell runs what you just sent.
- The toggle is unconditional, like tmux's. It can be armed on a single pane
  and stays armed when a tab drops back to one, so a later split resumes
  broadcasting. The badge is on screen whenever the mode is on.
- It is never persisted: a restored workspace starts with broadcast off.

### `term.*` actions

| Action                  | Default                                |
| ----------------------- | -------------------------------------- |
| `term.copy`             | `Cmd+C` / `Ctrl+Shift+C`               |
| `term.paste`            | `Cmd+V` / `Ctrl+Shift+V`               |
| `term.find`             | `Cmd+F`                                |
| `term.toggle-regex`     | `Ctrl+R` (in Find)                     |
| `term.next-match`       | `Enter` (in Find)                      |
| `term.prev-match`       | `Shift+Enter` (in Find)                |
| `term.prev-prompt`      | `Cmd+Up`                               |
| `term.next-prompt`      | `Cmd+Down`                             |
| `term.jump-failure`     | `Cmd+Shift+E`                          |
| `term.select-output`    | `Cmd+Shift+O`                          |
| `term.scroll-page-up`   | `PageUp`                               |
| `term.scroll-page-down` | `PageDown`                             |
| `term.scroll-top`       | `Shift+Home`                           |
| `term.scroll-bottom`    | `Shift+End`                            |
| `term.font-inc`         | `Cmd+=`                                |
| `term.font-dec`         | `Cmd+-`                                |
| `term.font-reset`       | `Cmd+0`                                |
| `term.copy-mode`        | `Cmd+Shift+Space` / `Ctrl+Shift+Space` |

An override replaces the action's whole default chord list with the single
chord you give, but inherits the action's Shift tolerance. Where Shift is a
keyboard artefact rather than meaningful — the font-zoom keys, copy/paste,
Find — a stray Shift still matches, so rebinding `term.find` to `Cmd+G` also
answers `Cmd+Shift+G`. Where Shift picks a direction (`term.next-match` vs
`term.prev-match`) it is matched exactly.

Several `term.*` actions only fire in context, and that gate is _not_ part of
the binding: `Ctrl+C` still sends SIGINT when nothing is selected, the Find
keys only apply while the search bar is open, and `term.copy` only copies when
there is a selection.

#### Shell integration: prompt marks, failures, output selection

Four actions are driven by OSC 133 semantic marks, which your shell must emit
(bash via `bash-preexec`, or the zsh/fish shell-integration snippets). Without
them these keys do nothing:

- `term.prev-prompt` / `term.next-prompt` scroll between prompts.
- `term.jump-failure` scrolls to the most recent command that exited non-zero,
  and each failure gets a red tick in the scrollbar track. Repeated presses
  walk back through older failures and then wrap to the newest. A command whose
  shell reported _no_ exit status never counts as a failure.
- `term.select-output` selects exactly the output region of the command under
  the cursor and enters copy mode with that selection live, so `y` or `Cmd+C`
  copies it. With the cursor on a fresh prompt it selects the previous
  command's output, which is the usual case right after a command finishes.

All four are no-ops while a full-screen app owns the alt screen, since marks
are not recorded there.

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

### `term.copy-mode.*` — copy mode

`term.copy-mode` (`Cmd+Shift+Space`, or `Ctrl+Shift+Space`) enters copy mode: a
vim-keyed state for scrolling the buffer, selecting text, and copying it
without a mouse. While it is active **no key reaches the shell** — every
keystroke is consumed by the terminal until you leave the mode — and incoming
output is frozen so the text you are selecting cannot scroll away underneath
you. A status bar across the top shows the key hints and an `output paused`
marker; it takes over the topmost visible row for as long as the mode is
active, so the newest output — the usual reason to enter copy mode — stays
visible, and the covered line is one `k` away.

A click anywhere in the pane also leaves copy mode, handing selection back to
the mouse.

These actions are rebindable like any other, but they are deliberately left out
of the `Cmd+/` help overlay — twenty extra rows in a flat list helps nobody.

| Action                          | Default               | Does                                    |
| ------------------------------- | --------------------- | --------------------------------------- |
| `term.copy-mode.exit`           | `Escape` / `q`        | Leave copy mode                         |
| `term.copy-mode.left`           | `h` / `Left`          | Move one cell left                      |
| `term.copy-mode.down`           | `j` / `Down`          | Move one row down                       |
| `term.copy-mode.up`             | `k` / `Up`            | Move one row up                         |
| `term.copy-mode.right`          | `l` / `Right`         | Move one cell right                     |
| `term.copy-mode.word-fwd`       | `w`                   | Start of the next word                  |
| `term.copy-mode.word-back`      | `b`                   | Start of the previous word              |
| `term.copy-mode.line-start`     | `0` / `Home`          | First column                            |
| `term.copy-mode.line-end`       | `$` / `End`           | Last non-blank cell                     |
| `term.copy-mode.top`            | `g`                   | Oldest scrollback row                   |
| `term.copy-mode.bottom`         | `G`                   | Newest row                              |
| `term.copy-mode.half-page-up`   | `Ctrl+U`              | Up half a screen                        |
| `term.copy-mode.half-page-down` | `Ctrl+D`              | Down half a screen                      |
| `term.copy-mode.page-up`        | `PageUp` / `Ctrl+B`   | Up one screen                           |
| `term.copy-mode.page-down`      | `PageDown` / `Ctrl+F` | Down one screen                         |
| `term.copy-mode.select-char`    | `v`                   | Start/cancel a character-wise selection |
| `term.copy-mode.select-line`    | `V`                   | Start/cancel a line-wise selection      |
| `term.copy-mode.yank`           | `y` / `Enter`         | Copy the selection and exit             |
| `term.copy-mode.search-fwd`     | `/`                   | Open the search bar, searching forward  |
| `term.copy-mode.search-back`    | `?`                   | Open the search bar, searching backward |
| `term.copy-mode.next-match`     | `n`                   | Move to the next match                  |
| `term.copy-mode.prev-match`     | `N`                   | Move to the previous match              |
| `term.copy-mode.prev-mark`      | `[`                   | Previous shell prompt (needs OSC 133)   |
| `term.copy-mode.next-mark`      | `]`                   | Next shell prompt (needs OSC 133)       |

With no selection yet, `y` copies the single cell under the cursor. Pressing
`v` or `V` a second time cancels the selection without leaving the mode.

In the search bar, typing edits the query as usual; `Enter` closes it and moves
the copy cursor onto the match, `Shift+Enter` does the same in the opposite
direction, and `Escape` closes the bar but stays in copy mode.

The `$` and `?` defaults are the US-layout `Shift+4` and `Shift+/`. On another
layout, rebind them to whatever your keyboard actually produces.

Rebinding a copy-mode action to a chord that holds `Ctrl`, `Alt`, or `Cmd`
works, as does rebinding it to a bare printable key. Both reach copy mode; they
simply arrive through different platform events (macOS delivers an unmodified
printable key as text input, not as a key press).

## Full example

```ini
[font]
family = JetBrainsMono NFM
size   = 13

[general]
theme              = TokyoNight
scrollback         = 20000
bell               = visual
scrollbar          = 6
middle-click-paste = true

[keybindings]
workspace.splitVertical   = Cmd+D
workspace.splitHorizontal = Cmd+Shift+D
term.find                 = Cmd+G
term.toggle-regex         = none
```
