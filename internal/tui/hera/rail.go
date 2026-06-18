package hera

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RailStateStore persists the rail's UI state (fold/collapse + selection) across
// argus / daemon restarts. *db.DB satisfies it implicitly (LoadRailState /
// SaveRailState over the config table); remote mode passes nil and persistence
// is simply off. See BUG-002 / openspec/changes/rail-state-persist.
type RailStateStore interface {
	LoadRailState() (string, error)
	SaveRailState(state string) error
}

// railViewState is the JSON shape persisted under the store's single key. Only
// NON-default fold entries are listed (orchestrators default expanded, per-coord
// Archive expandos default closed), so an absent/empty blob restores all
// defaults. Filter state is transient and deliberately absent.
type railViewState struct {
	Collapsed        []int64 `json:"collapsed"`          // orchestrator ids currently collapsed
	CoordArchiveOpen []int64 `json:"coord_archive_open"` // orch ids whose per-coord Archive expando is open
	FreelanceCollap  bool    `json:"freelance_collapsed"`
	ArchiveCollapsed bool    `json:"archive_collapsed"`
	SelectionRef     int64   `json:"selection_ref"` // currentRef() identity: role id, or -orch id for a header
}

// railRowKind enumerates the flattened display-row types. uint8 keeps it small;
// there are only a handful of values.
type railRowKind uint8

const (
	rrRule           railRowKind = iota // non-selectable separator
	rrSectionHeader                     // "Pinned" / "Freelance" label
	rrOrch                              // orchestrator header (selectable, collapsible)
	rrRole                              // role under an orchestrator (selectable)
	rrFreelanceRole                     // freelance-kind role (selectable)
	rrArchiveExpando                    // "Archive (N)" fold (selectable, collapsible)
	rrEmpty                             // empty-state placeholder (non-selectable)
)

// railRow is one flattened display line. orch/role point into the Model (never
// copied) so selection can hand the live projection back to 6b.
type railRow struct {
	kind  railRowKind
	label string
	orch  *OrchView
	role  *RoleView
	depth int
	dim   bool // archived placement → dimmed style

	// Collapse target (only one is set, and only when collapsible).
	collOrchID    int64 // >0 → toggle collapsed[collOrchID]
	collFreelance bool
	collArchive   bool  // bottom Archive section (archived ROOT orchestrators)
	archiveOwner  int64 // >0 → per-coordinator Archive expando for this orch's archived roles
}

func (r railRow) selectable() bool {
	switch r.kind {
	case rrOrch, rrRole, rrFreelanceRole, rrArchiveExpando:
		return true // both the bottom Archive section and per-coordinator expandos
	case rrSectionHeader:
		// The Freelance fold header is selectable so the cursor can land on it
		// to collapse/expand; the plain "Pinned" label is not.
		return r.collFreelance
	default:
		return false
	}
}

// heraIconCoord is the coordinator/orchestrator marker (Nerd Font md, same
// codepoint Hera's rail used for iconCoord).
const heraIconCoord = rune(0x0F0E7B) // 󰹻

// Rail is the left navigation widget of the Hera view. It renders the Model
// read-only: sections, cursor, and per-orchestrator collapse. No mutation
// verbs live here (6c).
type Rail struct {
	*tview.Box

	model  Model
	rows   []railRow
	cursor int
	offset int

	// Collapse state survives rebuilds. Orchestrators default expanded; the
	// Freelance section defaults expanded; the Archive section defaults
	// collapsed.
	collapsed        map[int64]bool
	freelanceCollap  bool
	archiveCollapsed bool
	// coordArchiveOpen tracks per-coordinator Archive expandos (keyed by orch id).
	// Absent/false → collapsed (the default), matching the bottom Archive section.
	coordArchiveOpen map[int64]bool

	// Filter state (the `/` rail name filter). filterInput is true while the
	// operator is TYPING (drives the `/ <query>` line, the key routing, and the
	// global-handler guard); filterQuery is the active query — non-empty NARROWS
	// the rail (ancestry-preserving) and survives leaving input mode (Enter
	// accepts). Filter state is transient and is NOT persisted. filterVis is the
	// per-build visibility memo (orch id → visible), recomputed each filtered
	// buildRows and nil when no filter is active.
	filterInput bool
	filterQuery string
	filterVis   map[int64]bool

	focused   bool // drives the border-highlight style
	animFrame int  // spinner frame for in-motion role glyphs (recomputed each Draw)

	// prMeta is the daemon-populated "pr" namespace cache (taskID -> {state,url}),
	// the same best-effort source the Details roster reads. A managed role whose
	// bound task has a non-empty "url" renders a PR indicator. nil → no PR cells.
	prMeta map[string]map[string]string

	// onSelectionChanged fires (with the selected row's ref) whenever the
	// cursor lands on a different selectable row. 6b binds the panes off it;
	// 6a wires it only for the focus-guard smoke test. May be nil.
	onSelectionChanged func()

	// store persists fold/selection state across restarts (BUG-002). nil in
	// remote mode (and until SetStateStore wires it) → persistence off.
	// pendingSelRef holds a persisted selection ref to apply on the FIRST model
	// build (rows don't exist at load time); it is consumed once, after which the
	// live cursor wins.
	store         RailStateStore
	pendingSelRef int64
	// firstRunCollapse is true when no saved rail state exists (first run). On
	// the first non-empty model build, ALL orchestrators are seeded as collapsed
	// so the rail starts fully collapsed instead of fully expanded. One-shot:
	// cleared after the seed, just like pendingSelRef.
	firstRunCollapse bool
}

// NewRail builds an empty rail. Archive starts collapsed (matches the task
// list's archive default).
func NewRail() *Rail {
	return &Rail{
		Box:              tview.NewBox(),
		collapsed:        make(map[int64]bool),
		archiveCollapsed: true,
		coordArchiveOpen: make(map[int64]bool),
	}
}

// SetOnSelectionChanged registers the selection callback.
func (r *Rail) SetOnSelectionChanged(fn func()) { r.onSelectionChanged = fn }

// SetFocused records whether the rail holds keyboard focus, switching its
// border between the focused and unfocused palette.
func (r *Rail) SetFocused(v bool) { r.focused = v }

// SetPRMeta wires the best-effort "pr" namespace cache so managed rail rows can
// render a PR indicator. Pass nil to clear it (the indicator just won't render).
func (r *Rail) SetPRMeta(m map[string]map[string]string) { r.prMeta = m }

// rolePR reports whether the role's bound task has a non-empty "pr" url in the
// cache (an open pull request worth flagging on the rail row).
func (r *Rail) rolePR(role *RoleView) bool {
	if role == nil || role.TaskID == "" || r.prMeta == nil {
		return false
	}
	kv := r.prMeta[role.TaskID]
	return kv != nil && kv["url"] != ""
}

// SetModel replaces the snapshot and rebuilds rows, preserving the cursor's
// selectable target where possible.
func (r *Rail) SetModel(m Model) {
	prev := r.currentRef()
	r.model = m
	r.buildRows()
	// A persisted selection (BUG-002) takes precedence on the FIRST build after a
	// restore — the rows didn't exist when SetStateStore ran. It is one-shot:
	// once applied here it's zeroed, so later rebuilds keep the live cursor. The
	// cursor is written directly (not via setCursor), so the restore never
	// re-persists.
	if r.pendingSelRef != 0 {
		prev = r.pendingSelRef
		r.pendingSelRef = 0
	}
	r.restoreCursor(prev)
	r.clampCursor()
}

// Model returns the current snapshot (read-only; for tests/inspection).
func (r *Rail) Model() Model { return r.model }

// --- persistence (BUG-002) ---

// SetStateStore wires the persistence seam and immediately restores the saved
// state: the fold maps and section booleans (keyed by stable DB ids, valid
// before any model loads) are applied now; the selection ref is stashed and
// applied on the first model build (rows don't exist yet). A nil store, a read
// error, or a malformed/empty blob all leave the rail at its defaults (logged,
// never fatal) — matching the no-legacy-migration policy. Call once at
// construction, before the first Refresh.
func (r *Rail) SetStateStore(s RailStateStore) {
	r.store = s
	if s == nil {
		return
	}
	raw, err := s.LoadRailState()
	if err != nil {
		uxlog.Log("[hera-view] rail state load failed: %v", err)
		return
	}
	if strings.TrimSpace(raw) == "" {
		// Nothing persisted yet (first run) — start fully collapsed on the
		// first model build instead of defaulting to fully expanded.
		r.firstRunCollapse = true
		return
	}
	var st railViewState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		uxlog.Log("[hera-view] rail state parse failed (keeping defaults): %v", err)
		return
	}
	for _, id := range st.Collapsed {
		r.collapsed[id] = true
	}
	for _, id := range st.CoordArchiveOpen {
		r.coordArchiveOpen[id] = true
	}
	r.freelanceCollap = st.FreelanceCollap
	r.archiveCollapsed = st.ArchiveCollapsed
	r.pendingSelRef = st.SelectionRef
}

// persist serializes the live fold maps + current selection and writes them
// through the store. nil store → no-op; a write error is logged, never fatal.
// The rail hands the store a fully-serialized immutable string, so a store impl
// is free to write it asynchronously without racing the UI thread (shipped
// synchronously here — a local SQLite upsert is sub-millisecond).
func (r *Rail) persist() {
	if r.store == nil {
		return
	}
	st := railViewState{
		Collapsed:        trueKeys(r.collapsed),
		CoordArchiveOpen: trueKeys(r.coordArchiveOpen),
		FreelanceCollap:  r.freelanceCollap,
		ArchiveCollapsed: r.archiveCollapsed,
		SelectionRef:     r.currentRef(),
	}
	b, err := json.Marshal(st)
	if err != nil {
		uxlog.Log("[hera-view] rail state marshal failed: %v", err)
		return
	}
	if err := r.store.SaveRailState(string(b)); err != nil {
		uxlog.Log("[hera-view] rail state save failed: %v", err)
	}
}

// trueKeys returns the sorted ids whose value is true (the non-default fold
// entries). Sorted so the serialized blob is stable across saves (no spurious
// rewrites from Go's randomized map iteration).
func trueKeys(m map[int64]bool) []int64 {
	var out []int64
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- filter ---

// Filtering reports whether the rail is in search INPUT mode (the operator is
// typing a query). page.go skips rail mutations while this holds, and app.go
// lets the global rune shortcuts (1/2/3/q/?) fall through to the rail as filter
// input. It is FALSE once a filter is accepted (Enter) even though the query
// stays applied — accepted-but-not-typing resumes normal key handling.
func (r *Rail) Filtering() bool { return r.filterInput }

// filterActive reports whether the rail rows are NARROWED (a non-empty query is
// applied), regardless of input mode. An empty/all-whitespace query narrows
// nothing.
func (r *Rail) filterActive() bool { return strings.TrimSpace(r.filterQuery) != "" }

// filterMatches reports whether name satisfies the current query: every
// whitespace-separated term must be a case-insensitive substring. An empty
// query matches everything (so narrowing only begins once a real term is typed).
func (r *Rail) filterMatches(name string) bool {
	terms := strings.Fields(strings.ToLower(r.filterQuery))
	if len(terms) == 0 {
		return true
	}
	ln := strings.ToLower(name)
	for _, t := range terms {
		if !strings.Contains(ln, t) {
			return false
		}
	}
	return true
}

// computeVisible builds the orchestrator-visibility memo for the active filter:
// an orchestrator is visible when its own name matches, OR any of its roles
// matches, OR any sub-orchestrator it bridges (recursively) is visible —
// ancestry-preserving so a matching nested agent always keeps its parents.
// Cycle-safe via an in-progress set (a cycle edge contributes nothing rather
// than recursing forever), mirroring BridgeSubtree's visited guard.
func (r *Rail) computeVisible(bridge map[string]*OrchView) map[int64]bool {
	vis := make(map[int64]bool)
	inProgress := make(map[int64]bool)
	var visit func(o *OrchView) bool
	visit = func(o *OrchView) bool {
		if v, ok := vis[o.ID]; ok {
			return v
		}
		if inProgress[o.ID] {
			return false // cycle edge: break recursion, contribute nothing
		}
		inProgress[o.ID] = true
		v := r.filterMatches(o.Name)
		for i := range o.Roles {
			if r.filterMatches(o.Roles[i].Name) {
				v = true
			}
		}
		for i := range o.Roles {
			w := &o.Roles[i]
			if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
				continue
			}
			if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != o.ID {
				if visit(c) {
					v = true
				}
			}
		}
		inProgress[o.ID] = false
		vis[o.ID] = v
		return v
	}
	for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			visit(&sec[i])
		}
	}
	return vis
}

// isCollapsed / isCoordArchiveOpen apply the auto-expand rule: while a filter is
// active every node renders EXPANDED so matching rows are never hidden behind a
// fold. The persisted fold maps are read (not mutated), so clearing the filter
// restores the operator's folds untouched.
func (r *Rail) isCollapsed(id int64) bool {
	if r.filterActive() {
		return false
	}
	return r.collapsed[id]
}

func (r *Rail) isCoordArchiveOpen(id int64) bool {
	if r.filterActive() {
		return true
	}
	return r.coordArchiveOpen[id]
}

// orchVisible reports whether the orchestrator should render under the active
// filter (always true when no filter is active).
func (r *Rail) orchVisible(id int64) bool {
	return !r.filterActive() || r.filterVis[id]
}

// anyOrchVisible reports whether any orchestrator in the section is visible
// under the active filter (used to prune an empty section header). True for any
// non-empty section when no filter is active.
func (r *Rail) anyOrchVisible(secs []OrchView) bool {
	if !r.filterActive() {
		return len(secs) > 0
	}
	for i := range secs {
		if r.filterVis[secs[i].ID] {
			return true
		}
	}
	return false
}

// anyFreelanceVisible reports whether any freelance role matches the active
// filter (prunes the Freelance section header). True for any non-empty set when
// no filter is active.
func (r *Rail) anyFreelanceVisible() bool {
	if !r.filterActive() {
		return len(r.model.Freelance) > 0
	}
	for i := range r.model.Freelance {
		if r.filterMatches(r.model.Freelance[i].Name) {
			return true
		}
	}
	return false
}

// workerRowVisible reports whether a worker row should render under the active
// filter: its own name matches, OR it bridges a visible sub-orchestrator (so the
// bridge parent is kept for a nested match). Always true when no filter active.
// The bridge child is resolved through the canonical parent (the same primitive
// placement uses) so visibility and nesting agree.
func (r *Rail) workerRowVisible(ownerID int64, w *RoleView, canonical map[int64]canonParent, placed map[int64]bool) bool {
	if !r.filterActive() {
		return true
	}
	if r.filterMatches(w.Name) {
		return true
	}
	if c := r.workerBridgeChild(ownerID, w, canonical, placed); c != nil && r.filterVis[c.ID] {
		return true
	}
	return false
}

// currentRef returns a stable identity for the row under the cursor so the
// cursor can be re-pinned after a rebuild. RoleID for roles, OrchID (negated
// to avoid colliding with role ids) for orch headers, 0 otherwise.
func (r *Rail) currentRef() int64 {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return 0
	}
	row := r.rows[r.cursor]
	switch {
	case row.role != nil:
		return row.role.RoleID
	case row.orch != nil:
		return -row.orch.ID
	default:
		return 0
	}
}

func (r *Rail) restoreCursor(ref int64) {
	if ref == 0 {
		return
	}
	for i, row := range r.rows {
		switch {
		case row.role != nil && row.role.RoleID == ref:
			r.cursor = i
			return
		case row.orch != nil && -row.orch.ID == ref:
			r.cursor = i
			return
		}
	}
}

// --- row building ---

func (r *Rail) buildRows() {
	r.rows = nil

	if r.model.IsEmpty() {
		r.rows = append(r.rows, railRow{kind: rrEmpty, label: "No hera orchestrators"})
		return
	}

	// On first run (no saved state) collapse ALL orchestrators so the rail
	// opens in a tidy summary view. One-shot: cleared after the seed.
	if r.firstRunCollapse {
		for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
			for _, o := range sec {
				r.collapsed[o.ID] = true
			}
		}
		r.firstRunCollapse = false
	}

	// Nesting machinery: each child orchestrator's SINGLE canonical parent is
	// precomputed once (`canonical`) so placement is deterministic and
	// FOLD-INDEPENDENT — a child with two valid bridge-parents (coordinator-spawn
	// and worker-bridge) renders under exactly one of them regardless of which is
	// collapsed (the multi-parent fold-migration quirk). `consumed` = has a
	// canonical parent, so it nests rather than rendering as a top-level root;
	// `placed` guards single-placement + cycles across the whole build (an
	// orchestrator is rendered at most once, breaking any bridge cycle).
	canonical := r.model.canonicalParents()
	consumed := make(map[int64]bool, len(canonical))
	for id := range canonical {
		consumed[id] = true
	}
	placed := make(map[int64]bool)
	// Structural reachability under FULL expansion (collapse/archive fold
	// IGNORED): an orchestrator whose canonical-parent chain reaches a true root is
	// not a genuine top-level root — it is merely hidden when an ancestor is
	// folded. The safety sweep below consults this so collapse/archive-HIDDEN
	// children stay folded instead of leaking to the top; only true cycle-orphans
	// (a chain that loops without ever reaching a root) get rescued.
	structReach := r.structuralReach(canonical)

	// Filter visibility memo (ancestry-preserving). nil when no filter is active,
	// so every non-filtered path below short-circuits to today's behaviour. The
	// raw bridge index (every bridge, not just canonical parents) drives the memo
	// so a match anywhere in a multi-bridge subtree keeps its ancestors visible.
	if r.filterActive() {
		r.filterVis = r.computeVisible(r.model.bridgeIndex())
	} else {
		r.filterVis = nil
	}

	// 1. Pinned section. Pinned orchestrators are always top-level roots
	// (user intent), even if some worker bridges them. The header is pruned when
	// no pinned orchestrator is visible under the active filter.
	if r.anyOrchVisible(r.model.Pinned) {
		r.rows = append(r.rows, railRow{kind: rrSectionHeader, label: "Pinned"})
		for i := range r.model.Pinned {
			r.appendOrch(&r.model.Pinned[i], 0, r.model.Pinned[i].Archived, canonical, placed)
		}
	}

	// 2. Active orchestrators (no section header). Render roots (no canonical
	// parent) first; then a safety sweep rescues only TRUE cycle-orphans — an
	// orchestrator left unplaced AND whose canonical chain reaches no root. A child
	// that is merely hidden because an ancestor is collapsed/archived is
	// structurally reachable, so it stays folded instead of leaking to the top.
	for i := range r.model.Active {
		if !consumed[r.model.Active[i].ID] {
			r.appendOrch(&r.model.Active[i], 0, false, canonical, placed)
		}
	}
	for i := range r.model.Active {
		id := r.model.Active[i].ID
		if !placed[id] && !structReach[id] {
			r.appendOrch(&r.model.Active[i], 0, false, canonical, placed)
		}
	}

	// 3. Freelance section. The header + separator are pruned when no freelance
	// role matches the active filter; the section auto-expands while filtering.
	if r.anyFreelanceVisible() {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:          rrSectionHeader,
			label:         fmt.Sprintf("Freelance (%d)", len(r.model.Freelance)),
			collFreelance: true,
		})
		if !r.freelanceCollap || r.filterActive() {
			for i := range r.model.Freelance {
				if r.filterActive() && !r.filterMatches(r.model.Freelance[i].Name) {
					continue
				}
				r.rows = append(r.rows, railRow{kind: rrFreelanceRole, role: &r.model.Freelance[i], depth: 1})
			}
		}
	}

	// 4. Archive section (collapsed by default): archived orchestrators not
	// already nested under a live parent (placed). A consumed-but-unplaced
	// archived orphan (pure cycle) still surfaces here. Under an active filter
	// only visible archived roots are listed, and the section auto-expands.
	var archivedRoots []*OrchView
	for i := range r.model.Archived {
		if placed[r.model.Archived[i].ID] {
			continue
		}
		if !r.orchVisible(r.model.Archived[i].ID) {
			continue
		}
		archivedRoots = append(archivedRoots, &r.model.Archived[i])
	}
	if len(archivedRoots) > 0 {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:        rrArchiveExpando,
			label:       fmt.Sprintf("Archive (%d)", len(archivedRoots)),
			collArchive: true,
		})
		if !r.archiveCollapsed || r.filterActive() {
			for _, o := range archivedRoots {
				r.appendOrch(o, 1, true, canonical, placed)
			}
		}
	}
}

// structuralReach returns every orchestrator whose CANONICAL-parent chain reaches
// a true root (an orchestrator with no canonical parent) under FULL expansion —
// collapse/archive fold IGNORED. Because canonical placement is exactly what the
// render nests (both the worker-bridge and coordinator-spawn shapes flow through
// canonicalParents), this can never drift from what is actually drawn. The render
// passes respect fold; structuralReach deliberately does not, so a child merely
// hidden behind a collapsed or archived ancestor is still "reachable" here and is
// therefore NOT re-leaked to the top by the safety sweep — only a true
// cycle-orphan (a chain that loops without ever reaching a root) is.
func (r *Rail) structuralReach(canonical map[int64]canonParent) map[int64]bool {
	var resolves func(id int64, seen map[int64]bool) bool
	resolves = func(id int64, seen map[int64]bool) bool {
		cp, ok := canonical[id]
		if !ok {
			return true // no canonical parent → a top-level root → reachable
		}
		if seen[id] {
			return false // chain cycles without reaching a root
		}
		seen[id] = true
		return resolves(cp.orchID, seen)
	}
	reach := make(map[int64]bool)
	for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			if resolves(sec[i].ID, map[int64]bool{}) {
				reach[sec[i].ID] = true
			}
		}
	}
	return reach
}

// appendOrch emits an orchestrator HEADER (the folded coordinator) and, when
// expanded, its non-coordinator roles nested via appendOrchWorkers. The header
// is rendered once per build (placed guard). dim propagates an archived
// placement down the whole subtree.
func (r *Rail) appendOrch(o *OrchView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool) {
	if placed[o.ID] {
		return
	}
	// Filter: an orchestrator with no match anywhere in its subtree is not placed
	// (left unplaced so the bottom-Archive / safety passes also skip it).
	if !r.orchVisible(o.ID) {
		return
	}
	placed[o.ID] = true
	r.rows = append(r.rows, railRow{
		kind:       rrOrch,
		orch:       o,
		depth:      depth,
		dim:        dim,
		collOrchID: o.ID,
	})
	if r.isCollapsed(o.ID) {
		return
	}
	r.appendOrchWorkers(o, depth+1, dim, canonical, placed)
}

// appendOrchWorkers emits o's non-coordinator role rows at `depth`. A worker
// that bridges a not-yet-placed child orchestrator nests that child's workers
// one level deeper, immediately under the worker row (the worker row IS the
// child's coordinator — same multi-binding task — so no separate child header is
// drawn). The bridging row carries collOrchID = child.ID so Space folds the
// nested subtree, and a chevron marks it foldable. The placed guard breaks any
// bridge cycle and guarantees single placement.
//
// The bridging row keeps its PARENT worker selection context (a worker role
// under o): nesting is purely visual, so mutations (notably Ctrl+D) act on the
// worker role, never the child orchestrator — conservative multi-binding safety.
func (r *Rail) appendOrchWorkers(o *OrchView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool) {
	var archived []*RoleView
	for i := range o.Roles {
		w := &o.Roles[i]
		if w.Kind == db.HeraKindCoordinator {
			continue // folded into the header / the bridging row above
		}
		// An archived worker that BRIDGES a not-yet-placed child is a structural
		// sub-coordinator, not a finished leaf: it renders in place (dimmed) so its
		// child sub-team still nests. Hoisting it into the collapsed Archive expando
		// would consume the child (consumedSet) without ever placing it, leaving the
		// child to be safety-swept flat to the top level — the archived-worker
		// under-nesting bug. Only archived LEAF workers (no live child to bridge)
		// fold into the expando. db.SubtreeOrchIDs nests the child regardless of the
		// parent-side role's archived state, so this mirrors it.
		if w.Archived && r.workerBridgeChild(o.ID, w, canonical, placed) == nil {
			archived = append(archived, w)
			continue
		}
		if !r.workerRowVisible(o.ID, w, canonical, placed) {
			continue // filtered out (no name match, bridges no visible child)
		}
		r.appendWorkerRow(o.ID, w, depth, dim, canonical, placed)
	}

	// Coordinator-spawned sub-teams: a child orchestrator whose coordinator is the
	// SAME agent as o's (the multi-orch coordinator hera_new_orchestrator creates)
	// has no worker row to nest under — the parent's coordinator IS the bridge. It
	// nests as its own sub-orchestrator header directly under o, recursively, at
	// the worker depth, ONLY when o is its canonical coordinator-spawn parent. The
	// canonical guard (plus the placed guard) breaks the shared-task symmetry so a
	// multi-parent child renders under exactly one parent regardless of fold.
	for _, child := range r.model.coordBridgeChildren(o) {
		if placed[child.ID] {
			continue
		}
		if cp, ok := canonical[child.ID]; !ok || !cp.coordSpawn || cp.orchID != o.ID {
			continue
		}
		r.appendOrch(child, depth, dim, canonical, placed)
	}

	// Per-coordinator Archive (N) expando: archived roles fold under their
	// coordinator's active agents, collapsed by default. Distinct from the bottom
	// Archive section (archived ROOT orchestrators). Archived roles render dimmed
	// and still nest any sub-team they bridge (forced-dim down the subtree). Under
	// an active filter only visible archived roles list (the expando is pruned
	// when none match), and the expando auto-expands.
	visibleArchived := archived
	if r.filterActive() {
		visibleArchived = visibleArchived[:0:0]
		for _, w := range archived {
			if r.workerRowVisible(o.ID, w, canonical, placed) {
				visibleArchived = append(visibleArchived, w)
			}
		}
	}
	if len(visibleArchived) > 0 {
		r.rows = append(r.rows, railRow{
			kind:         rrArchiveExpando,
			archiveOwner: o.ID,
			depth:        depth,
			dim:          dim,
			label:        fmt.Sprintf("Archive (%d)", len(archived)),
		})
		if r.isCoordArchiveOpen(o.ID) {
			for _, w := range visibleArchived {
				r.appendWorkerRow(o.ID, w, depth+1, true, canonical, placed)
			}
		}
	}
}

// workerBridgeChild returns the not-yet-placed child orchestrator that nests under
// worker w — the orchestrator whose CANONICAL parent is ownerID via the
// worker-bridge shape AND whose coordinator bridge task matches w's bridge task —
// or nil. Resolving through the precomputed canonical parent (NOT a first-wins
// bridge-index lookup) is what makes a multi-parent child render under one
// deterministic parent regardless of fold/order: when the same coordinator task
// coordinates two orchestrators, the bridge index would pick whichever appears
// first, but only the canonical assignment is honoured here. Coordinator-kind,
// torn-down, and already-placed links do not bridge. Shared by appendOrchWorkers
// (hoist-vs-nest decision) and appendWorkerRow (the nest itself) so both agree.
func (r *Rail) workerBridgeChild(ownerID int64, w *RoleView, canonical map[int64]canonParent, placed map[int64]bool) *OrchView {
	if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
		return nil
	}
	ck := bridgeTaskID(w)
	if ck == "" {
		return nil
	}
	for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			c := &sec[i]
			cp, ok := canonical[c.ID]
			if !ok || cp.coordSpawn || cp.orchID != ownerID || placed[c.ID] {
				continue
			}
			if c.CoordBridgeTaskID() == ck {
				return c
			}
		}
	}
	return nil
}

// appendWorkerRow emits one worker role row at `depth` and, when it bridges a
// not-yet-placed child orchestrator, nests the child's workers one level deeper.
// The worker ROW dims when the worker itself is archived (an honest per-node
// signal); the child subtree dims only from inherited dim or the CHILD's own
// archived state — an active child under an archived bridging worker stays
// normal (it is live work, not archived placement).
func (r *Rail) appendWorkerRow(ownerID int64, w *RoleView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool) {
	rowDim := dim || w.Archived
	child := r.workerBridgeChild(ownerID, w, canonical, placed)
	// Under an active filter, only bridge a VISIBLE child: a non-matching subtree
	// must not surface (and the bridging row drops its chevron).
	if child != nil && r.filterActive() && !r.filterVis[child.ID] {
		child = nil
	}
	collID := int64(0)
	if child != nil {
		collID = child.ID
	}
	r.rows = append(r.rows, railRow{kind: rrRole, role: w, depth: depth, dim: rowDim, collOrchID: collID})
	if child != nil {
		placed[child.ID] = true
		if !r.isCollapsed(child.ID) {
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed)
		}
	}
}

// --- navigation ---

// CursorDown moves to the next selectable row.
func (r *Rail) CursorDown() { r.step(1) }

// CursorUp moves to the previous selectable row.
func (r *Rail) CursorUp() { r.step(-1) }

func (r *Rail) step(dir int) {
	if len(r.rows) == 0 {
		return
	}
	i := r.cursor
	for {
		i += dir
		if i < 0 || i >= len(r.rows) {
			return // no further selectable row; leave cursor put
		}
		if r.rows[i].selectable() {
			r.setCursor(i)
			return
		}
	}
}

func (r *Rail) setCursor(i int) {
	if i == r.cursor {
		return
	}
	r.cursor = i
	if r.onSelectionChanged != nil {
		r.onSelectionChanged()
	}
	// Persist the new selection (BUG-002). setCursor is the single funnel for
	// user-driven cursor moves (via step); the load-restore and filter rebuilds
	// write r.cursor directly (not through here), so neither persists.
	r.persist()
}

func (r *Rail) clampCursor() {
	if len(r.rows) == 0 {
		r.cursor = 0
		return
	}
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.cursor >= len(r.rows) {
		r.cursor = len(r.rows) - 1
	}
	// Land on a selectable row if the clamp parked on a rule/header.
	if !r.rows[r.cursor].selectable() {
		for i := r.cursor; i < len(r.rows); i++ {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
		for i := r.cursor; i >= 0; i-- {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
	}
}

// ToggleCollapse flips the fold state of the collapsible row under the cursor.
// Roles and rules are no-ops.
func (r *Rail) ToggleCollapse() {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return
	}
	row := r.rows[r.cursor]
	switch {
	case row.collOrchID > 0:
		r.collapsed[row.collOrchID] = !r.collapsed[row.collOrchID]
	case row.archiveOwner > 0:
		r.coordArchiveOpen[row.archiveOwner] = !r.coordArchiveOpen[row.archiveOwner]
	case row.collFreelance:
		r.freelanceCollap = !r.freelanceCollap
	case row.collArchive:
		r.archiveCollapsed = !r.archiveCollapsed
	default:
		return
	}
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
	r.persist() // fold change (BUG-002)
}

// Selected returns the RoleView under the cursor, or nil when the cursor is on
// a header/orchestrator. 6b uses this to bind the panes.
func (r *Rail) Selected() *RoleView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].role
}

// SelectedOrch returns the OrchView under the cursor, or nil.
func (r *Rail) SelectedOrch() *OrchView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].orch
}

// Selection returns the (role, orchestrator) context under the cursor. The
// orchestrator is resolved from the selected role's OrchID — correct even in
// the multi-binding case where the same task surfaces under two orchestrators
// (each via a distinct role with a distinct OrchID) — or taken directly when
// the cursor rests on an orchestrator header. 6b binds the panes off this; 6c
// reads it for mutations via the orchestrator disambiguator.
func (r *Rail) Selection() Selection {
	role := r.Selected()
	orch := r.SelectedOrch()
	if orch == nil && role != nil {
		orch = r.model.OrchByID(role.OrchID)
	}
	sel := Selection{Role: role, Orch: orch}
	// A role row whose collOrchID is set is a bridging sub-coordinator row; carry
	// the child orchestrator id so Ctrl+D can cascade the nested sub-team.
	if r.cursor >= 0 && r.cursor < len(r.rows) {
		if row := r.rows[r.cursor]; row.role != nil && row.collOrchID > 0 {
			sel.BridgeChildOrchID = row.collOrchID
		}
	}
	return sel
}

// Rows returns the flattened row count (test seam).
func (r *Rail) Rows() int { return len(r.rows) }

// OrchCollapsed reports whether the given orchestrator ID is folded (test seam).
func (r *Rail) OrchCollapsed(id int64) bool { return r.collapsed[id] }

// CursorIndex returns the cursor's row index (test seam).
func (r *Rail) CursorIndex() int { return r.cursor }

// --- rendering ---

// Draw paints the rail inside a bordered panel, covering its full bounding
// rect (DrawBorderedPanel blanks the interior) so no stale cells survive — per
// the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage instead).
func (r *Rail) Draw(screen tcell.Screen) {
	r.DrawForSubclass(screen, r)
	r.animFrame = spinnerFrame()
	x, y, w, h := r.GetRect()
	if w <= 0 || h <= 0 {
		return
	}
	borderStyle := theme.StyleBorder
	if r.focused {
		borderStyle = theme.StyleFocusedBorder
	}
	// Reflect an applied query in the border title (while typing AND after Enter).
	title := " Hera "
	if r.filterActive() {
		title = " Hera /" + r.filterQuery + " "
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	// While typing, a `/ <query>` input line takes the top row; the rows render
	// below it over the reduced viewport. DrawBorderedPanel already blanked the
	// interior, so no stale cells survive (no Sync — CLAUDE.md UX rules).
	rowY := inner.Y
	rowH := inner.H
	if r.filterInput {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "/ "+r.filterQuery+"▌", theme.StyleSelected)
		rowY = inner.Y + 1
		rowH = inner.H - 1
		if rowH <= 0 {
			return
		}
	}
	r.adjustOffset(rowH)
	for vis := 0; vis < rowH; vis++ {
		idx := r.offset + vis
		if idx >= len(r.rows) {
			break
		}
		r.drawRow(screen, inner.X, rowY+vis, inner.W, r.rows[idx], idx == r.cursor)
	}
}

// adjustOffset keeps the cursor visible within the inner viewport height.
func (r *Rail) adjustOffset(viewH int) {
	if viewH <= 0 {
		return
	}
	if r.cursor < r.offset {
		r.offset = r.cursor
	}
	if r.cursor >= r.offset+viewH {
		r.offset = r.cursor - viewH + 1
	}
	if r.offset < 0 {
		r.offset = 0
	}
}

func (r *Rail) drawRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	const marker = '›'
	indent := row.depth * 2

	// Selection marker gutter.
	gutterStyle := theme.StyleDimmed
	if selected {
		screen.SetContent(x, y, marker, nil, theme.StyleSelected)
		gutterStyle = theme.StyleSelected
	}
	textX := x + 2 + indent
	textW := w - 2 - indent
	if textW <= 0 {
		return
	}

	switch row.kind {
	case rrRule:
		for c := x; c < x+w; c++ {
			screen.SetContent(c, y, '─', nil, theme.StyleBorder)
		}
	case rrEmpty:
		widget.DrawText(screen, textX, y, textW, row.label, theme.StyleDimmed)
	case rrSectionHeader:
		style := theme.StyleTitle
		if selected {
			style = theme.StyleSelected
		}
		label := row.label
		if row.collFreelance {
			label = chevron(r.freelanceCollap && !r.filterActive()) + " " + label
		}
		widget.DrawText(screen, textX, y, textW, label, style)
	case rrArchiveExpando:
		style := theme.StyleDimmed
		if selected {
			style = theme.StyleSelected
		}
		// Per-coordinator expandos fold via coordArchiveOpen; the bottom Archive
		// section folds via archiveCollapsed. While filtering both auto-expand, so
		// the chevron reads expanded to match the rendered rows.
		collapsed := r.archiveCollapsed && !r.filterActive()
		if row.archiveOwner > 0 {
			collapsed = !r.isCoordArchiveOpen(row.archiveOwner)
		}
		widget.DrawText(screen, textX, y, textW, chevron(collapsed)+" "+row.label, style)
	case rrOrch:
		r.drawOrchRow(screen, textX, y, textW, row, selected)
	case rrRole, rrFreelanceRole:
		r.drawRoleRow(screen, textX, y, textW, row, selected, gutterStyle)
	}
}

func (r *Rail) drawOrchRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	o := row.orch
	nameStyle := theme.StyleProject
	if row.dim {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	// Coordinator status glyph first (the header IS the coordinator — folded
	// from a redundant child row). It reads with the same vocabulary as worker
	// rows; the glyph keeps its own status style even when the row is selected
	// (the glyph never lies). Worker-less / coordinator-less orchestrators skip it.
	if coord := o.CoordRole(); coord != nil {
		glyph, gstyle := statusIcon(coord, row.dim, r.animFrame)
		screen.SetContent(col, y, glyph, nil, gstyle)
		col += 2
	}
	// chevron
	if col < x+w {
		screen.SetContent(col, y, []rune(chevron(r.isCollapsed(o.ID)))[0], nil, nameStyle)
		col += 2
	}
	// coordinator marker
	if col < x+w {
		screen.SetContent(col, y, heraIconCoord, nil, nameStyle)
		col += 2
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}
	live := liveRoleCount(o)
	label := o.Name
	count := fmt.Sprintf(" (%d)", live)
	if len(label)+len(count) > remaining {
		widget.DrawText(screen, col, y, remaining, label, nameStyle)
		return
	}
	widget.DrawText(screen, col, y, remaining, label, nameStyle)
	widget.DrawText(screen, x+w-len(count), y, len(count), count, theme.StyleDimmed)
}

func (r *Rail) drawRoleRow(screen tcell.Screen, x, y, w int, row railRow, selected bool, _ tcell.Style) {
	role := row.role
	icon, iconStyle := statusIcon(role, row.dim, r.animFrame)
	nameStyle := theme.StyleNormal
	if row.dim {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2
	// A bridging sub-coordinator row (collOrchID set) reads like a nested
	// orchestrator header: a fold chevron + the coordinator marker before the
	// name, so the operator can tell it carries (and folds) a child subtree.
	if row.collOrchID > 0 {
		if col < x+w {
			markerStyle := nameStyle
			if !selected {
				markerStyle = theme.StyleProject
				if row.dim {
					markerStyle = theme.StyleDimmed
				}
			}
			screen.SetContent(col, y, []rune(chevron(r.isCollapsed(row.collOrchID)))[0], nil, markerStyle)
			col += 2
			if col < x+w {
				screen.SetContent(col, y, heraIconCoord, nil, markerStyle)
				col += 2
			}
		}
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}
	// PR indicator: a managed row whose bound task has an open PR renders a
	// right-aligned "PR" tag, reserving space so the name truncates instead of
	// overwriting it. Mirrors the Details roster's PR mark, on the rail row.
	const prTag = "PR"
	if r.rolePR(role) && remaining > len(prTag)+1 {
		widget.DrawText(screen, col, y, remaining-len(prTag)-1, role.Name, nameStyle)
		prStyle := theme.StyleInReview
		if row.dim {
			prStyle = theme.StyleDimmed
		}
		widget.DrawText(screen, x+w-len(prTag), y, len(prTag), prTag, prStyle)
		return
	}
	widget.DrawText(screen, col, y, remaining, role.Name, nameStyle)
}

// spinnerFrame computes the current spinner animation frame from wall-clock
// time, mirroring the task list's updateSpinnerFrame. Recomputed on each Draw so
// a WORKING role's glyph advances as long as the spinner loop keeps redrawing
// (it does while any session is actively running).
func spinnerFrame() int {
	interval := widget.SpinnerTickInterval()
	if interval <= 0 {
		return 0
	}
	return int(time.Now().UnixMilli()/interval.Milliseconds()) % widget.SpinnerFrameCount()
}

// chevron returns the fold glyph for a collapsed/expanded state.
func chevron(collapsed bool) string {
	if collapsed {
		return "▸"
	}
	return "▾"
}

// liveRoleCount counts live, non-coordinator roles (the agents shown under the
// header). The coordinator is folded into the header itself, so it never inflates
// the (N) child count.
func liveRoleCount(o *OrchView) int {
	n := 0
	for i := range o.Roles {
		if o.Roles[i].Live && o.Roles[i].Kind != db.HeraKindCoordinator {
			n++
		}
	}
	return n
}

// statusIcon picks the glyph + style for a role row. ready_to_close (M4) wins
// over everything else with a distinct "ready to check off" mark; an
// operator/agent-set blocked or done assertion is honoured next; then GENUINE
// activity (a live binding whose bound argus task is in_progress — role.IsActive)
// animates the spinner; otherwise the hera role status / binding presence drives
// a static glyph. dim forces the dimmed style for archived placement (the glyph
// never lies — only the style dims).
//
// frame is the current spinner animation frame: only a genuinely-active role
// renders the active spinner's frame so it animates. The animated "working"
// glyph is sourced from REAL session activity (role.IsActive), NOT the hera role
// Status "working" field — that field is a manual/MCP-set ladder value that goes
// stale (it stays "working" after a session idles, stops, or dies), so binding
// the spinner to it made idle/stopped/dead roles animate. Mirrors the plugin's
// stateGlyph (spinner on in_progress + running). See BUG-003.
func statusIcon(role *RoleView, dim bool, frame int) (rune, tcell.Style) {
	if role.ReadyToClose {
		st := tcell.StyleDefault.Foreground(theme.ColorComplete).Bold(true)
		if dim {
			st = theme.StyleDimmed
		}
		return theme.IconReview, st
	}
	var glyph rune
	var style tcell.Style
	switch {
	case role.HasStatus && role.Status == db.HeraStatusBlocked:
		// A deliberate "I'm blocked" assertion must not be masked by the activity
		// spinner even while the task is technically still in_progress (waiting).
		glyph, style = theme.IconNeedsInput, theme.StyleNeedsInput
	case role.HasStatus && role.Status == db.HeraStatusDone:
		glyph, style = '✓', theme.StyleComplete
	case role.IsActive():
		// Genuinely producing output → animate. Sourced from real task activity,
		// never the stale hera role-status (BUG-003).
		glyph, style = widget.SpinnerFrame(frame), theme.StyleInProgress
	case role.HasStatus && role.Status == db.HeraStatusIdle:
		glyph, style = theme.IconMoonOutline, theme.StyleInReview
	case role.Live:
		glyph, style = theme.IconMoonStars, theme.StyleInReview
	default:
		glyph, style = theme.IconMoonOutline, theme.StyleDimmed
	}
	if dim {
		style = theme.StyleDimmed
	}
	return glyph, style
}

// CursorToParent moves the cursor to the nearest parent-coordinator row — the
// closest rrOrch header or bridging rrRole (collOrchID > 0) with a strictly
// smaller depth than the current row. No-op when the cursor is at the root
// (depth 0 with no qualifying ancestor) or on a section that has no coordinator
// parent (e.g. a freelance role). Called by the page's Left-arrow handler when
// the rail is focused (BUG-016).
func (r *Rail) CursorToParent() {
	if r.cursor <= 0 || r.cursor >= len(r.rows) {
		return
	}
	cur := r.rows[r.cursor]
	for i := r.cursor - 1; i >= 0; i-- {
		row := r.rows[i]
		if row.depth >= cur.depth {
			continue
		}
		// Only land on rows that represent a coordinator: an orchestrator header
		// (rrOrch) or a bridging worker row whose collOrchID marks it as the
		// coordinator of a nested sub-team (rrRole with collOrchID > 0).
		if row.kind == rrOrch || (row.kind == rrRole && row.collOrchID > 0) {
			r.setCursor(i)
			return
		}
	}
}

// InputHandler routes navigation keys. Left/Right are currently unused here
// (Up/Down/j/k drive the cursor, Space toggles collapse); they are free for
// future horizontal navigation now that the global handler no longer consumes
// them for tab switching (tab nav is 1/2/3 only).
func (r *Rail) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return r.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		// While typing a filter every keystroke is filter input — nav and mutation
		// keys are suppressed (page.handleRailMutation already bailed; app.go let
		// the global rune shortcuts through). See handleFilterKey.
		if r.filterInput {
			r.handleFilterKey(event)
			return
		}
		switch event.Key() {
		case tcell.KeyDown:
			r.CursorDown()
		case tcell.KeyUp:
			r.CursorUp()
		case tcell.KeyRune:
			switch event.Rune() {
			case 'j':
				r.CursorDown()
			case 'k':
				r.CursorUp()
			case ' ':
				r.ToggleCollapse()
			case '/':
				r.enterFilter()
			}
		}
	})
}

// enterFilter switches the rail into search INPUT mode, preserving any current
// query so re-pressing `/` after acceptance lets the operator edit/extend it
// (then Esc clears). The rows already reflect the (unchanged) query, so no
// rebuild is needed here.
func (r *Rail) enterFilter() {
	r.filterInput = true
	uxlog.Log("[hera-view] rail filter: input mode (query=%q)", r.filterQuery)
}

// handleFilterKey routes a keystroke while in search INPUT mode. Esc clears and
// restores the full rail; Enter accepts (query stays, input mode off so j/k
// navigate the filtered set); Up/Down move the cursor WITHIN the filtered rows
// without leaving input mode (so the operator can select a row while still
// typing/editing the query — matching the Tasks-tab `/` filter); Backspace trims
// a rune; any other rune extends the query. Every query change rebuilds + re-pins
// the cursor onto a visible row.
func (r *Rail) handleFilterKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEscape:
		r.filterInput = false
		r.filterQuery = ""
		r.rebuildAfterFilter()
		uxlog.Log("[hera-view] rail filter: cleared")
	case tcell.KeyEnter:
		r.filterInput = false
		uxlog.Log("[hera-view] rail filter: accepted (query=%q)", r.filterQuery)
	case tcell.KeyDown:
		// Navigate the filtered set while typing — step() only lands on selectable
		// rows, and a narrowed buildRows contains only visible rows, so the cursor
		// can never reach a filtered-out row.
		r.CursorDown()
	case tcell.KeyUp:
		r.CursorUp()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r.filterQuery != "" {
			runes := []rune(r.filterQuery)
			r.filterQuery = string(runes[:len(runes)-1])
			r.rebuildAfterFilter()
		}
	case tcell.KeyRune:
		r.filterQuery += string(event.Rune())
		r.rebuildAfterFilter()
	}
}

// rebuildAfterFilter rebuilds the rows for the current query and re-pins the
// cursor onto a still-visible selectable row. It writes the cursor directly
// (restoreCursor/clampCursor), never via setCursor, so a filter change neither
// fires the selection callback nor (per BUG-002) persists rail state — the
// filter is transient.
func (r *Rail) rebuildAfterFilter() {
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
}
