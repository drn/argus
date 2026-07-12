# Fix coordinator self-promotion (prose + code guardrail)

## Why

A hera coordinator that followed its own orientation text could call `hera_new_orchestrator` on itself, gaining a second (coordinator) binding on its own task. The Hera rail then rendered a phantom nested "sub-coordinator" row that drove the identical PTY as its parent, and — in the live repro — the "sub-coordinator" did the actual work solo instead of dispatching. Prose guidance alone is not a reliable defense: an LLM can ignore orientation text (it did). The invariant should be enforced in code.

## What Changes

- **MODIFIED (behavioral):** `hera_new_orchestrator` (`toolHeraNewOrchestrator`) SHALL reject a caller whose task already holds a live **coordinator**-kind binding under any orchestrator, widening the existing same-orchestrator-only guard. The rejection error directs the caller to `hera_spawn_worker` (whose `project=` handles cross-repo) and, for genuine multi-project/multi-phase decomposition, the worker-promotion pattern or a `kind=subcoord` plan node. Callers holding only worker/freelance bindings (worker self-promotion) or no binding (fresh bootstrap) remain allowed.
- **Prose (non-behavioral, already on the branch):** `HeraCoordinatorOrientation` rewritten to lead with dispatch-don't-implement, keep the never-self-invoke guardrail, and frame a new coordinator session by multi-project/phase scope (not "the work is cross-repo"). Kept as complementary guidance — it teaches the model the whole pattern; the code guardrail is the hard floor beneath it.
- **Docs:** gotcha bullet in `context/knowledge/gotchas/orchestration.md` + index summary.

## Capabilities

- **Modified Capabilities:** `hera-coordination` (the `hera_new_orchestrator` requirement).

## Impact

- Code: `internal/mcp/hera.go` (`toolHeraNewOrchestrator` — new early guard before orchestrator creation).
- Tests: `internal/mcp/hera_test.go` (reject-coordinator, allow-worker-promotion, allow-fresh-bootstrap).
- No change to programmatic coordinator creation (`SpawnHeraCoordinator` rail `n` key, `MaterializeHeraSubCoordinator` gater) — neither routes through the MCP tool.
- Folded into PR #835 (which already carries the prose + docs); the PR becomes behavioral and ships this change folder, archived within the PR.
