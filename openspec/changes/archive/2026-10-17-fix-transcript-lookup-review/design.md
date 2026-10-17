## Context

Missing-transcript fallback must distinguish genuine absence from a different valid transcript location or an inspection error.

## Goals / Non-Goals

**Goals:** Preserve saved conversations, keep kick errors fail closed, and verify identities across both RPC hops.

**Non-Goals:** Automatic width kicks never discard unsaved sessions. The coordinator explicitly authorized fresh recovery only for explicit Hera revive.

## Decisions

Resolve the effective Claude directory using the same account and backend environment precedence as launch. Inspect exact IDs under logical and physical worktree encodings. Errors defer automatic kicks; uncertain relaunches attempt resume with the stored ID. Explicit revive shares the pending-restart path with an opt-in fresh recovery flag. Probe availability only for candidate kicks, off the UI thread. Consecutive Hera deferrals back off from 10 to 60 seconds and reset on rebind.

## Risks / Trade-offs

Transient filesystem errors defer kicks rather than risk conversation loss. Remote lookup continues using the existing transcript listing endpoint; a dedicated existence endpoint is deferred.
