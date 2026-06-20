# Multi-terminal Docking Example — Implementation Spec

`examples/multiterm/` — an example program demonstrating multiple terminal
widgets arranged in a go-gui `DockLayout` with native menus and keyboard
shortcuts.

This is a **standalone example**, not [ROADMAP.md](../../ROADMAP.md) Phase 39
(`term/session`). It deliberately uses simpler UX (see Scope vs. Roadmap).

## Overview

- Starts with a single terminal in a window, like `examples/demo`.
- **Cmd+T** (File → New Terminal) adds a second terminal as a tab in the
  current group, activating the dock layout.
- Once ≥2 terminals exist, the dock layout allows drag-and-drop
  rearrangement: tabs can be reordered within a group, dragged to
  split the group (top/bottom/left/right), or dragged to the window
  edge to create a new split. Splits are drag-only — no keyboard
  shortcuts for split in this example.
- **Cmd+W** (File → Close Terminal) closes the focused **terminal tab**
  (not the window — macOS users may expect ⌘W to close the window).
  Closing the last terminal quits the app immediately, without the quit
  confirmation dialog.
- **Cmd+Q** (File → Quit), the title-bar close button, or the OS quit
  gesture shows a native confirmation dialog when any terminals are still
  running, to prevent accidental loss of session layout. Quitting with no
  live terminals exits immediately.
- **Cmd+Shift+[** / **Cmd+Shift+]** cycle through tabs in the current
  group (Window → Previous/Next Tab).
- Native menubar with File, Edit, and Window menus where the backend
  supports it (macOS is the primary target).
- Keep multi-terminal orchestration in the example. Only change `term`
  or go-gui if choosing to support terminal-surface click-to-focus or
  unique per-Term theme context menus (see Edge Cases).

## Scope vs. Roadmap

| Topic | This example | Phase 39 (`term/session`) |
|-------|--------------|---------------------------|
| Close pane/tab | ⌘W | ⌘⇧W |
| Split shortcuts | drag-only | ⌘D / ⌘⇧D |
| Last pane closed | quit app | replace with fresh tab |
| Code location | `examples/multiterm` | `term/session` package |

## State Model

```go
type AppState struct {
    Root     *gui.DockNode
    Terms    map[string]*term.Term // panelID → Term
    Titles   map[string]string     // panelID → tab label (from OSC 0/2)
    Focused  string                // panelID of focused terminal
    NextID   int                   // monotonic counter for "term-N" IDs
}
```

- `Root` is the dock tree, persisted across frames via `OnLayoutChange`.
- `Terms` maps panel IDs to live `*term.Term` instances.
- `Titles` stores the last known title per terminal (updated via
  `Cfg.OnTitle`).
- `Focused` tracks which terminal has input focus.
- `NextID` ensures unique panel IDs (`term-0`, `term-1`, …).

Initialize state and wire it into the window before the backend runs:

```go
state := &AppState{
    Root:    initialLayout(),
    Terms:   map[string]*term.Term{},
    Titles:  map[string]string{},
    Focused: "term-0",
    NextID:  1,
}

app := gui.NewApp()
w := gui.NewWindow(gui.WindowCfg{
    State:  state,
    Title:  "go-term",
    Width:  900,
    Height: 600,
    OnCloseRequest: func(w *gui.Window) {
        confirmQuit(w)
    },
    OnInit: func(w *gui.Window) {
        w.UpdateView(mainView)
        // Create the first terminal (see New Terminal command for
        // the shared newTermCfg helper).
        id := "term-0"
        t, err := term.New(w, newTermCfg(id, w))
        if err != nil {
            log.Fatalf("term.New: %v", err)
        }
        state := gui.State[AppState](w)
        state.Terms[id] = t
        state.Titles[id] = id
        state.Focused = id
        // Register commands on w and menubar on app (see Commands
        // and Native Menu sections).
    },
})
defer closeAllTerms(state)
backend.RunApp(app, w)
```

The first `Term` is created in `OnInit`, stored as `term-0`, and its title
entry is seeded to `"term-0"`. Commands and the native menubar are
registered here as well (see Commands and Native Menu sections below).

## View Structure

```
gui.Column (FillFill, fixed to window size)
  ├── gui.DockLayout
  │     ├── Panels: dynamic — one DockPanelDef per active Term
  │     │     ID:    "term-N"
  │     │     Label: app.Titles["term-N"] or "term-N" if empty
  │     │     Content: [ t.View(w) ]
  │     │     Closable: len(app.Terms) > 1
  │     ├── Root:   app.Root
  │     ├── OnLayoutChange → persist to app.Root
  │     ├── OnPanelSelect  → focusPanel (see Focus Routing)
  │     └── OnPanelClose   → closePanel(app, panelID, w)
```

The outer `gui.Column` is sized to the window via `w.WindowSize()`:

```go
ww, wh := w.WindowSize()
return gui.Column(gui.ContainerCfg{
    Width:  float32(ww),
    Height: float32(wh),
    Sizing: gui.FixedFixed,
    Content: []gui.View{
        gui.DockLayout(dockCfg),
    },
})
```

`dockCfg` includes:

```go
gui.DockLayoutCfg{
    Root:   app.Root,
    Panels: panels,
    OnLayoutChange: func(root *gui.DockNode, w *gui.Window) {
        gui.State[AppState](w).Root = root
    },
    OnPanelSelect: func(groupID, panelID string, w *gui.Window) {
        focusPanel(gui.State[AppState](w), panelID, w)
    },
    OnPanelClose: func(panelID string, w *gui.Window) {
        closePanel(gui.State[AppState](w), panelID, w)
    },
}
```

### View Generator

`mainView(w *gui.Window) gui.View` is the single view generator set via
`w.UpdateView(mainView)` in `OnInit`. It:

1. Reads window dimensions.
2. Walks `app.Root` with `DockTreeCollectPanelNodes` (returns panel
   **groups**, not IDs), flattens each group's `PanelIDs`, and keeps only
   IDs present in `app.Terms` (intersection avoids orphaned tabs or leaked
   PTYs when tree and state drift).
3. Builds `[]DockPanelDef` — one per surviving ID, calling `t.View(w)`
   for each. Set `Closable: len(app.Terms) > 1`.
4. Passes `app.Root` into `DockLayoutCfg`.

`OnTitle` calls `w.UpdateWindow()` to trigger a layout refresh; this
re-runs `mainView` so `DockPanelDef.Label` picks up the updated title
without needing `w.UpdateView(mainView)`.

**First frame:** before `OnInit` fires, `app.Terms` is empty but
`app.Root` references `"term-0"`. `collectPanelIDs` returns an empty
list, so the first frame renders an empty dock layout. `OnInit` creates
the first Term and calls `w.UpdateView(mainView)`, triggering a rebuild
with the terminal visible. This flash is imperceptible in practice.

```go
func collectPanelIDs(root *gui.DockNode, terms map[string]*term.Term) []string {
    var ids []string
    for _, g := range gui.DockTreeCollectPanelNodes(root) {
        for _, id := range g.PanelIDs {
            if _, ok := terms[id]; ok {
                ids = append(ids, id)
            }
        }
    }
    return ids
}
```

Because go-gui calls the view generator each frame, terminal views are
always fresh.

## Term Configuration

A `newTermCfg` helper captures `panelID` by closure so `OnTitle` and
`OnExit` can reference it without a loop-variable footgun:

```go
func newTermCfg(panelID string, w *gui.Window) term.Cfg {
    return term.Cfg{
        NoWindowHandler: true, // example owns event dispatch
        TextStyle:       gui.TextStyle{Family: "JetBrainsMono Nerd Font", Size: 12},
        // Omit Themes in this example — all Terms share the hardcoded
        // "term-theme-menu" context-menu ID (see Edge Cases).
        OnTitle: func(title string) {
            w.QueueCommand(func(w *gui.Window) {
                app := gui.State[AppState](w)
                app.Titles[panelID] = title
                w.UpdateWindow() // trigger layout refresh so tab label updates
            })
        },
        OnExit: func() {
            // Runs on reader goroutine — must QueueCommand to touch GUI state.
            w.QueueCommand(func(w *gui.Window) {
                app := gui.State[AppState](w)
                closePanel(app, panelID, w)
            })
        },
    }
}
```

The same helper is used for both the initial Term in `OnInit` and the
"New Terminal" command. Call `term.New(w, newTermCfg(panelID, w))`,
check the error, then store the result in `app.Terms`.

**Order matters:** `term.New` calls `w.SetIDFocus` for the new instance.
Always call `focusPanel` immediately after creating a Term so keyboard
input routes to the intended pane.

Key: `NoWindowHandler: true` — the example installs its own `w.OnEvent`
handler, which routes window-level events (focus gain/loss) to the
focused Term's `HandleWindowEvent`. Keyboard and character input are
routed automatically by go-gui's `IDFocus` system; they do not go
through `w.OnEvent`.

## Focus Routing

Implement focus changes through one helper, used by `OnPanelSelect`,
new terminal, close terminal, and tab cycling:

```go
func focusPanel(app *AppState, panelID string, w *gui.Window) {
    if panelID == "" || panelID == app.Focused {
        return
    }
    if prev, ok := app.Terms[app.Focused]; ok {
        prev.SetFocused(false)
        prev.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
    }
    app.Focused = panelID
    if group, ok := gui.DockTreeFindGroupByPanel(app.Root, panelID); ok {
        app.Root = gui.DockTreeSelectPanel(app.Root, group.ID, panelID)
    }
    if t, ok := app.Terms[panelID]; ok {
        t.SetFocused(true)
        t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
    }
}
```

`OnPanelSelect` delegates to `focusPanel` only — do not call
`DockTreeSelectPanel` in both places.

**Re-entrancy guard:** `focusPanel` sets `app.Focused = panelID` *before*
calling `DockTreeSelectPanel`. This ordering is load-bearing: if
DockLayout observes the tree mutation and re-fires `OnPanelSelect`, the
early-return guard `if panelID == app.Focused` breaks the cycle.

### Click-to-focus (v1: tab bar only)

**Default for this example:** focus switches via the dock tab bar
(`OnPanelSelect`) and keyboard shortcuts only. Terminal-surface
click-to-focus is unsupported in v1.

Clicking inside an unfocused terminal's content area would require one of:

1. A term-level focus callback/hook at the start of `Term.onClick`.
2. A go-gui capture/pre-click hook on a wrapper view.
3. Tab-bar-only focus (chosen for v1).

A parent wrapper `OnClick` alone is insufficient — go-gui delivers clicks
to children first, and `Term.onClick` sets `e.IsHandled = true`.

If click-to-focus is added later, option 2 might look like:

```go
func wrapTerminalView(panelID string, termView gui.View, w *gui.Window) gui.View {
    return gui.Column(gui.ContainerCfg{
        ClickButton: gui.MouseLeft,
        OnClick: func(layout *gui.Layout, e *gui.Event, w *gui.Window) {
            focusPanel(gui.State[AppState](w), panelID, w)
        },
        Content: []gui.View{termView},
    })
}
```

### Window-level event dispatch

Installed in `OnInit`. Forwards **unhandled** window events to the focused
Term. `HandleWindowEvent` emits DECSET ?1004 focus reports on
`EventFocused`/`EventUnfocused`; mouse-down inside the terminal canvas is
handled by `Term.onClick` and does not reach `w.OnEvent`. Keyboard input
is handled separately by go-gui's focus system — each Term's outer
`gui.Column` has a unique `IDFocus` set by `View()`, so keystrokes reach
`onChar`/`onKeyDown` automatically.

```go
w.OnEvent = func(e *gui.Event, w *gui.Window) {
    app := gui.State[AppState](w)
    if t, ok := app.Terms[app.Focused]; ok {
        t.HandleWindowEvent(e)
    }
}
```

### Initial focus

After creating the first Term, set `app.Focused = "term-0"`. The Term
constructor sets `focused = true` internally, and `View()` reasserts
`SetIDFocus` on each layout rebuild.

## Commands & Keyboard Shortcuts

Registered via `w.RegisterCommands(...)` in `OnInit`, wired to native
menu items via `CommandID`. **Every app-level command must set
`Global: true`** so `commandDispatch` runs before the focused terminal
consumes the key.

| Command ID     | Label            | Shortcut (macOS) | Action |
|----------------|------------------|------------------|--------|
| `term.new`     | New Terminal     | ⌘T               | Add tab to current group |
| `term.close`   | Close Terminal   | ⌘W               | Close focused terminal |
| `term.nextTab` | Next Tab         | ⌘⇧]              | Select next tab in group |
| `term.prevTab` | Previous Tab     | ⌘⇧[              | Select previous tab in group |
| `file.quit`    | Quit             | ⌘Q               | Quit app, confirming if terminals alive |

```go
_ = w.RegisterCommands(
    gui.Command{
        ID: "term.new", Label: "New Terminal",
        Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper},
        Global: true,
        Execute: func(_ *gui.Event, w *gui.Window) { /* new terminal */ },
    },
    gui.Command{
        ID: "term.close", Label: "Close Terminal",
        Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper},
        Global: true,
        Execute: func(_ *gui.Event, w *gui.Window) {
            closePanel(gui.State[AppState](w), gui.State[AppState](w).Focused, w)
        },
    },
    gui.Command{
        ID: "term.nextTab", Label: "Next Tab",
        Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift},
        Global: true,
        Execute: func(_ *gui.Event, w *gui.Window) { /* cycle tab */ },
    },
    gui.Command{
        ID: "term.prevTab", Label: "Previous Tab",
        Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift},
        Global: true,
        Execute: func(_ *gui.Event, w *gui.Window) { /* cycle tab */ },
    },
    gui.Command{
        ID: "file.quit", Label: "Quit",
        Shortcut: gui.Shortcut{Key: gui.KeyQ, Modifiers: gui.ModSuper},
        Global: true,
        Execute: func(_ *gui.Event, w *gui.Window) { confirmQuit(w) },
    },
)
```

Note: ⌘Q from the OS menubar triggers `DispatchQuitRequest` →
`OnCloseRequest`, not the command registry. Both paths must call
`confirmQuit`.

### Command implementations

**New Terminal** (`term.new`):
1. Allocate `panelID = fmt.Sprintf("term-%d", app.NextID)`; increment.
2. Create `*term.Term` via `term.New(w, newTermCfg(panelID, w))` (same
   helper used for the initial Term).
3. Store in `app.Terms[panelID]`, `app.Titles[panelID] = panelID`.
4. Add to dock tree:
   - If `app.Focused` is set, find its group via `DockTreeFindGroupByPanel`
     and call `app.Root = gui.DockTreeAddTab(app.Root, groupID, panelID)`.
   - Otherwise:
     `app.Root = gui.DockPanelGroup("main", []string{panelID}, panelID)`.
5. Call `focusPanel(app, panelID, w)` — required because step 2 briefly
   stole `IDFocus` inside `term.New`.

**Close Terminal** (`term.close`):
1. Call `closePanel(app, app.Focused, w)`.
2. `closePanel` is the single close implementation, shared by `term.close`,
   `OnPanelClose`, and `OnExit`:

   ```go
   func closePanel(app *AppState, panelID string, w *gui.Window)
   ```

   - If `panelID` is empty or `app.Terms[panelID]` is missing, no-op.
     This makes the helper safe when a user-initiated `t.Close()` later
     triggers `OnExit` and queues a second close.
   - If only one terminal remains, close that Term, delete it from state,
     then call `w.Close()` to quit (no quit confirmation — see Edge Cases).
   - Otherwise: find the group containing `panelID` via
     `DockTreeFindGroupByPanel` *before* removing from the tree.
   - `t.Close()` the target Term, delete from `app.Terms`
     and `app.Titles`, remove from dock tree via
     `app.Root = gui.DockTreeRemovePanel(app.Root, panelID)`.
   - Only pick a new focused panel if the closed panel was the active one:
     `if panelID == app.Focused { ... }`. Otherwise, the focused panel
     stays unchanged (e.g. closing a background tab).
   - When picking a new focus: prefer another panel in the same group
     (found earlier), fall back to any remaining panel in `app.Terms`.
   - Call `focusPanel(app, newPanelID, w)` for the replacement focus.
3. `OnPanelClose` receives the closing panel's ID directly; it calls
   `closePanel(app, panelID, w)` and does not read `app.Focused`.

If `DockTreeFindGroupByPanel` returns `ok == false` for the focused
panel, the panel is orphaned (not in the tree). Fall back to selecting
any remaining panel and rebuild the tree from the surviving Terms:

```go
func rebuildTreeFromTerms(app *AppState) {
    ids := make([]string, 0, len(app.Terms))
    for id := range app.Terms {
        ids = append(ids, id)
    }
    sort.Strings(ids) // deterministic tab order
    if len(ids) == 0 {
        app.Root = gui.DockPanelGroup("__dock_empty__", nil, "")
        app.Focused = ""
        return
    }
    selected := ids[0]
    if app.Focused != "" {
        if _, ok := app.Terms[app.Focused]; ok {
            selected = app.Focused
        }
    }
    app.Root = gui.DockPanelGroup("main", ids, selected)
}
```

`DockPanelGroup` is a pure struct literal — no validation — so passing
`""` as selectedID or `nil` as panelIDs is safe.

**`Close()` blocks:** `term.Close()` waits for the PTY reader goroutine to
drain (2-second timeout). During this window the main thread is blocked.
This is most noticeable when closing the last terminal (⌘W on a single
tab), since the UI is frozen until `Close()` returns and `w.Close()`
runs. This is inherent to `term.Close()`'s contract, not a bug in the
example.

**Next/Previous Tab** (`term.nextTab`, `term.prevTab`):
1. Find the group containing `app.Focused` via `DockTreeFindGroupByPanel`.
   If not found (orphaned panel), no-op (cycling within a broken tree is
   meaningless; close the orphaned tab instead).
2. If the group has ≤1 panel, no-op (nothing to cycle to).
3. Cycle to the next/previous `panelID` in the group's `PanelIDs` slice
   (wrap around at edges).
4. Call `focusPanel(app, newPanelID, w)`.

**Quit** (`file.quit` / `OnCloseRequest`):
Both call `confirmQuit(w)`:

```go
func confirmQuit(w *gui.Window) {
    app := gui.State[AppState](w)
    alive := 0
    for _, t := range app.Terms {
        if t.Alive() {
            alive++
        }
    }
    if alive == 0 {
        closeAllTerms(app)
        w.Close()
        return
    }
    body := fmt.Sprintf("%d terminal(s) are still running. Quit anyway?", alive)
    w.NativeConfirmDialog(gui.NativeConfirmDialogCfg{
        Title: "Quit go-term",
        Body:  body,
        Level: gui.AlertWarning,
        OnDone: func(r gui.NativeAlertResult, w *gui.Window) {
            if r != gui.DialogOK {
                return
            }
            closeAllTerms(gui.State[AppState](w))
            w.Close()
        },
    })
}
```

`NativeConfirmDialog` is async (`OnDone` callback) — do not block the
main thread waiting for the result. Count `Alive()`, not `len(Terms)`,
for the dialog message.

Use one cleanup helper for quit and deferred teardown:

```go
func closeAllTerms(app *AppState) {
    for id, t := range app.Terms {
        _ = t.Close()
        delete(app.Terms, id)
        delete(app.Titles, id)
    }
    app.Root = gui.DockPanelGroup("__dock_empty__", nil, "")
    app.Focused = ""
}
```

## Native Menu

```go
app.SetNativeMenubar(gui.NativeMenubarCfg{
    AppName:         "go-term",
    IncludeEditMenu: true, // auto-wires Cut/Copy/Paste
    Menus: []gui.NativeMenuCfg{
        {
            Title: "File",
            Items: []gui.NativeMenuItemCfg{
                {Text: "New Terminal", CommandID: "term.new", Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper}},
                {Text: "Close Terminal", CommandID: "term.close", Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper}},
                {Separator: true},
                {Text: "Quit", CommandID: "file.quit", Shortcut: gui.Shortcut{Key: gui.KeyQ, Modifiers: gui.ModSuper}},
            },
        },
        {
            Title: "Window",
            Items: []gui.NativeMenuItemCfg{
                {Text: "Next Tab", CommandID: "term.nextTab", Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift}},
                {Text: "Previous Tab", CommandID: "term.prevTab", Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift}},
            },
        },
    },
})
```

- `IncludeEditMenu: true` auto-generates Cut/Copy/Paste with standard
  shortcuts. These work because the focused Term's `onChar`/`onKeyDown`
  handlers receive keystrokes through go-gui's focus system.
- Register commands **before** `SetNativeMenubar` so `CommandID` resolves.

Native menubars are installed on a `gui.App`, not a standalone window.
Use the app runner pattern shown in State Model.

## Initial Layout

```go
func initialLayout() *gui.DockNode {
    return gui.DockPanelGroup("main", []string{"term-0"}, "term-0")
}
```

A single panel group containing one terminal. When the second terminal
is added, the dock layout's tab bar appears automatically (DockLayout
renders tabs when a group has ≥2 panels).

## File Layout

```
examples/multiterm/
  main.go       // AppState struct, main, mainView, command registration,
                // term creation, focus routing, event dispatch
  SPEC.md       // This file
```

Single file — the example should be self-contained and readable in one
sitting. Aim for ~350 lines; defer click-to-focus and theme menus to
keep it manageable.

## Package Boundaries

- **term package** — `NoWindowHandler`, `SetFocused`,
  `HandleWindowEvent`, `View`, `Close`, and `OnTitle` are sufficient for
  basic multi-terminal docking. A small addition is needed only if this
  example supports terminal-surface click-to-focus or unique per-Term
  theme context menus.
- **go-gui** — `DockLayout`, `NativeMenubarCfg`, `Command`, and `App` are
  sufficient for docking, menus, and shortcuts. A capture/pre-click hook
  would be an alternative way to support terminal-surface click-to-focus.

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Close last terminal (⌘W) | Quit app immediately; no quit confirmation. `Close()` blocks up to 2s. |
| Quit with live terminals | Native confirmation dialog; cancel leaves app running |
| Close window (title bar) | Same as Quit — confirm if terminals alive |
| Drag tab to window edge | DockLayout wraps root in a new split |
| Resize window | DockLayout + Term both handle resize (Term calls `TIOCSWINSZ` in `OnDraw`) |
| Term exits (shell dies) | `OnExit` fires — auto-closes the tab via `closePanel`. Shell death and queued-close race is safe: `Alive()` returns false before `OnExit` fires. |
| All terms in a group closed | `DockTreeRemovePanel` collapses the empty group |
| Rapid Cmd+T | Each press creates a new Term; PTY spawning is serial but fast |
| Theme change via context menu | Disabled in v1 (omit `Themes`); enabling requires unique per-Term menu IDs in `term.View` |
| Tab cycling with single terminal | No-op (guard: `len(group.PanelIDs) ≤ 1`) |
| Focused panel not in dock tree | `DockTreeFindGroupByPanel` returns `ok == false`; fall back to any remaining panel, rebuild tree from `app.Terms` |
| Focused panel orphaned, tab cycle | No-op (unlike close, which recovers). Close the orphaned tab to restore normal state. |
| Close background (unfocused) tab | Focus stays on current panel; no focus shift |
| Click unfocused terminal pane | Unsupported in v1 (tab bar / shortcuts only) |
| OSC title update | `w.UpdateWindow()` triggers layout refresh so tab label updates immediately |
| Split-pane focus change | `OnPanelSelect` emits `EventFocused`/`EventUnfocused` so PTY programs see focus transitions |
| First frame | Empty dock layout before `OnInit` creates first Term; imperceptible |

## Window Close vs. Quit

macOS treats the title-bar close button as "close window," not "quit app."
For an app with running shells, both should confirm before exiting. Wire
`WindowCfg.OnCloseRequest` to the same `confirmQuit` helper as
`file.quit`:

```go
gui.WindowCfg{
    OnCloseRequest: func(w *gui.Window) {
        confirmQuit(w)
    },
    …
}
```

The callback owns the decision: `confirmQuit` calls `w.Close()` when
appropriate, or does nothing when the user cancels.

## Test Plan

Manual checks after `cd examples/multiterm && go run .`:

- [ ] Single terminal starts; typing reaches the shell.
- [ ] ⌘T adds a second tab; tab bar appears; new tab receives focus.
- [ ] ⌘⇧] / ⌘⇧[ cycle tabs; keystrokes follow the active tab.
- [ ] Drag a tab to split the layout; both panes remain usable.
- [ ] Close a background tab (tab ×); focus stays on the current pane.
- [ ] Shell exit (`exit`) auto-closes its tab.
- [ ] ⌘W on the last tab quits without a confirmation dialog.
- [ ] ⌘Q (or window close) with running shells shows confirm; Cancel keeps app open.
- [ ] Resize window; both panes receive `SIGWINCH`.

## Open Questions

1. **Window title**: With multiple terminals, what should the window
   title show? Start with "go-term" static; optionally show the
   focused terminal's title as a subtitle via `w.SetTitle(title)`.
2. **Restoring layout on restart**: Out of scope for the example —
   always start with a single terminal.
