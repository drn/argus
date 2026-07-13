# Tasks — add-hera-mutation-rest-api

TDD throughout (red-green-refactor): write failing tests from the delta specs
before implementation in every stage. Run `make pre-pr` clean before
opening/updating the PR; run `make test-cover` after and target ≥95% on touched
packages.

## 1. Extract shared hera-mutation validation (`internal/hera`)

Behavior-preserving refactor — no test in `internal/mcp` should need to
change.

- [x] 1.1 Identify the caller-identity-agnostic logic in
      `internal/mcp/hera.go` (`toolHeraSpawnWorker`'s post-coordinator-guard
      body) and `internal/mcp/hera_plan.go` (`heraPlanCoordinatorGuard`'s
      post-guard body for `hera_plan_node`/`hera_block`/`hera_plan`/
      `hera_plan_node_update`/`hera_unblock`/`hera_plan_node_cancel`).
- [x] 1.2 Move it into `internal/hera` (new file(s); e.g.
      `internal/hera/mutations.go`) as functions taking an already-resolved
      `*db.HeraOrchestrator` + coordinator `*db.HeraRole` (or role IDs) rather
      than a `cwd`-resolved `callerRoleResult` — so both `internal/mcp` and
      `internal/api` can supply their own caller resolution and share
      everything downstream.
- [x] 1.3 Update `internal/mcp/hera.go`/`hera_plan.go` to call the extracted
      functions after their existing `resolveCallerRole`/
      `heraPlanCoordinatorGuard` resolution. Full existing `internal/mcp`
      hera test suite (`hera_test.go`, `hera_plan_test.go`,
      `hera_send_status_test.go`, `hera_subcoord_test.go`,
      `hera_failed_test.go`) stays green, unmodified in intent.
- [x] 1.4 `go test ./internal/hera/... ./internal/mcp/...` green; no behavior
      change observable from any MCP tool caller.

## 2. REST endpoint design, validation, auth (`internal/api`)

Write failing tests from the `rest-api` delta first.

- [x] 2.1 `internal/api/hera_mutations.go`: `orch_id` path-param parsing (400
      on non-numeric), resolve orchestrator (404 if unknown), resolve its live
      coordinator role (409 "orchestrator has no live coordinator" if none) —
      the shared precondition for all eight endpoints.
- [x] 2.2 `POST /api/hera/orchestrators/{orch_id}/workers` — calls the
      extracted spawn-worker logic (task 1.2). 201 with role/task summary; 400
      missing prompt; 400 unknown backend/model (bubbles `agent.CreateAndStart`
      validation).
- [x] 2.3 `POST /api/hera/orchestrators/{orch_id}/messages` — coordinator-
      sender-only send. 201 with `{message_id, to_role_id, delivery_mode}`;
      400 missing body/tldr/to, tldr>120 chars; 404 unknown recipient role id
      or role not in this orchestrator; 409 self-send (to == the coordinator's
      own role); 413 body>64KiB; 429 rate-limited/inbox-full.
- [x] 2.4 `POST /api/hera/orchestrators/{orch_id}/plan/nodes` — single planned
      node. 201; 400 missing name/prompt (or missing goal for `kind=subcoord`);
      name uniquified same as MCP.
- [x] 2.5 `POST /api/hera/orchestrators/{orch_id}/plan` — whole graph, one
      transaction (nodes then edges; edges reference in-batch nodes by name).
      201 with counts; any node/edge validation error rolls back the whole
      call (`CreateHeraPlan` semantics, verbatim).
- [x] 2.6 `PATCH /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}` —
      200; 400 neither prompt nor project supplied; 404 unknown role_id (or
      not in this orchestrator); 409 role already materialized.
- [x] 2.7 `POST /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}/cancel`
      — 200; 404 unknown role_id; 409 already materialized.
- [x] 2.8 `POST /api/hera/orchestrators/{orch_id}/plan/blocks` — 201; 400/409
      cycle, cross-orchestrator, self-block (mirrors `heraBlockErrMessage`).
- [x] 2.9 `DELETE /api/hera/orchestrators/{orch_id}/plan/blocks` — 200,
      idempotent no-op on a missing edge.
- [x] 2.10 `internal/api/routes.go`: register all eight routes. Confirm the
      auth tier decided in the proposal's Open Question 1 (default: open to
      any authenticated token, same as `GET /api/hera`) — add a `requireMaster`
      gate only if that decision changes.
- [x] 2.11 `go test ./internal/api/...` green.

## 3. macOS app (ArgusKit + Argus UI)

- [x] 3.1 `ArgusKit`: request/response models for all eight endpoints
      (`Models+HeraMutations.swift`) + client methods
      (`ArgusClient+HeraMutations.swift`), Swift unit tests for
      encoding/decoding.
- [x] 3.2 `HeraTab.swift`: spawn-worker form on a coordinator/orchestrator
      row.
- [x] 3.3 `HeraTab.swift`: send-message compose on a coordinator row
      (recipient role picker sourced from the existing roster fetch).
- [x] 3.4 `HeraTab.swift`: plan-node create/edit/cancel + block/unblock UI,
      confirmation dialogs on cancel-node and remove-block (matching the
      existing stop/delete confirmation pattern).
- [x] 3.5 `make mac-build && make mac-test` green.

## 4. Web SPA (`internal/api/static`)

- [x] 4.1 `index.html`: spawn-worker form + JS wiring, re-running `loadHera()`
      on success.
- [x] 4.2 `index.html`: send-message compose + JS wiring.
- [x] 4.3 `index.html`: plan-node create/edit/cancel + block/unblock UI + JS
      wiring.
- [x] 4.4 Bump `SW_VERSION` in `internal/api/static/sw.js`.
- [x] 4.5 `web-tests/tests/hera-mutations.spec.ts`: cover all three mutation
      surfaces against `cmd/argus-test-server`'s seeded roster (extend
      `seedHera` with a coordinator + planned node fixture if the current seed
      doesn't cover mutation preconditions).

## 5. Cross-layer tests

- [x] 5.1 Confirm coverage on all new/touched packages via `make test-cover`
      (target ≥95% touched-package floor per this repo's testing norms).
- [x] 5.2 `make test-cover-gate` — full race suite + the 88% repo-wide floor —
      green.

## 6. README

- [x] 6.1 Add the eight new endpoints to the REST API reference table
      (`README.md`, near the existing `GET /api/hera` row) — factual table
      update only, no top-half/marketing edit needed (per the README rule,
      this isn't a new pillar-class surface).

## 7. Gotcha documentation

- [x] 7.1 Document the "REST mutations always act as the target
      orchestrator's live coordinator, resolved server-side from `orch_id`,
      never a caller-supplied identity" invariant — likely a new section in
      `context/knowledge/gotchas/hera-view.md` or `misc.md`, cross-referenced
      from `web-remote.md`'s existing "Hera tab (read-only /api/hera roster)"
      bullet (which needs updating — it's no longer read-only after this
      change) and from `macos-app.md`.
  - [x] 7.1a Update the `context/knowledge/index.md` bullet-count cells for
        any touched gotcha file.
- [x] 7.2 Document the `internal/hera` extraction: what moved out of
      `internal/mcp`, and the contract both `internal/mcp` and `internal/api`
      now depend on (so a future MCP-side change doesn't silently break REST,
      or vice versa).

## 8. Archive

- [x] 8.1 Once implementation + review lands, run `openspec archive
      add-hera-mutation-rest-api` (or the manual merge-and-move fallback) —
      fold these delta specs into `openspec/specs/rest-api/spec.md`,
      `openspec/specs/macos-app/spec.md`, `openspec/specs/mobile-pwa/spec.md`,
      and move this change folder under
      `openspec/changes/archive/<date>-add-hera-mutation-rest-api/` — in the
      SAME PR, before merge, per this repo's CLAUDE.md.
