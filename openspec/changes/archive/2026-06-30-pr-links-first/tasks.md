# Tasks

## 1. Scope PR detection to github.com pull requests
- [x] 1.1 Rewrite `prPathRe` in `internal/links/links.go` to anchor
      `^/<owner>/<repo>/pull/<number>(/|$)` and gate `IsPR` on `host == github.com`.
- [x] 1.2 Update the `prPathRe`/`IsPR` doc comments.

## 2. List PR links first
- [x] 2.1 Stable-sort `Extract`'s output so PR links lead, preserving in-group order.

## 3. Tests
- [x] 3.1 `internal/links/links_test.go`: flip GitHub-Enterprise / GitLab cases to
      `false` in `TestIsPR`; add a non-github.com `/pull/` case; add a PR-first
      ordering case (PR printed after a non-PR) and an in-group stability case.
- [x] 3.2 `internal/api/handlers_test.go`: update `TestHandleGetLinks` expected
      order (PR now leads).

## 4. Docs & gate
- [x] 4.1 Update the `links.IsPR` bullet in `context/knowledge/gotchas/misc.md`
      (github.com-only) and add a PR-first ordering bullet.
- [x] 4.2 `make pre-pr` green.

## 5. Archive
- [x] 5.1 Fold deltas into `openspec/specs/task-linking/spec.md` and move this
      change to `openspec/changes/archive/<date>-pr-links-first/`.
