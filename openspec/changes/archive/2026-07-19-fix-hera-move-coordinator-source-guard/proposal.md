## Why

**hera-freelancer-bug** — `hera_move` lets a caller currently holding a LIVE
COORDINATOR binding relocate itself to a worker/freelance role under a
different orchestrator. `hera_move` only validates the DESTINATION `kind`
(rejecting `coordinator`); it never checks the SOURCE role being ended. Ground
truth from a live repro: two coordinators (each running its own orchestrator
with its own worker history) each called `hera_move(kind="freelance")` to join
a new team. Each call ended that coordinator's own coordinator binding
(`end_reason: "moved"`), leaving its original orchestrator coordinator-less —
still listed in the rail, but headless, with its whole prior subtree orphaned
— and created a brand-new, structurally disconnected `freelance` role under
the target orchestrator with no link back to the subtree it used to run.

There is no agent-facing tool to properly nest an EXISTING coordinator (and
its subtree) under a new parent — that capability
(`internal/tui/hera.AdoptOps.ReparentCoordinator`) is wired only to the Hera
TUI's `J` key, a human-only action. An agentic coordinator reaching for
`hera_move` to accomplish "join this other team" is reaching for the only
tool it has, and that tool silently does the wrong thing.

## What Changes

- **`hera_move` rejects the call when the caller's resolved SOURCE binding is
  coordinator-kind**, ending nothing and creating nothing. The error names the
  caller's role + orchestrator, explains that this would orphan the whole
  subtree, and directs the caller to ask a human to use the Hera TUI's `J`
  (adopt/reparent) key instead — there is no agent-facing equivalent today.
- No new tool, no new capability, no schema change — a validation guard added
  to an existing tool, mirroring the existing destination-`kind` guard.

## Capabilities

### Modified Capabilities

- `hera-coordination`: the `hera_move` requirement gains a new rejection case —
  the caller's SOURCE binding being coordinator-kind is rejected (ending and
  creating nothing), in addition to the existing "no live binding" /
  "same orchestrator" / "destination kind=coordinator" rejections.

## Impact

- **Modified code:** `internal/mcp/hera.go` (`toolHeraMove`) — one guard added
  after `resolveCallerRole`, before any mutation.
- **Docs:** `.claude/skills/hera/SKILL.md` and
  `internal/skills/builtin/hera/SKILL.md` — a decision-rule bullet warning
  agents never to `hera_move` their own coordinator binding, and pointing them
  at the human-only `J` key instead.
- **No new key, no new MCP tool, no schema change, no daemon RPC change.**
  Specs are LOCAL DOCS only (`openspec/project.md`); the quality gate stays
  `make pre-pr`.
