## Context

`add-hera-closeout-banner` shipped a two-state Enter toggle for a closed-out worker/freelance task's dead session (arm the banner, then dismiss to read-only replay), with an explicit Decision 4: "no separate third state — further Enters just keep toggling the banner on and off." `add-enter-closeout-guard-parity` then extended the same guard (and the same toggle) to the plain Tasks tab via a shared `App.reattachClosedOut(pane, t)`.

Dogfooding this exact PR (msg #5528) surfaced that Decision 4 doesn't hold up in practice: a third deliberate Enter press reads as clear operator intent to reopen the task, not an accidental repeat. This design reverses that one decision while keeping everything else (the two-Enter sequence, the shared cross-tab plumbing, the close-out detection itself) unchanged.

## Goals / Non-Goals

**Goals:**
- A third Enter (immediately following arm-then-dismiss) actually starts the session.
- The revive must not leave the task in a state where the guard immediately re-fires on the new session's own next natural exit.
- Both tabs (Hera, plain Tasks) get the behavior automatically via the existing shared plumbing — no per-tab special-casing.

**Non-Goals:**
- No confirmation modal before reviving. Three consecutive Enters is already a deliberate, keyboard-only action; adding a modal would be inconsistent with how lightweight the rest of this interaction is.
- Not touching `ReviveHeraWorkerToInProgress`, `hera_revive`, or any other existing revive path — this is a new, narrowly-scoped fourth caller pattern for a bare-taskID override, not a refactor of the revive family.
- Not changing what counts as "closed out" (`HeraWorkerAwaitingCloseout`'s own two signals are unchanged) — only what happens once the operator overrides it.

## Decisions

**Track the third step with a second bool on `TerminalPane`, not a counter.** `closedOutDismissedOnce` is set by `DismissClosedOutBanner` and read via `ClosedOutReadyToRevive()` (`!closedOutBannerShown && closedOutDismissedOnce`). A plain int counter was considered and rejected: the existing two-bool-adjacent shape (`closedOutBannerShown` already lives here) keeps the state machine legible as "which of the 3 named steps are we at" rather than an opaque number, and `ResetVT` already has a single, obvious place to reset both flags together.

**Reuse `ClearHeraReadyToClose` + `UpsertHeraRoleStatus`, don't touch `ReviveHeraWorkerToInProgress`.** `ReviveHeraWorkerToInProgress` exists specifically to *refuse* to act on a closed-out worker (the #707/BUG-050 invariant — a worker never self-completes, only a coordinator/human closes it out). Adding a bypass flag to it would weaken an invariant every other caller (the TUI's suspended-worker revive, the daemon's supervisor-reattach, `hera_revive`) depends on staying strict. Instead, `db.ClearHeraCloseout` is a new, narrowly-scoped primitive that clears the two underlying signals directly, and `forceReviveClosedOut` calls it immediately before the ordinary `startSession` — composing two already-correct primitives instead of teaching one of them a new bypass mode.

**Clear the close-out signals BEFORE `startSession`, not after.** If the newly-started session exits fast (crash, immediate error), the ordinary post-exit reconciliation runs before any "clear after" code would get a chance to — and would re-derive the identical closed-out state from the still-stale markers, silently undoing the operator's override. Clearing first means reconciliation sees a genuinely non-closed-out task throughout.

**No `pane.ClearClosedOutState()` skip-if-harmless shortcut.** Once `startSession` attaches a live session, `handleAgentKey`'s Enter-to-restart guard (`sess == nil || !sess.Alive()`) makes the closed-out flags unreachable anyway. Clearing them explicitly in `forceReviveClosedOut` is not strictly required for correctness today, but is cheap and removes a "is this actually safe to ignore" question for the next person reading the code.

## Risks / Trade-offs

- **Effectively removes a previously-shipped, spec'd safety rail** (Decision 4 existed to prevent exactly this kind of override). Mitigation: three consecutive Enters is a high-friction, unambiguous gesture — nobody arrives at this state by accident, unlike a single misplaced keypress.
- **`ClearHeraCloseout` touches role status directly**, which could race a coordinator's own `s`/`S` status step or a concurrent `hera_revive` call. Mitigation: `UpsertHeraRoleStatus` is already the single write-path every other status mutation goes through, so this doesn't introduce a new race class, only a new caller of an existing one — same risk profile as the pre-existing `ClearBlockedRoleStatus`.
- **Local-mode only** (matches `heraTaskClosedOut`/`heraKickRestartClosedOut`): in `--remote` mode `a.db` is `*apistore.Store`, which has no `ClearHeraCloseout` equivalent, so the clear silently no-ops and `startSession` still runs. The remote daemon owns its own closed-out state on its side.
