## Why

The native Hera rail (`internal/tui/hera/rail.go`) is a flat, single-level orchestrator list. The plugin Hera rail (the gold standard, `hera/internal/view/rail_list.go`) nested sub-orchestrators inline under their bridging worker rows, folded the coordinator into the orchestrator header, animated a running spinner, and showed PR state and a per-coordinator archive expando directly on rail rows. Aaron observed the regressions live: a redundant `coord` child row under every header, a flat wall of rows where the plugin showed a handful of trees, static status glyphs, and PR/archive state buried in the Details pane.

`docs/NATIVE-VS-PLUGIN-PARITY-MATRIX.md` enumerates nine genuine rail regressions; `docs/RAIL-PARITY-ANALYSIS.md` traces the three independent gaps behind the flat rail; `docs/OLD-RAIL-SNAPSHOT.md` is the target layout (6 roots / 19 nested) reconstructed from the frozen plugin DB. This change closes the rail-side regressions against that oracle without re-litigating the plugin's design.

## What Changes

- **Bridging breadth** — broaden the multi-binding bridge (`db.SubtreeOrchIDs`, `hera.workerTaskSet`/`heraTreeNodes`) from LIVE-binding-only to the coordinator's LATEST binding regardless of liveness, excluding only the operator-teardown end-reasons `reparented`/`user_deleted`. This is the load-bearing prerequisite: an ended-but-not-torn-down bridge (e.g. a coordinator whose task completed) must still nest its child.
- **Fold the coordinator into the orchestrator header** — stop rendering the coordinator-kind role as a redundant child row; the orchestrator header row IS the coordinator and carries its status glyph.
- **Nest the rail** — render sub-orchestrators indented under their bridging worker row, recursively, consuming the corrected subtree. Cycle-safe (visited set) and archived-bridge-aware (dim, don't drop). The Details-pane orchestration tree (which resolves orchestrators by ID, not rail-row position) is untouched.
- **Per-coordinator `Archive (N)` expando** — render archived roles under each coordinator's agents in a collapsed-by-default expando, in addition to the bottom Archive section for archived orchestrators.
- **Spinner** — animate the status glyph for a running (working) agent via the shared `widget.SpinnerFrame`, instead of a static glyph.
- **PR indicator on rail rows** — render a `PR` cell on managed role rows from the cached `task_meta` "pr" namespace, not only in the Details roster.

## Capabilities

### Modified Capabilities

- `hera-view`: the rail structure becomes a nested orchestrator tree (was flat); the coordinator folds into the orchestrator header (was a child row); status icons animate for running agents (were static); rail role rows carry a PR indicator and a per-coordinator archive expando (were Details-only / bottom-only). The multi-binding bridge keys off the latest binding with a teardown guard (was live-only).

## Impact

- **Modified code:** `internal/db/hera.go` (latest-binding query + teardown-reason constants), `internal/db/hera_subtree.go` (`SubtreeOrchIDs` SQL), `internal/tui/hera/reader.go` (reader seam), `internal/tui/hera/model.go` (`RoleView` bridge fields, `OrchView.CoordBridgeTaskID`), `internal/tui/hera/tree.go` (`workerTaskSet`/`heraTreeNodes` bridge), `internal/tui/hera/rail.go` (nesting, fold-coord, spinner, PR cell), `internal/tui/hera/page.go`/`panes.go` (thread `prMeta` into the rail).
- **No schema change** — the `hera_bindings.end_reason` column already exists; the new constants are values, not columns.
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.
