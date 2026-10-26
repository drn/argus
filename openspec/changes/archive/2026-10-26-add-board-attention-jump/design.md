## Context

Unread coordinator boards introduced by #1075 need to participate in the existing attention cycle. The base branch already supports task-id landing on coordinator headers. Rail rows, partial-fold reveal, the popup's private rail and excursion identity tracking must agree.

## Goals / non-goals

**Goals:** Extend existing jump scans with setting-gated unread coordinator boards, exact coordinator landing and identity-based excursion membership.

**Non-Goals:** Modify #1075's hint row, mailbox styling or accordion; change REST, web or macOS behavior; merge or deploy this PR. No new REST contract means no client parity implementation is required.

## Decisions

- Resolve board ownership from a row's coordinator orchestrator, including bridging rows. Keep one shared rail qualification for both scans; preserve needs-input-only semantics in the shared model.
- Preserve task-based landing for existing needs-input targets, but use exact orchestrator selection for board targets to avoid landing on another binding of the same task. Reuse the existing selection callback and landing logic.
- Add board coordinator role IDs to the existing excursion set and count. Counts never drive re-arming independently of identity.
- Reuse sticky reveal's canonical-parent path propagation for unread boards before building rows. This preserves fold ordering without introducing new status glyph semantics.
- Feed the board setting explicitly to the popup's private rail on every refeed.

## Risks / trade-offs

- Shared task bindings can hide a distinct coordinator board → exact orchestrator landing for board candidates and a shared-task regression test.
- Reusing reveal flags can leak board behavior with the setting off → derive reveal from each fresh model only when enabled and cover both settings.
- Refresh must not steal the selected plan → board auto-landing is forced only by an explicit board jump; ordinary refresh keeps #1075's behavior.
