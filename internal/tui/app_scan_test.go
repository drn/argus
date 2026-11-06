package tui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestScanRotationRetainsReadSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	ids := make([]string, needsInputScanBatchSize+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("scan-%d", i)
		testutil.NoError(t, os.WriteFile(agent.SessionLogPath(ids[i]), []byte("Done.\n"), 0o644))
	}
	a := &App{}
	reads := 0
	a.needsInputTailReader = func(id string) []byte {
		reads++
		return readSessionLogTailBytes(id, detectNeedsInputTailBytes)
	}
	// Prime both batches; then force the first batch to be skipped.
	a.detectNeedsInputSticky(nil, ids, nil)
	a.detectNeedsInputSticky(nil, ids, nil)
	testutil.Equal(t, reads, len(ids))
	snapshot, ok := a.needsInputLogStat[ids[0]]
	testutil.Equal(t, ok, true)
	a.needsInputScanCursor = 1
	a.detectNeedsInputSticky(nil, ids, nil)
	testutil.Equal(t, a.needsInputLogStat[ids[0]], snapshot)
	testutil.Equal(t, reads, len(ids))
	// A write during the gap must not replace that baseline until read.
	p := agent.SessionLogPath(ids[0])
	testutil.NoError(t, os.WriteFile(p, []byte(workingBurstLog), 0o644))
	mt := time.Now().Add(time.Hour)
	testutil.NoError(t, os.Chtimes(p, mt, mt))
	a.needsInputScanCursor = 1
	a.detectNeedsInputSticky(nil, ids, nil)
	testutil.Equal(t, a.needsInputLogStat[ids[0]], snapshot)
	a.detectNeedsInputSticky(nil, ids, nil)
	testutil.Equal(t, a.needsInputRawSignals[ids[0]].working, true)
	testutil.Equal(t, reads, len(ids)+1)
}

func TestScanIdleWithoutRunningThenFirstRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	testutil.NoError(t, os.WriteFile(agent.SessionLogPath("solo"), []byte(workingBurstLog), 0o644))
	a := &App{}
	a.detectNeedsInputSticky([]string{"solo"}, nil, nil)
	a.detectNeedsInputSticky(nil, []string{"solo"}, nil)
	testutil.Equal(t, a.needsInputRawSignals["solo"].hasTail, true)
	testutil.Equal(t, a.needsInputRawSignals["solo"].working, true)
}

func TestScanFirstIdleOutsideRotationRefreshesAllSignals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	ids := make([]string, needsInputScanBatchSize+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("task-%02d", i)
		testutil.NoError(t, os.WriteFile(agent.SessionLogPath(ids[i]), []byte("Done.\n"), 0o644))
	}
	a := &App{}
	a.detectNeedsInputSticky(nil, ids, nil)
	// task-00 is outside batch 1, but just became idle. Its fresh idle
	// read must also update working/parked/fingerprint and their baseline.
	testutil.NoError(t, os.WriteFile(agent.SessionLogPath(ids[0]), []byte("❯ 1. Yes\n"), 0o644))
	a.needsInputScanCursor = 1
	got := a.detectNeedsInputSticky([]string{ids[0]}, ids, nil)
	testutil.DeepEqual(t, got, []string{ids[0]})
	testutil.Equal(t, a.needsInputRawSignals[ids[0]].parked, true)
	got = a.detectNeedsInputSticky([]string{ids[0]}, ids, got)
	testutil.DeepEqual(t, got, []string{ids[0]})
	testutil.Equal(t, a.needsInputRawSignals[ids[0]].parked, true)
}

func TestAutoClearBlockedHeraRoles_ReusesScanWorking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	st := mkHeraBlockedRole(t, d, "cached")
	// There is deliberately no log. Without the cached working reading the
	// role never clears; direct input is older than the block.
	a := &App{db: d, needsInputRawSignals: map[string]needsInputRawSignals{
		"cached": {hasTail: true, working: true},
	}}
	for range agent.NeedsInputResumeTicks {
		a.autoClearBlockedHeraRoles([]string{"cached"})
	}
	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusWorking)
}

func TestScanIdleReadCounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	const id = "idle-scan"
	p := agent.SessionLogPath(id)
	testutil.NoError(t, os.WriteFile(p, []byte("❯ 1. Yes\n"), 0o644))
	reads := 0
	a := &App{needsInputTailReader: func(id string) []byte {
		reads++
		return readSessionLogTailBytes(id, detectNeedsInputTailBytes)
	}}
	got := a.detectNeedsInputSticky([]string{id}, []string{id}, nil)
	testutil.Equal(t, reads, 1)
	testutil.DeepEqual(t, got, []string{id})
	for range 4 {
		got = a.detectNeedsInputSticky([]string{id}, []string{id}, got)
	}
	testutil.Equal(t, reads, 1)
	testutil.DeepEqual(t, got, []string{id})
	// First idle tick must read even if last tick's running reading is cached.
	a.detectNeedsInputSticky(nil, []string{id}, got)
	a.detectNeedsInputSticky([]string{id}, []string{id}, got)
	testutil.Equal(t, reads, 2)
	testutil.NoError(t, os.WriteFile(p, []byte("Done.\n"), 0o644))
	a.detectNeedsInputSticky([]string{id}, []string{id}, got)
	testutil.Equal(t, reads, 3)
	a.tasks = []*model.Task{{ID: id, Archived: true}}
	testutil.Equal(t, len(a.detectNeedsInputSticky([]string{id}, []string{id}, got)), 0)
	testutil.Equal(t, reads, 3)
}

func TestScanWorkTable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name                    string
		idle, changed, archived bool
		reads                   int
		renders                 uint64
	}{
		{"idle unchanged", true, false, false, 0, 0},
		{"idle changed", true, true, false, 1, 1},
		{"running changed", false, true, false, 1, 1},
		{"running unchanged", false, false, false, 0, 0},
		{"archived running", false, true, true, 1, 1},
		{"archived idle", true, true, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = "work-table"
			testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
			path := agent.SessionLogPath(id)
			testutil.NoError(t, os.WriteFile(path, []byte("Done.\n"), 0o644))
			reads := 0
			a := &App{tasks: []*model.Task{{ID: id, Archived: tc.archived}}, needsInputTailReader: func(id string) []byte {
				reads++
				return readSessionLogTailBytes(id, detectNeedsInputTailBytes)
			}}
			var idleIDs []string
			if tc.idle {
				idleIDs = []string{id}
			}
			tick := func() {
				t.Helper()
				a.detectNeedsInputSticky(idleIDs, []string{id}, nil)
				// Include the archived ContentIdle fallback in total scan work.
				agent.ContentIdle([]string{id}, map[string]bool{id: tc.idle}, a.needsInputTailReader, needsInputScreenSize, a.needsInputScreen, nil, time.Now(), a.contentIdleSignalOf)
			}
			tick()
			reads = 0
			before := a.needsInputScreen.RenderCount()
			if tc.changed {
				testutil.NoError(t, os.WriteFile(path, []byte("Finished another step.\n"), 0o644))
			}
			tick()
			testutil.Equal(t, reads, tc.reads)
			testutil.Equal(t, a.needsInputScreen.RenderCount()-before, tc.renders)
		})
	}
}
