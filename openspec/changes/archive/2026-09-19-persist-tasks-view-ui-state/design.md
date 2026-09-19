## Context

The Tasks view's `H` key toggles whether hera-managed tasks (spawned workers,
live coordinators) are hidden from the plain task list. The top-level shell
has three tabs — Tasks, Hera (displayed "Projects"), Settings — switched via
`1`/`2`/`3`. Both pieces of state are currently pure in-memory fields
(`TaskListView.hideHeraManaged`, `Header.activeTab`) that reset to their
defaults (visible, Tasks tab) on every argus restart.

The Hera rail (`internal/tui/hera/rail.go`) already persists its own
fold/selection state across restarts via a `RailStateStore` interface backed
by a key in the `config` key-value table inside `~/.argus/data.sql`
(`hera.rail_view_state`, see `openspec/specs/hera-view/spec.md`,
"Requirement: The rail persists its fold and selection state across
restarts"). That mechanism already covers "remember which coordinator/agent
was selected" — the shell just always boots on the Tasks tab today, so the
restored selection is never visible until the user manually switches to the
Hera tab.

## Goals / Non-Goals

**Goals:**
- Persist the Tasks-view `H` (hide-hera-managed) toggle across a full argus
  restart.
- Persist the last active top-level tab and restore it on launch, so a user
  who quits while on the Hera tab reopens on the Hera tab — making the
  already-existing rail-selection memory visible again.

**Non-Goals:**
- No change to the Hera rail's own fold/selection persistence — it already
  works; this change only makes the Hera tab reachable-by-default so that
  memory is visible.
- No persistence of the Tasks-view substring filter text, cursor position, or
  Settings sub-tab/section.
- No behavior change in `--remote` mode — both new keys are local-only, like
  the existing rail state.

## Decisions

**Storage: two new keys in the existing `config` key-value table**, not
`config.toml` and not a new state file. `config.toml` is a manually-edited
declarative override layer that doesn't even exist in this dogfood checkout;
writing to it from a keypress handler would fight its mtime-based live-reload
cache. The `config` table already holds exactly this class of "ephemeral
per-machine UI state that survives a restart but isn't user-facing
configuration" (`hera.rail_view_state`, `ui.spinner`, `sandbox.enabled`, …).
New keys, added in a new `internal/db/ui_view_state.go` mirroring
`internal/db/hera_rail_state.go`:
- `ui.hide_hera_managed` — `"true"` / `"false"`.
- `ui.last_tab` — `"tasks"` / `"hera"` / `"settings"` (a plain string, not the
  `widget.Tab` int, so `internal/db` doesn't need to import the `tui/widget`
  package; the string↔`widget.Tab` mapping lives in `internal/tui`).

Both are guarded local-only via the same `if d, ok := a.db.(*db.DB); ok`
type-assert idiom already used ~13 times in `app.go` (including the existing
`heraPage.SetRailStateStore(d)` wiring) — no new interface type is
introduced; `*db.DB`'s new methods are called directly at each site, matching
the codebase's existing repeated-type-assert convention rather than adding an
extra abstraction layer for two call sites.

**Hide-hera-managed: restore via a new non-firing setter.**
`TaskListView` gains `SetHideHeraManaged(hidden bool)`, which sets the field
directly without invoking `OnHeraManagedToggle` (that callback is reserved
for user-driven `H` presses; firing it on restore would be a "toggle" that
never happened). Called once at startup, before the first task load, from the
persisted value. The existing `OnHeraManagedToggle` callback (today: log +
redraw) additionally persists the new value on every real toggle.

**Last-tab: persist at the two chokepoints that actually change the header's
active tab**, not inside every `SwitchToPage` call site. Auditing
`internal/tui/app.go` shows `a.header.SetTab(...)` is called from exactly two
places: the top of `switchTab()` (covers `1`/`2`/`3` and arrow-key
switching) and inside `exitAgentView()` (covers the ~7 call sites that leave
the classic full-screen agent view directly, all of which land on Tasks).
Every other `pages.SwitchToPage("tasks")` call site (modal dismissals like
`closeConfirmDelete`, `closeRenameModal`, …) redisplays the Tasks *page*
without touching the header's active-tab state, so they need no new
persistence call. `switchTab(TabTasks)` while `a.mode == modeAgent` delegates
to `exitAgentView()`, so both sites can fire for the same transition — an
idempotent double-write, harmless (Hera never sets `modeAgent`, so this path
is only reachable when the tab was already Tasks; see
`internal/tui/app.go:1999-2000`).

**Restore-on-launch: reuse the existing pre-`Run()` injection point.**
`App.Run()` already calls `a.applyStartupSkew()` after constructing the
screen but before `a.tapp.Run()`, explicitly documented as safe to mutate
`a.pages`/`a.mode`/`a.tapp.SetFocus` before the draw goroutine exists. A new
`a.restoreLastTab()` call is added alongside it: loads the persisted tab and,
if it wasn't Tasks (the zero-value default the shell already boots into),
calls the existing `switchTab()` to land there — reusing the exact same code
path a `2` keypress would take, so `switchToHeraTab2()`'s refresh/focus/label
logic runs unchanged.

## Risks / Trade-offs

**[Risk]** A user could quit on the Settings tab and be surprised to reopen
there instead of Tasks. → **Mitigation**: this is the literal ask ("remember
what view the user was last on"); Settings is treated the same as Hera for
symmetry and simplicity (no special-casing one of the three tabs), and it's
one keypress (`1`) to return to Tasks.

**[Risk]** A malformed or unreadable value in either config key. → **Mitigation**:
same fail-open pattern as the rail state — an unset/empty/unrecognized value
falls back to the current defaults (hera-managed visible, Tasks tab), logged
via `uxlog.Log`, never fatal.

## Migration Plan

Additive only — new config-table keys, no schema migration, no change to
existing keys or behavior when absent (first run behaves exactly as today).
No rollback concerns beyond reverting the change.
