package hera

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
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
	rrRule             railRowKind = iota // non-selectable separator
	rrSectionHeader                       // "Pinned" / "Freelance" label
	rrOrch                                // orchestrator header (selectable, collapsible)
	rrRole                                // role under an orchestrator (selectable)
	rrFreelanceRole                       // freelance-kind role (selectable)
	rrArchiveExpando                      // "Archive (N)" fold (selectable, collapsible)
	rrEmpty                               // empty-state placeholder (non-selectable)
	rrPinnedBreadcrumb                    // line 1 of a pinned non-root entry (selectable; dimmed icon + lineage)
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

	// ancestryOnly is true while a filter is active AND this coordinator/
	// orchestrator heading row (rrOrch, or a bridging rrRole/rrPinnedBreadcrumb
	// standing in for a nested coordinator) is on screen ONLY to preserve tree
	// ancestry for a matching descendant — its own name (or its folded-in
	// coordinator's name, for an rrOrch header) does NOT itself match the
	// query. Such a row is never selectable (BUG-028-RAIL): arrow nav and
	// first-match auto-select skip straight past it to a real match, and it
	// renders dimmed so it's visually obvious it can't be selected. Always
	// false when no filter is active.
	ancestryOnly bool

	// Collapse target (only one is set, and only when collapsible).
	collOrchID    int64 // >0 → toggle collapsed[collOrchID]
	collFreelance bool
	collArchive   bool  // bottom Archive section (archived ROOT orchestrators)
	archiveOwner  int64 // >0 → per-coordinator Archive expando for this orch's archived roles

	// Two-line pinned non-root entry (add-hera-pin-nonroot). breadcrumb is the
	// dimmed lineage trail drawn on an rrPinnedBreadcrumb row (line 1).
	// breadcrumbCont marks the rrRole continuation (line 2): the non-selectable
	// name line that pairs with the preceding rrPinnedBreadcrumb row.
	breadcrumb     string
	breadcrumbCont bool
}

func (r railRow) selectable() bool {
	// An ancestry-only heading (BUG-028-RAIL) is on screen purely for tree context —
	// its own name never matched the typed query, so it can never be a valid
	// selection target while filtering. Checked first so it overrides every
	// kind below.
	if r.ancestryOnly {
		return false
	}
	switch r.kind {
	case rrOrch, rrFreelanceRole, rrArchiveExpando, rrPinnedBreadcrumb:
		return true // both the bottom Archive section and per-coordinator expandos
	case rrRole:
		// The continuation (line 2) of a two-line pinned entry is non-selectable —
		// the cursor anchors on the preceding rrPinnedBreadcrumb (line 1).
		return !r.breadcrumbCont
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
	// the rail (ancestry-preserving). A single Enter selects the current match
	// AND clears the filter in one step (BUG-028-RAIL) — there is no longer an
	// "accepted but still narrowed" resting state, so filterQuery is always ""
	// whenever filterInput is false. Filter state is transient and is NOT
	// persisted. filterVis is the per-build visibility memo (orch id →
	// visible), recomputed each filtered buildRows and nil when no filter is
	// active.
	filterInput bool
	filterQuery string
	filterVis   map[int64]bool

	// pinnedFloat is the set of role ids that float OUT of their parent subtree
	// into the Pinned section as a two-line breadcrumb entry (add-hera-pin-nonroot).
	// Recomputed each buildRows by collectPinnedRoles; appendOrchWorkers consults
	// it to suppress a floated worker row (and its bridged child, already hoisted
	// + placed in the Pinned pass) from the active tree. nil when nothing floats.
	pinnedFloat map[int64]bool

	// canonical is the per-build canonical-parent map (add-hera-kanban-status),
	// recomputed each buildRows and reused by Selection() to stamp
	// Selection.TopLevelOrch — an orchestrator absent from this map is a true
	// root. nil after the empty-model early return (no orchestrators to have
	// parents at all).
	canonical map[int64]canonParent

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

// rolePR reports whether the role's bound task has a PR in an actionable
// review state (mirrors theme.PRGlyph / model.PRState.IsActionable) — a
// merged, closed, draft, or unknown-state PR leaves a url in the cache but
// must not flag the rail row.
func (r *Rail) rolePR(role *RoleView) bool {
	if role == nil || role.TaskID == "" || r.prMeta == nil {
		return false
	}
	kv := r.prMeta[role.TaskID]
	if kv == nil {
		return false
	}
	s, err := model.ParsePRState(kv["state"])
	return err == nil && s.IsActionable()
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
// typing a query). page.go skips rail mutations while this holds — EXCEPT
// Enter, which is special-cased to select the current match and clear the
// filter in one step (BUG-028-RAIL) — and app.go lets the global rune shortcuts
// (1/2/3/q/?) fall through to the rail as filter input. Enter and Esc both
// fully clear the query (ClearFilter), so there is no longer an
// "accepted-but-still-narrowed" resting state — Filtering() and filterActive()
// now always flip together.
func (r *Rail) Filtering() bool { return r.filterInput }

// filterActive reports whether the rail rows are NARROWED (a non-empty query is
// applied). An empty/all-whitespace query narrows nothing. Since Enter/Esc both
// fully clear filterQuery, this can only be true while Filtering() is also true.
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

// orchMatchesOwnQuery reports whether o itself — not merely a visible
// descendant — is a text match against the active query: its own name, OR its
// folded-in coordinator's name. The coordinator has no separate rail row (it IS
// the header — see appendOrch), so a query matching the coordinator's name must
// count as the header's own match, not ancestry (BUG-028-RAIL): otherwise searching
// for a coordinator by name would never let Enter jump straight to it.
func (r *Rail) orchMatchesOwnQuery(o *OrchView) bool {
	if r.filterMatches(o.Name) {
		return true
	}
	if coord := o.CoordRole(); coord != nil && r.filterMatches(coord.Name) {
		return true
	}
	return false
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
		case row.role != nil && !row.breadcrumbCont && row.role.RoleID == ref:
			// Anchor on the breadcrumb line (line 1), never the non-selectable
			// continuation (line 2) which carries the same role pointer.
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
		r.canonical = nil
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
	r.canonical = canonical
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
	// (user intent), even if some worker bridges them. Pinned NON-ROOT roles
	// (add-hera-pin-nonroot) float OUT of their parent subtree into the same
	// section as a two-line breadcrumb entry, after the pinned orchestrators. The
	// header renders when a pinned orchestrator is visible OR any role floats; it
	// is pruned when neither holds under the active filter.
	floated := r.collectPinnedRoles(canonical)
	pinnedRendered := r.anyOrchVisible(r.model.Pinned) || len(floated) > 0
	if pinnedRendered {
		r.rows = append(r.rows, railRow{kind: rrSectionHeader, label: "Pinned"})
		for i := range r.model.Pinned {
			r.appendOrch(&r.model.Pinned[i], 0, r.model.Pinned[i].Archived, canonical, placed)
		}
		for _, pe := range floated {
			r.appendPinnedRole(pe, canonical, placed)
		}
	}

	// 2. Active orchestrators, partitioned into kanban-status sub-groups
	// (add-hera-kanban-status): active (headerless, exactly the historical
	// rendering) → Backlog (N) → Blocked (N) → Done (N). Each group is scoped
	// to TOP-LEVEL (root — no canonical parent) orchestrators only; a
	// nested/bridged orchestrator's own kanban status is never consulted for
	// placement — it always nests under its canonical parent regardless of
	// these section boundaries. Within each group: render roots first; then a
	// safety sweep rescues only TRUE cycle-orphans carrying that group's status
	// — an orchestrator left unplaced AND whose canonical chain reaches no
	// root. A child that is merely hidden because an ancestor is
	// collapsed/archived is structurally reachable, so it stays folded instead
	// of leaking to the top.
	//
	// The headerless `active` group preserves the historical horizontal-rule
	// divider (BUG-027) separating the Pinned section from it — inserted only
	// when the Pinned section rendered AND this group produced ≥1 row (no
	// stray rule when nothing is pinned, none when Pinned is the only
	// content). The backlog/blocked/done groups instead get their OWN
	// unconditioned leading divider whenever non-empty — the SAME convention
	// the Freelance/Archive sections below already use (always lead with a
	// divider when non-empty, regardless of what rendered above) — so no
	// "was anything rendered above" state needs tracking across three more
	// group boundaries. An empty group renders neither its header nor a
	// divider.
	kanbanGroups := []struct {
		status db.HeraKanbanStatus
		label  string // "" marks the headerless active group
	}{
		{db.HeraKanbanActive, ""},
		{db.HeraKanbanBacklog, "Backlog"},
		{db.HeraKanbanBlocked, "Blocked"},
		{db.HeraKanbanDone, "Done"},
	}
	for _, g := range kanbanGroups {
		groupStart := len(r.rows)
		n := 0 // count of top-level orchestrators actually placed in this group
		for i := range r.model.Active {
			ov := &r.model.Active[i]
			if consumed[ov.ID] || placed[ov.ID] || kanbanStatusOf(ov) != g.status {
				continue
			}
			r.appendOrch(ov, 0, false, canonical, placed)
			if placed[ov.ID] {
				n++
			}
		}
		for i := range r.model.Active {
			ov := &r.model.Active[i]
			if placed[ov.ID] || structReach[ov.ID] || kanbanStatusOf(ov) != g.status {
				continue
			}
			r.appendOrch(ov, 0, false, canonical, placed)
			if placed[ov.ID] {
				n++
			}
		}
		if n == 0 {
			continue
		}
		if g.label == "" {
			if pinnedRendered {
				// Insert the divider between the last Pinned row and the first active row.
				r.rows = append(r.rows[:groupStart], append([]railRow{{kind: rrRule}}, r.rows[groupStart:]...)...)
			}
			continue
		}
		header := railRow{kind: rrSectionHeader, label: fmt.Sprintf("%s (%d)", g.label, n)}
		r.rows = append(r.rows[:groupStart], append([]railRow{{kind: rrRule}, header}, r.rows[groupStart:]...)...)
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
		kind:         rrOrch,
		orch:         o,
		depth:        depth,
		dim:          dim,
		collOrchID:   o.ID,
		ancestryOnly: r.filterActive() && !r.orchMatchesOwnQuery(o),
	})
	if r.isCollapsed(o.ID) {
		// Partial-fold reveal: a closed coordinator whose subtree needs input
		// still peeks through to the specific ancestor chain(s) down to each
		// needs-input leaf, even though the fold stays visually closed —
		// every other sibling at every level stays fully hidden (add-hera-
		// jump-question). Pure rendering: fold state (isCollapsed) is never
		// mutated by this branch.
		if o.SubtreeNeedsInput {
			r.appendOrchWorkers(o, depth+1, dim, canonical, placed, true)
		}
		return
	}
	r.appendOrchWorkers(o, depth+1, dim, canonical, placed, false)
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
// revealOnly restricts this call to the partial-fold-reveal path: o is a
// closed ancestor whose subtree needs input, so ONLY children whose own
// SubtreeNeedsInput is true are emitted (recursively, at every level below —
// each level's OWN collapse flag is ignored, since the operator can't see or
// toggle a fold inside a subtree that isn't otherwise visible). Archived and
// pinned-floated roles are skipped exactly as in normal mode (they already
// have their own, independent visibility mechanisms — Archive expando /
// Pinned section — so the reveal leaves them alone rather than layering a
// third), and the per-coordinator Archive expando itself is never rendered
// in reveal mode.
func (r *Rail) appendOrchWorkers(o *OrchView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool, revealOnly bool) {
	var archived []*RoleView
	for i := range o.Roles {
		w := &o.Roles[i]
		if w.Kind == db.HeraKindCoordinator {
			continue // folded into the header / the bridging row above
		}
		// A pinned non-root role floats into the Pinned section (rendered first,
		// already placed) — suppress it (and its hoisted bridged child) here so it
		// renders exactly once (add-hera-pin-nonroot).
		if r.pinnedFloat[w.RoleID] {
			continue
		}
		if w.Archived {
			if !revealOnly {
				// BUG-022 Q3: HIDING a worker (or a bridging sub-coordinator) folds it
				// into the per-coordinator Archive expando — and a bridging sub-coord
				// drags its WHOLE subtree in with it (structure retained INSIDE the
				// expando), NOT rendered dimmed-in-place. ALL archived workers go to
				// the expando here; the expando's appendWorkerRow (below) nests any
				// bridged sub-team beneath the hidden worker when the expando is open.
				// The orphaning hazard the old in-place rule guarded against (an
				// archived bridging worker hoisted while its child is left unplaced →
				// safety-swept flat to the top) is now closed by `structuralReach`: a
				// child whose canonical-parent chain reaches a root is never re-leaked
				// by the safety sweep, so when the expando is COLLAPSED the child stays
				// hidden under its hidden parent instead of leaking. See
				// TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando (both fold states).
				archived = append(archived, w)
			}
			continue
		}
		if revealOnly {
			if !w.SubtreeNeedsInput {
				continue // prune — no needs-input descendant on this path
			}
			r.appendWorkerRow(o.ID, w, depth, dim, canonical, placed, true)
			continue
		}
		if !r.workerRowVisible(o.ID, w, canonical, placed) {
			continue // filtered out (no name match, bridges no visible child)
		}
		r.appendWorkerRow(o.ID, w, depth, dim, canonical, placed, false)
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
		if revealOnly {
			if !child.SubtreeNeedsInput {
				continue
			}
			r.appendOrchRevealPath(child, depth, dim, canonical, placed)
			continue
		}
		r.appendOrch(child, depth, dim, canonical, placed)
	}

	if revealOnly {
		return // no per-coordinator Archive expando in reveal mode
	}

	// Per-coordinator Archive (N) expando: HIDDEN (archived) roles fold under their
	// coordinator's active agents, collapsed by default. Distinct from the bottom
	// Archive section (archived ROOT orchestrators). Hidden roles render dimmed and
	// — for a bridging sub-coordinator — still nest their WHOLE bridged sub-team
	// beneath them INSIDE the expando (forced-dim down the subtree), so hiding a
	// sub-coord collapses its subtree out of the main view with structure retained
	// (BUG-022 Q3). Under an active filter only visible hidden roles list (the
	// expando is pruned when none match), and the expando auto-expands.
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
				r.appendWorkerRow(o.ID, w, depth+1, true, canonical, placed, false)
			}
		}
	}
}

// appendOrchRevealPath emits a coordinator-spawned child orchestrator's own
// header row and recurses into it in reveal-only mode, UNCONDITIONALLY —
// ignoring the child's own collapse flag entirely. It is only ever reached
// from appendOrchWorkers' revealOnly branch, already gated on the child's
// SubtreeNeedsInput being true, for a closed ANCESTOR the operator can't see
// into — so the child's own fold state is not meaningful here (there is
// nothing visible for the operator to have toggled), unlike appendOrch's
// normal collapsed-vs-expanded gate at the top level.
func (r *Rail) appendOrchRevealPath(o *OrchView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool) {
	if placed[o.ID] {
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
	r.appendOrchWorkers(o, depth+1, dim, canonical, placed, true)
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
//
// revealOnly (partial-fold reveal): when true, the bridged child — a nested
// sub-coordinator whose worker row IS its coordinator, so it gets no separate
// header — recurses in reveal-only mode UNCONDITIONALLY, ignoring the
// child's own collapse flag, mirroring appendOrchRevealPath's rationale (the
// caller already gated this row on w.SubtreeNeedsInput being true before
// calling appendWorkerRow, so the child is known to need the reveal).
func (r *Rail) appendWorkerRow(ownerID int64, w *RoleView, depth int, dim bool, canonical map[int64]canonParent, placed map[int64]bool, revealOnly bool) {
	rowDim := dim || w.Archived
	child := r.workerBridgeChild(ownerID, w, canonical, placed)
	// Under an active filter, only bridge a VISIBLE child: a non-matching subtree
	// must not surface (and the bridging row drops its chevron). Filtering and
	// reveal never overlap in practice (the `/` filter force-expands every
	// fold, so appendOrch's revealOnly branch — gated on isCollapsed — never
	// triggers while filtering), so this check is skipped in reveal mode.
	if child != nil && !revealOnly && r.filterActive() && !r.filterVis[child.ID] {
		child = nil
	}
	collID := int64(0)
	if child != nil {
		collID = child.ID
	}
	r.rows = append(r.rows, railRow{
		kind:         rrRole,
		role:         w,
		depth:        depth,
		dim:          rowDim,
		collOrchID:   collID,
		ancestryOnly: !revealOnly && r.filterActive() && !r.filterMatches(w.Name),
	})
	if child != nil {
		placed[child.ID] = true
		if revealOnly {
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, true)
		} else if !r.isCollapsed(child.ID) {
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, false)
		} else if child.SubtreeNeedsInput {
			// Partial-fold reveal (BUG-064): the child is still collapsed even
			// though its ancestor (o) just expanded — reveal-only mirrors
			// appendPinnedRole's identical fallback so re-expanding an outer
			// coordinator doesn't blank out a still-closed nested
			// coordinator's own needs-input leaf.
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, true)
		}
	}
}

// --- pinned non-root roles (add-hera-pin-nonroot) ---

// pinnedRoleEntry pairs a floated pinned role with its computed lineage trail.
type pinnedRoleEntry struct {
	role       *RoleView
	breadcrumb string
}

// collectPinnedRoles walks the Active and Archived orchestrators and returns
// every pinned NON-coordinator role that floats OUT of its parent subtree into
// the Pinned section, paired with its lineage breadcrumb. It also (re)sets
// r.pinnedFloat (the role-id suppression set appendOrchWorkers consults) so the
// floated row renders exactly once. Roles under a PINNED orchestrator are NOT
// collected — that orchestrator already floats as a root and its roles stay
// nested under it (mirrors the plugin's rolePinnedOut). Under an active filter a
// role is kept only when its own name matches OR it bridges a filter-visible
// child. A role whose orchestrator cannot be resolved for the breadcrumb is
// skipped (logged) rather than rendered without lineage.
func (r *Rail) collectPinnedRoles(canonical map[int64]canonParent) []pinnedRoleEntry {
	var out []pinnedRoleEntry
	floatSet := make(map[int64]bool)
	for _, sec := range [][]OrchView{r.model.Active, r.model.Archived} {
		for i := range sec {
			o := &sec[i]
			for j := range o.Roles {
				rv := &o.Roles[j]
				if !rv.Pinned || rv.Kind == db.HeraKindCoordinator {
					continue
				}
				if r.filterActive() && !r.filterMatches(rv.Name) {
					// Keep a non-matching pinned row only when it bridges a visible
					// child (so a matching nested subtree is not orphaned).
					c := r.workerBridgeChild(o.ID, rv, canonical, map[int64]bool{})
					if c == nil || !r.filterVis[c.ID] {
						continue
					}
				}
				bc := r.pinnedBreadcrumb(rv, canonical)
				if bc == "" {
					uxlog.Log("[hera-view] pinned role %d (%s): parent orch %d unresolved, not floating", rv.RoleID, rv.Name, rv.OrchID)
					continue
				}
				out = append(out, pinnedRoleEntry{role: rv, breadcrumb: bc})
				floatSet[rv.RoleID] = true
			}
		}
	}
	r.pinnedFloat = floatSet
	return out
}

// pinnedBreadcrumb builds a floated role's lineage trail: the orchestrator-name
// chain from the root down to and including the role's own orchestrator
// (role.OrchID), joined with " › " and trailing " › " (e.g. "root › sub › ").
// The chain is walked via canonicalParents — the SAME deterministic, fold-
// independent parentage the rail nests by — so the trail matches the rendered
// tree. Returns "" when any orchestrator on the chain cannot be resolved.
func (r *Rail) pinnedBreadcrumb(rv *RoleView, canonical map[int64]canonParent) string {
	var names []string
	seen := make(map[int64]bool)
	for id := rv.OrchID; id != 0; {
		o := r.model.OrchByID(id)
		if o == nil {
			return "" // unresolvable parent → caller skips (no context-free render)
		}
		names = append(names, o.Name)
		if seen[id] {
			break // cycle guard (matches BridgeSubtree's visited discipline)
		}
		seen[id] = true
		cp, ok := canonical[id]
		if !ok {
			break // reached a top-level root
		}
		id = cp.orchID
	}
	var b strings.Builder
	for i := len(names) - 1; i >= 0; i-- {
		b.WriteString(names[i])
		b.WriteString(" › ")
	}
	return b.String()
}

// appendPinnedRole emits a floated pinned role as a two-line entry: line 1 is a
// selectable rrPinnedBreadcrumb (dimmed icon + lineage), line 2 a non-selectable
// rrRole continuation (the name). When the role bridges a child orchestrator (a
// pinned sub-coordinator) the breadcrumb row carries collOrchID = child.ID (so
// Space folds it and Ctrl+D cascades), and the child's subtree is hoisted
// beneath the entry and marked placed so the active passes render it exactly
// once (full parity with the plugin's BUG-021).
func (r *Rail) appendPinnedRole(pe pinnedRoleEntry, canonical map[int64]canonParent, placed map[int64]bool) {
	rv := pe.role
	child := r.workerBridgeChild(rv.OrchID, rv, canonical, placed)
	if child != nil && r.filterActive() && !r.filterVis[child.ID] {
		child = nil // a non-visible child does not hoist (and the chevron drops)
	}
	collID := int64(0)
	if child != nil {
		collID = child.ID
	}
	bcAncestry := r.filterActive() && !r.filterMatches(rv.Name)
	r.rows = append(r.rows, railRow{kind: rrPinnedBreadcrumb, role: rv, depth: 0, breadcrumb: pe.breadcrumb, collOrchID: collID, ancestryOnly: bcAncestry})
	r.rows = append(r.rows, railRow{kind: rrRole, role: rv, depth: 1, breadcrumbCont: true, ancestryOnly: bcAncestry})
	if child != nil {
		placed[child.ID] = true
		if !r.isCollapsed(child.ID) {
			r.appendOrchWorkers(child, 2, child.Archived, canonical, placed, false)
		} else if child.SubtreeNeedsInput {
			// Partial-fold reveal applies here too: a pinned role's own bridged
			// child can be closed with a hidden needs-input descendant.
			r.appendOrchWorkers(child, 2, child.Archived, canonical, placed, true)
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

// SelectByTaskID moves the cursor to the first selectable role row whose bound
// task is taskID, firing onSelectionChanged so the panes rebind. Returns true
// when such a row exists in the CURRENT (built) row set. Used by the plan view's
// leaf-Enter to jump to a node's role WITHIN the Hera view (BUG-002) — never a
// Tasks-tab switch. A taskID with no visible row (e.g. under a collapsed branch
// the plan never surfaces) is a no-op returning false.
func (r *Rail) SelectByTaskID(taskID string) bool {
	if taskID == "" {
		return false
	}
	for i, row := range r.rows {
		if row.selectable() && row.role != nil && row.role.TaskID == taskID {
			r.setCursor(i) // funnels through onSelectionChanged + persist
			return true
		}
	}
	return false
}

// EnsureAncestorsExpanded uncollapses orchID and every ancestor on its canonical
// parent chain up to the root, so a row nested under folded coordinator(s) becomes
// visible. It walks canonicalParents() — the SAME fold-independent parentage the
// rail nests by — with a visited guard against cycles, handling deeply nested
// sub-coordinators (the WHOLE chain, not just the immediate parent). When any fold
// actually flips it rebuilds the rows and persists the change, so the expand
// survives like a user Space-toggle. Used by the plan view's leaf-Enter join
// (BUG-007): a folded coordinator must not swallow the join — expand first, then
// SelectByTaskID can see (and select) the now-built row.
func (r *Rail) EnsureAncestorsExpanded(orchID int64) {
	if orchID == 0 {
		return
	}
	canonical := r.model.canonicalParents()
	seen := make(map[int64]bool)
	changed := false
	for id := orchID; id != 0; {
		if seen[id] {
			break // cycle guard (matches BridgeSubtree's visited discipline)
		}
		seen[id] = true
		if r.collapsed[id] {
			r.collapsed[id] = false
			changed = true
		}
		cp, ok := canonical[id]
		if !ok {
			break // reached a top-level root
		}
		id = cp.orchID
	}
	if !changed {
		return
	}
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
	r.persist() // fold change (BUG-002), like ToggleCollapse
}

// needsInputTaskID returns the argus task id this row's OWN needs-input
// signal points at, and whether the row qualifies as a jump target at all
// (add-hera-jump-question, Ctrl+G). "Own" — needsInputOwn(), the same unit
// the switcher's needs-input-first sort and the rail's leaf "(?)" glyph both
// key on — deliberately requires row.role (a genuine rrRole/rrFreelanceRole/
// rrPinnedBreadcrumb row): a top-level orchestrator's rrOrch HEADER row never
// qualifies, even when the coordinator's OWN signal (not just the rolled-up
// SubtreeNeedsInput a folded header always shows for any descendant) is set —
// appendOrchWorkers folds a top-level coordinator's role entirely into the
// header (never emitting it as its own row), so SelectByTaskID — which only
// ever matches row.role — could never land the jump on it; offering it as a
// candidate would produce a "found but unreachable" dead cycle stop. A NESTED
// sub-coordinator is unaffected: it bridges as an ordinary role-bearing worker
// row in its PARENT orchestrator (appendWorkerRow), so its own need is a
// perfectly reachable candidate here. A non-selectable row (e.g. a pinned
// entry's non-selectable continuation line) never qualifies either, mirroring
// SelectByTaskID's own selectable() gate.
func (row railRow) needsInputTaskID() (string, bool) {
	if !row.selectable() || row.role == nil {
		return "", false
	}
	if row.role.needsInputOwn() && row.role.TaskID != "" {
		return row.role.TaskID, true
	}
	return "", false
}

// NextNeedsInputTaskID returns the argus task id of the next row — in today's
// built rail order (Pinned → Active depth-first → Freelance → Archive), the
// SAME traversal appendOrchWorkers already reveals a hidden needs-input leaf
// through when its ancestor is folded — whose own signal needs input,
// scanning strictly AFTER the current cursor and wrapping around the whole
// ring back to (and including) the cursor itself. Mirrors SelectByTaskID's
// scan-and-select shape, but position- rather than id-driven: repeated calls
// therefore cycle forward through every candidate in turn (never repeatedly
// returning the row the cursor already sits on) while a single remaining
// candidate keeps re-selecting itself once the ring wraps all the way
// around — the caller (HeraPage.JumpToNextNeedsInput) does the actual
// ancestor-expand + select. Returns ("", false) when no row qualifies or the
// rail is empty.
func (r *Rail) NextNeedsInputTaskID() (string, bool) {
	n := len(r.rows)
	if n == 0 {
		return "", false
	}
	cursor := r.cursor
	if cursor < 0 || cursor >= n {
		cursor = 0
	}
	for step := 1; step <= n; step++ {
		i := (cursor + step) % n
		if id, ok := r.rows[i].needsInputTaskID(); ok {
			return id, true
		}
	}
	return "", false
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
	if orch != nil {
		_, hasParent := r.canonical[orch.ID]
		sel.TopLevelOrch = !hasParent
	}
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
	// Reflect the query in the border title while typing (BUG-028-RAIL: Enter/Esc
	// both fully clear the query, so it's never non-empty once input mode ends).
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
		// A two-line pinned entry highlights its continuation (line 2) when the
		// preceding breadcrumb (line 1, the cursor anchor) is selected.
		selected := idx == r.cursor
		if r.rows[idx].breadcrumbCont && idx-1 == r.cursor {
			selected = true
		}
		r.drawRow(screen, inner.X, rowY+vis, inner.W, r.rows[idx], selected)
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

	// Selection marker gutter. The continuation (line 2) of a two-line pinned
	// entry never draws the '›' marker — the cursor anchors on line 1 — but its
	// text still renders selected-style when line 1 is selected.
	gutterStyle := theme.StyleDimmed
	if selected {
		if !row.breadcrumbCont {
			screen.SetContent(x, y, marker, nil, theme.StyleSelected)
		}
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
	case rrPinnedBreadcrumb:
		r.drawPinnedBreadcrumbRow(screen, textX, y, textW, row)
	case rrRole, rrFreelanceRole:
		if row.breadcrumbCont {
			r.drawBreadcrumbNameRow(screen, textX, y, textW, row.role, row.ancestryOnly, selected)
			return
		}
		r.drawRoleRow(screen, textX, y, textW, row, selected, gutterStyle)
	}
}

func (r *Rail) drawOrchRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	o := row.orch
	// greyed folds in ancestryOnly (BUG-028-RAIL) alongside the existing archived-
	// placement dim, so a heading kept only for tree context reads visually
	// non-selectable too.
	greyed := row.dim || row.ancestryOnly
	nameStyle := theme.StyleProject
	if greyed {
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
		glyph, gstyle := statusIcon(coord, greyed, r.animFrame)
		screen.SetContent(col, y, glyph, nil, gstyle)
		col += 2
	} else if o.SubtreeNeedsInput {
		// Coordinator-less orchestrator (e.g. its coordinator role was nuked):
		// no coord glyph carries the needs-input rollup, so surface it directly
		// (BUG-028-RAIL). Without this, a blocked worker under a collapsed header — the
		// default "tidy summary" view — shows no needs-input cue at all, unlike the
		// always-flat task list. Style stays needs-input even when dimmed/selected
		// (the glyph never lies), matching statusIcon's ready_to_close/needs-input.
		gstyle := theme.StyleNeedsInput
		if greyed {
			gstyle = theme.StyleDimmed
		}
		screen.SetContent(col, y, theme.IconNeedsInput, nil, gstyle)
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
	// greyed folds in ancestryOnly (BUG-028-RAIL) alongside the existing archived-
	// placement dim, so a bridging heading kept only for tree context reads
	// visually non-selectable too.
	greyed := row.dim || row.ancestryOnly
	icon, iconStyle := statusIcon(role, greyed, r.animFrame)
	nameStyle := theme.StyleNormal
	if greyed {
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
				if greyed {
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

// drawPinnedBreadcrumbRow renders line 1 of a two-line pinned non-root entry:
// the role's status glyph (dimmed — context only, the selection cue is the
// gutter marker drawn by drawRow) followed by the dimmed lineage trail. An
// over-wide trail is left-truncated with a leading "…" so the NEAREST parent
// (rightmost text) stays visible (add-hera-pin-nonroot).
func (r *Rail) drawPinnedBreadcrumbRow(screen tcell.Screen, x, y, w int, row railRow) {
	role := row.role
	if role == nil {
		return
	}
	icon, _ := statusIcon(role, true, r.animFrame) // force dimmed below
	screen.SetContent(x, y, icon, nil, theme.StyleDimmed)
	col := x + 2
	availW := w - 2
	if availW <= 0 {
		return
	}
	widget.DrawText(screen, col, y, availW, truncRunesLeft(row.breadcrumb, availW), theme.StyleDimmed)
}

// drawBreadcrumbNameRow renders line 2 of a two-line pinned non-root entry: the
// role name (indented under line 1). It renders selected-style when the
// preceding breadcrumb line (the cursor anchor) is selected, dimmed when the
// role is archived OR the entry is an ancestry-only filter heading (BUG-028-RAIL),
// normal otherwise (add-hera-pin-nonroot).
func (r *Rail) drawBreadcrumbNameRow(screen tcell.Screen, x, y, w int, role *RoleView, ancestryOnly bool, selected bool) {
	if role == nil {
		return
	}
	nameStyle := theme.StyleNormal
	if role.Archived || ancestryOnly {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	widget.DrawText(screen, x, y, w, role.Name, nameStyle)
}

// truncRunesLeft left-truncates s to at most max runes, prepending "…" when
// truncation occurs, so the rightmost (nearest-ancestor) text of an overflowing
// breadcrumb trail stays visible. Rune-aware (not byte-aware) per the rail's
// truncation rules (add-hera-pin-nonroot).
func truncRunesLeft(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(max-1):])
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

// kanbanStatusOf returns o's kanban status, defaulting an unset/empty value to
// active (add-hera-kanban-status). BuildModel always populates KanbanStatus
// from the DB's NOT NULL DEFAULT 'active' column, so this default only matters
// for hand-built test fixtures (and any other OrchView constructed outside
// BuildModel) that never set the field — treating them as active preserves
// their historical headerless-active-group placement.
func kanbanStatusOf(o *OrchView) db.HeraKanbanStatus {
	if o.KanbanStatus == "" {
		return db.HeraKanbanActive
	}
	return o.KanbanStatus
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
// over everything else with a distinct "ready to check off" mark; the
// needs-input "(?)" indicator is honoured next — the role's OWN signal
// (authoritative needs-input flag or a self-asserted blocked status) OR the
// subtree ROLLUP (any descendant needs input, transitively across bridges), so
// attention bubbles up to every ancestor coordinator and the root (BUG-018);
// then a done assertion, then GENUINE activity (a live binding whose bound argus
// task is in_progress — role.IsActive) animates the spinner; otherwise the hera
// role status / binding presence drives a static glyph. dim forces the dimmed
// style for archived placement (the glyph never lies — only the style dims).
//
// frame is the current spinner animation frame: only a genuinely-active role
// renders the active spinner's frame so it animates. The animated "working"
// glyph is sourced from REAL session activity (role.IsActive), NOT the hera role
// Status "working" field — that field is a manual/MCP-set ladder value that goes
// stale (it stays "working" after a session idles, stops, or dies), so binding
// the spinner to it made idle/stopped/dead roles animate. Mirrors the plugin's
// stateGlyph (spinner on in_progress + running). See BUG-003.
func statusIcon(role *RoleView, dim bool, frame int) (rune, tcell.Style) {
	// Single source of truth shared with the plan-view node projection
	// (widget.RoleStatusIcon) so the two surfaces render 1:1 (BUG-007). The
	// precedence + vocabulary live in widget; this only maps RoleView → inputs.
	// ShowsNeedsInput folds in the BuildModel subtree rollup (BUG-018); IsActive is
	// the honest live+in_progress "working" signal, never the stale hera status
	// (BUG-003).
	return widget.RoleStatusIcon(roleStatusInputs(role), dim, frame)
}

// roleStatusInputs maps a RoleView to the primitive signals the shared
// classifier switches on. Kept next to statusIcon so the rail's contract is
// visible; the plan-view projection builds the same inputs from its own RoleView.
func roleStatusInputs(role *RoleView) widget.RoleStatusInputs {
	return widget.RoleStatusInputs{
		ReadyToClose: role.ReadyToClose,
		NeedsInput:   role.ShowsNeedsInput(),
		Failed:       role.HasStatus && role.Status == db.HeraStatusFailed,
		Done:         role.HasStatus && role.Status == db.HeraStatusDone,
		Active:       role.IsActive(),
		Idle:         role.HasStatus && role.Status == db.HeraStatusIdle,
		Live:         role.Live,
	}
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

// enterFilter switches the rail into search INPUT mode. filterQuery is always
// "" here — Enter and Esc both fully clear it on exit (BUG-028-RAIL), so there is no
// "accepted but still narrowed" state to resume; every `/` press starts a fresh
// query.
func (r *Rail) enterFilter() {
	r.filterInput = true
	uxlog.Log("[hera-view] rail filter: input mode")
}

// handleFilterKey routes a keystroke while in search INPUT mode. Esc and Enter
// both fully clear the filter via ClearFilter — Enter, at the bare-Rail level,
// can't itself perform the "jump into the agent" half (that needs a HeraPage
// wrapper's Selection/OnReattach wiring — see page.go's handleRailMutation,
// which intercepts Enter FIRST and resolves the jump before calling
// ClearFilter). Up/Down move the cursor WITHIN the filtered rows without
// leaving input mode (so the operator can select a row while still
// typing/editing the query — matching the Tasks-tab `/` filter, though unlike
// it the Hera rail resolves Enter in ONE step, not two — BUG-028-RAIL); Backspace
// trims a rune; any other rune extends the query. Every query change rebuilds,
// auto-selecting the FIRST real match (never an ancestry-only heading) so the
// operator sees the top candidate live as they type.
func (r *Rail) handleFilterKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEscape:
		r.ClearFilter()
		uxlog.Log("[hera-view] rail filter: cleared")
	case tcell.KeyEnter:
		r.ClearFilter()
		uxlog.Log("[hera-view] rail filter: selected + cleared")
	case tcell.KeyDown:
		// Navigate the filtered set while typing — step() only lands on selectable
		// rows (ancestry-only headings excluded), and a narrowed buildRows contains
		// only visible rows, so the cursor can never reach a filtered-out or
		// ancestry-only row.
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

// ClearFilter exits search input mode and fully resets the query, rebuilding
// the full unfiltered rail. Shared by Esc (discard) and Enter (BUG-028-RAIL: select
// then clear) — both restore the SAME full-rail state; a HeraPage-level Enter
// just resolves the selection BEFORE calling this. Rebuilds via
// rebuildAfterFilter, which writes the cursor directly (never via setCursor),
// so clearing the filter itself never fires the selection callback nor
// persists rail state.
func (r *Rail) ClearFilter() {
	r.filterInput = false
	r.filterQuery = ""
	r.rebuildAfterFilter()
}

// rebuildAfterFilter rebuilds the rows for the current query. While the query
// still narrows the rail it auto-selects the FIRST real match (BUG-028-RAIL) so the
// operator sees the live top candidate as they type or backspace; once the
// query is empty (cleared) it re-pins the cursor by stable identity onto
// whatever row it held before, matching the pre-filter selection. Either way
// the cursor is written directly (never via setCursor), so a filter change
// neither fires the selection callback nor (per BUG-002) persists rail state —
// the filter is transient.
func (r *Rail) rebuildAfterFilter() {
	ref := r.currentRef()
	r.buildRows()
	if r.filterActive() {
		r.jumpToFirstMatch()
	} else {
		r.restoreCursor(ref)
	}
	r.clampCursor()
}

// jumpToFirstMatch pins the cursor onto the FIRST real query match in the
// narrowed rows — a coordinator/orchestrator/pinned/freelance row whose own
// name (or, for an orchestrator header, its folded-in coordinator's name)
// itself matched the query. It never lands on an ancestry-only heading
// (selectable() already excludes those) nor a structural fold header
// (Freelance/Archive expando — always selectable but never itself a text
// match). Live auto-select while typing (BUG-028-RAIL): Enter can jump straight
// into the top candidate with no separate "lock" step. A no-op (leaving the
// cursor for clampCursor to resolve) when the narrowed rows contain no real
// match.
func (r *Rail) jumpToFirstMatch() {
	for i, row := range r.rows {
		if !row.selectable() {
			continue
		}
		isMatch := false
		switch row.kind {
		case rrOrch, rrPinnedBreadcrumb, rrFreelanceRole:
			isMatch = true
		case rrRole:
			isMatch = !row.breadcrumbCont
		}
		if isMatch {
			r.cursor = i
			return
		}
	}
}
