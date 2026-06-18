# Delta: Focus-aware hotkey hints in the Hera bottom bar (BUG-007)

Adds to the `hera-view` capability: the status bar bottom row shows different
hint sets depending on which Hera region holds focus.

## ADDED Requirements

### Requirement: Bottom bar shows focus-aware hints while the Hera tab is active

The system SHALL render different hotkey hint sets in the bottom status bar
depending on which Hera region holds keyboard focus:

- **Rail focused** (default, `heraFocus == 0`): rail nav + mutation keys — `j/k nav`, `SP fold`, `/ filter`, `Tab pane`, `w spawn`, `n coord`, `s/S status`, `R retire`, `C prune`, `^r prune-all`, `^d del`, `? help`, `q quit`.
- **Coordinator or agent pane focused** (`heraFocus == 1` or `2`): pane keys — `^Q rail`, `Tab pane`, `^Z fullscreen`, `Cmd+↑↓ rail nav`, `Sh+↑↓ scroll`, `? help`. `q` and `1/2/3` are intentionally omitted: when a pane is focused those keys reach the PTY, not argus globals.

Hints update on the same frame as a focus change (keyboard or mouse). Other
tabs are unaffected. Key names match `modal/help.go` "Hera View (rail)"
section exactly — no undocumented keys are surfaced.

Derived from: `internal/tui/widget/statusbar.go` (`SetHeraFocus`, `heraFocus`
field, `Draw()` TabHera case), `internal/tui/hera/page.go` (`OnFocusChange`,
`notifyFocusChange()`, defer in `InputHandler`, click notify in `MouseHandler`),
`internal/tui/app.go` (`heraPage.OnFocusChange` wiring, `switchToHeraTab2`
reset).

#### Scenario: Rail-focused hints include spawn and filter keys

- **GIVEN** the Hera tab is active and the rail holds focus
- **WHEN** the bottom bar renders
- **THEN** the hint row includes `spawn`, `filter`, `fold`, and `retire` — the mutation keys absent from the prior static hint set

#### Scenario: Pane-focused hints include rail and scroll keys

- **GIVEN** the Hera tab is active and a coordinator or agent pane holds focus
- **WHEN** the bottom bar renders
- **THEN** the hint row includes `rail` (Ctrl+Q) and `scroll` — and does NOT include `spawn` or `filter`

#### Scenario: Hints update on Tab keypress

- **GIVEN** the Hera tab is active with the rail focused
- **WHEN** the user presses Tab to advance to the coordinator pane
- **THEN** the statusbar `heraFocus` field is set to 1 (FocusCoord) on the same frame, reflecting the new focus region

#### Scenario: Tab entry resets to rail hints

- **GIVEN** the operator has a pane focused on a previous Hera tab visit
- **WHEN** they switch away and return to the Hera tab
- **THEN** the statusbar resets to `heraFocus == 0` (rail hints) because the focus machine always starts on the rail
