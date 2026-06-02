# Plan: Session Artifact Rendering & Viewing in Argus Web

**Date:** 2026-05-30
**Source:** `memory/handoff/2026-05-30-160252-render-view-session-artifacts-argus-web.md` (KB handoff)
**Status:** Implemented
**Current Phase:** Done (all 5 phases)

## Goal

Let an agent/skill register a produced artifact (HTML report, PDF, rendered markdown, image) and view it — rendered, not just downloaded — in the Argus Web PWA, including on mobile over Tailscale. Solve the triggering case: `/coach` writes `/tmp/coaching-reports/coaching-YYYY-MM-DD.html`, currently unviewable on mobile.

## Background

Argus agents routinely emit rich artifacts to `/tmp`. Two problems: (a) `/tmp` is ephemeral, and (b) Argus Web (`internal/api`) is a task dashboard with **no file-serving capability** — all web assets are a compile-time `//go:embed static/...` FS (`internal/api/routes.go:10`), so per-session artifacts can't reuse it. We need durable, Argus-owned storage plus **new runtime routes** that read from disk — scoped and sanitized so a path-traversal bug can't turn the Tailnet endpoint into a whole-disk reader.

Key facts established during codebase exploration:
- `task.Worktree` resolves a task's working dir; `db.Get(id)` is the lookup. Handlers like `handleGitStatus`/`handleFileTree` (`handlers.go:1515+`) already 404 on missing task/worktree and reject `..`/absolute paths — the model to follow.
- `authMiddleware` (`auth.go:126`) protects all non-skip paths; `/api/*` is **not** in the skip list, so new `/api/tasks/{id}/artifacts*` routes are authenticated automatically. It accepts `Authorization: Bearer` **or** `?token=` (needed because `<iframe src>` can't set headers — same pattern EventSource uses).
- **GOTCHA — `corsMiddleware` sets `X-Frame-Options: DENY` globally** (`server.go:204`). This blocks framing the artifact in an `<iframe>` even same-origin. The raw-serve handler must override it to `SAMEORIGIN` (set again before `WriteHeader`; handler wins over middleware).
- `agent.go:338` injects `ARGUS_TASK_ID` into every agent process env. MCP cwd-resolution helper `resolveTask(id, cwd)` (`mcp/server.go:1693`, longest-prefix) is the standard way an agent self-identifies; reuse it.
- Sandbox (`agent/sandbox.go`) already allows writes to `/tmp`, `/private/tmp`, and `WORKTREE`. **Copy-on-register from those locations needs zero sandbox changes.**
- DB is SQLite with `CREATE TABLE IF NOT EXISTS` blocks in `internal/db/schema.go`; `task_messages` (schema.go:220) is the closest table to mirror. Task delete unwinds via `agent.RemoveWorktreeAndBranch` (`cleanup.go:50`).
- MCP optional capabilities are wired with `Set*` methods + an `*Enabled()` gate (clipboard/messaging pattern). Both MCP and API run in the daemon and share one `*db.DB`.
- SPA sub-views (`#files-view`, `index.html:1004`) open from the overflow menu (`renderOverflowMenu`, `index.html:3152`); `openFilesView` (`index.html:3798`) is the template for an artifacts sub-view. **SW_VERSION (`sw.js:10`, currently `argus-shell-v50`) must be bumped on any `index.html` change.**

## Requirements

### Must Have
- Durable, Argus-owned per-task artifact storage at `~/.argus/artifacts/<task-id>/`, surviving `/tmp` cleanup and TUI/daemon restarts.
- Registration mechanism: MCP tool `artifact_register({path, title?, type?, id?, cwd?})` that copies the source file into durable storage and records metadata. Type inferred from extension when omitted.
- Authenticated serving routes scoped to the **registered set** (never an arbitrary path):
  - `GET /api/tasks/{id}/artifacts` → JSON list of metadata.
  - `GET /api/tasks/{id}/artifacts/{name}` → raw bytes with correct `Content-Type`.
- SPA rendering in the task-detail view: HTML (sandboxed iframe), PDF (iframe/object + download fallback), image (`<img>`), markdown (client-rendered into a sandboxed iframe). Works on the mobile PWA over Tailscale.
- Security: reject `..`, absolute paths, and symlink escapes; serve only files whose metadata is registered for that exact task; `X-Frame-Options: SAMEORIGIN` (not DENY) only on the raw route; HTML iframe sandboxed without `allow-same-origin` so artifact JS can't read the parent's token/localStorage.
- Tests: path-traversal rejection, symlink-escape rejection, content-type correctness, auth enforcement (401 without token), scoping (unregistered name → 404), MCP copy + manifest row, X-Frame-Options override.

### Should Have
- Artifact dir removed on task delete (extend the delete/cleanup path).
- `uxlog` calls on register (copy result, size), serve (hits/misses), and list — per the logging requirements.
- A documented guideline for artifact-producing skills: emit **self-contained** files (inline CSS, no CDN — `/coach`'s HTML already does this).

### Won't Do (this iteration)
- Injected sandbox-writable artifact dir + `ARGUS_ARTIFACTS_DIR` env var (handoff option "a"). Copy-on-register (option "b") needs no sandbox change and is zero-change for existing skills; defer "a".
- Changing the `/coach` skill itself (it lives in the forge repo, not here) — tracked as a downstream follow-up; this PR ships the mechanism + guideline.
- Public hosting / tunnels (out of policy). Artifact GC/quotas beyond delete-time cleanup.

## Technical Approach

**Storage:** new `agent.ArtifactsDir(taskID)` helper (sibling to `SessionsDir`) returning `~/.argus/artifacts/<task-id>`. Daemon (unsandboxed) does the copy, so reading the agent's `/tmp` or worktree source works.

**Manifest:** new `artifacts` SQLite table — `id, task_id, name, filename, type, size, created_at` — with an index on `task_id`. `name` is the display title; `filename` is the sanitized on-disk basename. Serving resolves `(task_id, name|filename)` → row → `filepath.Join(ArtifactsDir(taskID), filename)`, then defends with `filepath.Clean` + `EvalSymlinks` + `strings.HasPrefix(resolved, dir+sep)`. The DB row **is** the scoping allowlist — user-supplied `{name}` only selects a row, never builds a path directly.

**Registration (MCP):** `artifact_register` resolves the task (`resolveTask`), validates the source path is readable and within size cap, sanitizes the basename, copies into the durable dir (atomic temp+rename), upserts the manifest row (last-write-wins per `(task_id, filename)`). New `ArtifactStore` interface + `SetArtifactManager` wiring + `artifactsEnabled()` gate, mirroring clipboard/messaging.

**Serving (API):** two handlers mirroring `handleGetOutput`'s shape. List returns metadata JSON. Raw sets explicit `Content-Type` per `type`, `X-Frame-Options: SAMEORIGIN`, `Cache-Control: no-store`, streams the file. Both registered in `routes()`; auth is automatic.

**SPA:** "Artifacts" overflow item → `openArtifactsView()` sub-view (clone `#files-view`). List → tap → viewer chooses by type: HTML/PDF/image use `src = <raw-url>?token=<TOKEN>`; HTML iframe gets `sandbox="allow-scripts"` (no `allow-same-origin`). Markdown: `authedFetch` the text, render client-side, inject via `srcdoc` into a `sandbox=""` iframe (no token in URL, no X-Frame-Options dependency).

## Decisions

| Decision | Rationale |
|----------|-----------|
| Copy-on-register from `/tmp`/worktree, not an injected writable dir | Zero sandbox-profile change; backward-compatible with `/coach` which already writes `/tmp`. Defers handoff option "a". |
| DB `artifacts` table as the scoping allowlist | Serving keys off a registered row, not a user path — structurally prevents arbitrary FS reads. Matches existing SQLite conventions. |
| Override `X-Frame-Options: SAMEORIGIN` on the raw route only | Global `DENY` (set in corsMiddleware) blocks all framing; relax narrowly so the SPA iframe works without weakening other routes. |
| HTML iframe `sandbox="allow-scripts"` (no `allow-same-origin`) | Artifact is agent-generated/low-trust; opaque origin denies it access to the parent's API token in localStorage while still rendering interactive reports. |
| Markdown rendered client-side into `srcdoc` sandbox | Avoids a Go markdown+sanitizer dependency on the trusted server and keeps the token out of the URL. (Open: vendor a tiny MD lib vs. minimal inline renderer.) |

## Implementation Steps

### Phase 1: Storage + manifest + registration (Go core)
**Status:** complete

- [ ] Add `ArtifactsDir(taskID string) string` — `internal/agent/session.go` (next to `SessionsDir`) — `~/.argus/artifacts/<id>`.
- [ ] Add `artifacts` table + `task_id` index — `internal/db/schema.go` — mirror `task_messages` block.
- [ ] Add `model.Artifact` struct — `internal/model/` — fields: ID, TaskID, Name, Filename, Type, Size, CreatedAt.
- [ ] Add DB methods `AddArtifact`, `Artifacts(taskID)`, `GetArtifact(taskID, name)`, `DeleteArtifacts(taskID)` — `internal/db/` — parameterized SQL, mutex-guarded.
- [ ] Add type inference + basename sanitization helper (reject `..`, `/`, empty; map ext→type html/markdown/pdf/image/text) — `internal/api/` or a small shared helper.
- [ ] Wire artifact dir removal into task delete — extend the delete path alongside `RemoveWorktreeAndBranch` (`agent/cleanup.go` caller in daemon).

### Phase 2: MCP `artifact_register` tool
**Status:** complete

- [ ] Define `ArtifactStore` interface + `SetArtifactManager` + `artifactsEnabled()` — `internal/mcp/server.go` — mirror clipboard wiring.
- [ ] Add `artifactToolDefs` entry + dispatch case + `toolArtifactRegister` handler — copy source (size-capped, atomic), upsert manifest, return summary. Reuse `resolveTask`.
- [ ] Wire `SetArtifactManager` from the daemon — `internal/daemon/daemon.go` (where MCP `Set*` are called).
- [ ] Document the self-contained-artifact guideline in the tool description.

### Phase 3: API serving routes
**Status:** complete

- [ ] `handleListArtifacts` + `handleGetArtifact` — `internal/api/handlers.go` — mirror `handleGetOutput`; raw route sets explicit Content-Type, `X-Frame-Options: SAMEORIGIN`, `Cache-Control: no-store`; defend path with Clean+EvalSymlinks+prefix check.
- [ ] Register `GET /api/tasks/{id}/artifacts` and `GET /api/tasks/{id}/artifacts/{name}` — `internal/api/routes.go`.
- [ ] `uxlog` on register/list/serve success+failure.

### Phase 4: SPA rendering (mobile PWA)
**Status:** complete

- [ ] Add `#artifacts-view` sub-view + styles — `internal/api/static/index.html` (clone `#files-view`).
- [ ] Add "Artifacts" overflow item + `openArtifactsView()`/`closeArtifactsView()` + list fetch + per-type viewer (HTML/PDF/image via `src?token=`; markdown via fetch→render→`srcdoc`).
- [ ] **Bump `SW_VERSION` v50 → v51** — `internal/api/static/sw.js`.

### Phase 5: Tests + docs
**Status:** complete

- [ ] API handler tests (httptest): list, raw content-type per type, 401 without token, 404 for unregistered name, `..`/absolute/symlink-escape rejection, X-Frame-Options=SAMEORIGIN.
- [ ] MCP `toolArtifactRegister` tests: copy + manifest row, type inference, bad source path, size cap, cwd resolution.
- [ ] DB method tests: add/list/get/delete round-trip.
- [ ] Run `make pre-pr` to green (build, vet, fmt-check, lint-pr, vuln, test-cover-gate ≥88).
- [ ] Add gotchas to `context/knowledge/gotchas/web-remote.md`: X-Frame-Options override, iframe sandbox/token-isolation, copy-on-register scoping, SW bump.

## Testing Strategy

- Path traversal: `name` = `../../etc/passwd`, absolute path, and a symlink inside the artifact dir pointing out → all 404/400, no bytes leaked.
- Scoping: file physically present in the dir but **not** in the manifest → 404 (DB row is the allowlist).
- Content-Type correctness per type; `nosniff` already global.
- Auth: no token → 401; valid token (master & device) → 200.
- X-Frame-Options is `SAMEORIGIN` on the raw route, still `DENY` elsewhere.
- MCP copy is atomic and idempotent (re-register same name overwrites, single row).

## Risks & Open Questions

| Risk | Mitigation |
|------|------------|
| Path-traversal → whole-disk read over Tailnet | DB-row allowlist + Clean+EvalSymlinks+prefix check; no user path ever joined directly. Dedicated negative tests. |
| HTML artifact JS steals API token from parent | `sandbox="allow-scripts"` without `allow-same-origin` → opaque origin. |
| PDF rendering flaky in iOS Safari iframes | Provide an "Open / Download" fallback link next to the embedded viewer. |
| Stale PWA shell hides new UI | Bump `SW_VERSION` (enforced by CLAUDE.md rule). |
| Coverage gate (88% floor) regression | Co-author tests with code per phase; target platform-agnostic paths. |

- Markdown rendering: vendor a tiny client MD lib vs. a minimal inline renderer vs. server-side goldmark+bluemonday? Recommendation: client-side minimal renderer in `srcdoc` to avoid Go deps and keep rendering off the trusted server. **Confirm in /dev.**
- Should artifacts be visible in the TUI too, or web-only this iteration? Handoff scopes "Argus Web" — propose web-only now, TUI later.
- Artifact retention/quota policy beyond delete-time cleanup — defer.

## Dependencies

- Downstream (separate forge repo): update `/coach` to call `artifact_register` after writing its HTML. Not in this PR.

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|

## Estimated Scope

**Phases:** 5
**Tasks:** ~24
**Files touched:** ~10 (`agent/session.go`, `db/schema.go`, `db/*artifacts*`, `model`, `mcp/server.go`, `daemon/daemon.go`, `api/handlers.go`, `api/routes.go`, `static/index.html`, `static/sw.js`) + tests + gotchas doc
