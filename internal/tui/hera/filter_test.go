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

func TestRail_FilterEscClearsEnterAccepts(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	// `/` enters input mode.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), true)

	// Type "alpha".
	for _, ru := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, r.filterQuery, "alpha")
	testutil.Equal(t, r.depthOf("alpha") >= 0, true)
	testutil.Equal(t, r.depthOf("gamma"), -1)

	// Backspace trims a rune.
	h(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.filterQuery, "alph")

	// Enter accepts: input mode off, query stays, rail stays narrowed.
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), false)
	testutil.Equal(t, r.filterQuery, "alph")
	testutil.Equal(t, r.depthOf("alpha") >= 0, true)

	// j/k now navigate the filtered set (normal nav resumed).
	before := r.CursorIndex()
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	if r.CursorIndex() == before && r.Rows() > 1 {
		t.Error("j did not move the cursor within the filtered set")
	}

	// Re-open `/` preserves the query for editing, then Esc clears everything.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), true)
	testutil.Equal(t, r.filterQuery, "alph")
	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.Filtering(), false)
	testutil.Equal(t, r.filterQuery, "")
	testutil.Equal(t, r.depthOf("gamma") >= 0, true) // full rail restored
}

func TestRail_FilterArrowsNavigateWhileTyping(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	h := r.InputHandler()

	// Enter filter mode and type a query that keeps MULTIPLE selectable rows
	// visible: "co" matches the "coord" role in both orchestrators, so their
	// headers (ancestry) plus the matching roles all survive the narrow.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "co" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, r.Filtering(), true)
	if r.Rows() < 2 {
		t.Fatalf("filtered set too small to navigate: %d rows", r.Rows())
	}

	// The cursor starts on a visible, selectable filtered-in row.
	testutil.Equal(t, r.rows[r.CursorIndex()].selectable(), true)

	// Down arrow navigates WITHIN the filtered set without leaving input mode —
	// this is the fix (previously Down was ignored while typing, so the operator
	// could never move the selection into the filtered list).
	moved := false
	for i := 0; i < r.Rows(); i++ {
		before := r.CursorIndex()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		if r.CursorIndex() != before {
			moved = true
		}
		// Input mode stays active (query remains editable) and the cursor only
		// ever rests on a selectable row that survived the filter.
		testutil.Equal(t, r.Filtering(), true)
		testutil.Equal(t, r.rows[r.CursorIndex()].selectable(), true)
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
	testutil.Equal(t, r.filterQuery, "cox")
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
	// Enter filter mode; "wk" narrows to the two workers (plus their orch
	// ancestry header) — "coord" does not match.
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "wk" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	testutil.Equal(t, p.RailFiltering(), true)

	// Down arrow walks the filtered rows WHILE typing until the selection lands
	// on a worker role — the previously-impossible "navigate into the filtered
	// list" path.
	for i := 0; i < p.Rail().Rows(); i++ {
		if role := p.Rail().Selection().Role; role != nil && role.Kind == db.HeraKindWorker {
			break
		}
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	}
	sel := p.Rail().Selection()
	testutil.Equal(t, sel.Role != nil, true)
	testutil.Equal(t, sel.Role.Kind, db.HeraKindWorker)
	// Navigating did not exit input mode (query stays editable).
	testutil.Equal(t, p.RailFiltering(), true)

	// Enter commits the filter (exits input mode); a SECOND Enter acts on the
	// navigated row (reattach), mirroring the Tasks-tab `/` filter.
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.RailFiltering(), false)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, gotReattach, 1)
	testutil.Equal(t, reattached.Role != nil && reattached.Role.Kind == db.HeraKindWorker, true)
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
