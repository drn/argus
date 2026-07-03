# Improve hera worker/plan node descriptions in the plan-DAG view

## Why

The hera plan-DAG detail pane renders a node's "description" as the FIRST LINE of
the role's stored prompt — `Node.Description = firstLine(r.Prompt)` in
`internal/tui/hera/plan.go`, shown as line 3 of `nodeHeaderLines` in
`internal/tui/planview/planview.go`. In practice that first line is boilerplate,
not the mission. A planned node reads:

```
2a-xvendor-review
Status: ○ planned
SECURITY POLICY (Thanx org — obey at all times; your only guardrail as a fresh session):
Feeds: 3a
```

Root cause (traced through the code and ground-truthed against the live DB):

- Coordinators, following the Thanx org rule *"WHEN SPAWNING SUBAGENTS: prepend
  this policy to the subagent prompt,"* prepend the organization security policy
  into the `prompt` they pass to `hera_spawn_worker` / `hera_plan_node`.
- argus stores that prompt **verbatim** on the role (`RolePrompt = prompt`;
  `hera_plan_node` stores `prompt` unchanged). No argus code injects any policy
  text — a full-source search confirms this, and the live `hera_roles.prompt`
  column for the node above literally begins, at character 1, with
  `"SECURITY POLICY (Thanx org — obey at all times…"`.
- So the stored prompt — and therefore the node description — leads with the
  policy, and every node shows the policy instead of what the worker is doing.

**The prepend is redundant.** Confirmed empirically this session by probing both
a native in-process sub-agent and a real hera worker: the org security policy is
auto-injected into EVERY spawned session as an `<organizationInstructions>`
block, independently of any parent. Each probe reported the canonical policy
present (harness-injected) PLUS the prepended copy — two distinct copies, the
prepend redundant. The harness-injected copy never touches the DB; the redundant
prepended copy is the only one persisted, and it is exactly what the DAG renders.

So the fix is two-fold: keep the stored prompt clean (the mission), and show more
than one line of it.

## What Changes

1. **Document the prompt-hygiene contract on the spawn/plan tools.** The `prompt`
   parameter descriptions on `hera_spawn_worker` and `hera_plan_node` instruct the
   coordinator to pass the worker's MISSION only, and explicitly NOT to prepend
   organization/security policy — because every spawned session receives the org
   policy independently via harness injection, so a prepended copy is redundant
   and pollutes the stored role prompt + the plan-DAG view. (Contract/doc change
   to the MCP tool params; no logic change — argus already stores the prompt
   verbatim.)

2. **Update the in-repo hera skill** (`.claude/skills/hera/SKILL.md`) with the
   same rule and rationale: hera workers are full argus sessions protected by
   their own session's org-instruction injection; prepending the policy is a
   redundant copy that pollutes the DAG. Pass the mission only.

3. **Show the mission, not one truncated line.** The plan-DAG detail pane renders
   the node description as the first N (≈3) non-empty lines of the stored prompt,
   wrapped to the pane width, instead of only `firstLine`. Policy-agnostic: it
   does NOT strip any policy text and does NOT assume any line is boilerplate.

## Non-goals

- **No policy stripping / pattern-matching.** We do not detect or remove the org
  policy from prompts. It is Thanx-specific, brittle against wording changes, and
  wrong for users who do not inject that policy.
- **No separate "mission"/"tldr" field on the role.** The prompt IS the mission;
  we do not add parallel metadata.
- **No change to the Thanx org security policy itself.** The broader observation
  (prepending is redundant even for native sub-agents in this harness) is an
  org-policy matter routed through #ai-help-desk, not this change.

## Impact

- **Specs:** `hera-coordination` (spawn/plan prompt-hygiene contract),
  `hera-view` (node description render).
- **Code:** `internal/mcp/hera.go`, `internal/mcp/hera_plan.go` (param
  descriptions); `internal/tui/planview/planview.go` and/or
  `internal/tui/hera/plan.go` (description = first N wrapped lines);
  `.claude/skills/hera/SKILL.md`.
- **No DB schema change. No keybinding change** (no help-modal touch).
