**Design doc:** `openspec/changes/add-coordinator-context-management/design.md`

Note on structure: every stage below depends only on the immediately preceding
stage (a strict linear chain), deliberately — this change will execute as a
linear plan-DAG spine with no fan-in, so tasks.md mirrors that shape rather
than exposing independent branches that would need reconciling back together.

## 1. Tests

- [x] 1.1 Write failing tests for the `hera_status` `handoff_note`/`request_recycle` params (both accept-and-apply and reject-for-non-coordinator paths) — from the `hera-coordination` delta scenarios
- [x] 1.2 Write failing tests for `HeraConfig.CoordinatorContextBudget` default (`200000`) and override-from-config.toml — from the `config-management` delta scenarios
- [x] 1.3 Write failing tests for the `argus coord-hook` Stop-hook subcommand: no-op on missing `ARGUS_TASK_ID`, no-op on non-coordinator role, unconditional `context_size` stamp, over-budget nudge, nudge recurrence and its stop condition — from the `coordinator-context-management` delta scenarios
- [x] 1.4 Write failing tests for `recycle_coord`: same task/worktree/branch/binding survives, self-service waits for idle, human-forced does not wait, seed prompt requires zero follow-up tool calls, stray background job is cleaned up before restart
- [x] 1.5 Write failing tests for the `B` rail keybinding: confirmation modal fires on a coordinator selection, no-op on a non-coordinator selection, help-modal listing includes `B`
- [x] 1.6 Confirm every `it should X` acceptance criterion in `design.md` maps to a failing test written above (Prove-It Pattern) — note any gap before proceeding. **Gaps found:** D2's "no `hera_messages`/`hera_role_status` schema change" is a negative/architectural constraint with no natural Go-test expression — verify by diff review at Stage 10, not a Stage 1 test. D2's "[decision_fork]/[impasse] tldr convention" and D4's two orientation/skill-text criteria (all five habits present; SKILL.md tightened) are Stage 5's own scope (task 5.3 — orientation-text snapshot test), not enumerated in Stage 1's task list — correctly deferred, not a Stage 1 gap.

## 2. Config field

**Depends on:** Stage 1

- [x] 2.1 Add `CoordinatorContextBudget int` (`toml:"coordinator_context_budget"`) to `HeraConfig`, wired into the default-config constructor at `200000`
- [x] 2.2 Make the Stage 1.2 tests green

## 3. hera_status extension

**Depends on:** Stage 2

- [x] 3.1 Add optional `handoff_note`/`request_recycle` params to the `hera_status` MCP tool schema and handler (`internal/mcp/hera.go`)
- [x] 3.2 Stamp `task_meta` (`hera`, `handoff_note`) when supplied; record a pending-recycle intent when `request_recycle=true`
- [x] 3.3 Reject both params with a naming error when the caller is not a coordinator
- [x] 3.4 Make the Stage 1.1 tests green

## 4. Context-budget Stop hook

**Depends on:** Stage 3

- [x] 4.1 Add the `argus coord-hook` subcommand (`cmd/argus/`): parse the hook's stdin for `transcript_path`, gate on `ARGUS_TASK_ID` + resolved coordinator role
- [x] 4.2 Tail the transcript JSONL for the latest assistant message's `usage.cache_read_input_tokens`
- [x] 4.3 Self-discover the daemon's REST port and `~/.argus/api-token`; overwrite `task_meta` (`hera`, `context_size`) via the REST API
- [x] 4.4 Compare against the project's `coordinator_context_budget`; emit a Stop-hook block decision with the reach-a-seam nudge when at/over budget
- [x] 4.5 Make the Stage 1.3 tests green
- [x] 4.6 Document the required one-time manual `~/.claude/settings.json` global Stop-hook registration (this is external to argus's own config — argus cannot write to the user's global settings file)

## 5. Coordinator-discipline spawn orientation and shared skill

**Depends on:** Stage 4

- [x] 5.1 Extend `HeraCoordinatorOrientation` (`internal/agent/hera_spawn.go`) with the five habits: small-window framing, low-default-effort-with-escalation, the sharpened delegation rule (native sub-agent for investigation vs. `hera_spawn_worker` for worktree-scoped work), pointers-not-payloads, distillate-harvest-before-retire
- [x] 5.2 Tighten `.claude/skills/hera/SKILL.md` §4 (the coordination-decision rule) with the "delegate with prejudice, but don't be dumb about it" language, without changing the existing decision triad
- [x] 5.3 Add/update an orientation-text snapshot test asserting all five habits are present

## 6. recycle_coord primitive

**Depends on:** Stage 5

- [ ] 6.1 Implement the daemon-side kill-and-restart on the same task (`BuildCmd(task, cfg, resume=false)` path; confirm no stale `SessionID` is pinned across the restart)
- [ ] 6.2 Implement the self-service path: consume the pending-recycle intent from Stage 3, defer the actual restart until `session.IsIdle()`
- [ ] 6.3 Implement the human-forced path: immediate kill-and-restart, no idle wait
- [ ] 6.4 Implement stray background-job cleanup before restart (session-identity job lookup + stop, addressing the known `task_stop`-doesn't-kill-everything failure mode)
- [ ] 6.5 Implement seed-prompt assembly: role's stored mission prompt + current plan-DAG node states for the orchestrator + `task_meta.handoff_note` (if present), composed server-side into the new session's opening prompt
- [ ] 6.6 Make the Stage 1.4 tests green

## 7. hera-view rail keybinding

**Depends on:** Stage 6

- [ ] 7.1 Bind `B` on the rail: confirmation modal on a coordinator selection, no-op on a non-coordinator selection
- [ ] 7.2 Wire the confirmed action to `recycle_coord`'s human-forced (immediate) path
- [ ] 7.3 Add the `B` entry to `HelpSections` (`internal/tui/modal/help.go`) and its `help_test.go` assertion (required in the same PR per this repo's key-binding convention)
- [ ] 7.4 Make the Stage 1.5 tests green

## 8. Documentation

**Depends on:** Stage 7

- [ ] 8.1 Add gotcha entries to `context/knowledge/gotchas/orchestration.md` (or the appropriate existing file): the Stop-hook's global-settings requirement and self-gating rationale, the `hera_status` param additions, `recycle_coord`'s same-task mechanism and stray-job cleanup, the `B` keybinding's idle-vs-immediate distinction from self-service recycle
- [ ] 8.2 Update `context/knowledge/index.md` bullet counts for any touched gotcha file
- [ ] 8.3 Update the README Reference appendix: new MCP tool params, new keybinding table entry (per this repo's "top half is marketing, appendix updates in place" convention — no top-half edit expected for this change)

## 9. Pre-PR gate

**Depends on:** Stage 8

- [ ] 9.1 Run the full `make pre-pr` (build, vet, fmt-check, lint-pr, vuln, test-cover-gate) and fix every gap it surfaces before proceeding — run before the review stage, not only at the end

## 10. Review

**Depends on:** Stage 9

- [ ] 10.1 Single bounded direct review pass against `design.md` and the delta specs (correctness, spec-compliance, no regressions to existing hera-coordination/hera-view behavior)
- [ ] 10.2 Address any findings and re-run the affected portion of Stage 9's gate

## 11. Archive and ship

**Depends on:** Stage 10

- [ ] 11.1 Archive the OpenSpec change: fold the delta requirements into `openspec/specs/coordinator-context-management/spec.md` (new), `openspec/specs/hera-coordination/spec.md`, `openspec/specs/hera-view/spec.md`, and `openspec/specs/config-management/spec.md` (all modified in place), then move the change folder to `openspec/changes/archive/<date>-add-coordinator-context-management/` — all in the same branch, before merge
- [ ] 11.2 Re-run `make pre-pr` after archiving to confirm no drift
- [ ] 11.3 Push the final branch via `iris_push` and `hera_send` coord the branch name plus a plain-language summary of how it works
