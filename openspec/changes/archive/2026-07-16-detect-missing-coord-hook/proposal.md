# Detect a missing coord-hook Stop-hook registration

## Why

`argus coord-hook` (the context-budget Stop hook backing `coordinator-context-management`) requires a one-time manual registration in the user's global `~/.claude/settings.json` — Argus cannot write to that file on the user's behalf. That registration step is easy to skip, and nothing in Argus ever surfaces that it was skipped: the hook fires silently for every coordinator session when present, and is silently absent when it isn't. On the author's own machine the hook went unregistered for the feature's entire life with zero signal anywhere — no error, no warning, no missing data flagged — until a manual SQL query against `task_meta` turned up zero `context_size` rows.

A shipped, load-bearing feature with a silent-failure install step is a product gap independent of the feature's own correctness: nothing needs to be broken for adoption to be zero.

## What Changes

- `argus doctor` (already the CLI's read-only installation-health command) gains an additional, independent check: whether `~/.claude/settings.json` registers a `Stop` hook whose command references `argus coord-hook`. It prints one of three states — registered, not registered (with the exact snippet to add), or unknown (settings file missing/unreadable) — alongside the existing binary-coherence table.
- This check is purely advisory: it does NOT affect `argus doctor`'s existing exit-code contract (still governed solely by the binary-coherence verdict), since the two concerns are independent and the command's existing scripting contract should not silently change.

## Capabilities

### Modified Capabilities

- `binary-coherence`: `argus doctor` gains the Stop-hook registration check (additive to the existing verdict table, no change to the verdict/exit-code semantics).

## Impact

- **New code:** a pure classifier in `internal/doctor` (parses already-read Stop-hook commands, decides registered/not); I/O glue in `cmd/argus/doctor.go` (reads `~/.claude/settings.json`, best-effort).
- **Modified code:** `cmd/argus/doctor.go` (`runDoctor` prints the additional section).
- **No breaking changes.** Purely additive output; exit-code contract unchanged.
- **Not addressed here:** a proactive (non-doctor-invocation) nudge (daemon-startup banner, first-coordinator-spawn prompt) — evaluated and deferred; `argus doctor` was judged the more targeted, testable, lower-risk fix for now. Left as a named follow-up, not silently dropped.
