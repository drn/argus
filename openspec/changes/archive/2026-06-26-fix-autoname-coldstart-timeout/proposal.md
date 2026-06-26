# Raise the auto-naming deadline above the claude CLI cold-start tail

## Why

Recent task auto-renames still fail intermittently — the task keeps its
original regex slug — even after the 06-24 fix raised the deadline 30s → 45s.
Investigation of the live `~/.argus/daemon.log` confirmed the failure mode has
shifted entirely to `signal: killed` (the deadline SIGKILL-ing the `claude`
process before Haiku replies):

- The two most-recent failures are both `signal: killed`. `signal: killed`
  fires exactly at the deadline, so the task-create → kill gap *is* the timeout
  in effect:
  - `06-24 00:32`: created `00:31:30` → killed `00:32:00` = **30.0s** (pre-fix binary)
  - `06-26 00:10`: created `00:09:48` → killed `00:10:33` = **45.0s** (the 06-24
    fix WAS deployed — verified the running binary predated the kill — and 45s
    still timed out)

- The `claude` CLI (v2.1.193) cold-start latency has ballooned far past the
  "6-8s end-to-end" the code comments assume. Measured against the exact
  auto-naming invocation: **~3-5s warm, ~20s cold, and >45s under load**. Each
  auto-name spawns a fresh `claude -p` — always effectively a cold start — and
  when it races the newly-created task's *own* `claude` agent cold-start (plus
  KB indexing and other live sessions) for CPU/IO, it exceeds 45s and is killed.

The 15s → 30s → 45s progression has been chasing a moving cold-start tail
conservatively. Because the call is a fire-and-forget background goroutine, a
generous deadline has **zero UX cost** (documented in `gotchas/misc.md`), so
there is no reason to keep the cap marginal.

## What Changes

- **Raise the call deadline from 45s to 120s.** This is ~2.6× the observed
  failing wall (45s) and well above the heaviest observed cold start, ending the
  chase. Correct the stale "6-8s" latency comment to the measured cold-start
  reality.

Behavior that does NOT change: fail-open semantics, the manual-rename CAS
guard, the empty-prompt and CLI-unavailable skip paths, output sanitization,
the single-retry transient policy, and the deliberate decision that both
attempts share one deadline (a slow attempt-0 hang correctly yields no retry —
the retry is for fast-failing transients, which still get ~all of the larger
budget).

## Impact

- Affected specs: `auto-naming`
- Affected code: `internal/llm/namegen.go` (+ `namegen_test.go`)
- No schema, API, or keybinding changes.
