## MODIFIED Requirements

### Requirement: Needs-input summary box above the rail (area 5)

The system SHALL draw a fixed one-line bordered "Needs input" summary box at the top of the rail column whenever one or more needs-input tasks have no presence in the Hera model, and SHALL reduce the rail's drawn height by the box height while it is shown. The box reports the count of such tasks as `"N tasks need input"` (or `"1 task needs input"` for a count of one). When no such task needs input the box has zero height and the rail occupies the full column.

The counted set is the needs-input set pushed by the App (`SetNeedsInput`) MINUS every argus task the Hera model knows: each role's live and structural binding (`TaskID` and `BridgeTaskID`) across the Pinned, Active, and Archived orchestrator sections and the Freelance section. Coordinators, managed workers (including those whose subtree row is folded — their cue already bubbles up via the subtree rollup), Hera freelance-roles, and tasks bound to ARCHIVED roles (their `BridgeTaskID` survives the binding ending) are therefore never counted; only tasks invisible from the Hera tab are.

The App SHALL feed the box only needs-input tasks that are currently `in_progress` (`needsInputInProgress` — task-list-parity, the SAME gate the flat task list uses for its own `(?)`). This is a DELIBERATELY NARROWER gate than the per-role rollup's `in_progress OR live` (BUG-A, #707) — the box only ever surfaces UNMANAGED tasks (ones with no Hera presence at all, so there is no live-role signal to consult), so it has no liveness signal to admit on and intentionally stays `in_progress`-scoped like the task list it mirrors. The needs-input scan is sticky (a finished task idling at its final prompt keeps the marker in its log tail); this gate keeps the box from tallying a finished/unmanaged task that shows no `(?)` anywhere.

The box is a passive heads-up: it has no keybinding, no focus, and no click-to-jump. Geometry is computed in `Draw` (no tview.Flex, no `screen.Sync()`); the box and the rail each paint their full bounding rect through `widget.DrawBorderedPanel`. The text is left-padded one cell from the border. On a terminal too short to keep the rail usable the box yields and is not drawn. The box is never drawn in remote mode (the page short-circuits to its unavailable banner first).

Derived from: `internal/tui/widget/attentionsummary.go` (the widget + left padding), `internal/tui/hera/page.go` (`Draw` geometry + count), `internal/tui/app.go` (`needsInputInProgress` gate → `SetNeedsInput` feed), `internal/tui/hera/model.go` (managed-task-id walk over role `TaskID`/`BridgeTaskID`), `context/knowledge/gotchas/hera-view.md` (no-Sync / full-rect rules).

#### Scenario: An unmanaged needs-input task is summarised

- **WHEN** a task in the `(?)` needs-input state has no role binding anywhere in the Hera model
- **THEN** the box is drawn at the top of the rail column and the rail is shrunk by the box height

#### Scenario: Count text pluralises

- **WHEN** exactly one unmanaged task needs input
- **THEN** the box reads `"1 task needs input"`, and reads `"N tasks need input"` when N is greater than one

#### Scenario: Managed, folded, and freelance tasks are excluded

- **WHEN** the only needs-input tasks are a coordinator, a managed worker whose subtree row is folded, and a Hera freelance-role
- **THEN** the count is zero and the box is not drawn, because each is already represented in the rail

#### Scenario: A finished (non in_progress) unmanaged task is not counted

- **WHEN** the sticky needs-input set still contains an unmanaged task that has finished (its status is no longer in_progress)
- **THEN** the count excludes it and the box does not render — matching the task list, which shows `(?)` only while in_progress

#### Scenario: A task bound to an archived role is not counted

- **WHEN** a needs-input task is bound to an archived Hera role whose live binding has ended
- **THEN** the count excludes it, because the role's structural binding (`BridgeTaskID`) keeps Hera presence after the binding ends

### Requirement: Live plan node icons are 1:1 with the rail (area 6)

A LIVE plan node's status icon (glyph AND style, including the animated spinner for a genuinely-active node) SHALL be identical to what the rail's status icon renders for the same role, computed through a SINGLE shared classifier so the two surfaces can never drift — not a parallel glyph table. The shared vocabulary: ready-to-close → review clipboard; needs-input → the needs-input glyph (so a worker blocked on a prompt is actionable from the DAG); done → `✓`; genuinely-active → the animated spinner (the plan view recomputes the frame at draw so it animates in lockstep); idle → moon-outline; live-quiet → moon-stars. Two plan-view-specific overlays the rail has no concept of: a PLANNED (never-bound) node renders the `○` circle, and a FAILED node (bound task result reports failure) renders `✕`. The header Status line uses the same resolved icon. The animated-spinner re-resolution applies ONLY when the shared classifier actually resolved to the spinner; a higher-precedence signal (notably needs-input on a genuinely-active role) resolves to its STATIC glyph and the node SHALL NOT animate, so it renders 1:1 with the rail's `?` rather than swapping in the spinner frame.

#### Scenario: A live node's icon equals the rail's

- **WHEN** a live worker role is in any status (done / working / idle / in-review / needs-input)
- **THEN** its plan node renders the same glyph and style the rail's status icon renders for that role, and a working node animates

#### Scenario: Needs-input outranks active without animating (BUG-012)

- **WHEN** a live worker role is genuinely active (`Live && SessionRunning && !SessionIdle`, independent of its bound task's status) AND the role also needs input (blocked on a prompt, or a descendant in its subtree does)
- **THEN** its plan node renders the static needs-input `?` glyph and style — identical to the rail row — and is NOT flagged animated, so the widget does not swap the `?` for the live spinner frame at draw

#### Scenario: Planned and failed overlays

- **WHEN** a node is a never-bound planned role, or a bound role whose task reports failure
- **THEN** the planned node renders `○` and the failed node renders `✕`
