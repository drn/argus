## Why

**BUG-F — a reactivated Hera worker never spins on the rail because `ready_to_close` outranks the `active` spinner in the status-icon precedence.**

This is the icon-precedence completion of BUG-C. BUG-C fixed the `active` SIGNAL (`RoleView.IsActive()` → `Live && SessionRunning && !SessionIdle`), so a live worker rolled to `in_review` that keeps producing output is now correctly `active`. But the shared status-icon classifier's precedence still masks the spinner:

```
needs-input → ready_to_close → failed → done → active(spinner) → idle → live → default
```

A hera worker that finished a stage rolls to `in_review` and gets `meta:hera.ready_to_close=true` (`RollHeraWorkerToReview`). When the user then interacts with that still-live worker (e.g. types "hi") and it produces output again, `IsActive()` is now TRUE — but `RoleStatusIcon` hits `case in.ReadyToClose` (position 2) and returns the static review glyph BEFORE it ever reaches `case in.Active` (position 5). So a genuinely-working reactivated worker shows the static review glyph instead of the spinner. Reproduced live with a `ready_to_close=true` worker actively "Crafting…".

## What Changes

- **`RoleStatusIcon` / `RoleStatusInputs` (internal/tui/widget/rolestatusicon.go): move `case in.Active` ABOVE the stale-able resting states (`ready_to_close`, `failed`, `done`).** New precedence: `needs-input → active(spinner) → ready_to_close → failed(red ✕) → done → idle → live → default`.
- Rationale (mirrors BUG-A's needs-input > ready_to_close reasoning): `active` is the HONEST, content-derived "producing output right now" signal (`Live && SessionRunning && !SessionIdle`, BUG-C) — NOT the stale-able hera role-status/meta. A worker genuinely producing output is working again, so the spinner is the truer current state and must not be masked by the done-roll's `ready_to_close` stamp (or a stale `done`/`failed` role-status). When the worker goes idle again, `IsActive` drops false (the SessionRunning / !SessionIdle gate) and the resting glyph correctly returns — so the resting close-out / done / failed state is preserved.
- `needs-input` stays highest (a worker blocked on the user is more urgent than one merely producing; the two are mutually exclusive in practice).
- This is a pure switch-case reorder + doc comment; it changes BOTH shared surfaces 1:1 (the rail's `statusIcon` and the plan-view node projection), which is desired — a working plan node should also spin. `planNodeIcon` derives its `Animated` flag from the classifier's own resolved glyph, so it tracks the reorder automatically (BUG-012).

## Capabilities

- `idle-detection` — an actively-producing role's working spinner outranks the stale-able `ready_to_close` / `failed` / `done` glyphs in the shared status-icon classifier, so a reactivated worker in the #707 `in_review` close-out window animates rather than showing the static review glyph.

## Out of scope

- `RoleView.IsActive` (the `active` SIGNAL) is untouched — BUG-C already made it correct; this change is purely the icon ORDERING.
- The needs-input gate (BUG-A), task status / reconcile / revive logic, and `SessionRunning` plumbing (BUG-C) are all left intact.
