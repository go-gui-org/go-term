# go-term: Roadmap

## Context

`go-term` is a feature-rich terminal emulator widget for the
[go-gui](https://github.com/go-gui-org/go-gui) framework. 38 phases are
complete — the widget covers modern terminal feature parity
(ghostty/iTerm2/kitty) including 24-bit color, truecolor SGR, alt
screen, logical reflow, sixel/kitty/iTerm2 graphics, BiDi/RTL text,
Kitty Keyboard Protocol, and pixel-perfect scrollback.

Work remaining: native split panes/tabs, then 1.0 API stabilisation.

Each phase below is sized for one focused PR, demo-testable by running
`cd examples/demo && go run .` and exercising the new behavior.
Phase 39 is five sub-phases (39a–e), each its own PR.

## Package layering for Phase 39

The pane/tab layer sits *above* `term`, not inside it. The `term`
package already exposes the right surface: `Cfg.NoWindowHandler`,
`Term.SetFocused`, `Term.HandleWindowEvent`, `Term.Rows/Cols/Write/PID/Alive`.
A new package `term/session` owns the split tree, tab bar, keybindings,
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

## Active

### Phase 39 — Native Splits, Panes, and Tabs

**Why:** A defining feature of modern terminals is native window
multiplexing, turning the emulator into a full workspace without
depending on `tmux`.

**Package:** Pane/tab logic lives in a new `term/session` package.
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
| New tab | Cmd+T |
| Close tab | Cmd+Ctrl+W |
| Next tab | Cmd+Shift+] |
| Previous tab | Cmd+Shift+[ |

Cmd+W is reserved for macOS window-close; panes close with Cmd+Shift+W
instead. All bindings are overridable in session config (see 39e).

**Focus traversal on close:** nearest sibling in the split tree; if none,
the parent's other child; if the last pane in a tab closes, the tab is
removed. Closing the last tab replaces it with a fresh single-pane tab
so the session is never empty.

#### Parser prerequisites (delivered in 39a or a small pre-PR)

- [x] DECRQM (`CSI ? Pn $ p`): reply with DECRPM (`CSI ? Pn ; V $ y`)
      reporting which DEC private modes are set, reset, or not
      recognized. Needed so apps inside panes can probe terminal
      capabilities correctly.
- [x] DA2 (`CSI > c`): reply with secondary device attributes
      (`CSI > 0 ; 0 ; 0 c`) so apps that query terminal identity
      don't fall back to lowest-common-denominator modes.

#### 39a — Pane model (`term/session`)

- [ ] `pane` struct: owns a `*term.Term`, split-tree node, flex ratio,
      border style. Border is rendered by a shared go-gui canvas or
      container padding — not inside `widget_draw.go`.
- [ ] Split tree: `SplitNode` with leaf-pane / horz-split / vert-split
      variants; `Add()`, `Remove()`, `Find()`, `Walk()` primitives.
- [ ] Each pane calls `term.New(w, cfg)` with `NoWindowHandler: true`.
- [ ] `Cfg.OnTitle` wired per-pane so the session layer captures OSC 0/2
      for tab titles.

**Verify:** Open two panes, `echo $$` in each returns different PIDs.

#### 39b — Focus routing

- [ ] Focused pane receives keyboard input via `SetFocused(true)`.
      All others get `SetFocused(false)` → dimmed cursor.
- [ ] Mouse click in a pane's canvas sets focus to that pane.
- [ ] Focus border: active pane gets a 1–2 px colored border (theme
      accent); unfocused panes get a dimmed or invisible border.
- [ ] Cmd+] / Cmd+[ cycle focus to next/previous pane in depth-first
      split-tree order.

**Verify:** Click between panes, keystrokes go to the focused one.
Cursor dims on unfocused panes.

#### 39c — Split layout

- [ ] Cmd+D: split focused pane vertically (side-by-side).
- [ ] Cmd+Shift+D: split focused pane horizontally (stacked).
- [ ] Drag handle (2–4 px wide between panes) to resize. Resize
      distributes flex ratios; the handle is a go-gui Column/Row
      with a mouse-drag handler.
- [ ] Cmd+Shift+W: close focused pane (kill its PTY via `Term.Close`).
- [ ] Re-layout on window resize: distribute space proportionally to
      flex ratios. Min pane size enforced so no pane collapses to zero.

**Verify:** Split → resize drag → close leaves remaining panes
correctly laid out. Window resize distributes space.

#### 39d — Tab model

- [ ] Tab bar rendering above the pane layout: go-gui Row of tab
      buttons + a "+" new-tab button. Each tab shows the active
      pane's OSC 0/2 title (truncated to ~30 chars, ellipsized).
- [ ] Cmd+T: new tab containing a single full-width pane running $SHELL.
- [ ] Cmd+Ctrl+W: close current tab. Kills all panes in the tab,
      removes the tab bar entry. Last tab → replaces with fresh pane.
- [ ] Cmd+Shift+] / Cmd+Shift+[ (or Cmd+{ / Cmd+}): switch to
      next/previous tab.
- [ ] Tab title updates when the active pane emits OSC 0/2.

**Verify:** Create tabs, switch between them, close tabs; pane split
trees survive tab switches.

#### 39e — Persistence / config

- [ ] Save session to JSON: tab list → split tree → pane CWD, flex
      ratio, shell command (if non-default). Writes to
      `~/.config/go-term/session.json` by default.
- [ ] Restore session on launch: parse JSON, spawn PTYs with saved CWD
      via OSC 7 write after shell starts, restore split ratios.
- [ ] Keybinding map in session JSON; overrides the defaults listed
      above. Stored per-session, not global.
- [ ] CLI flag: `--session <path>` to load a named session file;
      `--save-session <path>` to write on quit.

**Verify:** Save a 3-tab layout with splits, quit, relaunch with
`--session`. Terminals restore with correct CWDs, splits, and titles.

---

## Completed (archived)

Phases 0–38 are done. Phase 31 (Disk-Backed Scrollback) was skipped — deferred
until real-world memory pressure warrants it.

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
7. **Pane/tab package location:** `term/session` — a layer above `term` that
   wires `*term.Term` instances through their public API only. No pane logic
   inside `term/widget.go`.
8. **Pane keybindings:** follow kitty/iTerm2 conventions. Cmd+D (vert split),
   Cmd+Shift+D (horz split), Cmd+[ / ] (cycle panes), Cmd+Shift+W (close pane).
   Cmd+W reserved for macOS window-close; not bound to pane actions.
   All bindings overridable via session JSON.
9. **Focus traversal on pane close:** nearest sibling → parent's other child →
   parent. Last pane in tab → close tab. Last tab → replace with fresh pane
   so session never empty.
10. **Tab switching with no splits:** Cmd+Shift+[ / ] cycle tabs regardless
    of whether panes exist in the current tab. Cmd+[ / ] always cycle panes.
