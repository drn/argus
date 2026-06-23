## 1. Model + persistence

- [x] 1.1 Add `Model string` `json:"model,omitempty"` to `model.ScheduledTask`
      (`internal/model/schedule.go`), documented as optional per-schedule override.
- [x] 1.2 `internal/db/schema.go`: add `model TEXT NOT NULL DEFAULT ''` to the
      `scheduled_tasks` CREATE TABLE and an idempotent
      `ALTER TABLE scheduled_tasks ADD COLUMN model TEXT NOT NULL DEFAULT ''`
      (mirroring the `run_once_at` migration just below it).
- [x] 1.3 `internal/db/schedules.go`: add `model` to the SELECT column lists
      (Schedules + GetSchedule), the INSERT and UPDATE statements, and `scanSchedule`
      (preserve column order across all five sites).
- [x] 1.4 Tests (`internal/db/schedules_test.go`): round-trip a schedule with a
      non-empty Model through Add → Get → Update → Get; assert default `''` on a
      row inserted without one.

## 2. Fire path threads the model

- [x] 2.1 `internal/scheduler/scheduler.go`: extend `TaskCreator` to
      `func(name, prompt, project, backend, taskModel string) (*model.Task, error)`;
      `fire()` passes `sched.Model`; update the type comment at line ~27 (drop
      "schedules carry no per-task model override").
- [x] 2.2 `internal/daemon/daemon.go` (~818): the creator closure accepts the new
      `taskModel` param and forwards it via `HeadlessInput{… Model: taskModel}`.
- [x] 2.3 Tests (`internal/scheduler/scheduler_test.go`): the fake creator records
      the model arg; assert a schedule with Model fires a task carrying it, and an
      empty Model fires with empty model (both tick and RunNow paths).

## 3. REST + apiclient + apistore

- [x] 3.1 `internal/api/schedules.go`: add `Model` to `scheduleJSON` (omitempty) and
      `toScheduleJSON`; add `Model *string` to `scheduleRequest`; `applyScheduleRequest`
      sets `sched.Model = strings.TrimSpace(*req.Model)` when non-nil.
- [x] 3.2 `internal/apiclient/schedules.go`: add `Model` to `ScheduleJSON` and
      `ScheduleReq`.
- [x] 3.3 `internal/apistore/convert.go`: `scheduleReqFromModel` sets `Model`;
      `internal/apistore/store.go` `Schedules()` reads `w.Model` (the `/raw`
      GetSchedule path already round-trips via the model struct — verify, no change).
- [x] 3.4 Tests: `internal/api/schedules_test.go` partial-update of only `model`
      (other fields preserved) + create echoes it; `apistore` convert/round-trip test.

## 4. MCP tools

- [x] 4.1 `internal/mcp/server.go`: add a `model` string property to the
      `schedule_create` and `schedule_update` input schemas (description matching the
      task `model` arg at line ~573); parse it in `toolScheduleCreate`
      (set `sched.Model`) and `toolScheduleUpdate` (pointer → set/clear, like Backend).
- [x] 4.2 Tests (`internal/mcp/*_test.go`): schedule_create with `model` persists it;
      schedule_update sets and clears it.

## 5. TUI schedule form

- [x] 5.1 `internal/tui/scheduleform.go`: add a Model field row mirroring the
      new-task form's per-backend selector (default / `agent.BackendModels` / custom…),
      rebuilt when the backend selection changes. Bump `sfFieldCount`, add the field
      index, label ("Model:"), selector draw, nav, and `Result()`/`LoadSchedule()`
      wiring. The form already takes `backends`; pass enough config to resolve models
      (mirror how NewScheduleForm is constructed — extend its inputs if needed).
- [x] 5.2 Tests (`internal/tui/scheduleform_test.go`): Result carries the selected
      model; LoadSchedule selects the stored model; custom entry round-trips; changing
      backend rebuilds options and resets to default.
- [x] 5.3 If the form construction signature changes, update its call site(s) in
      `internal/tui/settings.go` (and any test helpers).

## 6. Web SPA

- [x] 6.1 `internal/api/static/index.html`: add a Model field to the schedule editor
      modal (`sm-model` select + optional custom input), populated from the selected
      backend's models like the new-task `create-model` field; include `model` in the
      `saveScheduleFromModal` body; show the model in the schedule list line when set;
      load the stored value in `openScheduleEditor`.
- [x] 6.2 Bump `SW_VERSION` in `internal/api/static/sw.js`.
- [x] 6.3 If the Playwright harness (`cmd/argus-test-server`) or web tests assert the
      schedule modal shape, extend them.

## 7. Docs + knowledge

- [x] 7.1 README Reference appendix: note `model` on the schedule fields / the
      `schedule_create`/`schedule_update` MCP tool params (update the relevant table
      in place; no top-half edit).
- [x] 7.2 `context/knowledge/gotchas/misc.md` (scheduled tasks bullet): add that a
      schedule carries an optional per-schedule model override threaded to the fired
      task via `TaskCreator`'s `taskModel` arg → `HeadlessInput.Model`; empty = backend
      default. Bump the misc.md bullet count in `context/knowledge/index.md`.

## 8. Gate + apply + operational change

- [x] 8.1 `make pre-pr` passes clean.
- [x] 8.2 Archive this change in-PR: merge the delta into
      `openspec/specs/scheduling/spec.md` and move this folder to
      `openspec/changes/archive/<date>-add-schedule-model-override/`.
- [ ] 8.3 (post-merge, after the daemon restarts on the new binary)
      `PUT /api/schedules/1781743040260282000` with `{"model":"sonnet"}` to set the
      Gryffin Jersey Watch schedule to sonnet; verify via a list read. Deferred
      because the running daemon does not know the new field until it restarts.
