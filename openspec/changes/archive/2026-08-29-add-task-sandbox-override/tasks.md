## 1. Model + persistence

- [x] 1.1 Add `SandboxOverride string` `json:"sandbox_override,omitempty"` to
      `model.Task` (`internal/model/task.go`), documented as an optional
      per-task tri-state override (`""`/`"enabled"`/`"disabled"`), set once at
      creation like `Archetype`/`Profile`/`BaseBranch`.
- [x] 1.2 `internal/db/schema.go`: add `sandbox_override TEXT NOT NULL DEFAULT ''`
      to the `tasks` CREATE TABLE and an idempotent
      `ALTER TABLE tasks ADD COLUMN sandbox_override TEXT NOT NULL DEFAULT ''`
      (mirroring the `profile` column migration just above/below it).
- [x] 1.3 `internal/db/tasks.go`: add `sandbox_override` to `taskColumns`, the
      `Add` INSERT, the `Update` UPDATE, and the row-scan — preserve column
      order across all sites (mirror `profile`/`archetype` exactly).
- [x] 1.4 Tests (`internal/db/db_test.go`): round-trip a task with a
      non-empty `SandboxOverride` through Add → Get → Update → Get; assert
      default `""` on a row inserted without one.

## 2. Resolution precedence

- [x] 2.1 `internal/agent/agent.go`: extend `ResolveSandboxConfig` with a third
      precedence tier — after the existing global→project merge, check
      `task.SandboxOverride` (`"enabled"` → `result.Enabled = true`,
      `"disabled"` → `result.Enabled = false`, `""` → unchanged) — mirroring
      `ResolveBackend`'s task-override-wins pattern immediately below it in the
      same file. Update the doc comment to state the three-tier precedence
      (global → project → task).
- [x] 2.2 Tests (`internal/agent/agent_test.go`): task
      override wins over a conflicting project override; task override wins
      over a conflicting global setting; empty override falls back to the
      existing project/global resolution unchanged.

## 3. Creation path threads the override

- [x] 3.1 `internal/agent/create.go`: add `SandboxOverride string` to
      `CreateInput` (next to `Profile`/`BaseBranch`); set
      `task.SandboxOverride = strings.TrimSpace(input.SandboxOverride)` in the
      `task := &model.Task{...}` construction, before the
      existing `task.Sandboxed = IsTaskSandboxed(task, cfg)` line so the
      override is already on `task` when that resolution runs.
- [x] 3.2 `internal/daemon/headless.go`: add `SandboxOverride string` to
      `HeadlessInput` and thread it into the `agent.CreateInput{...}` literal
      in `HeadlessCreateTask`.
- [x] 3.3 Tests (`internal/agent/create_test.go`): a `CreateAndStart` call with
      a sandbox override persists it on the created task. Deviation from the
      original wording: the assertion uses `ResolveSandboxConfig(...).Enabled`
      rather than `task.Sandboxed` — `task.Sandboxed` additionally gates on
      `IsSandboxAvailable()` (real `sandbox-exec` probe), which is always false
      on CI's `ubuntu-latest` runners, so asserting it directly would make the
      test vacuously pass/fail by platform rather than by the override logic.

## 4. TUI new-task form

- [x] 4.1 `internal/tui/newtaskform.go`: add `ntFieldSandbox` between
      `ntFieldArchetype` and `ntFieldPrompt` (renumbered `ntFieldPrompt`,
      `ntFieldName`, `ntFieldCount`; updated the `focused` field's doc comment
      listing field indices).
- [x] 4.2 Added `sandboxIdx int` state (default 0, `sandboxInherit`'s zero
      value). Reused `internal/tui/projectform.go`'s existing package-level
      `sandboxOptions`/`sandboxInherit`/`sandboxEnabled`/`sandboxDisabled`
      directly (same `tui` package, so no duplicate var/consts were needed) —
      an initial pass had duplicated a local `ntSandboxOptions`, caught and
      removed. Added `SandboxOverride() string` and `sandboxDisplayLabel()
      string` mirroring `ProfileOverride()`'s shape.
- [x] 4.3 Added `handleSandboxKey` (Left/Right cycle the 3 options; Enter/Down →
      `ntFieldPrompt`; Up → `ntFieldArchetype`), mirroring
      `handleArchetypeKey`. Updated `handleArchetypeKey`'s Down transition to
      land on `ntFieldSandbox` instead of `ntFieldPrompt`.
- [x] 4.4 Wired `ntFieldSandbox` into: the `InputHandler` field-index switch
      (routes to `handleSandboxKey`), the `PasteHandler` switch's
      selector-fields no-op branch (comment updated to list `ntFieldSandbox`
      alongside `ntFieldBackend`/`ntFieldProfile`/`ntFieldArchetype`),
      `visibleField` (needed no change — always visible, no hide flag), and
      the draw routine (a `drawSelector` call alongside the Profile/Archetype
      rows, using `sandboxOptions`/`f.sandboxIdx`; `selectorRows` modal-height
      calc bumped accordingly).
- [x] 4.5 `Task()`: added `SandboxOverride: f.SandboxOverride()` to the
      returned `*model.Task` literal.
- [x] 4.6 `internal/tui/app.go`: added `SandboxOverride: task.SandboxOverride`
      to the `agent.CreateInput{...}` literal built from the new-task form's
      result.
- [x] 4.7 Tests (`internal/tui/newtaskform_test.go`): default is Inherit and
      `Task().SandboxOverride == ""`; cycling right twice lands on Disabled and
      submits `"disabled"` (and wraps back to Inherit / Left wraps to
      Disabled); Enter/Down/Up focus nav in and out of the field. Three
      pre-existing tests hardcoded the old field numbering
      (`TestNewTaskForm_UpArrowLeavesPrompt`, `TestNewTaskForm_EnterOnSelector`,
      `TestNewTaskForm_HiddenArchetypeNavSkips`) and were updated to expect the
      new Sandbox field in the sequence rather than left silently stale.

## 5. REST API

- [x] 5.1 `internal/api/handlers.go`: added
      `SandboxOverride string \`json:"sandbox_override"\`` to `createTaskReq`;
      added a `validateSandboxOverride` function rejecting anything
      other than `""`/`"enabled"`/`"disabled"` with 400, mirroring
      `validateBackend`'s shape; called from `handleCreateTask` (and the
      multipart handler, see 6.1) before task creation.
- [x] 5.2 `internal/api/server.go`: extended the `TaskCreator` func type with a
      trailing `sandboxOverride string` parameter (after `taskModel`, before
      `autoName`) and updated its doc comment.
- [x] 5.3 `internal/daemon/daemon.go`: the `api.New` closure
      accepts the new parameter and forwards it via
      `HeadlessInput{..., SandboxOverride: sandboxOverride}`.
- [x] 5.4 `cmd/argus-test-server/main.go`: updated the local
      `creator` closure's signature to match the extended `TaskCreator` type.
- [x] 5.5 `handleCreateTask`: passes `req.SandboxOverride` through to
      `s.createTask(...)` in the new trailing position. Also updated
      `handleForkTask`'s call to carry `src.SandboxOverride` (a fork now
      inherits the source task's sandbox override alongside its existing
      backend/model inheritance — not explicitly listed in the original plan,
      but required for the positional signature change to compile correctly,
      and consistent with the existing fork-inherits-backend/model behavior).
- [x] 5.6 Tests (`internal/api/handlers_test.go`): a create
      request with `sandbox_override: "enabled"` persists it on
      the created task (readable via `GET /api/tasks/:id`); an invalid value
      is rejected with 400; a fork inherits the source task's override
      (`TestHandleForkTask_InheritsSandboxOverride`). Also extended the
      multipart path's `parseMultipartTaskForm` (a `sandbox_override` field,
      not originally listed under this section but needed so uploads via the
      web form don't silently drop the sandbox choice) with matching tests in
      `uploads_test.go` and a multipart bad-value rejection test.

## 6. Web SPA

- [x] 6.1 `internal/api/static/index.html`: added a Sandbox override
      `<select id="create-sandbox">` to the new-task form (Inherit/Enabled/
      Disabled, matching the mobile-pwa spec's value set), positioned after
      the Model field; included `sandbox_override` in both the JSON and
      multipart `createTask()` POST bodies, and reset the select to Inherit
      after a successful create (mirroring the existing name/prompt reset).
- [x] 6.2 Bumped `SW_VERSION` in `internal/api/static/sw.js` (v68 → v69).
- [x] 6.3 Checked the Playwright harness (`web-tests/tests/new-task.spec.ts`
      and others) — none assert the new-task form's field shape (they cover
      project-dropdown recovery only), so there was nothing to extend.

## 7. Docs + knowledge

- [x] 7.1 README Reference appendix: noted `sandbox_override` on the
      `POST /api/tasks` row, and added a paragraph on the per-task override
      next to the existing per-project override description — in place, no
      top-half edit.
- [x] 7.2 `context/knowledge/gotchas/sandbox.md`: added a bullet documenting
      the three-tier precedence (global → project → task) and that the task
      override is baked into `task.Sandboxed` at creation time exactly like
      the existing project-override case, never re-derived. Bumped the bullet
      count (27 → 28) in `context/knowledge/index.md`.

## 7a. Remote-mode gap found on self-review (not in the original plan)

- [x] A self-review pass caught that `--remote` mode's TUI new-task path
      (`App.createTaskTransactional` → `remoteTaskCreator.CreateTask` →
      `apistore.Store.CreateTask` → `apiclient.CreateTaskReq`) never carried
      `SandboxOverride`, even though the REST server-side endpoint (section 5)
      already accepted it — the TUI form would silently drop the operator's
      choice whenever running against a remote daemon. Fixed by adding
      `SandboxOverride` to `apiclient.CreateTaskReq`, widening
      `apistore.Store.CreateTask` and the `remoteTaskCreator` interface with a
      trailing `sandboxOverride string` parameter, and updating the call site
      in `internal/tui/app.go`. Unlike the pre-existing `base_branch`/
      attachment gaps (which are structural — the REST JSON path itself has no
      field for them), this one was purely an oversight since the REST field
      already existed; fixed rather than logged-and-accepted. Updated
      `context/knowledge/gotchas/remote-tui.md`'s new-task-creation bullet to
      note the contrast. Tests: `internal/apistore/store_test.go`
      (`TestStore_CreateTask` now asserts the request body carries
      `sandbox_override`), `internal/tui/create_remote_test.go` (the
      compile-time `remoteTaskCreator` canary plus
      `TestCreateTaskTransactional_RemoteRoutesThroughCreateTask` now asserts
      the override reaches the mock).

## 7b. Post-archive spec wording fix + a real `openspec validate` quirk found

- [x] Reworded the merged "Sandbox override selector" requirement in
      `openspec/specs/forms-and-modals/spec.md` from "on the new-task prompt"
      to "on the new-agent prompts" — the TUI form is shared by the new-task,
      new-worker, and new-coordinator prompts (per `SetHideArchetype`'s
      existing precedent for the Archetype selector), and the implementation
      never hides the Sandbox selector on any of them, so the original
      delta-authored wording undersold the actual scope. Also noted explicitly
      that Sandbox, unlike Archetype, is NOT hidden on the coordinator prompt.
- [x] While rewording, `openspec validate --specs --strict` started failing
      with `requirements.20.text: Requirement must contain SHALL or MUST
      keyword` despite SHALL being present three times in the paragraph.
      Bisected by reverting pieces one at a time: the actual cause is that
      `openspec`'s SHALL/MUST linter only scans the requirement paragraph's
      FIRST PHYSICAL LINE (not the full paragraph, not sentence-aware) — the
      longer parenthetical in the reworded opening sentence pushed the word
      "SHALL" past a hard-wrapped `\n` onto line 2, and the linter never saw
      it. Not a content problem; a source-formatting/tooling interaction. Fixed
      by reflowing the opening sentence so "SHALL" lands on the same physical
      line as the requirement's first words, matching the file's existing
      long-line style for other multi-clause requirements. Confirmed via
      `openspec validate --specs --strict`: 57/57 passing.

## 8. Gate + apply

- [x] 8.1 `make pre-pr` passes clean (build, vet, fmt-check, lint-pr,
      test-cover-gate at 88.7%). `make vuln` fails on pre-existing Go 1.26.1
      stdlib-only CVEs (fixed in 1.26.2, a toolchain bump unrelated to this
      change) — CI runs `vuln` with `continue-on-error: true` for exactly this
      class of finding per `context/knowledge/gotchas/ci-gates.md`.
- [x] 8.2 Archived this change in-PR via `openspec archive`: merged the delta
      specs into `openspec/specs/{sandbox-execution,forms-and-modals,
      mobile-pwa,data-persistence,rest-api}/spec.md` and moved this folder to
      `openspec/changes/archive/2026-08-29-add-task-sandbox-override/`.
