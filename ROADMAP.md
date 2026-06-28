# go-term: Roadmap

## Context

`go-term` is a feature-rich terminal emulator widget for the
[go-gui](https://github.com/go-gui-org/go-gui) framework. 39 phases are
complete — the widget covers modern terminal feature parity
(ghostty/iTerm2/kitty) including 24-bit color, truecolor SGR, alt
screen, logical reflow, sixel/kitty/iTerm2 graphics, BiDi/RTL text,
Kitty Keyboard Protocol, pixel-perfect scrollback, and native
split panes/tabs.

Work remaining: 1.0 API stabilisation.

Each phase below is sized for one focused PR, demo-testable by running
`cd examples/demo && go run .` and exercising the new behavior.

## Package layering for Phase 39

The pane/tab layer sits *above* `term`, not inside it. The `term`
package already exposes the right surface: `Cfg.NoWindowHandler`,
`Term.SetFocused`, `Term.HandleWindowEvent`, `Term.Rows/Cols/Write/PID/Alive`.
A new package `term/workspace` owns the split tree, tab bar, keybindings,
layout persistence, and creates/destroys `*term.Term` instances.
`term/widget.go` does not grow further — the pane manager wires Terms
together through their public API only.

## Architecture

Three layers; dependencies flow downward:

```
examples/demo/main.go         gui.NewWindow + term.New + backend.Run
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw.go      OnDraw: bg/fg/graphics/cursor render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/widget_mouse.go     Mouse button/motion/wheel; SGR encoding.
term/widget_clipboard.go Cmd+C/V; opt-in OSC 52 clipboard write.
term/widget_scroll.go    Scrollbar, momentum scroll, ViewSubPx math.
        │
        ▼
term/parser.go           VT state machine entry point. Bytes → grid mutations.
term/parser_csi.go       CSI dispatch (SGR, cursor, erase, modes, DECSCUSR, KKP…)
term/parser_osc.go       OSC dispatch (0/1/2/7/8/9/10/11/12/52/133/777/1337)
term/parser_dcs.go       DCS dispatch (DECRQSS, XTGETTCAP, sixel, sync)
term/parser_apc.go       APC dispatch (Kitty Graphics Protocol)
        │
        ▼
term/grid.go             Cell buffer + cursor state + alt-screen. Pure data.
term/grid_cursor.go      Cursor move, save/restore, DECSCUSR.
term/grid_edit.go        Erase, insert/delete lines/chars.
term/grid_mark.go        OSC 133 semantic shell marks.
term/grid_reflow.go      Logical line reflow on resize.
term/grid_scroll.go      Scroll regions; pixel-accurate ViewSubPx math.
term/grid_search.go      Literal and RE2 regex search.
term/grid_selection.go   Content-relative text selection.
term/scrollback.go       Scrollback ring buffer.
term/bidi.go             Unicode Bidirectional Algorithm (UAX#9) for RTL text.
term/graphics.go         Graphic type; sixel decoder; PNG data-URL encoder.

term/pty.go              creack/pty wrapper. Spawns $SHELL, resize ioctl.
term/palette.go          256-color ANSI table + RGB resolution helpers.
```

## Phase ordering rationale

Phases are ordered by (a) prerequisite chain, (b) user-visible impact,
(c) implementation simplicity. Early phases unlocked obviously-broken
behavior in common tools (vim colors, copy/paste, scrollback). Late
phases unlocked advanced apps (tmux, mouse-aware editors) and polish.

---

## Recently completed

### Phase 39 — Native Splits, Panes, and Tabs ✅

**Status:** 39a–39d done. Splits, focus routing, tabs, flex-ratio
layout, and keyboard pane resize ship in `term/workspace`. 39e
(persistence/config) is now planned in detail below — not yet implemented.

**Why:** A defining feature of modern terminals is native window
multiplexing, turning the emulator into a full workspace without
depending on `tmux`.

**Package:** Pane/tab logic lives in a new `term/workspace` package.
The `term` package already exposes the necessary API
(`Cfg.NoWindowHandler`, `SetFocused`, `HandleWindowEvent`, `Rows`,
`Cols`, `Write`, `PID`, `Alive`, `OnExit`). No new surface in `term/`
unless a parser gap forces it (see parser notes below).

**Default keybindings** follow kitty/iTerm2 conventions to avoid
colliding with macOS window shortcuts and common shell bindings:

| Action | Key |
|--------|-----|
| Split vertical | Cmd+D |
| Split horizontal | Cmd+Shift+D |
| Close pane | Cmd+Shift+W |
| Next pane | Cmd+] |
| Previous pane | Cmd+[ |
| Resize pane | Cmd+Ctrl+Arrow (grow active pane toward that edge) |
| New tab | Cmd+T |
| Close tab | Cmd+Ctrl+W |
| Next tab | Cmd+Shift+] |
| Previous tab | Cmd+Shift+[ |
| Show/hide shortcuts | Cmd+/ (Esc closes) |

Cmd+W is reserved for macOS window-close; panes close with Cmd+Shift+W
instead. All bindings are overridable in workspace config (see 39e).

**Focus traversal on close:** nearest sibling in the split tree; if none,
the parent's other child; if the last pane in a tab closes, the tab is
removed. Closing the last tab replaces it with a fresh single-pane tab
so the workspace is never empty.

#### Parser prerequisites (delivered in 39a or a small pre-PR)

- [x] DECRQM (`CSI ? Pn $ p`): reply with DECRPM (`CSI ? Pn ; V $ y`)
      reporting which DEC private modes are set, reset, or not
      recognized. Needed so apps inside panes can probe terminal
      capabilities correctly.
- [x] DA2 (`CSI > c`): reply with secondary device attributes
      (`CSI > 0 ; 0 ; 0 c`) so apps that query terminal identity
      don't fall back to lowest-common-denominator modes.

#### 39a — Pane model (`term/workspace`)

- [x] `pane` struct: owns a `*term.Term`, split-tree node, flex ratio,
      border style. Border is rendered by a shared go-gui canvas or
      container padding — not inside `widget_draw.go`.
- [x] Split tree: `SplitNode` with leaf-pane / horz-split / vert-split
      variants; `Add()`, `Remove()`, `Find()`, `Walk()` primitives.
- [x] Each pane calls `term.New(w, cfg)` with `NoWindowHandler: true`.
- [x] `Cfg.OnTitle` wired per-pane so the workspace layer captures OSC 0/2
      for tab titles.

**Verify:** Open two panes, `echo $$` in each returns different PIDs.

#### 39b — Focus routing

- [x] Focused pane receives keyboard input via `SetFocused(true)`.
      All others get `SetFocused(false)` → dimmed cursor.
- [x] Mouse click in a pane's canvas sets focus to that pane.
- [x] Focus border: active pane gets a 1–2 px colored border (theme
      accent); unfocused panes get a dimmed or invisible border.
- [x] Cmd+] / Cmd+[ cycle focus to next/previous pane in depth-first
      split-tree order.

**Verify:** Click between panes, keystrokes go to the focused one.
Cursor dims on unfocused panes.

#### 39c — Split layout

- [x] Cmd+D: split focused pane vertically (side-by-side).
- [x] Cmd+Shift+D: split focused pane horizontally (stacked).
- [x] Keyboard pane resize: Cmd+Ctrl+Arrow moves the focused pane's
      nearest same-axis split divider toward that edge (Right/Down raise
      the ratio, Left/Up lower it), so every arrow stays live regardless of
      which side of the split the pane is on. Mutates the split node's flex
      `Ratio` (`resizeActivePane` / `findResizeSplit`); clamped to
      `[minRatio, maxRatio]` with a pixel floor at layout. Direct chords,
      no resize mode. Keyboard only — no mouse-drag handle.
- [x] Cmd+Shift+W: close focused pane (kill its PTY via `Term.Close`).
- [x] Re-layout: `splitView` threads a pixel box down the tree and honors
      each node's flex `Ratio`. Window resize redistributes space
      proportionally; `ratioSplit`'s `minPanePx` floor keeps no pane from
      collapsing below 40px when there is room.

**Verify:** Split → Cmd+Ctrl+Arrow resize → close leaves remaining panes
correctly laid out. Window resize distributes space by flex ratio.

#### 39d — Tab model

- [x] Tab bar rendering above the pane layout: go-gui Row of tab
      buttons. Each tab shows the active pane's OSC 0/2 title
      (truncated to ~30 chars, ellipsized). New-tab is Cmd+T.
- [x] Cmd+T: new tab containing a single full-width pane running $SHELL.
- [x] Cmd+Ctrl+W: close current tab. Kills all panes in the tab,
      removes the tab bar entry. Last tab → replaces with fresh pane.
- [x] Cmd+Shift+] / Cmd+Shift+[ (or Cmd+{ / Cmd+}): switch to
      next/previous tab.
- [x] Tab title updates when the active pane emits OSC 0/2.

**Verify:** Create tabs, switch between them, close tabs; pane split
trees survive tab switches.

#### 39e — Persistence / config

**Goal:** Save the full workspace (tabs → split trees → per-pane CWD,
ratio, optional command) to JSON and restore it on launch, plus
user-overridable keybindings. All persistence logic lives in
`term/workspace`; the only `term/` change is a new `Cfg.Dir` field so a
restored pane spawns its shell in the saved directory.

**Design decisions (proposed):**

1. **CWD restore via `term.Cfg.Dir`, not OSC 7.** Add `Cfg.Dir string`
   to `term.Cfg`; `startPTY` sets `cmd.Dir` when non-empty (falls back to
   the process CWD when blank or the dir no longer exists — never fail the
   spawn). OSC 7 is the shell *reporting* its CWD *to* the terminal; it
   cannot set the directory, so the original roadmap note was wrong.
   Writing `cd <dir>` into the PTY is rejected: it races shell startup and
   pollutes shell history.
2. **Capture uses existing API.** `Term.Cwd()` already returns the
   OSC-7-reported directory; ratios, dirs, focus, and the split tree are
   all in-memory. No new capture-side `term/` surface.
3. **Schema is versioned.** Top-level `{"version":1, ...}`. Unknown
   future versions → log + start fresh rather than erroring out.
4. **Command field is reserved but unused for now.** The workspace only
   ever spawns the default shell today, so `command`/`args` serialize as
   empty. Field exists so a future custom-command pane round-trips.
5. **Atomic writes.** Save writes to a temp file in the same dir then
   `os.Rename` over the target, so a crash mid-write never corrupts an
   existing workspace file.
6. **Human config lives in a separate, hand-edited file — not the layout
   JSON.** The two have opposite lifecycles: layout is machine-written and
   overwritten on every quit; config is hand-authored and read-only to the
   app. Bundling them would clobber user edits on save. Config uses a
   comment-friendly INI-style format (`[section]` headers, `key = value`
   lines), extensible to themes and future settings — not JSON, not TOML.
   Keybindings are the `[keybindings]` section. See 39e-4.
7. **Config root: prefer `~/.config/go-term`, fall back per platform.**
   Terminal tooling conventionally uses `~/.config` even on macOS, so
   `configDir()` resolves: (1) `$XDG_CONFIG_HOME/go-term` when
   `XDG_CONFIG_HOME` is set; else (2) `~/.config/go-term` when `~/.config`
   exists; else (3) `os.UserConfigDir()/go-term` (→ `~/Library/Application
   Support/go-term` on macOS). On Linux (1)/(2) already match
   `os.UserConfigDir()`, so the extra macOS-only check is the only
   behavioral difference. Layout = `workspace.json`, human config =
   `config` (extensionless, like git's). A single `configDir() (string,
   error)` helper builds both paths.

**Schema (`version 1`):**

```json
{
  "version": 1,
  "activeTab": 0,
  "tabs": [
    {
      "activeLeaf": "tab-0-pane-1",
      "root": {
        "dir": "vertical",
        "ratio": 0.5,
        "first":  { "leafID": "tab-0-pane-0", "cwd": "/home/u",     "command": "", "args": null },
        "second": { "leafID": "tab-0-pane-1", "cwd": "/home/u/src", "command": "", "args": null }
      }
    }
  ]
}
```

Keybindings are **not** in this file; they live in a separate hand-edited
`config` file (see 39e-4).

A node is a leaf when `first`/`second` are absent and `leafID` is set;
otherwise it is a split. Leaf IDs are regenerated deterministically on
load (`<tabID>-pane-N`); the persisted IDs are advisory, used only to wire
`activeLeaf` back to the right node.

##### 39e-1 — `term.Cfg.Dir` spawn directory

- [ ] Add `Cfg.Dir string` (doc: "working directory for the child; empty
      = inherit process CWD").
- [ ] `startPTY` sets `cmd.Dir = cfg.Dir` when non-empty and the path
      exists (`os.Stat` guard); otherwise leave unset.
- [ ] Test: spawn with `Dir` set, run `pwd`, assert output matches.

**Verify:** `term.New` with `Cfg.Dir: "/tmp"` → first prompt's `pwd` is
`/tmp`.

##### 39e-2 — Serialization (`term/workspace/persist.go`)

- [ ] `persistedWorkspace`, `persistedTab`, `persistedNode` structs with
      JSON tags matching the schema above.
- [ ] `(*Workspace).snapshot() persistedWorkspace`: walk `ws.tabs`, each
      tab's `root` tree, emit per-leaf `Cwd()` + ratio. Pure, no I/O,
      grabs no locks beyond reading already-main-thread state.
- [ ] `(*Workspace).Save(path string) error`: marshal `snapshot()`,
      `MkdirAll(dir)`, atomic temp-write + rename.
- [ ] `func configDir() (string, error)`: resolve in order —
      `$XDG_CONFIG_HOME/go-term` if set, else `~/.config/go-term` if
      `~/.config` exists, else `os.UserConfigDir()/go-term` (→ `~/Library/
      Application Support/go-term` on macOS). `defaultWorkspacePath` =
      `configDir()/workspace.json`; the human config file is `configDir()/config`.
- [ ] Round-trip test: build a 2-tab / nested-split workspace in memory,
      `snapshot()`, marshal, unmarshal, assert tree shape + ratios + dirs.

##### 39e-3 — Restore on launch

- [ ] `func Restore(w *gui.Window, cfg Cfg, path string) (*Workspace, error)`:
      read+parse JSON; on missing file, empty file, or version mismatch,
      fall back to `New(w, cfg)` (log the reason, never hard-fail).
- [ ] `buildTabFromPersisted`: rebuild the `splitNode` tree, regenerate
      leaf IDs, spawn each pane via `addPane` with `term.Cfg.Dir` set from
      the saved `cwd`. Reuse the existing `termCfg` plumbing — thread a
      per-pane `dir` argument through `newTab`/`addPane`/`termCfg`.
- [ ] Restore `activeTab` and each tab's `focused` leaf; assert the
      focus invariant ("active terminal owns IDFocus") via `refresh()`.
- [ ] Test (no real PTY): inject a fake term constructor or assert tree
      structure pre-spawn; confirms IDs/ratios/active selections wire up.

##### 39e-4 — Human config file (`config`, INI-style)

Hand-edited config in its own file, never touched by save-on-quit.
INI-style: `[section]` headers; `key = value` lines; `#` starts a comment;
blank lines ignored; whitespace around `=` trimmed. Designed to grow
(themes, font, scrollback) — Phase 39e implements only `[keybindings]`.
In that section the key is the short command name (suffix after
`workspace.`), so the file stays terse:

```
# ~/Library/Application Support/go-term/config   (macOS)
# ~/.config/go-term/config                        (Linux)

[keybindings]
# <command> = <chord>.  Unlisted commands keep their defaults.
splitVertical = Cmd+E
closePane     = Cmd+W
nextTab       = Ctrl+Tab
```

- [ ] Minimal INI parser `parseConfig(r io.Reader) (config, []error)`:
      tracks the current `[section]`, returns a struct with a
      `keybindings map[string]string` (and room for future sections);
      collects per-line errors without aborting. Keep it ~40 lines, no dep.
- [ ] Define a **canonical, platform-independent** chord format: always
      `+`-separated, `Cmd`=`ModSuper`, plus `Ctrl`/`Alt`/`Shift`, then the
      key name (`A`–`Z`, `0`–`9`, `F1`–`F25`, `[`/`]`/`/`, `Tab`, `Enter`,
      arrows, etc.). Do **not** reuse `gui.Shortcut.String()` — verified
      display-only and platform-dependent (bare `⌘⇧` glyphs, no separator
      on darwin; `Super+` text elsewhere) with no inverse parser in go-gui.
- [ ] `parseShortcut(string) (gui.Shortcut, bool)` mapping modifier/key
      names → exported `gui.Mod*` / `gui.Key*` constants. Keep the name
      table local to workspace; go-gui's `keyNameMap` is unexported.
      (`formatShortcut` only needed if a "dump current bindings" command is
      added later — defer it.)
- [ ] `Cfg.ConfigPath string` (empty = default `configDir()/config`).
      Loaded in `New`/`Restore`; missing file is fine (defaults, no error).
- [ ] `registerCommands` resolves each `[keybindings]` entry to a full
      command ID, parses the chord, and swaps the default `Shortcut` when
      it parses cleanly; parse errors and unknown command names are logged
      and the default retained. Guard against a chord that collides with
      another command (go-gui `RegisterCommand` already rejects duplicate
      shortcuts — surface that error, keep the default).
- [ ] The help overlay already renders from the live command registry, so
      overridden bindings display correctly with no extra work.
- [ ] Tests: valid `[keybindings]` overrides the right commands;
      comments/blank lines/section headers handled; bad chord and unknown
      command reported but don't abort; collision falls back to default.

##### 39e-5 — CLI flags + save-on-quit (`examples/demo/main.go`)

- [ ] `flag` parsing: `--workspace <path>` (load via `Restore`, default
      path when flag omitted but file exists), `--save-workspace <path>`
      (write on quit; defaults to the load path when set).
- [ ] Wire save into the existing `OnCloseRequest` / quit path so the
      layout is written before `w.Close()` (both the confirm-Yes branch
      and the no-confirm branch).
- [ ] Optional: a `Cmd+S`-style "Save Workspace" command — **defer unless
      trivial**; quit-time save covers the verify scenario.

**Verify:** Save a 3-tab layout with nested splits and distinct CWDs per
pane, quit, relaunch with `--workspace`. Tabs, split trees, ratios, active
tab/pane, and per-pane CWDs all restore. Add a `[keybindings]` override to
the `config` file and confirm the new chord works and shows in the Cmd+/
overlay.

**Open questions:**

1. ~~Confirm go-gui exposes a `gui.Shortcut` (de)serialization helper.~~
   **Resolved:** it does not. `Shortcut.String()` exists but is display-only
   and platform-dependent (mac glyphs, no separator) with no inverse, so
   39e-4 hand-rolls `parseShortcut`. Keybindings also moved out of the
   layout JSON into a separate hand-edited `config` file (INI-style,
   extensible to themes/other settings) rooted at `os.UserConfigDir()`, so
   save-on-quit never clobbers hand edits.
2. Should restore validate that saved `cwd` paths still exist and silently
   drop to `$HOME` when gone (proposed: yes, in 39e-1's stat guard)?
3. Default-path behavior with no flags: auto-load+auto-save the default
   file (sticky workspace, like kitty session) vs require explicit flags
   (proposed: explicit flags only for the demo; auto-load is a later
   opt-in to avoid surprising the existing single-shell demo UX).

---

## Completed (archived)

Phases 0–39 are done. Phase 31 (Disk-Backed Scrollback) was skipped — deferred
until real-world memory pressure warrants it.

**Phase 39e** (persistence/config) — planned in detail under "Recently
completed → Phase 39", not yet implemented: workspace JSON save/restore,
keybinding overrides, `--workspace` / `--save-workspace` CLI flags.

| Phase | Description | Key capability unlocked |
|-------|-------------|------------------------|
| 0 | Landed roadmap in repo | Planning infrastructure |
| 1 | 256-color + 24-bit truecolor | `ls --color`, `bat`, vim themes |
| 2 | Cursor save/restore + show/hide | `tput civis`/`cnorm`, Ctrl+L |
| 3 | Scrollback ring buffer + mouse wheel + PgUp/PgDn | `seq 1 5000`, scroll up |
| 4 | Text selection + copy to clipboard | Drag-select, Cmd+C |
| 5 | Paste + bracketed paste mode | Cmd+V, no auto-execute |
| 6 | Scroll regions + IL/DL/ICH/DCH + IND/RI | vim, `less` smooth scrolling |
| 7 | Alt screen (DECSET 1049/47/1047) | vim full-screen, `htop` |
| 8 | OSC: window title + CWD | `printf '\x1b]0;hello\x07'` |
| 9 | Mouse reporting (X10 + SGR 1006) | tmux pane-click, vim `mouse=a` |
| 10 | Cursor style (DECSCUSR) + blink | `printf '\x1b[6 q'` bar cursor |
| 11 | Wide chars + emoji (East Asian Wide) | `echo 你好 🍣` |
| 12 | Italic, Dim, Strikethrough | `printf '\x1b[3mITALIC\x1b[0m'` |
| 13 | Logical line wrapping (reflow) | Resize, text reflows |
| 14 | OSC 52 clipboard + OSC 8 hyperlinks | `ls --hyperlink`, remote clipboard |
| 15 | Search in scrollback | Cmd+F, find "error" |
| 16 | Coalesced text + caching | 37µs foreground pass, 0 allocs |
| 17 | Persistent selection | Selection survives scroll/resize |
| 18 | Visual bell | `printf '\a'` flash |
| 19 | Scrollbar indicator | Scroll-position thumb on right edge |
| 20 | Extended underline styles + colors | Curly, dotted, dashed underlines |
| 21 | Customizable tab stops (HTS/TBC) | Legacy CLI app tab layouts |
| 22 | Meta/Alt key encoding | Alt+F for word-forward |
| 23 | Enhanced keypad + function keys | `mc`, `htop` F-keys |
| 24 | Color themes + palette API | Runtime theme switching |
| 25 | Dirty row tracking | `top`/`htop` lower CPU |
| 26 | Semantic shell integration (OSC 133) | Cmd+Up/Down prompt jumping |
| 27 | Kitty Keyboard Protocol | Neovim distinct Tab/Ctrl+I |
| 28 | SGR-Pixels mouse (1016) | Pixel-precise mouse coords |
| 29 | Regular expression search | `[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}` |
| 30 | External control / API (OSC 10/11/12) | `printf '\x1b]11;rgb:ff/00/00\x07'` |
| 31 | Disk-backed scrollback | **SKIPPED** |
| 32 | Sixel graphics | `img2sixel` image previews |
| 33 | OS notifications (OSC 9/777) | Desktop notify on build complete |
| 34 | iTerm2 inline images (OSC 1337) | `imgcat image.png` |
| 35 | Smooth pixel-perfect scrolling | Sub-cell scroll delta, momentum |
| 36 | Kitty Graphics Protocol | `kitten icat` high-perf images |
| 37 | Font ligatures | Fira Code `!=` → single glyph |
| 38 | Bidirectional text (BiDi) + RTL | `echo "שלום"` RTL rendering |
| 39 | Native splits, panes, tabs (`term/workspace`) | Built-in multiplexing; no `tmux` (39e persistence deferred) |

## End-to-end verification (every phase)

1. `go vet ./...` clean.
2. `go build ./...` clean.
3. `go test -race -count=1 ./...` passes.
4. `cd examples/demo && go run .` and verify visually.
   For 39a+ also run `cd examples/panedemo && go run .`.
5. Smoke matrix: `ls --color`, `cat /etc/hosts`, `vim` + `:q!`, resize → `stty size`, Ctrl+C interrupts `sleep 100`.
6. CI: `go vet`, `go build ./examples/...`, `go test -race -count=1 ./...`, `golangci-lint`.
7. Multi-Term integration test: two `Term` instances with `NoWindowHandler`,
   verify `SetFocused` routes keystrokes to the correct PTY, and
   `HandleWindowEvent` forwards focus-reporting sequences for the focused pane only.


## Resolved decisions

1. **Color encoding:** pack RGB+flag into a single `uint32` per FG/BG.
2. **Scrollback cap:** default 5000, bounded at 100k.
3. **Selection mouse:** left-drag select + Cmd/Ctrl+C copy. No middle-click PRIMARY.
4. **Alt-screen scrollback:** suppress while alt is active (kitty/iTerm/ghostty default).
5. **Cursor blink:** honor DECSCUSR; allow `Cfg.CursorBlink *bool` override.
6. **Public API:** `Cfg`, `Term`, `Theme`, `NamedTheme`, `New`, `View`, `Close`, `Cwd`, `SetTheme`.
7. **Pane/tab package location:** `term/workspace` — a layer above `term` that
   wires `*term.Term` instances through their public API only. No pane logic
   inside `term/widget.go`.
8. **Pane keybindings:** follow kitty/iTerm2 conventions. Cmd+D (vert split),
   Cmd+Shift+D (horz split), Cmd+[ / ] (cycle panes), Cmd+Shift+W (close pane).
   Cmd+W reserved for macOS window-close; not bound to pane actions.
   All bindings overridable via workspace JSON.
9. **Focus traversal on pane close:** nearest sibling → parent's other child →
   parent. Last pane in tab → close tab. Last tab → replace with fresh pane
   so workspace never empty.
10. **Tab switching with no splits:** Cmd+Shift+[ / ] cycle tabs regardless
    of whether panes exist in the current tab. Cmd+[ / ] always cycle panes.
