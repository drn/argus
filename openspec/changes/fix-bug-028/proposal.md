# Fix BUG-028: Hera rail surfaces needs-input on coordinator-less headers

## Why

In the native Hera view the needs-input "(?)" rollup is computed for an
orchestrator's whole subtree (BUG-018/BUG-023) but is only RENDERED through a
coordinator role's status glyph. The rail collapses every orchestrator by
default (the "tidy summary" view), so a blocked worker is hidden and the operator
relies on the collapsed header to surface the cue. When an orchestrator has no
coordinator role to carry the glyph — e.g. its coordinator role was nuked
(BUG-022 Tier-2) — the header renders NO needs-input cue at all, even though the
rollup flag is set. This diverges from the always-flat task list, whose
project-folder aggregate (`projectStatusIcon`) always shows "(?)" for any blocked
task.

Investigation note: the originally-hypothesised root cause (the `app.go`
`needsInputInProgress` filter on the Hera feed) is a NO-OP for this bug — a
permission-blocked worker stays `in_progress` (only session exit / `hera_status`
done rolls it out), so the filter keeps it and the per-role + with-coordinator
rollup paths already render "(?)" correctly (shipped with BUG-023, #772). The
only reproducible gap is the coordinator-less header rendering.

## What Changes

- The needs-input rollup is stamped on the `OrchView` (`SubtreeNeedsInput`), not
  only on the coordinator role, so a collapsed orchestrator header can surface it
  with no coordinator role present.
- The rail's `drawOrchRow` renders the needs-input glyph on the header when the
  orchestrator's subtree needs input and there is no coordinator role to carry
  it. With a coordinator role present, behaviour is unchanged (the coordinator's
  status glyph already carries the rollup).
- No new keybinding, no glyph vocabulary change, no detection change. The
  BUG-023 in_progress gate is untouched: a finished worker still clears the
  header rollup.

## Impact

- Affected spec: `hera-view` (the needs-input rollup requirement).
- Affected code: `internal/tui/hera/model.go` (`OrchView.SubtreeNeedsInput`,
  `rollupNeedsInput`), `internal/tui/hera/rail.go` (`drawOrchRow`).
- Behavioural change scoped to rail rendering; no data, schema, or key changes.
