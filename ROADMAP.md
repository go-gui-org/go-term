# go-term: Roadmap

## Context

`go-term` is a feature-rich terminal emulator widget for the
[go-gui](https://github.com/mike-ward/go-gui) framework. 38 phases are
complete — the widget covers modern terminal feature parity
(ghostty/iTerm2/kitty) including 24-bit color, truecolor SGR, alt
screen, logical reflow, sixel/kitty/iTerm2 graphics, BiDi/RTL text,
Kitty Keyboard Protocol, and pixel-perfect scrollback.

Work remaining: native split panes/tabs, then 1.0 API stabilisation.

Each phase below is sized for one focused PR, demo-testable by running
`cd examples/demo && go run .` and exercising the new behavior.

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

#### 39a — Pane model

- [ ] Define pane struct: owns a `*Term`, has a tree node, dimensions, border.
- [ ] Pane tree: root split node, leaf panes; create/destroy/focus primitives.
- [ ] Each pane spawns its own PTY + shell.

**Verify:** Open two panes, `echo $$` in each returns different PIDs.

#### 39b — Focus routing

- [ ] Focused pane receives keyboard input. Unfocused panes dim their cursor.
- [ ] Mouse click in a pane sets focus.
- [ ] Focus border highlight on the active pane.
- [ ] Cmd+[ / Cmd+] or Cmd+Shift+[ / ] cycle between panes.

**Verify:** Click between panes, keystrokes go to the focused one.

#### 39c — Split layout

- [ ] Cmd+D: split focused pane vertically (side-by-side).
- [ ] Cmd+Shift+D: split focused pane horizontally (stacked).
- [ ] Drag handle between panes to resize.
- [ ] Cmd+W: close focused pane (kill its PTY).
- [ ] Re-layout on window resize; distribute space proportionally.

**Verify:** Split → resize drag → close leaves remaining panes correctly laid out.

#### 39d — Tab model

- [ ] Tab bar rendering (above the pane layout).
- [ ] Cmd+T: new tab with a single full-width pane.
- [ ] Cmd+Shift+W: close current tab.
- [ ] Cmd+Shift+[ / ] (no pane splits) or Cmd+{ / }: switch tabs.
- [ ] Tab title derived from active pane's OSC 0/2 title.

**Verify:** Create tabs, switch between them, close tabs; panes survive tab switches.

#### 39e — Persistence/config

- [ ] Save layout to JSON: tab→split tree→pane working directories.
- [ ] Restore layout on launch: spawn PTYs, restore CWD via OSC 7 write.
- [ ] Configurable keybindings for split/tab/focus actions.
- [ ] Session file format; `go-term --session ~/.config/go-term/session.json`.

**Verify:** Save a 3-tab layout with splits, quit, relaunch — terminals restore.

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
5. Smoke matrix: `ls --color`, `cat /etc/hosts`, `vim` + `:q!`, resize → `stty size`, Ctrl+C interrupts `sleep 100`.
6. CI: `go vet`, `go build ./examples/demo`, `go test -race -count=1 ./...`, `golangci-lint`.


## Resolved decisions

1. **Color encoding:** pack RGB+flag into a single `uint32` per FG/BG.
2. **Scrollback cap:** default 5000, bounded at 100k.
3. **Selection mouse:** left-drag select + Cmd/Ctrl+C copy. No middle-click PRIMARY.
4. **Alt-screen scrollback:** suppress while alt is active (kitty/iTerm/ghostty default).
5. **Cursor blink:** honor DECSCUSR; allow `Cfg.CursorBlink *bool` override.
6. **Public API:** `Cfg`, `Term`, `Theme`, `NamedTheme`, `New`, `View`, `Close`, `Cwd`, `SetTheme`.
