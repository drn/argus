package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// filterModel builds a small nested tree for filter tests:
//
//	R (coord)           orch 1, coord task tr
//	  worker "alpha"    role 12
//	  worker "bridge"   role 13, bridges child C
//	    C.worker "gamma" (nested under C, which is consumed by bridge)
//	free-zeta           freelance
//	old-orch (archived) with worker "delta"
func filterModel() Model {
	return Model{
		Active: []OrchView{
			{ID: 1, Name: "R", Roles: []RoleView{
				{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tr", BridgeTaskID: "tr"},
				{RoleID: 12, OrchID: 1, Name: "alpha", Kind: db.HeraKindWorker, Live: true, TaskID: "t-alpha", BridgeTaskID: "t-alpha"},
				{RoleID: 13, OrchID: 1, Name: "bridge", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
			}},
			{ID: 2, Name: "C", Roles: []RoleView{
				{RoleID: 21, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
				{RoleID: 22, OrchID: 2, Name: "gamma", Kind: db.HeraKindWorker, Live: true, TaskID: "t-gamma", BridgeTaskID: "t-gamma"},
			}},
		},
		Freelance: []RoleView{
			{RoleID: 91, Name: "free-zeta", Kind: db.HeraKindFreelance},
		},
		Archived: []OrchView{
			{ID: 9, Name: "old-orch", Archived: true, Roles: []RoleView{
				{RoleID: 99, OrchID: 9, Name: "delta", Kind: db.HeraKindWorker, Archived: true},
			}},
		},
	}
}

func TestRail_FilterMatches(t *testing.T) {
	r := NewRail()
	cases := []struct {
		name  string
		query string
		want  bool
	}{
		{"alpha", "", true},             // empty query matches all
		{"alpha", "   ", true},          // whitespace-only matches all
		{"alpha", "alp", true},          // substring
		{"Alpha", "alp", true},          // case-insensitive
		{"alpha", "ALP", true},          // case-insensitive query
		{"alpha", "x", false},           // no match
		{"alpha-beta", "alp bet", true}, // every term must match (AND)
		{"alpha-beta", "alp zzz", false},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.query, func(t *testing.T) {
			r.filterQuery = c.query
			testutil.Equal(t, r.filterMatches(c.name), c.want)
		})
	}
}

func TestRail_FilterNarrowsAndPreservesAncestry(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// Match only the deeply-nested "gamma": its parent coordinator R and the
	// bridging "bridge" row (and child C's workers) must remain visible.
	r.filterQuery = "gamma"
	r.buildRows()

	testutil.Equal(t, r.hasOrchHeader("R"), true) // ancestry kept
	testutil.Equal(t, r.depthOf("gamma") >= 0, true)
	testutil.Equal(t, r.depthOf("bridge") >= 0, true) // bridging parent kept
	// Non-matching sibling "alpha" is hidden.
	testutil.Equal(t, r.depthOf("alpha"), -1)
	// Freelance + Archive sections prune entirely (no member matches "gamma").
	for _, row := range r.rows {
		testutil.Equal(t, row.kind == rrSectionHeader && row.label == "Pinned", false)
		if row.kind == rrSectionHeader {
			testutil.Equal(t, row.collFreelance, false) // no Freelance header
		}
		testutil.Equal(t, row.kind == rrArchiveExpando, false) // no bottom Archive
	}
}

func TestRail_FilterMatchesDirectWorker(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	r.filterQuery = "alpha"
	r.buildRows()

	testutil.Equal(t, r.hasOrchHeader("R"), true)
	testutil.Equal(t, r.depthOf("alpha") >= 0, true)
	testutil.Equal(t, r.depthOf("gamma"), -1) // sibling subtree hidden
	// "bridge" matches neither "alpha" nor keeps a visible child → hidden.
	testutil.Equal(t, r.depthOf("bridge"), -1)
}

func TestRail_FilterAutoExpandsAndRestoresFold(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// Collapse R; its children vanish.
	r.collapsed[1] = true
	r.buildRows()
	testutil.Equal(t, r.depthOf("alpha"), -1)

	// A filter matching a child force-expands R despite the fold.
	r.filterQuery = "alpha"
	r.buildRows()
	testutil.Equal(t, r.depthOf("alpha") >= 0, true)

	// The persisted fold map is UNCHANGED — clearing the filter re-collapses R.
	testutil.Equal(t, r.collapsed[1], true)
	r.filterQuery = ""
	r.buildRows()
	testutil.Equal(t, r.depthOf("alpha"), -1)
}

func TestRail_FilterPrunesFreelanceAndArchiveHeaders(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// "zeta" matches only the freelancer → Freelance header shows, others pruned.
	r.filterQuery = "zeta"
	r.buildRows()
	freelanceHeader, freelanceRole := false, false
	for _, row := range r.rows {
		if row.kind == rrSectionHeader && row.collFreelance {
			freelanceHeader = true
		}
		if row.kind == rrFreelanceRole && row.role.Name == "free-zeta" {
			freelanceRole = true
		}
	}
	testutil.Equal(t, freelanceHeader, true)
	testutil.Equal(t, freelanceRole, true)
	testutil.Equal(t, r.hasOrchHeader("R"), false) // no orch matches "zeta"

	// "delta" matches only the archived worker → bottom Archive expando shows and
	// auto-expands to reveal the archived orchestrator + its role.
	r.filterQuery = "delta"
	r.buildRows()
	archiveExpando := false
	for _, row := range r.rows {
		if row.kind == rrArchiveExpando && row.collArchive {
			archiveExpando = true
		}
	}
	testutil.Equal(t, archiveExpando, true)
	testutil.Equal(t, r.depthOf("delta") >= 0, true) // auto-expanded
}

// TestRail_FilterEscClears pins Esc's discard behavior: full reset, no
// selection carried through the clear beyond identity-based cursor restore.
func TestRail_FilterEscClears(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), true)

	for _, ru := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, r.filterQuery, "alpha")
	testutil.Equal(t, r.depthOf("alpha") >= 0, true)
	testutil.Equal(t, r.depthOf("gamma"), -1)

	// Backspace trims a rune.
	h(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.filterQuery, "alph")

	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), false)
	testutil.Equal(t, r.filterQuery, "")
	testutil.Equal(t, r.depthOf("gamma") >= 0, true) // full rail restored

	// Re-opening `/` always starts a fresh (empty) query — there is no
	// "preserved for editing" state anymore (BUG-028-RAIL: Enter/Esc both fully
	// clear).
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), true)
	testutil.Equal(t, r.filterQuery, "")
}

// TestRail_FilterEnterSelectsAndClears pins the NEW one-Enter behavior at the
// bare-Rail level (BUG-028-RAIL, supersedes the old "Enter accepts, query stays,
// input mode off" two-step). A bare Rail (no HeraPage wrapper) can't fire the
// reattach half itself, but it MUST still fully clear the filter — query reset
// AND input mode off — in a SINGLE Enter, exactly like Esc, while re-pinning the
// cursor by identity onto the row that was selected under the filter so a
// HeraPage-level caller (which resolves Selection() BEFORE clearing) sees the
// right target.
func TestRail_FilterEnterSelectsAndClears(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, r.Filtering(), true)
	sel := r.Selected()
	testutil.Equal(t, sel != nil && sel.Name == "alpha", true)

	// A SINGLE Enter — no separate "lock" step.
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), false)
	testutil.Equal(t, r.filterQuery, "")
	testutil.Equal(t, r.depthOf("gamma") >= 0, true) // full rail restored

	// The cursor still rests on "alpha" (re-pinned by stable identity across
	// the clear-triggered rebuild).
	sel2 := r.Selected()
	testutil.Equal(t, sel2 != nil && sel2.Name == "alpha", true)
}

func TestRail_FilterArrowsNavigateWhileTyping(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	// Enter filter mode and type a query that keeps MULTIPLE real (non
	// ancestry-only) matches visible: "a" matches "alpha", "gamma", the
	// freelancer "free-zeta", and the archived "delta" — none of the headers
	// on the path to them (R, C's bridging "bridge" row, "old-orch") contain
	// "a" themselves, so they render as ancestry-only (non-selectable) and are
	// skipped by navigation.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "a" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, r.Filtering(), true)
	if r.Rows() < 2 {
		t.Fatalf("filtered set too small to navigate: %d rows", r.Rows())
	}

	// The cursor starts on the first REAL match — "alpha" — auto-selected live
	// as the operator typed (BUG-028-RAIL), not merely "some selectable row".
	testutil.Equal(t, r.rows[r.CursorIndex()].selectable(), true)
	sel := r.Selected()
	testutil.Equal(t, sel != nil && sel.Name == "alpha", true)

	// Down arrow navigates WITHIN the filtered set without leaving input mode,
	// landing only on real matches — never the ancestry-only "R" header or the
	// ancestry-only bridging "bridge" row.
	moved := false
	for i := 0; i < r.Rows(); i++ {
		before := r.CursorIndex()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		if r.CursorIndex() != before {
			moved = true
		}
		testutil.Equal(t, r.Filtering(), true)
		row := r.rows[r.CursorIndex()]
		testutil.Equal(t, row.selectable(), true)
		testutil.Equal(t, row.ancestryOnly, false)
	}
	testutil.Equal(t, moved, true)

	// Up arrow likewise navigates back up while still typing.
	atBottom := r.CursorIndex()
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	if r.CursorIndex() == atBottom {
		t.Error("Up did not move the cursor within the filtered set")
	}
	testutil.Equal(t, r.Filtering(), true)

	// Typing still extends the query after navigating (arrows didn't break input).
	h(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone), noFocus)
	testutil.Equal(t, r.filterQuery, "ax")
}

// TestRail_FilterAncestryOnlyHeaderNotSelectable pins the core BUG-028-RAIL
// invariant: a coordinator/orchestrator heading kept on screen ONLY for
// ancestry (its own name — and its folded-in coordinator's name — do not match
// the query) renders ancestryOnly and is never selectable.
func TestRail_FilterAncestryOnlyHeaderNotSelectable(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// "gamma" matches only the deeply-nested worker. R's header (own name "R",
	// coordinator name "coord") matches neither.
	r.filterQuery = "gamma"
	r.buildRows()

	var headerRow *railRow
	for i := range r.rows {
		if r.rows[i].kind == rrOrch && r.rows[i].orch.Name == "R" {
			headerRow = &r.rows[i]
		}
	}
	testutil.Equal(t, headerRow != nil, true)
	testutil.Equal(t, headerRow.ancestryOnly, true)
	testutil.Equal(t, headerRow.selectable(), false)

	// The bridging "bridge" row is likewise ancestry-only: its own name doesn't
	// match "gamma" either, it's only visible because it bridges to visible C.
	var bridgeRow *railRow
	for i := range r.rows {
		if r.rows[i].role != nil && r.rows[i].role.Name == "bridge" {
			bridgeRow = &r.rows[i]
		}
	}
	testutil.Equal(t, bridgeRow != nil, true)
	testutil.Equal(t, bridgeRow.ancestryOnly, true)
	testutil.Equal(t, bridgeRow.selectable(), false)

	// "gamma" itself is a real match — selectable, not ancestry-only.
	var gammaRow *railRow
	for i := range r.rows {
		if r.rows[i].role != nil && r.rows[i].role.Name == "gamma" {
			gammaRow = &r.rows[i]
		}
	}
	testutil.Equal(t, gammaRow != nil, true)
	testutil.Equal(t, gammaRow.ancestryOnly, false)
	testutil.Equal(t, gammaRow.selectable(), true)
}

// TestRail_FilterArrowNavSkipsAncestryOnlyRows confirms arrow nav has nowhere
// else to go when the ONLY other rows in the narrowed set are ancestry-only —
// the cursor stays pinned on the sole real match rather than landing on a
// heading kept only for context.
func TestRail_FilterArrowNavSkipsAncestryOnlyRows(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "gamma" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	sel := r.Selected()
	testutil.Equal(t, sel != nil && sel.Name == "gamma", true)

	before := r.CursorIndex()
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), before)
	testutil.Equal(t, r.rows[r.CursorIndex()].role.Name, "gamma")

	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), before)
	testutil.Equal(t, r.rows[r.CursorIndex()].role.Name, "gamma")
}

// TestRail_FilterOrchestratorOwnNameIsRealMatch pins the header own-match rule
// (BUG-028-RAIL): the coordinator is folded into the orchestrator header with no
// separate row, so a query matching the coordinator's NAME must count as the
// header's own match, not ancestry — otherwise searching a coordinator by name
// could never select its header directly.
func TestRail_FilterOrchestratorOwnNameIsRealMatch(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// "co" matches the coordinator role's name ("coord") in BOTH orchestrators.
	r.filterQuery = "co"
	r.buildRows()

	found := false
	for i := range r.rows {
		if r.rows[i].kind == rrOrch && r.rows[i].orch.Name == "R" {
			found = true
			testutil.Equal(t, r.rows[i].ancestryOnly, false)
			testutil.Equal(t, r.rows[i].selectable(), true)
		}
	}
	testutil.Equal(t, found, true)
}

// TestRail_FilterAutoSelectsFirstRealMatchWhileTyping pins the live
// auto-select behavior (BUG-028-RAIL): typing a query that narrows to a sole real
// match selects it immediately, with no arrow key needed.
func TestRail_FilterAutoSelectsFirstRealMatchWhileTyping(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	sel := r.Selected()
	testutil.Equal(t, sel != nil && sel.Name == "alpha", true)
}

func TestRail_FilterInputLineAndTitleRender(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 12)

	r := NewRail()
	r.SetFocused(true)
	r.SetModel(filterModel())
	r.SetRect(0, 0, 40, 12)

	h := r.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "alp" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	r.Draw(sim)
	sim.Show()

	// The `/ alp` input line renders on the first inner row (y=1, inside border).
	testutil.Contains(t, rowText(sim, 1, 40), "/ alp▌")

	// The query reflects in the border title (row 0).
	testutil.Contains(t, rowText(sim, 0, 40), "/alp")
}

func TestPage_MutationKeysAreFilterInputWhileTyping(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "worker", db.HeraKindWorker, "t-worker")
	p := NewHeraPage(d)
	p.Refresh()

	spawned, renamed, archived, pinned, deleted, stepped := 0, 0, 0, 0, 0, 0
	p.OnSpawnWorker = func(Selection) { spawned++ }
	p.OnRename = func(Selection) { renamed++ }
	p.OnArchiveToggle = func(Selection) { archived++ }
	p.OnPinToggle = func(Selection) { pinned++ }
	p.OnDelete = func(Selection) { deleted++ }
	p.OnStatusAdvance = func(Selection) { stepped++ }

	h := p.InputHandler()
	// `/` enters input mode (consumed by the rail, not a mutation).
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), true)

	// Mutation rune-keys typed while filtering append to the query and fire NO
	// callback (covers w/a/r/P and the status-step `s`).
	for _, ru := range "warPs" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, p.Rail().filterQuery, "warPs")

	// Ctrl+D (the destructive delete/cascade) must ALSO be suppressed while
	// typing — its handleRailMutation branch bails before Selection().
	h(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl), noFocus)

	testutil.Equal(t, spawned, 0)
	testutil.Equal(t, renamed, 0)
	testutil.Equal(t, archived, 0)
	testutil.Equal(t, pinned, 0)
	testutil.Equal(t, deleted, 0)
	testutil.Equal(t, stepped, 0)

	// Enter accepts — normal mutation routing resumes.
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), false)
}

// TestPage_FilterArrowNavigateThenEnterSelects pins the NEW one-Enter behavior
// at the HeraPage level (BUG-028-RAIL, supersedes the old two-Enter "commit then
// reattach" flow): typing auto-selects the first real match live, Up/Down move
// within the narrowed set while still typing, and a SINGLE Enter reattaches
// into the current selection AND clears the filter together.
func TestPage_FilterArrowNavigateThenEnterSelects(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "team")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkalpha", db.HeraKindWorker, "t-alpha")
	seedBoundRole(t, d, orch, "wkbeta", db.HeraKindWorker, "t-beta")
	p := NewHeraPage(d)
	p.Refresh()

	var reattached Selection
	gotReattach := 0
	p.OnReattach = func(s Selection) { reattached = s; gotReattach++ }

	h := p.InputHandler()
	// Enter filter mode; "wk" narrows to the two workers — the "team" header
	// (own name "team", coordinator name "coord") matches neither, so it
	// renders as an ancestry-only heading and is skipped by both auto-select
	// and arrow nav.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "wk" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, p.RailFiltering(), true)

	// Typing alone already auto-selected the FIRST real match — a worker —
	// live, with no arrow press needed (BUG-028-RAIL).
	sel := p.Rail().Selection()
	testutil.Equal(t, sel.Role != nil, true)
	testutil.Equal(t, sel.Role.Kind, db.HeraKindWorker)

	// Down arrow moves within the narrowed real matches, staying in input mode.
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), true)
	sel2 := p.Rail().Selection()
	testutil.Equal(t, sel2.Role != nil && sel2.Role.Kind == db.HeraKindWorker, true)

	// A SINGLE Enter selects the current match, jumps into it (reattach), and
	// clears the filter — no second Enter (BUG-028-RAIL).
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), false)
	testutil.Equal(t, gotReattach, 1)
	testutil.Equal(t, reattached.Role != nil && reattached.Role.Kind == db.HeraKindWorker, true)
}

// TestPage_FilterEnterJumpsIntoSoleOrchestratorMatch is the concrete BUG-028-RAIL
// acceptance scenario: typing a query that narrows to a coordinator's OWN
// orchestrator name (e.g. "/bugb" for "hera-bugbash") auto-selects that
// orchestrator's header live, and a single Enter jumps straight into it.
func TestPage_FilterEnterJumpsIntoSoleOrchestratorMatch(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "hera-bugbash")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "worker", db.HeraKindWorker, "t-worker")
	p := NewHeraPage(d)
	p.Refresh()

	var reattached Selection
	gotReattach := 0
	p.OnReattach = func(s Selection) { reattached = s; gotReattach++ }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "bugb" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, p.RailFiltering(), true)

	// "bugb" matches the orchestrator's own name — the header is a real match
	// (not ancestry-only), auto-selected live by typing alone.
	sel := p.Rail().Selection()
	testutil.Equal(t, sel.Orch != nil && sel.Orch.Name == "hera-bugbash", true)
	testutil.Equal(t, sel.Role == nil, true) // header selection = coordinator

	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), false)
	testutil.Equal(t, p.Rail().filterQuery, "")
	testutil.Equal(t, gotReattach, 1)
	testutil.Equal(t, reattached.Orch != nil && reattached.Orch.Name == "hera-bugbash", true)
}

func TestRail_FilterMatchesBridgeWorkerOnly(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())

	// Query matches ONLY the bridging worker's own name ("bridge"); its child
	// orchestrator C / worker gamma do not match. The bridge row stays visible via
	// its own match, but with no visible child it drops its fold chevron
	// (collOrchID == 0) and gamma is hidden.
	r.filterQuery = "bridge"
	r.buildRows()

	testutil.Equal(t, r.hasOrchHeader("R"), true)
	testutil.Equal(t, r.depthOf("bridge") >= 0, true)
	testutil.Equal(t, r.depthOf("gamma"), -1)

	var bridgeRow *railRow
	for i := range r.rows {
		if r.rows[i].role != nil && r.rows[i].role.Name == "bridge" {
			bridgeRow = &r.rows[i]
		}
	}
	testutil.Equal(t, bridgeRow != nil, true)
	testutil.Equal(t, bridgeRow.collOrchID, int64(0)) // chevron dropped (no visible child)
}

func TestRail_FilterCycleSafe(t *testing.T) {
	// A↔B mutually bridge. computeVisible's in-progress guard must break the
	// cycle so a filtered buildRows terminates and still produces rows. A query
	// matching worker "wa" forces the recursion A→B→A (the cycle edge) during A's
	// own visibility computation.
	a := orchView(1, "A", "ta", wk("wa", "tb"))
	b := orchView(2, "B", "tb", wk("wb", "ta"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{a, b}})

	r.filterQuery = "wa"
	r.buildRows() // must not hang
	if r.Rows() == 0 {
		t.Error("filtered cycle produced no rows")
	}
	testutil.Equal(t, r.depthOf("wa") >= 0, true)
}

func TestPage_SlashInPaneDoesNotEnterFilter(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "worker", db.HeraKindWorker, "t-worker")
	p := NewHeraPage(d)
	p.Refresh()
	h := p.InputHandler()

	// Move focus into the coordinator pane, then `/` must forward to the PTY (a
	// no-op with no live session) and MUST NOT enter rail filter mode.
	p.Machine().Advance()
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), false)
}

// rowText reads a screen row into a trimmed-right string (test helper).
func rowText(sim tcell.SimulationScreen, y, w int) string {
	out := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		s, _, _ := sim.Get(x, y)
		if s == "" {
			out = append(out, ' ')
			continue
		}
		out = append(out, []rune(s)[0])
	}
	// trim trailing spaces
	end := len(out)
	for end > 0 && out[end-1] == ' ' {
		end--
	}
	return string(out[:end])
}
