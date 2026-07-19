## Context

A scoped `/spec-audit` of `internal/tui/hera/` against `openspec/specs/hera-view/spec.md`
(triggered by the `exclude-archived-from-needs-input-rollup` change, see
`.workflow/audits/2026-07-19/modules/hera-needs-input.md`) found four
requirements whose text still describes a **task-status-gated** model of
needs-input / status-icon / spinner behavior, while the shipped code has been
**liveness/session/content-based** since PR #824 ("Bug bash 3" — commit
`2e24e9e0`, BUG-A / BUG-C / BUG-F / #707). The spec was never updated when that
PR landed. This change is pure spec catch-up: zero code changes, confined to
`openspec/specs/hera-view/spec.md`.

The authoritative source for "what's actually true" is the code's own
extensively-documented comments (`internal/tui/hera/model.go:130-178`,
`:1020-1057`; `internal/tui/widget/rolestatusicon.go:8-93`), which already
explain each BUG-#/#707 mechanism in detail. This change transcribes that
documented reality into the spec — it does not introduce new design.

## Goals / Non-Goals

**Goals:**

- Rewrite the four contradicted requirements (and their scenarios) so
  `hera-view.md` accurately describes the shipped precedence, gating, and
  spinner-animation rules.
- Preserve every requirement's SHALL/scenario structure; only correct the
  substance that is factually wrong against the code.
- Cross-reference the four PRs/bugs (#824, BUG-A, BUG-C, BUG-F, #707, D2) so
  future readers can trace *why* the rule is what it is, matching the code
  comments' own practice.

**Non-Goals:**

- No code changes. `internal/tui/hera/model.go`,
  `internal/tui/widget/rolestatusicon.go`, and `internal/tui/hera/rail.go` are
  already correct — only the spec is wrong.
- Not re-auditing the rest of `internal/tui/hera/` (adopt.go, plan.go,
  details.go, panes.go, focus.go, ops.go beyond kanban, page.go,
  refresher.go) — out of scope per the audit's own scope boundary.
- Not touching the kanban requirement (spec 2094-2138) — the audit spot-checked
  it as consistent (freshly shipped, #869).

## Decisions

### D1 — Rewrite "Status-icon precedence on role rows" (`spec.md:139-168`) to the actual classifier

**Current (wrong):** `(1) ready_to_close wins over everything; (2) blocked/done
role status; (3) IsActive — a live binding whose bound task is in_progress —
spinner; (4) idle; (5) live; (6) unbound`. No `Failed` case.

**Actual** (`widget/rolestatusicon.go:46,64-93`): `NeedsInput > Active >
ReadyToClose > Failed > Done > Idle > Live > default`, where:

- `NeedsInput` (highest, BUG-A): the role's own needs-input signal OR subtree
  rollup — outranks `ready_to_close` and everything else, since a role
  genuinely blocked on a user prompt is the one actionable thing in the
  subtree.
- `Active` (BUG-C, BUG-F): `RoleView.IsActive()` = `Live && SessionRunning &&
  !SessionIdle` — a purely session/content-derived "producing output right
  now" signal, NOT gated on the bound task's status. Outranks the stale-able
  resting states (`ready_to_close`/`failed`/`done`) because a worker
  genuinely producing output again is more current than any of those stamps.
- `ReadyToClose`: unchanged (task_meta `hera.ready_to_close`).
- `Failed` (new, D2 from `make-hera-plan-living`): hera role status `failed`
  — a distinct red `✕`, ranked below `Active`, above `Done`. Zero spec
  coverage today; add it.
- `Done`, `Idle`, `Live`, default: unchanged ordering, just re-ranked beneath
  the corrected `NeedsInput`/`Active`/`Failed` slots.

Rewrite the requirement's precedence list and its "Genuine activity renders
the animated spinner" / "Stale working role-status does not animate" /
"Blocked outranks activity" scenarios to drop the `in_progress` framing in
favor of the session-based one, and add a "ready_to_close does not outrank
needs-input" + "Failed renders a distinct glyph" scenario.

**Alternative considered:** Leave the old task-status framing and just append
a note that it's session-based "in practice." Rejected — the requirement's
own SHALL language would still be false, which is exactly the kind of
misdirection the audit flagged as the highest-severity problem (derived-from
actively pointing a future reader at the wrong gate).

### D2 — Fix the stale worker carve-out inside "Needs-input propagates up" (`spec.md:1358-1373`, 1395-1400)

**Current (wrong):** claims the `in_progress` gate applies "ONLY to
WORKER-kind roles" (BUG-023) — i.e., a worker leaving `in_progress` is
"finished" and stops surfacing `(?)`, while only non-worker roles are
liveness-gated. Its precedence sentence (`:1395-1400`) says the rollup ranks
"immediately below" `ready_to_close` and that `ready_to_close` "SHALL still
win."

**Actual** (`model.go:1020-1054`, `allowNeedsInput := taskInProgress ||
rv.Live`): every live role of ANY kind — worker, coordinator, or freelance —
surfaces needs-input when the App's content-aware `needsInputIDs` set flags
it, regardless of task status. A worker deliberately sitting in `in_review`
with its session alive CAN and MUST surface a fresh `(?)` (BUG-A, #707) — the
opposite of what BUG-023 is described as guarding here. BUG-023 (a *finished*
worker never pinning `(?)` forever) is actually protected because: (a) the
session exiting ends the live binding (`rv.Live` → false, the branch stops
running), and (b) the App's `needsInputIDs` set is itself content-aware and
clears once the underlying prompt is answered — not because of a task-status
gate. And per D1, needs-input outranks `ready_to_close`, not the reverse.

Rewrite both the worker/non-worker paragraph and the precedence sentence to
describe the real, uniform liveness+content-aware model (no per-kind
carve-out needed — the distinction was itself the stale part).

### D3 — Rewrite "Needs-input CLEARS and propagates up" (`spec.md:1486-1525`)

**Current (wrong):** normative text and derived-from both assert
`buildRoleView gates RoleView.NeedsInput on task.Status == in_progress`. This
is the audit's highest-severity finding (C) — it actively misdirects.

**Actual:** there is no task-status gate. Clearing happens because (a) the
App's `needsInputIDs` membership is itself content-aware (BUG-032/034/035) —
a task is only in the set while it shows a *current* awaiting-input signal,
clearing on user input or session exit — and (b) `rv.Live` goes false the
moment a binding ends. Rewrite the requirement to describe this mechanism
directly rather than reintroducing a task-status gate. The role's own hera
`blocked` status remains an independent, ungated source (unchanged — this
part of the requirement is already correct) cleared by `s`/`S`.

**Trade-off:** requirements D2 and D3 describe closely related territory
(both are about "when does needs-input stop counting"). Considered merging
them into one requirement. Rejected for this change — merging requirement
identity is a bigger structural edit than fixing the substance, and the audit
found the CONTENT wrong, not the split. Keep the split, fix each independently
so the diff stays reviewable; a future change can consolidate if the split
proves confusing in practice.

### D4 — Rewrite "Active agents animate a spinner glyph" (`spec.md:1923-1951`)

**Current (wrong):** defines "genuinely active" as `live binding AND bound
task in_progress AND not content-idle`. Scenario "Live-but-not-in_progress
role is static" directly contradicts BUG-C, which exists specifically to make
a live, content-active `in_review` role SPIN (the #707 close-out window).

**Actual** (`model.go:168-170`, same `IsActive()` as D1): `Live &&
SessionRunning && !SessionIdle` — no task-status term at all. Rewrite the
definition and replace the wrong scenario with one asserting the opposite: a
live, content-active role in `in_review` DOES spin (BUG-C). Keep the
content-idle/BUG-036 paragraph as-is — the audit found it accurate.

## Risks / Trade-offs

- **Spec-only change with no test to prove it "worked"** → Mitigated:
  correctness is judged by re-reading the requirement against the cited code
  lines, not by a test suite (there's no code to test). `openspec validate
  --strict` catches structural errors; substance is verified by the
  spec-document review before archiving.
- **Requirements D2/D3 stay split despite overlapping territory** → Accepted,
  see D3's trade-off above.
- **Precedence rewrite (D1) touches scenarios also referenced conceptually
  by D2's precedence sentence** → Mitigated: D1 and D2 are corrected together
  in this same change so they stay mutually consistent (both now say
  needs-input outranks everything).

## Acceptance criteria

- It should state the role-row status-icon precedence as `NeedsInput >
  Active > ReadyToClose > Failed > Done > Idle > Live > default`, with
  `Active` defined as session/content-based, not task-status-based.
- It should include a `Failed` (red ✕) scenario for the status-icon
  precedence requirement.
- It should describe the needs-input propagation requirement's worker
  behavior as liveness/content-based for every role kind, with no
  worker-specific task-status carve-out.
- It should state that the needs-input rollup outranks `ready_to_close` (not
  the reverse) in both the propagation and precedence requirements.
- It should describe the needs-input CLEARS requirement's mechanism as the
  content-aware `needsInputIDs` set + liveness ending on session exit, with
  no `task.Status == in_progress` gate claimed anywhere in that requirement.
- It should define spinner-animation "genuinely active" as `Live &&
  SessionRunning && !SessionIdle`, with a scenario confirming a live,
  content-active `in_review` role DOES animate (replacing the current
  contradicting scenario).
