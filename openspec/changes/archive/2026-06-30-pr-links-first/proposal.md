# Prioritize PR links and scope PR detection to github.com

## Why

The link picker (TUI both pickers + web PWA modal) lists extracted URLs in raw
extraction order — PR links land wherever they happened to appear in the agent's
output. The PR link is almost always the one the user wants to open, so it should
lead the list on every surface.

Separately, `links.IsPR` currently matches any host whose path contains
`/pull/<n>` or `/merge_requests/<n>` (deliberately catching GitHub Enterprise and
self-hosted GitLab). In practice this mis-flags non-`github.com` URLs as PRs — a
link was shown with the PR indicator that was not a github.com pull request. The
indicator should mean exactly one thing: a `github.com/<org>/<proj>/pull/<number>`
URL.

## What Changes

- **PR-first ordering.** `links.Extract` returns PR links before non-PR links,
  stably (relative order within each group preserved). Because both TUI pickers,
  the web modal, the REST `/api/tasks/{id}/links` endpoint, and the remote TUI all
  consume `Extract`'s output (the web client renders the server order verbatim and
  never re-sorts), this single change reorders every surface.
- **PR detection scoped to github.com pull requests.** `links.IsPR` returns true
  only when the host is `github.com` and the path is `/<org>/<proj>/pull/<number>`
  (optionally followed by a sub-path like `/files`). GitLab merge requests and
  GitHub Enterprise / self-hosted hosts are **no longer** flagged as PRs.

**BREAKING (acceptable per repo policy):** GitHub Enterprise and GitLab MR URLs
lose their PR indicator. They still appear in the link list as ordinary links.

## Impact

- Affected specs: `task-linking`
- Affected code: `internal/links/links.go` (`IsPR`, `prPathRe`, `Extract` sort).
  No TUI/web/REST callsite changes — they inherit order and `IsPR` from the shared
  package. No static-asset change, so no `SW_VERSION` bump.
- Affected tests: `internal/links/links_test.go` (IsPR host scoping, PR-first
  ordering), `internal/api/handlers_test.go` (expected link order flips).
