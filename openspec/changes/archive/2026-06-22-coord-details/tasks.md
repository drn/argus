# Tasks

## 1. Enrich the rail projection (additive, no behavior change to existing consumers)

- [ ] 1.1 Add `CreatedAt time.Time` to `OrchView`; populate from `o.CreatedAt` in `BuildModel`.
- [ ] 1.2 Add `CreatedAt`, `ArgusProject`, `WorktreePath`, `BindingStartedAt`, `StatusUpdatedAt`, `TaskName` to `RoleView`.
- [ ] 1.3 Switch `BuildModel`'s `roleToTask` map to carry the full live `*db.HeraBinding` so `buildRoleView` can read `WorktreePath` + `StartedAt`.
- [ ] 1.4 Populate the new `RoleView` fields in `buildRoleView` (role creation/project, live-binding worktree+start, role-status `UpdatedAt`, bound task name).

## 2. Restore the coordinator Details metadata (TDD)

- [ ] 2.1 Add a pure `deriveCoordMeta(*OrchView)` that computes Created, Last-activity (max), Agent name, Worktree, and distinct-sorted Repos — with a failing unit test first (Last-activity max + Repos distinct/sorted).
- [ ] 2.2 Cache the derived meta in `SetOrch`; render the metadata block in `Draw` between the coordinator status line and the Agents roster, and the `Summary:` placeholder after the roster.
- [ ] 2.3 Port `fmtDetailTime` (en-dash placeholder when zero) and `worktreeDisplay` (trailing `project/task` shortening) helpers.
- [ ] 2.4 Update `ContentHeight()` to match the new `Draw` row budget exactly, and update the `ContentHeight`/`ContentHeightMatchesDraw` tests to the new boundary line.

## 3. Validate

- [ ] 3.1 `make pre-pr` passes (build, vet, fmt-check, lint-pr, vuln, coverage gate).
- [ ] 3.2 Eyeball the rendered Details pane via `hera-view-probe`.
