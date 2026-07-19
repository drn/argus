---
tags: [argus]
audience: [shared]
---
## Code review methodology (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

When asked to review a diff, a PR, or your own changes before opening one, prefer the shipped
`hera-review` skill over an ad hoc read-through — it's a tagged-finding review contract
(`[AUTO-FIX]` / `[QUESTION]` / `[SPEC-DRIFT]` / `[ACKNOWLEDGED]` / `[SKIP]`), runnable directly via
`/hera-review` or read as the methodology to follow.

When the diff touches or adds tests, also run `/hera-review-test-adversary` — a corrective lens
that assumes a green test run proves nothing and checks whether the tests would actually catch a
plausible regression. It complements `hera-review`'s broader audit; run both when test coverage is
in scope.

Both run as a single reviewer. For a panel pass — multiple reviewers spawned and synthesized
together — use `hera-spawn-review` instead; see its own routing section for when to prefer it.
