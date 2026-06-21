# Tasks: Hera-view needs-input summary box

**Design doc:** `openspec/changes/add-hera-attention-bar/design.md`

## 1. Tests

- [x] 1.1 Write failing `internal/tui/widget` tests for `AttentionSummary`: `DesiredHeight()==0` at count 0; fixed positive height at count ≥1; `Draw` renders `"1 task needs input"` at count 1 and `"N tasks need input"` at count >1 through the bordered panel (assert against a SimulationScreen or captured cells)
- [x] 1.2 Write failing `internal/tui/hera` unit tests for the unmanaged-count helper: counts a needs-input task absent from the model; excludes a coordinator, a managed worker under a folded orchestrator, and a freelance-role; returns 0 when all needs-input tasks are model-known
- [x] 1.3 Write a failing `internal/tui/hera` SimulationScreen smoke test: with an unmanaged needs-input task fed via `SetNeedsInput`, the box draws at the top of the rail column and the rail rect starts `barH` rows lower; with none, the rail occupies the full column; remote mode never draws the box
- [x] 1.4 Confirm every `it should X` acceptance criterion in `design.md` maps to a failing test (Prove-It Pattern)

## 2. AttentionSummary widget

**Depends on:** Stage 1

- [x] 2.1 Add `internal/tui/widget/attentionsummary.go`: `AttentionSummary` (embeds `*tview.Box`), `NewAttentionSummary()`, `SetCount(int)`, `DesiredHeight() int` (0 at count 0, else 3), `Draw` rendering the pluralised count via `DrawBorderedPanel` + `StyleInReview`/`StyleNeedsInput`
- [x] 2.2 Make Stage 1.1 pass; keep the widget free of any Hera/App dependency (pure, count-only)

## 3. Hera page integration

**Depends on:** Stage 2

- [x] 3.1 Add a managed-task-id walk (page or `model.go` helper) collecting `TaskID`+`BridgeTaskID` over Pinned/Active/Archived role sets and Freelance roles; add an unmanaged-count function `= |needsInput − managed|`
- [x] 3.2 Hold an `*widget.AttentionSummary` on `HeraPage`; in `Draw` compute the count, `SetCount`, derive `barH = DesiredHeight()` with the short-terminal clamp, draw the box at `(x,y,railW,barH)` and the rail at `(x,y+barH,railW,h-barH)` (full column when `barH==0`); ensure the remote path still short-circuits before the box
- [x] 3.3 Add `uxlog.Log("[hera-view] attention summary: ...")` on the count 0↔N show/hide transition only
- [x] 3.4 Make Stages 1.2 and 1.3 pass

## 4. Docs

**Depends on:** Stage 3

- [x] 4.1 Add the gotcha to `context/knowledge/gotchas/hera-view.md`: box drawn in `Draw` (no Sync), exclusion = model-known task ids (incl. folded + freelance), shrinks rail by fixed height, remote no-op
- [x] 4.2 Run `make fmt` and the targeted suites (`go test ./internal/tui/hera/... ./internal/tui/widget/...`); confirm green
