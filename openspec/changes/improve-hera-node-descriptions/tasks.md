# Tasks — improve hera node descriptions

## 1. Tool-param prompt-hygiene contract (spec: hera-coordination)

- [x] 1.1 Update the `prompt` param description on `hera_spawn_worker`
  (`internal/mcp/hera.go`) to direct passing the worker's MISSION only and to
  state that org/security policy MUST NOT be prepended — every spawned session
  receives org instructions independently (harness-injected), so a prepended copy
  is redundant and pollutes the stored prompt + plan-DAG view.
- [x] 1.2 Update the `prompt` param description on `hera_plan_node`
  (`internal/mcp/hera_plan.go`) with the same guidance.
- [x] 1.3 Confirm no logic change: the supplied `prompt` is still stored verbatim
  on the role (no stripping, no transformation).
- [x] 1.4 Test: assert both tool schemas' `prompt` descriptions contain the
  mission-only guidance and do not instruct prepending policy.

## 2. Hera skill guidance (`.claude/skills/hera/SKILL.md`)

- [x] 2.1 Add a rule in the skill's spawn/plan sections: pass the mission only;
  do NOT prepend the org/security policy — hera workers are full argus sessions
  that receive the org policy via their own session's injection, so a prepended
  copy is a redundant duplicate that pollutes the DAG.
- [x] 2.2 Include the one-line rationale (harness auto-injects; verified) so the
  guidance is self-justifying.

## 3. Plan-DAG node description render (spec: hera-view)

- [x] 3.1 Change the node description source from `firstLine(r.Prompt)` to the
  first N (≈3) non-empty lines of the prompt in `heraPlanNodesWithBridge`
  (`internal/tui/hera/plan.go`) and/or the header composition in
  `nodeHeaderLines` (`internal/tui/planview/planview.go`).
- [x] 3.2 Wrap the description to the detail-pane width; grow the header line
  count accordingly without breaking the roster/graph split in the coordinator
  Details region.
- [x] 3.3 Keep it policy-agnostic: no stripping, no skipping, no line-1
  assumption. Preserve the `"(no description)"` placeholder for an empty prompt.
- [x] 3.4 Test: a multi-line prompt renders several header lines (not just the
  first); an empty prompt renders the placeholder; header height stays within the
  detail region.

## 4. Verification

- [ ] 4.1 `make pre-pr` passes clean (build → vet → fmt-check → lint-pr → vuln →
  test-cover-gate).
- [x] 4.2 Add/update gotchas in `context/knowledge/gotchas/hera-view.md` (node
  description = first N wrapped lines, policy-agnostic) and `.../orchestration.md`
  or the messaging/coordination gotcha (spawn/plan prompts carry the mission
  only, never a prepended policy — the worker gets it from its own session).

## 5. Archive (same PR, before merge)

- [ ] 5.1 `openspec archive improve-hera-node-descriptions` (or apply by hand:
  fold deltas into base specs, move the change folder to
  `openspec/changes/archive/2026-06-30-improve-hera-node-descriptions/`), commit
  on the change branch so base specs land atomically with the code.
