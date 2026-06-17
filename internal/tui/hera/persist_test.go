package hera

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// fakeStore is an in-memory RailStateStore for persistence tests: it returns a
// canned load blob and captures every save.
type fakeStore struct {
	load    string
	loadErr error
	saveErr error
	saved   []string
}

func (f *fakeStore) LoadRailState() (string, error) { return f.load, f.loadErr }
func (f *fakeStore) SaveRailState(s string) error {
	f.saved = append(f.saved, s)
	return f.saveErr
}

func (f *fakeStore) last() railViewState {
	var st railViewState
	if len(f.saved) > 0 {
		_ = json.Unmarshal([]byte(f.saved[len(f.saved)-1]), &st)
	}
	return st
}

func TestRail_StateStoreRestores(t *testing.T) {
	fs := &fakeStore{load: `{"collapsed":[2],"coord_archive_open":[2],"freelance_collapsed":true,"archive_collapsed":false,"selection_ref":12}`}
	r := NewRail()
	r.SetStateStore(fs)

	// Fold maps + section booleans apply immediately (stable ids, pre-model).
	testutil.Equal(t, r.collapsed[2], true)
	testutil.Equal(t, r.coordArchiveOpen[2], true)
	testutil.Equal(t, r.freelanceCollap, true)
	testutil.Equal(t, r.archiveCollapsed, false)
	testutil.Equal(t, r.pendingSelRef, int64(12))

	// The selection ref applies on the first model build (role 12 == wkr in
	// twoOrchModel; orch-1 is expanded since only orch-2 is collapsed).
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Selected().Name, "wkr")
	testutil.Equal(t, r.pendingSelRef, int64(0)) // consumed
}

func TestRail_StateStoreMalformedKeepsDefaults(t *testing.T) {
	// Malformed blobs keep the expanded default (not the first-run-collapse
	// path). Empty/whitespace-only blobs are the first-run case and trigger
	// collapse; genuinely malformed JSON does not.
	for _, blob := range []string{"not json", "{"} {
		t.Run("blob="+blob, func(t *testing.T) {
			fs := &fakeStore{load: blob}
			r := NewRail()
			r.SetStateStore(fs)
			testutil.Equal(t, r.archiveCollapsed, true)
			testutil.Equal(t, len(r.collapsed), 0) // no collapse seeded yet
			testutil.Equal(t, r.pendingSelRef, int64(0))
			testutil.Equal(t, r.firstRunCollapse, false) // NOT first-run path
			// After model load, orchestrators stay expanded (malformed ≠ first run).
			r.SetModel(twoOrchModel())
			testutil.Equal(t, r.collapsed[1], false)
			testutil.Equal(t, r.collapsed[2], false)
		})
	}
	// A load error also keeps defaults (logged, not fatal).
	fs := &fakeStore{loadErr: errors.New("boom")}
	r := NewRail()
	r.SetStateStore(fs)
	testutil.Equal(t, r.archiveCollapsed, true)
	testutil.Equal(t, r.firstRunCollapse, false)
}

func TestRail_FirstRunFullyCollapsed(t *testing.T) {
	// No saved state (empty blob) → rail starts fully collapsed on first model
	// build (BUG-009 fix). Both empty string and whitespace-only trigger this.
	for _, blob := range []string{"", "   "} {
		t.Run("blob="+blob, func(t *testing.T) {
			fs := &fakeStore{load: blob}
			r := NewRail()
			r.SetStateStore(fs)
			testutil.Equal(t, r.firstRunCollapse, true)
			testutil.Equal(t, len(r.collapsed), 0) // not yet seeded (no model)

			r.SetModel(twoOrchModel())
			// Both orchestrators from twoOrchModel (ids 1 and 2) must be collapsed.
			testutil.Equal(t, r.collapsed[1], true)
			testutil.Equal(t, r.collapsed[2], true)
			testutil.Equal(t, r.firstRunCollapse, false) // one-shot: consumed
		})
	}
}

func TestRail_FirstRunCollapseIsOneShot(t *testing.T) {
	// After the first non-empty model build, later rebuilds do NOT re-collapse
	// orchestrators that the user expanded.
	fs := &fakeStore{load: ""}
	r := NewRail()
	r.SetStateStore(fs)
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.collapsed[1], true) // seeded on first build

	// User expands orch-1.
	delete(r.collapsed, 1)
	// Rebuild (e.g. a tick refresh) must NOT re-collapse orch-1.
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.collapsed[1], false) // stays expanded
	testutil.Equal(t, r.collapsed[2], true)  // untouched
}

func TestRail_FirstRunNoStoreNoCollapse(t *testing.T) {
	// Without a store, no first-run collapse fires (remote mode / unset store).
	r := NewRail()
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.collapsed[1], false)
	testutil.Equal(t, r.collapsed[2], false)
}

func TestRail_PersistsOnToggleAndCursorMove(t *testing.T) {
	// Use a non-empty blob (valid prior state) so this is NOT a first-run — both
	// orchestrators start expanded and we test the toggle/cursor persist paths.
	fs := &fakeStore{load: `{"archive_collapsed":true}`}
	r := NewRail()
	r.SetStateStore(fs) // restores archive_collapsed=true, no first-run flag
	r.SetModel(twoOrchModel())
	testutil.Equal(t, len(fs.saved), 0) // SetModel/restore never persists

	// Collapse orch-1 (cursor starts on its header) → one save reflecting the fold.
	r.ToggleCollapse()
	if len(fs.saved) == 0 {
		t.Fatal("ToggleCollapse did not persist")
	}
	testutil.DeepEqual(t, fs.last().Collapsed, []int64{1})

	// A cursor move persists the new selection.
	n := len(fs.saved)
	r.CursorDown() // orch-1 collapsed → next selectable is orch-2 header
	if len(fs.saved) <= n {
		t.Fatal("cursor move did not persist")
	}
	testutil.Equal(t, fs.last().SelectionRef, int64(-2)) // -orch.ID for a header
}

func TestRail_SelectionRestoreIsOneShot(t *testing.T) {
	fs := &fakeStore{load: `{"selection_ref":12}`}
	r := NewRail()
	r.SetStateStore(fs)
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Selected().Name, "wkr") // restored

	// Move the cursor off the restored row, then rebuild — the live cursor wins,
	// the rebuild must NOT snap back to the persisted ref.
	r.CursorDown()
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
}

func TestRail_NilStoreNoPersist(t *testing.T) {
	r := NewRail() // no store wired
	r.SetModel(twoOrchModel())
	r.ToggleCollapse() // must not panic
	r.CursorDown()
	r.SetStateStore(nil) // explicit nil also safe
	r.ToggleCollapse()
}

func TestRail_FilterDoesNotPersist(t *testing.T) {
	fs := &fakeStore{}
	r := NewRail()
	r.SetStateStore(fs)
	r.SetModel(filterModel())
	testutil.Equal(t, len(fs.saved), 0)

	// Activating, typing, and clearing the `/` filter rebuilds via clampCursor
	// (direct cursor write), never setCursor — so no rail-state save fires. Filter
	// state is transient by design.
	h := r.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus)
	for _, ru := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, ru, tcell.ModNone), noFocus)
	}
	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, len(fs.saved), 0)
}

func TestPage_RailStatePersistsAcrossPages(t *testing.T) {
	d := memDB(t)
	o1 := seedOrch(t, d, "orch-a")
	seedBoundRole(t, d, o1, "coord", db.HeraKindCoordinator, "ta")
	seedBoundRole(t, d, o1, "wkr", db.HeraKindWorker, "tw")
	o2 := seedOrch(t, d, "orch-b")
	seedBoundRole(t, d, o2, "coord", db.HeraKindCoordinator, "tb")

	// Pre-seed a minimal prior-session blob so this test exercises the
	// restore-and-persist path, not first-run behaviour (first-run collapse is
	// covered by TestRail_FirstRunFullyCollapsed and
	// TestRail_FirstRunCollapseIsOneShot).
	if err := d.SaveRailState(`{"archive_collapsed":true}`); err != nil {
		t.Fatal(err)
	}

	// Page 1: wire the real *db.DB store, collapse the first orchestrator.
	p1 := NewHeraPage(d)
	p1.SetRailStateStore(d)
	p1.Refresh()
	testutil.Equal(t, p1.Rail().collapsed[o1], false)
	p1.Rail().ToggleCollapse() // cursor starts on orch-a's header → collapse + persist
	testutil.Equal(t, p1.Rail().collapsed[o1], true)

	// Page 2: a fresh page against the same DB restores the persisted fold.
	p2 := NewHeraPage(d)
	p2.SetRailStateStore(d)
	p2.Refresh()
	testutil.Equal(t, p2.Rail().collapsed[o1], true)
}
