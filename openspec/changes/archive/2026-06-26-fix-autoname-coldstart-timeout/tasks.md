# Tasks

- [x] Raise `llm.DefaultTimeout` from 45s to 120s in `internal/llm/namegen.go`
- [x] Correct the stale "6-8s end-to-end" latency comment to the measured
      cold-start reality (~3-5s warm, ~20s cold, >45s under load)
- [x] Update the timeout test in `internal/llm/namegen_test.go` to assert the
      new value (and that a deadline still bounds the operation)
- [x] Update `context/knowledge/gotchas/misc.md` timeout bullet (45s → 120s,
      cold-start-under-load reasoning)
- [x] `make pre-pr` green
- [x] Archive this change in-PR (fold delta into base `auto-naming` spec, move
      folder to `openspec/changes/archive/<date>-fix-autoname-coldstart-timeout/`)
