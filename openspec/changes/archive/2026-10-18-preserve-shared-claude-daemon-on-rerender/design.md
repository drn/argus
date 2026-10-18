## Context

Coordinator approval #14738 follows host CLI ground truth: Claude daemon runs on demand and can remain a child of the client that spawned it. Its ancestry does not imply task ownership.

## Goals / Non-Goals

**Goals:** Preserve the shared daemon and all its descendants during width-repair escalation while still removing ordinary task helpers.

**Non-Goals:** Change ordinary stops, solve the tracked PID-0 bind follow-up, or add web/macOS repair UI or viewport publication (existing named parity follow-ups).

## Decisions

- Inspect argv with the existing process library. Recognize the Claude executable followed by the exact daemon subcommand, including native version-directory executables and the official npm CLI entry point.
- Treat recognized daemons as traversal boundaries: never enqueue their children. Emit production and uxlog records for each preserved root.
- If argv inspection fails, omit that process from the captured graph and log degraded inspection; do not guess that its subtree is owned.
- Use a real fake-Claude process tree to prove escalation preserves a daemon-shaped descendant and its child while removing an ordinary sibling. Keep pure shape/selection tests for negative cases.

## Risks / Trade-offs

- Future Claude launch shapes may change → keep matching explicit and test known native/npm forms; inspection failures preserve unclassified subtrees.
- A preserved shared service that retains the task PTY may still delay drain → ownership protection takes precedence; the existing drain timeout bounds it.

## Validation

- The real-process regression failed before implementation: escalation selected all three descendants and the fake shared daemon stopped responding. After the fix, both daemon and hosted child respond to fresh signals, while the ordinary helper is reaped. Shape negatives, nested protected subtrees and injected argv inspection failures are covered.
- Final targeted race tests pass for agent/daemon in 3.288s/2.213s, including the supervisor stream-11 digest. Exact final `make -k pre-pr`, with ambient Argus/backend session variables scrubbed, passes build, vet, formatting and lint (zero issues). Agent/API/daemon/client/DB pass in 80.003s/77.292s/38.015s/30.773s/65.183s; Hera/terminal pass in 27.511s/76.703s.
- Only gate failures are previously waived pinned-Go advisories and the TUI 120.877s package budget. The complete TUI race rerun passes in 272.995s with a 600s limit. Replacing only that incomplete profile yields filtered coverage 88.5% (36190/40872), above the unchanged 88% floor. No Makefile/toolchain/test-threshold changes.
