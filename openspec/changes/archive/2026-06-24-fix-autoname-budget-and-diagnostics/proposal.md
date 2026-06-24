# Fix intermittent auto-naming failures + make them diagnosable

## Why

Recent task auto-renames fail inconsistently — the task keeps its original
regex slug. Investigation of the live `~/.argus` logs showed a cluster of
`[autoname] failed … err="claude -p failed: exit status 1"` lines with **no
captured reason**, plus one `signal: killed` (the 30s deadline firing).

Two root causes, both confirmed empirically:

1. **Budget cap headroom collapsed.** `internal/llm/namegen.go` caps each call
   at `--max-budget-usd 0.01`. The code comment claims each call is
   "~150 input + ~10 output tokens (≈ $0.0002)" — ~50× headroom. A live
   measurement (`--output-format json`) shows the real cost is
   **1235 input + 111 output tokens = $0.003388** — ~17× the documented
   estimate, leaving only ~3× headroom. The baseline ballooned because the
   `claude` CLI (now v2.1.187) and the `haiku` alias (now Haiku 4.5) are
   heavier than when the cap was tuned (Apr 2026). A longer pasted prompt
   (URLs, issue bodies, JSON error blobs — exactly the failing tasks) or a
   verbose Haiku reply now crosses $0.01 → `Error: Exceeded USD budget` →
   exit 1 → name unchanged.

2. **The failure reason is invisible.** `claude -p` writes runtime errors
   (budget overflow, and by the same design rate-limit / overload) to
   **stdout**, exits 1, and leaves **stderr empty** (verified:
   `STDOUT=[Error: Exceeded USD budget (1e-7)]  STDERR=[]`). The current code
   folds in only `ExitError.Stderr` and discards the captured stdout, so every
   real failure logs as a bare, undiagnosable `exit status 1`.

A transient overload/limit can also strand the slug permanently because the
call is one-shot with no retry.

## What Changes

- **Surface the failure reason from stdout.** On a non-zero exit, fold the
  captured stdout (where `claude -p` writes runtime errors) into the wrapped
  error, in addition to the existing stderr fold. Failures become diagnosable.
- **Raise the per-call budget cap** from `0.01` to `0.05` USD and correct the
  stale cost comment to the measured `~1235 input + ~111 output tokens
  (≈ $0.0034)`.
- **Retry once on a transient failure.** A non-zero exit (not the
  unavailable/empty-prompt skip cases, not invalid model output) is retried a
  single time with a short backoff before falling open to the slug, so a
  momentary overload/limit does not permanently strand the name.
- **Raise the call deadline** from 30s to 45s to absorb the slower, heavier
  current calls (the `signal: killed` case).

Behavior that does NOT change: fail-open semantics, the manual-rename CAS
guard, the empty-prompt and CLI-unavailable skip paths, output sanitization.

## Impact

- Affected specs: `auto-naming`
- Affected code: `internal/llm/namegen.go` (+ `namegen_test.go`)
- No schema, API, or keybinding changes.
