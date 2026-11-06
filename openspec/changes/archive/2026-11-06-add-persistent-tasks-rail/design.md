## Context

The Tasks page currently wraps a project-grouped `TaskListView`, git/preview column, and metadata panel. Enter switches to a separate agent page and hides the tab header. The Projects page instead owns a persistent 34-column rail and terminal regions, with explicit focus routing. Its `Rail` is coupled to Hera roles, orchestrator nesting, kanban, and binding metadata.

`TaskListView` already owns project grouping, pinned/archive partitioning, the `H` filter, per-project aggregation, task glyphs, and cursor restoration. Those remain the authoritative task semantics.

## Goals / non-goals

- Enable project task browsing alongside an existing terminal pane without duplicating status or session logic.
- Keep the default layout unchanged and support switching the preference without restarting sessions or the TUI.
- Keep shell input, mouse focus, PTY geometry, and background redraw ownership correct.
- Named follow-ups: **Web SPA persistent project task rail** and **macOS persistent project task rail**. No REST changes or client implementation in this change.
- No Hera-role projection for plain tasks, new session lifecycle policy, or configurable rail width.

## Decisions

### Reuse the task list with an opt-in rail mode

Extend `TaskListView` with rail-specific folder navigation rather than synthesizing Hera model entries or copying the Hera rail. Keep grouping, partition/filter predicates, rendering, and icon classification shared. Extract a small shared rail-width/layout helper or focus helper only where both pages benefit; keep Hera role/nesting logic untouched.

In rail mode, project headers are selectable; selecting or activating one expands that project. Archive starts collapsed and Space/Enter on its header toggles it. Pinned remains fully expanded. The subsequently approved `add-tasks-expand-all` change replaces the original independent-fold proposal: focus-fold is the default in both Tasks layouts, and rebindable F keeps every project open until toggled back, persisted locally as ui.tasks_expand_all. Existing persisted Tasks visibility/selection state continues to work.

### Keep one task list and one main terminal pane

Use the existing list instance in whichever Tasks layout is visible; never draw the same primitive twice in one frame. Place the existing agent page beside it while the preference is enabled. Keep the tab header visible. On rail cursor movement to a task, bind the existing session or replay without auto-start or claiming terminal focus. On a project/section header, clear the bound terminal and show a selection hint. Explicit Enter on a task uses existing start/resume and close-out guards; a completed or archived task remains view-only until its existing explicit restart action is used.

Avoid rebinding an unchanged task/session on periodic refresh. Every task change releases the previous daemon focus claim, clears stale pane/diff/clipboard state, and uses existing emulator reset and redraw-generation rules. Previewing while rail-focused does not mark a task focused; entering its terminal does.

### Match Projects focus without stealing terminal Tab

- Rail: existing Tasks mutations and `/` input retain their semantics; `j`/`k`, arrows, and mouse wheel navigate; Space/Enter focus project headers or toggle Archive. Enter on a task enters its terminal; Tab also enters an already-bound terminal without starting a dead session.
- Terminal: Tab/Shift+Tab and ordinary typing/paste reach the PTY. Ctrl+Q moves focus to the rail while keeping its task pane visible. Modified Left returns to the rail from the leftmost content pane; modified Right enters the content or continues through its visible panes.
- Preserve existing modified Up/Down task navigation and task mutation bindings. Update keymap/help and Reference documentation for changed contexts.
- Ctrl+Z changes the existing git/file side-panel zoom inside the right-hand area; the rail stays visible. Hidden content panels cannot receive focus. Mouse clicks focus only interactive regions; a second click in an already-focused live terminal keeps existing PTY click passthrough.
- With cross-tab arrows enabled, the Tasks rail becomes the leftmost Tasks region and the rightmost visible content pane still hops to Projects. Modal and filter guards keep precedence.

### Use Projects width and clamp safely

The rail gets 34 columns, clamped to the available Tasks content width like Projects. The agent area gets the remainder. On terminals at most 34 columns wide, only the rail is visible and focus is forced there; increasing width restores content availability without restarting the session. The remaining area uses the existing zoomed or split content layout and borders. PTY sizes come from the actual terminal's inner rectangle; never subtract the rail twice, use hidden page rectangles, or resize a PTY to zero.

### Persist a live UI preference

Add `UIConfig.TasksRail` with TOML key `tasks_rail`, default false, and config-store key `ui.tasks_rail`, including initial TOML import. Settings → Appearance renders “Persistent task rail” as a boolean toggle with no restart marker. Apply through the existing Settings config-update path. Enabling preserves the selected task and shows rail plus its session/replay. Disabling while Settings is active restores the legacy task-list landing on returning to Tasks. An active agent view reconciled with the flag changing off retains that task in the legacy agent view. Neither transition starts or stops a session.

## Risks / trade-offs

- Task-list expansion and selection rules differ between layouts → opt-in rail paths plus explicit disabled-mode regression assertions.
- Navigation can leak redraw loops or emulators → reuse existing pane and generation guard; skip identical binds; test repeated navigation.
- Width changes can corrupt full-screen history → retain authored-width replay/resize logic and publish actual pane sizes through existing off-thread paths.
- Focus mistakes can swallow input or send navigation to the agent → SimulationScreen tests through the real event loop for rail, terminal, mouse, modal return, and Tab/paste routing.
- Narrow screens have no usable content width → retain a visible rail and prevent invisible focus/invalid PTY resizes.

## Migration plan

No schema migration is required; absent preference values decode to false. Import explicit TOML values through the standard config bridge. Rollback is switching the toggle off. Archive the approved delta into base specs on this branch before the PR is merged.

## Open questions

None required before review; proposed interaction details above are explicit for Aaron's annotations.
