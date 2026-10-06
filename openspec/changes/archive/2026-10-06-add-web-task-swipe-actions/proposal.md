## Why

Managing task state from the phone PWA takes a tap into the detail view plus a
menu pick. The most common triage moves — "this one is done" and "that one isn't
actually done" — should be one gesture on the list row, as in mail/to-do apps.

## What Changes

- **Swipe a task row left** (in the web task list) to mark it `complete`.
- **Swipe a `complete` task row right** to mark it active again (`in_review`).
- A coloured, labelled action reveals under the row as it drags; releasing past
  a distance threshold commits, releasing short snaps back. The row animates
  out/back and the list re-renders from the new status.
- Gestures are ignored when they are mostly vertical (list scroll wins) and on
  rows where the action does not apply (swipe-left on `complete`, swipe-right on
  non-`complete`) — those rows resist/snap back with no request sent.
- Uses the existing `POST /api/tasks/{id}/status` endpoint; **no daemon/REST
  change**. A failed request snaps the row back and shows a toast.
- A tap still opens the detail view; a completed swipe never also fires the tap.

## Decisions

- **"Active again" maps to `in_review`, not `in_progress`.** `in_progress` implies
  a live agent session, which a bare status write cannot start; `in_review` is the
  honest "needs attention, no session running" state and the idle detector /
  reconciliation owns promotion from there. Easy to flip if `in_progress` is wanted.
- **Touch-only.** Pointer/mouse users keep the detail-view controls.

## Impact

- Affected spec: `mobile-pwa`.
- Code: `internal/api/static/index.html` (row markup/CSS + gesture handler),
  `internal/api/static/sw.js` (`SW_VERSION` bump).

## Non-Goals / Frontend Parity

- **TUI and macOS app**: no swipe analogue (keyboard-driven / pointer-driven
  surfaces already have status-set keys and menus); this is a touch-gesture
  affordance with no REST-surface change, so no parity work is required.
