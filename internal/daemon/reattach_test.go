package daemon

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// hasBounceSignal reports whether the task's inbox holds an ARGUS_BOUNCED note.
func hasBounceSignal(t *testing.T, d *Daemon, taskID string) bool {
	t.Helper()
	msgs, err := d.db.Inbox(taskID, db.InboxFilter{})
	testutil.NoError(t, err)
	for _, m := range msgs {
		if m.From == SystemTaskID && m.Body == `{"type":"ARGUS_BOUNCED"}` {
			return true
		}
	}
	return false
}

// TestReconcileOnStartup_Supervised_ReattachLiveFlipOrphan is the daemon-side
// P3 reconcile contract with a fake supervisor-client: the supervisor reports
// one task alive (re-attached) and one InProgress task it does NOT report
// (true orphan). Asserts the live task stays InProgress + is re-attached via
// Get, the orphan flips to InReview, the orphan (only) is signalled
// ARGUS_BOUNCED, and a hera binding on the live task survives.
func TestReconcileOnStartup_Supervised_ReattachLiveFlipOrphan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)

	live := &model.Task{Name: "live", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(live))
	orphan := &model.Task{Name: "orphan", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(orphan))

	// A live hera worker binding on the surviving task — must survive the bounce
	// (keyed on task-row existence, which still exists).
	bindWorker(t, d, live.ID)

	fake := &fakeSupClient{running: []string{live.ID}} // supervisor reports only `live`
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	// Live task re-attached: Get called, status preserved.
	testutil.DeepEqual(t, fake.getCalls, []string{live.ID})
	gotLive, err := d.db.Get(live.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotLive.Status, model.StatusInProgress)

	// Orphan flipped to InReview.
	gotOrphan, err := d.db.Get(orphan.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotOrphan.Status, model.StatusInReview)

	// Only the orphan is signalled — the re-attached agent was never interrupted.
	testutil.Equal(t, hasBounceSignal(t, d, orphan.ID), true)
	testutil.Equal(t, hasBounceSignal(t, d, live.ID), false)

	// The hera binding on the re-attached task is still live.
	bindings, err := d.db.ListHeraLiveBindingsByTask(live.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(bindings), 1)
}

// TestReconcileOnStartup_Supervised_LiveSetQueryFails_SkipsReconcile proves the
// nil-guard: a failed ListSessions RPC (Running()==nil) must NOT flip live
// agents to InReview (false termination). Nothing is re-attached, flipped, or
// signalled — the next bounce / TUI tick reconciles once the supervisor answers.
func TestReconcileOnStartup_Supervised_LiveSetQueryFails_SkipsReconcile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)

	task := &model.Task{Name: "maybe-alive", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(task))

	fake := &fakeSupClient{running: nil} // nil ⇒ RPC failed
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	testutil.Equal(t, len(fake.getCalls), 0) // no re-attach attempted
	got, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInProgress) // NOT flipped
	testutil.Equal(t, hasBounceSignal(t, d, task.ID), false)
}

// TestReconcileOnStartup_Supervised_AllOrphans covers the supervisor-also-died
// case: the supervisor reports an authoritative EMPTY live set (non-nil), so
// every InProgress task is a true orphan → all flip to InReview + all signalled.
func TestReconcileOnStartup_Supervised_AllOrphans(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)

	a := &model.Task{Name: "a", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(a))
	b := &model.Task{Name: "b", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(b))

	fake := &fakeSupClient{running: []string{}} // authoritative: nothing alive
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	testutil.Equal(t, len(fake.getCalls), 0) // nothing to re-attach
	for _, task := range []*model.Task{a, b} {
		got, err := d.db.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Status, model.StatusInReview)
		testutil.Equal(t, hasBounceSignal(t, d, task.ID), true)
	}
}

// TestReconcileOnStartup_OffMode_FlipsAllAndReplaysFile pins the in-process
// branch end-to-end through ReconcileOnStartup (no supervisor-client): every
// InProgress flips to InReview and the ARGUS_BOUNCED file is replayed + removed.
func TestReconcileOnStartup_OffMode_FlipsAllAndReplaysFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)
	testutil.Equal(t, d.supClient == nil, true) // in-process mode

	task := &model.Task{Name: "stale", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(task))

	// Seed the live-tasks file the previous cleanup would have written.
	dir := db.DataDir()
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	payload, _ := json.Marshal([]string{task.ID})
	path := liveTasksAtShutdownPath(dir)
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	d.ReconcileOnStartup()

	got, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	testutil.Equal(t, hasBounceSignal(t, d, task.ID), true)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected live-tasks file removed after OFF-mode replay")
	}
}

// TestReconcileOnStartup_Supervised_FirstStartAfterOffRun is the P4 migration
// case: the default flips ON, so the very first supervisor-mode start inherits a
// stale InProgress row AND a stale live-tasks file left by the last OFF-mode
// (in-process) run. Those old in-process agents died with the old daemon, and
// the fresh supervisor reports an authoritative empty live set. Asserts the
// transition is clean: the stale row flips to InReview, is signalled
// ARGUS_BOUNCED exactly once (via the orphan path, NOT a file replay), and the
// stale live-tasks file is DISCARDED so a later ON→OFF rollback can't replay it.
func TestReconcileOnStartup_Supervised_FirstStartAfterOffRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)

	stale := &model.Task{Name: "stale-inproc", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(stale))

	// Seed the live-tasks file the previous OFF-mode cleanup wrote.
	dir := db.DataDir()
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	payload, _ := json.Marshal([]string{stale.ID})
	path := liveTasksAtShutdownPath(dir)
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	// Now in supervisor mode (the P4 default), with a fresh supervisor that owns
	// nothing (the old in-process agents are gone).
	fake := &fakeSupClient{running: []string{}}
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	// No crash; the stale row is orphaned exactly as reattachSupervised dictates.
	testutil.Equal(t, len(fake.getCalls), 0) // nothing live to re-attach
	got, err := d.db.Get(stale.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)

	// Signalled exactly once — and via the orphan path, so a single ARGUS_BOUNCED.
	msgs, err := d.db.Inbox(stale.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	bounces := 0
	for _, m := range msgs {
		if m.From == SystemTaskID && m.Body == `{"type":"ARGUS_BOUNCED"}` {
			bounces++
		}
	}
	testutil.Equal(t, bounces, 1)

	// The stale file is discarded so a later rollback can't replay it.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected stale live-tasks file discarded on first supervisor-mode start")
	}
}

// TestSendBounceSignals_SkipsMissingAndArchived pins the shared helper directly:
// signals land only for existing, non-archived tasks; the returned count matches.
func TestSendBounceSignals_SkipsMissingAndArchived(t *testing.T) {
	d := openBounceTestDB(t)

	live := &model.Task{Name: "live", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(live))
	archived := &model.Task{Name: "arch", Status: model.StatusComplete}
	testutil.NoError(t, d.Add(archived))
	testutil.NoError(t, d.SetArchived(archived.ID, true))

	sent := sendBounceSignals(d, []string{live.ID, archived.ID, "ghost-id"})
	testutil.Equal(t, sent, 1)

	msgs, err := d.Inbox(live.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
}
