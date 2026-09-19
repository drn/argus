## Context

`Session.IsIdle()` is a stateless three-second raw-output check. Claude Code can redraw a spinner, elapsed timer, or prompt chrome indefinitely while doing no meaningful work, so the raw clock never expires. `agent.ContentIdle` already distinguishes that case by comparing animation-stripped emulated-screen fingerprints across ticks and rejecting screens that show the working affordance, but only the idle-push watcher and TUI currently carry its state.

Recycle and notify both run on roughly five-second daemon ticks. They need the same content signal, including for ordinary primary-screen scrollback, without folding stateful screen emulation into the cross-process `Session.IsIdle()` contract.

## Goals / Non-Goals

**Goals:**

- Share one stateful content-aware idle adapter between recycle and notify.
- Preserve immediate success when raw idle is already true.
- Preserve notify focus, deadline, ordering, cancellation, and exactly-once gates.
- Make a pending recycle that remains busy for two minutes visible in daemon logs.
- Verify primary-screen scrollback as well as alternate-screen behavior.

**Non-Goals:**

- Change `Session.IsIdle()` semantics or its RPC representation.
- Change other raw-idle consumers such as resize kicks, revive, or REST task state.
- Persist content-idle or recycle-wait tracking across daemon restarts.

## Decisions

### D1: Wrap `ContentIdle` in a reusable tracker

Add an `agent.ContentIdleTracker` that owns a `ScreenRenderer`, `ContentIdleState`, and synchronization. Its per-session check first accepts raw idle, otherwise reads a bounded substantive ring-buffer tail, renders it at the live PTY size, and advances the existing `ContentIdle` state machine.

This keeps the proven detector single-sourced. Duplicating fingerprint maps in recycle and notify would make lifecycle and safety fixes drift; changing `Session.IsIdle()` would violate its stateless RPC contract and impose screen emulation on every caller.

Each consumer owns its own tracker. Their tick cadence and candidate sets differ, so sharing a global tracker would couple otherwise independent services and let one consumer prune another's state.

### D2: Expand notify's narrow session interface only for detector inputs

Reliable delivery needs `RecentOutputTail` and `PTYSize` in addition to `IsIdle` and `WriteInput`. These methods already exist on both local and remote session handles. Notify continues to own all existing safety gates; content idle only broadens the idle predicate.

### D3: Track prolonged recycle waits in memory

`RecycleWatcher` records the first observed pending tick per task. It logs once after two minutes of continuous pending/non-idle state and clears the entry when the task is no longer pending or when recycle proceeds. This avoids persisted schema and repeated five-second log spam.

## Risks / Trade-offs

- **Screen emulation adds work to each busy candidate tick** → Only pending notify/recycle candidates are inspected; raw-idle sessions short-circuit; reads remain bounded by the existing substantive-tail ceiling.
- **A busy screen can be briefly stable** → The existing working-affordance guard and stability threshold remain load-bearing and unchanged.
- **Two consumers keep independent history** → This is intentional because they reconcile independently; both use the same implementation and semantics.
- **Daemon restart forgets a wait duration** → Accepted because the requested visibility is diagnostic and explicitly in-memory; the next continuous wait starts a fresh timer.

## Migration Plan

No data migration is required. Rollback restores raw-idle-only gating and removes the in-memory diagnostics.

## Open Questions

None.
