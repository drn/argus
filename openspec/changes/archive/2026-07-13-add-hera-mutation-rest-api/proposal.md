# Expose Hera mutation actions over REST, in web + macOS

## Why

`GET /api/hera` (the orchestration roster) is the only Hera surface the daemon
exposes over REST. Every mutation — spawning a worker, sending a role-addressed
message, authoring/editing the plan-DAG — is native-MCP-only, driven by an
agent process whose `cwd` resolves it to a live hera binding
(`internal/mcp/hera.go`'s `resolveCallerRole`). The web SPA and the macOS app
both ship a read-only Hera ("Projects") tab today, and both said proposals
(`openspec/changes/archive/2026-06-22-add-web-hera-tab`,
`openspec/changes/archive/2026-07-02-macos-app`) named this gap as an explicit,
deliberate follow-up rather than an oversight — pinned in
`openspec/specs/macos-app/spec.md` ("Hera roster (read-only)") and
`openspec/specs/mobile-pwa/spec.md` ("Hera orchestration tab"). This change is
that follow-up.

Scope, as decided with the user ahead of this proposal: three mutation
families — **spawn worker**, **send message**, **plan mutations**
(create/update/cancel a planned node, add/remove a blocking edge) — wired into
**both** the web app and the macOS app in the same change, per this repo's
three-surface parity rule. `hera_join`/`hera_move` (re-binding a task to a
different orchestrator) stay TUI/MCP-only and are not re-litigated here.

## Design principle: REST mutations act as the target orchestrator's coordinator

MCP's `hera_*` tools resolve "who is calling" from the calling agent process's
`cwd` — a live hera binding, usually a coordinator, sometimes a worker or
freelance. A REST/web/macOS caller has no such thing: it's a human operating a
dashboard over HTTP, not an agent process bound to a task's worktree. There is
no `cwd` to resolve, and no legitimate case for a human impersonating a
specific worker's or freelance's agent identity over the wire.

So every mutation endpoint in this change is scoped to `{orch_id}` (a value
the client already has from `GET /api/hera`) and is resolved **server-side** as
an action of **that orchestrator's coordinator role** — never a caller-supplied
identity. Concretely: `hera_spawn_worker` and all `hera_plan*` tools already
require the caller to hold a live coordinator binding, so this is a direct
match. `hera_send` is more general (workers/freelancers can send too, and MUST
supply `status`) — this change deliberately narrows the REST send endpoint to
**coordinator-as-sender only** (`to` required, no `status` field, matching
`hera_send`'s own coordinator-sender rule: "coordinators must supply an
explicit `to`", "optional for coordinator senders"). This is the "don't just
reuse MCP verb names uncritically" simplification: a human driving the web/mac
Hera tab is standing in for the coordinator's operator, not for an individual
worker.

A consequence: if an orchestrator's coordinator role has no live binding (e.g.
its task was stopped/deleted, or it was `hera_move`d away), every mutation
endpoint for that orchestrator returns `409` ("orchestrator has no live
coordinator") rather than silently picking another role.

## What Changes

### REST surface (`internal/api`)

New endpoints, all under `/api/hera/orchestrators/{orch_id}/...`, authenticated
like every other `/api/*` route (see Open Questions on the auth tier):

| Method   | Path                                                     | Mirrors                                    |
| -------- | --------------------------------------------------------- | ------------------------------------------- |
| `POST`   | `/api/hera/orchestrators/{orch_id}/workers`                | `hera_spawn_worker`                         |
| `POST`   | `/api/hera/orchestrators/{orch_id}/messages`               | `hera_send` (coordinator-sender only)       |
| `POST`   | `/api/hera/orchestrators/{orch_id}/plan/nodes`              | `hera_plan_node`                            |
| `POST`   | `/api/hera/orchestrators/{orch_id}/plan`                    | `hera_plan` (whole graph, one transaction)  |
| `PATCH`  | `/api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}`    | `hera_plan_node_update`                     |
| `POST`   | `/api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}/cancel` | `hera_plan_node_cancel`                 |
| `POST`   | `/api/hera/orchestrators/{orch_id}/plan/blocks`             | `hera_block`                                |
| `DELETE` | `/api/hera/orchestrators/{orch_id}/plan/blocks`             | `hera_unblock`                              |

Design deltas from the MCP tool shapes (all deliberate, see delta spec for
exact wire contracts):

- **Role addressing is by `role_id`, not name**, everywhere a role already
  exists (block/unblock edges, plan-node update/cancel, message recipient).
  MCP tools address by name because an LLM caller doesn't carry numeric IDs in
  its head; a REST/UI client already has `role_id` from the `GET /api/hera`
  response it just rendered. `hera_plan`'s whole-graph endpoint still
  references brand-new in-batch nodes by their (uniquified) `name`, since they
  have no ID yet until the transaction commits — matching the MCP tool exactly
  for that one case.
- **No `cwd`, no `orchestrator` disambiguation param** — `orch_id` in the path
  *is* the disambiguator; there's no "caller's task holds 2+ live bindings"
  case because there's no caller task.
- **Message send drops `status`** — status changes are out of scope for this
  change entirely (see Non-Goals); the coordinator-only send endpoint has
  nothing to apply it to.

Shared validation currently lives inline in `internal/mcp/hera.go` /
`internal/mcp/hera_plan.go` (coordinator-guard, name resolution, project
defaulting, sentinel-error → message mapping). This change extracts the
caller-identity-agnostic parts (everything past "resolve the target
orchestrator's coordinator role") into `internal/hera` so `internal/mcp` and
`internal/api` call the same functions instead of duplicating ~500 lines of
validation logic that could drift. `internal/mcp`'s existing behavior and
tests are unchanged — this is a behavior-preserving extraction, not a rewrite.

### macOS app (`macos/`)

- `ArgusKit` gains client methods for all eight endpoints plus their
  request/response models (`ArgusClient+HeraMutations.swift`,
  `Models+HeraMutations.swift`).
- The Hera tab (`HeraTab.swift`) gains: a "Spawn worker" action on a
  coordinator/orchestrator row (form: role name, prompt, project, branch,
  backend, model — all optional except prompt); a "Send message" action on a
  coordinator row (form: recipient role picker, body, tldr, optional
  in-reply-to); and a plan-node editor reachable from the orchestrator's plan
  view (create node, edit prompt/project, cancel, add/remove a blocking edge
  between two roles). Destructive-ish actions (cancel a node, remove a block)
  get a confirmation, matching the existing task lifecycle pattern
  (`macos-app` spec's "destructive actions require confirmation").

### Web SPA (`internal/api/static`)

- `index.html` gains the same three mutation surfaces on the existing "Hera"
  (`Projects`) tab: spawn-worker form, send-message compose, plan-node
  create/edit/cancel + block/unblock controls, wired to the new endpoints and
  re-running `loadHera()` on success to reflect the mutation.
  `SW_VERSION` bumps since the app shell changes.

## Non-Goals

- **`hera_join` / `hera_move`** (re-binding a task to a different
  orchestrator) — stays TUI/MCP-only. Explicitly out of scope per the
  agreed-upon scope for this change.
- **`hera_new_orchestrator`** (bootstrapping a brand-new orchestrator) — stays
  MCP-only. An orchestrator is created by an agent task self-promoting to
  coordinator; there's no human-driven analog ("become a coordinator") for a
  web/macOS operator to trigger.
- **`hera_status`** (standalone role-status set, independent of a message) —
  out of scope. The only status field named in the original scope decision was
  `hera_send`'s embedded `status`, and this change's send endpoint is
  coordinator-only-sender, where `status` is optional and unused by MCP itself
  — so no status mutation reaches REST in this change at all.
- **Message reading** (`hera_inbox`, `hera_mark_read`, `hera_tree_updates`,
  `hera_get_messages`) — out of scope. The new send endpoint is compose-only
  from the web/macOS perspective: sending a message via REST has no
  corresponding way to see replies or history from those same clients. Flagged
  as an explicit open question below, not silently punted.
- **Worker/freelance-as-sender** over REST — out of scope by the coordinator-
  only design above. If a real need for it emerges, it's a follow-up that adds
  a `from_role_id` + required `status` back onto the send endpoint.
- **No new validation semantics.** Cycle detection, cross-orchestrator block
  rejection, materialization checks, message caps (64 KiB body / 500 unread /
  50 sends-min / 120-char tldr) all reuse the exact existing `internal/db`
  sentinel errors — REST does not invent new rules, only new transport.
- **No TUI changes.** The TUI already has native Hera mutation UI via
  keybindings + MCP (`internal/tui/hera/ops.go`); untouched by this change.

## Capabilities

### Added Capabilities

None — no new top-level capability; this modifies existing ones.

### Modified Capabilities

- `rest-api`: adds the eight Hera mutation endpoints described above.
- `macos-app`: supersedes the "Hera roster (read-only)" requirement with
  spawn/send/plan-mutation UI.
- `mobile-pwa`: supersedes the "no mutation controls" clause of the "Hera
  orchestration tab" requirement with the same three mutation surfaces.

## Impact

- **New code:** `internal/api/hera_mutations.go` (+ `_test.go`); extracted
  shared validation in `internal/hera/` (new file(s), exact split TBD during
  implementation) consumed by both `internal/mcp` and `internal/api`;
  `macos/Sources/ArgusKit/ArgusClient+HeraMutations.swift` +
  `Models+HeraMutations.swift` (+ Swift tests); `web-tests/tests/hera-mutations.spec.ts`.
- **Modified code:** `internal/api/routes.go` (new routes);
  `internal/mcp/hera.go` / `internal/mcp/hera_plan.go` (call the extracted
  shared functions instead of inline logic — behavior-preserving);
  `macos/Sources/Argus/HeraTab.swift` (new UI); `internal/api/static/index.html`
  + `sw.js` (new UI, version bump); `README.md` (Reference: new REST endpoint
  rows); `context/knowledge/gotchas/hera-view.md` or a new file (REST-acts-as-
  coordinator invariant, validation-sharing contract between `mcp`/`api`).
- **No schema change** — reuses existing `hera_*` tables and `internal/db`
  methods verbatim.
- **Dependencies:** none added.

## Open Questions

1. **Auth tier.** The REST auth model is single-tier by default (every
   authenticated token — master, device, plugin-scoped — gets the same
   permissions except a small master-only denylist: backends CRUD,
   self-update, token management; see `internal/api/auth.go`'s `requireMaster`
   doc). Recommendation: these eight endpoints follow that default (any
   authenticated token), consistent with `GET /api/hera` and task
   creation/lifecycle already being open to device tokens. But this is new
   surface — a leaked device token can now spawn agent tasks and inject
   messages, not just read state — so flagging for explicit sign-off rather
   than assuming.
2. **Send-endpoint generality.** Confirm the coordinator-only-sender
   simplification (no `from_role_id`, no `status`) is acceptable, versus
   exposing `hera_send`'s full generality (arbitrary sender role + required
   status for worker/freelance). Recommendation: ship the narrower form first;
   broaden only if a concrete UI need for worker-as-sender shows up.
3. **Message visibility.** Should this change (or an immediate follow-up)
   also add a minimal read endpoint (e.g. `GET
   /api/hera/orchestrators/{orch_id}/messages`) so the web/macOS send UI isn't
   a one-way compose box with no way to see a reply? Currently out of scope
   per the agreed mutation-only scope; flagging because the resulting UX is
   unusual (send a message, never see the answer from the same client).
4. **Refactor sequencing.** Extracting shared validation out of
   `internal/mcp/hera.go`/`hera_plan.go` touches code with extensive gotcha
   documentation (M4–M6 hera work). Should that extraction land as its own
   preceding PR (tasks.md already sequences it before the new endpoints/UI),
   or is doing it inline in this change's PR acceptable?
