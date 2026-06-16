# Hera View

## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Ctrl+Z` toggles fullscreen for the focused content pane (a no-op while the rail itself is focused, but ALWAYS consumed so `0x1A` can never reach a pane PTY and suspend its agent); `Enter` enters the selected role's pane, reviving its session first — a dead session (no live session in the runner) is restarted via `startSession`, and a live worker/freelance session that is idle and not parked at a user prompt (the signature of a suspended/stuck agent) is revived in place via the runner's `KickRerender` stop-and-resume (resumed losslessly via `--session-id`, NOT the TUI-side `pendingRerenderRestart`, which would settle the worker at `InReview` from the Hera tab); `w` spawns a worker under the selected coordinator's orchestrator; `r` renames the selected role/orchestrator; `a` toggles archive; `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `Ctrl+D` deletes the selected role/orchestrator. Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

Derived from: `internal/tui/hera/rail.go:548` (rail `InputHandler`), `internal/tui/hera/page.go:288` (page `InputHandler` focus ladder + `Ctrl+Z`), `internal/tui/hera/page.go:371` (`handleRailMutation`), `internal/tui/heraactions.go` (`heraReattach` dead-vs-live revive), `internal/tui/modal/help.go:70` (help overlay Hera section).

`NOTE:` Native deliberately OMITS several plugin rail keys: `n` (new orchestrator — canonical path is the `hera_new_orchestrator` MCP tool), `J` (adopt/reparent freelancer), `/` (rail name filter), `Ctrl+R` (hera-prune — Tasks-tab-only in native), and `l` (toggle archived visibility). `Ctrl+Z` (fullscreen pane) is NO LONGER omitted — it is bound (see above), both to restore plugin parity and to close the suspend footgun. Plain Left/Right are unused by the rail (free for future horizontal nav). Per `docs/NATIVE-HERA-FOLLOWUPS.md`, the remaining gaps are known; `Cmd+↑/↓` rail-selection-while-pane-focused collides at the byte level with agent-view task navigation and remains an unresolved rebinding decision.

#### Scenario: Mutation key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected role
- **THEN** the archive-toggle callback fires for that role's `(role, orchestrator)` selection and the key does not leak to navigation

#### Scenario: Ctrl+Z toggles fullscreen instead of suspending the agent

- **WHEN** a content pane is focused and the user presses `Ctrl+Z`
- **THEN** the pane's fullscreen mode toggles, the rail stays visible, and the `0x1A` byte is consumed (never forwarded to the pane PTY, so no `SIGTSTP` is delivered to the agent)

#### Scenario: Ctrl+Z on the rail is an inert but consumed no-op

- **WHEN** the rail holds focus and the user presses `Ctrl+Z`
- **THEN** fullscreen does not turn on (the rail has no fullscreen mode) and the key is still consumed (it never leaks)

#### Scenario: Enter revives a suspended worker

- **WHEN** the rail is focused on a worker role whose session is live but idle and not parked at a user prompt (a suspended/stuck agent)
- **THEN** the reattach callback fires for that role and the App restarts the session in place and resumes it via `--session-id`, and focus advances into the pane

#### Scenario: Enter on a busy live worker does not restart it

- **WHEN** the rail is focused on a worker role whose live session is actively producing output (busy)
- **THEN** the session is NOT restarted — `Enter` only advances focus into the pane

#### Scenario: Omitted plugin key is inert

- **WHEN** the user presses `/`, `n`, `J`, or `l` while the rail is focused
- **THEN** nothing happens (no filter, no new-orchestrator, no adopt, no archive-visibility toggle) because native binds none of them

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Tab/Ctrl+Q, `Ctrl+Z`, Enter, `w`, `r`, `a`, `P`, `s`/`S`, and `Ctrl+D`

### Requirement: Three-region focus model and ladder (area 5)

The system SHALL track focus across three regions — rail, coordinator (HERA/middle) pane, and agent/details (right) region — via a focus machine that never lands focus on an absent region. `Advance`/`Retreat` step only through present regions; when a focused region disappears focus rebalances to the nearest present one (the rail is always present, so the fallback terminates). `Ctrl+Q` forces focus back to the rail. Mouse clicks focus the region under the cursor only if that region is present.

The focus machine SHALL also carry a fullscreen flag for the focused content pane. `Ctrl+Z` toggles it via the page (a no-op while the rail is focused). Fullscreen is an attribute of the *focused* pane: advancing the ladder while fullscreen keeps fullscreen on the new pane, while any transition that lands focus back on the rail (Retreat from the coordinator pane, `Ctrl+Q`/`ToRail`, or a rebalance off a disappearing pane) SHALL clear fullscreen — the rail has no fullscreen mode. Present-region flags are computed from the normal split geometry independent of fullscreen, so the ladder can still traverse the hidden pane.

Derived from: `internal/tui/hera/focus.go:34` (`FocusMachine` + fullscreen flag), `internal/tui/hera/focus.go:68` (`rebalance` clears fullscreen on rail), `internal/tui/hera/focus.go:89` (`Advance`/`Retreat`), `internal/tui/hera/focus.go` (`ToggleFullscreen`/`Fullscreen`), `internal/tui/hera/focus.go:131` (`SetRegion`), `internal/tui/hera/page.go:447` (`MouseHandler`).

#### Scenario: Advance skips an absent pane

- **WHEN** the agent region is absent (narrow terminal) and focus advances from the coordinator pane
- **THEN** focus does not move to the absent agent region (stays at the right-most present region)

#### Scenario: Focus rebalances off a disappearing region

- **WHEN** focus is on the agent region and that region becomes absent on the next layout
- **THEN** focus bumps to the coordinator pane if present, else to the rail

#### Scenario: Ctrl+Q returns to the rail

- **WHEN** any pane is focused and the user presses `Ctrl+Q`
- **THEN** focus returns to the rail

#### Scenario: Fullscreen toggles on a focused pane and survives advance

- **WHEN** the coordinator pane is focused, the user enables fullscreen, then advances the ladder to the agent region
- **THEN** fullscreen stays on for the now-focused agent region

#### Scenario: Returning to the rail clears fullscreen

- **WHEN** a content pane is fullscreen and focus returns to the rail (Retreat from the coordinator pane, `Ctrl+Q`, or a rebalance off a disappearing pane)
- **THEN** fullscreen turns off

### Requirement: Three-region layout computed in Draw (area 5)

The system SHALL lay out the view as rail | coordinator pane | agent/details region, computing rects in `Draw` (not via a tview.Flex). The rail is a fixed width (`heraRailWidth`); the remaining width splits evenly between the coordinator pane and the agent region. When the terminal is too narrow for a right area the rail takes the full width and both right regions are marked absent so focus cannot land on them. Every region paints through `widget.DrawBorderedPanel`/`FillArea` to cover its full bounding rect, and the view SHALL NOT call `screen.Sync()` for content updates.

When fullscreen is active and a content pane is focused, the system SHALL instead render the rail at its fixed width plus the single focused pane (coordinator pane, or the agent terminal / stacked details region) filling the entire remaining width; the other content region is not drawn and its recorded hit-test rect is collapsed to zero width. Present-region flags are still computed from the normal split so the focus ladder can traverse the hidden pane. Fullscreen rendering SHALL also paint full bounding rects and SHALL NOT call `screen.Sync()`.

Derived from: `internal/tui/hera/panes.go:14` (`heraRailWidth`), `internal/tui/hera/page.go:176` (`Draw` + fullscreen branch), `internal/tui/hera/page.go:208` (present-flag reconciliation), `context/knowledge/gotchas/hera-view.md` (no-Sync rule).

#### Scenario: Narrow terminal hides the right regions

- **WHEN** the terminal is narrower than the rail width plus a usable right area
- **THEN** the rail fills the width and both right regions are marked absent

#### Scenario: Fullscreen renders the rail plus a single pane

- **WHEN** fullscreen is active with the agent region focused
- **THEN** only the rail and the agent region are drawn, the agent region fills the full width to the right of the rail, and the coordinator pane's hit-test rect is zero-width

#### Scenario: Drawing the full view never calls Sync

- **WHEN** the view draws with live panes and a details region, fullscreen or not
- **THEN** `screen.Sync()` is called zero times
