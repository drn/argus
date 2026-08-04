package hera

import (
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// openFileDB opens a real, file-backed *db.DB in a fresh t.TempDir() — never
// touching real ~/.argus/. Unlike memDB's in-memory connection, a file-backed
// DB can be opened MULTIPLE times against the same path, which is required to
// exercise a genuine cross-connection PRAGMA data_version change (an
// in-memory `:memory:` DSN gives each connection its own private database).
func openFileDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.sql")
	d, err := db.Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// noFingerprintReader wraps a HeraReader so its OWN method set is exactly
// HeraReader's declared methods — DataVersion is NOT promoted even though the
// wrapped value implements it (Go only promotes an embedded INTERFACE
// field's declared methods, never the dynamic value's full method set beyond
// that interface). Mirrors the remote-mode nil reader and any HeraReader test
// double that never grows a DataVersion method.
type noFingerprintReader struct{ HeraReader }

// TestHeraPage_ShouldRebuild_FirstCallAlwaysTrue: no prior snapshot exists yet.
func TestHeraPage_ShouldRebuild_FirstCallAlwaysTrue(t *testing.T) {
	p := NewHeraPage(openFileDB(t))
	testutil.Equal(t, p.shouldRebuild(), true)
}

// TestHeraPage_ShouldRebuild_QuiescentSkipsAfterMarkRebuilt proves the core
// gate: once markRebuilt has snapshotted a rebuild, a second call with
// nothing changed reports false.
func TestHeraPage_ShouldRebuild_QuiescentSkipsAfterMarkRebuilt(t *testing.T) {
	p := NewHeraPage(openFileDB(t))
	testutil.Equal(t, p.shouldRebuild(), true)
	p.markRebuilt()
	testutil.Equal(t, p.shouldRebuild(), false)
	// Repeated calls with nothing changed stay false.
	testutil.Equal(t, p.shouldRebuild(), false)
}

// TestHeraPage_ShouldRebuild_DBFingerprintChangeTriggersRebuild proves a
// cross-connection DB write (a genuinely different data_version, the kind a
// daemon-driven hera mutation would produce) flips the gate back to true.
func TestHeraPage_ShouldRebuild_DBFingerprintChangeTriggersRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sql")
	reader, err := db.Open(path)
	testutil.NoError(t, err)
	defer func() { _ = reader.Close() }()
	writer, err := db.Open(path)
	testutil.NoError(t, err)
	defer func() { _ = writer.Close() }()

	p := NewHeraPage(reader)
	testutil.Equal(t, p.shouldRebuild(), true)
	p.markRebuilt()
	testutil.Equal(t, p.shouldRebuild(), false)

	testutil.NoError(t, writer.Add(&model.Task{Name: "cross-conn"}))

	testutil.Equal(t, p.shouldRebuild(), true)
}

// TestHeraPage_ShouldRebuild_RuntimeMapChangesTriggerRebuild proves each of
// the four per-tick runtime maps, changed INDIVIDUALLY with the DB
// fingerprint held stable, flips the gate — DB-only gating would freeze the
// rail's spinner/needs-input glyphs whenever the DB itself is quiet.
func TestHeraPage_ShouldRebuild_RuntimeMapChangesTriggerRebuild(t *testing.T) {
	cases := []struct {
		name  string
		apply func(p *HeraPage)
	}{
		{"needsInput", func(p *HeraPage) { p.SetNeedsInput([]string{"t1"}) }},
		{"sessionIdle", func(p *HeraPage) { p.SetSessionIdle([]string{"t1"}) }},
		{"sessionRunning", func(p *HeraPage) { p.SetSessionRunning([]string{"t1"}) }},
		{"sustainedActive", func(p *HeraPage) { p.SetSustainedActive([]string{"t1"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHeraPage(openFileDB(t))
			testutil.Equal(t, p.shouldRebuild(), true)
			p.markRebuilt()
			testutil.Equal(t, p.shouldRebuild(), false)

			tc.apply(p)

			testutil.Equal(t, p.shouldRebuild(), true)
			p.markRebuilt()
			testutil.Equal(t, p.shouldRebuild(), false)
		})
	}
}

// TestHeraPage_ShouldRebuild_UnsupportedFingerprintAlwaysRebuilds proves a
// reader without a DataVersion method (any HeraReader test double, or the
// remote-mode nil reader) is always treated as "changed" — the gate must
// never suppress a rebuild it cannot prove is safe to skip.
func TestHeraPage_ShouldRebuild_UnsupportedFingerprintAlwaysRebuilds(t *testing.T) {
	p := NewHeraPage(noFingerprintReader{HeraReader: memDB(t)})
	testutil.Equal(t, p.shouldRebuild(), true)
	p.markRebuilt()
	testutil.Equal(t, p.shouldRebuild(), true) // still true — nothing "proved" safe
}

// TestHeraPage_ShouldRebuild_RemoteModeAlwaysRebuilds: a nil reader (remote
// mode) has no fingerprint either, and must degrade identically.
func TestHeraPage_ShouldRebuild_RemoteModeAlwaysRebuilds(t *testing.T) {
	p := NewHeraPage(nil)
	testutil.Equal(t, p.shouldRebuild(), true)
	p.markRebuilt()
	testutil.Equal(t, p.shouldRebuild(), true)
}

// TestHeraPage_InvalidateChangeGate proves InvalidateChangeGate forces the
// NEXT shouldRebuild call to report true even with a stable fingerprint and
// unchanged runtime maps — the mechanism Refresh() uses to guarantee its own
// "forces an immediate rebuild" contract regardless of the gate.
func TestHeraPage_InvalidateChangeGate(t *testing.T) {
	p := NewHeraPage(openFileDB(t))
	testutil.Equal(t, p.shouldRebuild(), true)
	p.markRebuilt()
	testutil.Equal(t, p.shouldRebuild(), false)

	p.InvalidateChangeGate()

	testutil.Equal(t, p.shouldRebuild(), true)
}

// TestHeraPage_Refresh_ForcesRebuildDespiteSameConnectionBlindSpot is the
// end-to-end regression for design.md Decision 5: a hera mutation written
// through the SAME connection this page reads from does NOT bump the
// fingerprint as this page's own next read reports it (the documented
// same-connection blind spot, confirmed directly below) — yet Refresh()
// still performs a full rebuild that picks it up, because Refresh() itself
// invalidates the gate before flushing.
func TestHeraPage_Refresh_ForcesRebuildDespiteSameConnectionBlindSpot(t *testing.T) {
	d := openFileDB(t)
	p := NewHeraPage(d)
	p.Refresh()
	before := len(p.Rail().Model().Active) + len(p.Rail().Model().Pinned)
	testutil.Equal(t, before, 0)

	fpBefore := p.lastFingerprint
	seedBoundRole(t, d, seedOrch(t, d, "orch"), "coord", db.HeraKindCoordinator, "t-coord")

	// Confirm the blind spot is real for this exact scenario: reading the
	// fingerprint back through the SAME connection (d, == p.reader) does NOT
	// show the write that connection itself just made.
	fpAfter, ok := p.dbFingerprint()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, fpAfter, fpBefore)

	// Refresh() must still pick up the change.
	p.Refresh()
	after := len(p.Rail().Model().Active) + len(p.Rail().Model().Pinned)
	testutil.Equal(t, after >= 1, true)
}

// coordRoleView finds the "coord"-named role in m's first Active orchestrator
// (test helper for asserting on rebuilt-model content).
func coordRoleView(t *testing.T, m Model) RoleView {
	t.Helper()
	for _, rv := range m.Active[0].Roles {
		if rv.Name == "coord" {
			return rv
		}
	}
	t.Fatal("coord role not found in rebuilt model")
	return RoleView{}
}

// TestHeraPage_DoRefresh_SkipsRebuildWhenQuiescent proves doRefresh itself
// (not just the shouldRebuild unit) honors the gate: calling it again with
// nothing changed does not re-run BuildModel (the rail's model keeps the
// role's NeedsInput=false it was built with), while a genuine runtime-map
// change (needsInput) is picked up on the very next call.
func TestHeraPage_DoRefresh_SkipsRebuildWhenQuiescent(t *testing.T) {
	d := openFileDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	p.Refresh()
	testutil.Equal(t, coordRoleView(t, p.Rail().Model()).NeedsInput, false)

	// Flag the coordinator's task as needing input, but do NOT tell the page
	// (SetNeedsInput not called) — doRefresh should skip the rebuild entirely
	// since nothing IT knows about changed, so the rail's model still shows
	// the stale (but at-last-rebuild-accurate) NeedsInput=false.
	p.doRefresh()
	testutil.Equal(t, coordRoleView(t, p.Rail().Model()).NeedsInput, false)

	// Now tell the page — the very next doRefresh picks it up.
	p.SetNeedsInput([]string{"t-coord"})
	p.doRefresh()
	testutil.Equal(t, coordRoleView(t, p.Rail().Model()).NeedsInput, true)
}

// countingTasksReader wraps a HeraReader and counts calls to Tasks(), so
// tests can assert whether BuildModel's Tasks() call actually reached the
// underlying store or was served from a supplied snapshot instead.
type countingTasksReader struct {
	HeraReader
	calls int
}

func (r *countingTasksReader) Tasks() ([]*model.Task, error) {
	r.calls++
	return r.HeraReader.Tasks()
}

// TestHeraPage_SetTasks_AvoidsRedundantFetch proves doRefresh serves
// BuildModel's Tasks() call from a snapshot supplied via SetTasks instead of
// hitting the underlying reader a second time, when one has been supplied —
// and falls back to the reader's own fetch, unchanged, when it hasn't.
func TestHeraPage_SetTasks_AvoidsRedundantFetch(t *testing.T) {
	d := openFileDB(t)
	testutil.NoError(t, d.Add(&model.Task{Name: "t1"}))
	counting := &countingTasksReader{HeraReader: d}

	p := NewHeraPage(counting)
	p.Refresh() // no SetTasks call yet — falls back to the reader's own Tasks()
	testutil.Equal(t, counting.calls, 1)

	p.SetTasks([]*model.Task{{ID: "supplied", Name: "supplied"}})
	p.Refresh()
	testutil.Equal(t, counting.calls, 1) // unchanged — served from the snapshot instead
}
