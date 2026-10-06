# Tasks — add-web-hera-mutations

- [x] 1. Tests (red): handlers per endpoint (success, 404, 409, 400, 403 master gate, no hard delete, multi-bound preserved, hide stops session)
- [x] 2. `internal/hera`: shared nuke-subtree primitive (stop sessions, end bindings, nuke rows, archive tasks, preserve multi-bound); wire sweep trigger
- [x] 3. `internal/api/hera_mutate.go` handlers + routes + uxlog (`[api-hera]`)
- [x] 4. SPA: action menu, nuke confirm w/ preview, reload; bump SW_VERSION; escaping tests (`static_spa_refs_test`)
- [x] 5. Docs: README REST table, gotchas/web-remote.md + hera-view.md, AGENTS.md parity note
- [x] 6. Archive change (merge deltas into openspec/specs/{rest-api,mobile-pwa,hera-view}); `make pre-pr`
