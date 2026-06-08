package daemon

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// openTestDB opens an in-memory DB for bounce-related tests that operate on
// the db.DB directly without needing a full Daemon.
func openBounceTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestWriteLiveTasksFile_NoSessions: no file written when the runner is empty.
func TestWriteLiveTasksFile_NoSessions(t *testing.T) {
	r := agent.NewRunner(nil)
	dir := t.TempDir()
	testutil.NoError(t, writeLiveTasksFile(r, dir))
	if _, err := os.Stat(liveTasksAtShutdownPath(dir)); !os.IsNotExist(err) {
		t.Error("expected no file when runner has no active sessions")
	}
}

// TestWriteLiveTasksFile_WritesIDs: file contains the running task IDs.
// SetPendingRestartForTest makes tasks appear in Running() without needing
// real PTY sessions or goroutines.
func TestWriteLiveTasksFile_WritesIDs(t *testing.T) {
	r := agent.NewRunner(nil)
	r.SetPendingRestartForTest("task-a", true)
	r.SetPendingRestartForTest("task-b", true)
	t.Cleanup(func() {
		r.SetPendingRestartForTest("task-a", false)
		r.SetPendingRestartForTest("task-b", false)
	})

	dir := t.TempDir()
	testutil.NoError(t, writeLiveTasksFile(r, dir))

	data, err := os.ReadFile(liveTasksAtShutdownPath(dir))
	testutil.NoError(t, err)

	var ids []string
	testutil.NoError(t, json.Unmarshal(data, &ids))

	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	testutil.Equal(t, len(ids), 2)
	testutil.Equal(t, got["task-a"], true)
	testutil.Equal(t, got["task-b"], true)
}

// TestWriteLiveTasksFile_AtomicRename: the .tmp file is gone after success.
func TestWriteLiveTasksFile_AtomicRename(t *testing.T) {
	r := agent.NewRunner(nil)
	r.SetPendingRestartForTest("x", true)
	t.Cleanup(func() { r.SetPendingRestartForTest("x", false) })

	dir := t.TempDir()
	testutil.NoError(t, writeLiveTasksFile(r, dir))

	if _, err := os.Stat(liveTasksAtShutdownPath(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Error("expected .tmp file to be removed after rename")
	}
}

// TestReplayBounceSignals_NoFile: no-op and no error when the file is absent.
func TestReplayBounceSignals_NoFile(t *testing.T) {
	d := openBounceTestDB(t)
	testutil.NoError(t, replayBounceSignals(d, t.TempDir()))
}

// TestReplayBounceSignals_PostsAndCleansUp: message lands in inbox and file is removed.
func TestReplayBounceSignals_PostsAndCleansUp(t *testing.T) {
	d := openBounceTestDB(t)
	task := &model.Task{Name: "live-task", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(task))

	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	payload, _ := json.Marshal([]string{task.ID})
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	testutil.NoError(t, replayBounceSignals(d, dir))

	msgs, err := d.Inbox(task.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message, got %d", len(msgs))
	}
	testutil.Equal(t, msgs[0].From, SystemTaskID)
	testutil.Equal(t, msgs[0].Body, `{"type":"ARGUS_BOUNCED"}`)
	testutil.Equal(t, msgs[0].Kind, model.KindNote)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be removed after replay")
	}
}

// TestReplayBounceSignals_SkipsArchived: archived tasks receive no signal.
func TestReplayBounceSignals_SkipsArchived(t *testing.T) {
	d := openBounceTestDB(t)
	task := &model.Task{Name: "archived-task", Status: model.StatusComplete}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetArchived(task.ID, true))

	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	payload, _ := json.Marshal([]string{task.ID})
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	testutil.NoError(t, replayBounceSignals(d, dir))

	msgs, err := d.Inbox(task.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be removed even when all tasks are archived")
	}
}

// TestReplayBounceSignals_SkipsMissingTask: tasks that no longer exist are silently skipped.
func TestReplayBounceSignals_SkipsMissingTask(t *testing.T) {
	d := openBounceTestDB(t)
	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	payload, _ := json.Marshal([]string{"ghost-task-99999"})
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	testutil.NoError(t, replayBounceSignals(d, dir))

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be removed even for missing tasks")
	}
}

// TestReplayBounceSignals_CorruptFile: corrupt JSON is handled gracefully;
// the file is removed so it doesn't block subsequent starts.
func TestReplayBounceSignals_CorruptFile(t *testing.T) {
	d := openBounceTestDB(t)
	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	testutil.NoError(t, os.WriteFile(path, []byte("not-valid-json"), 0o644))

	testutil.NoError(t, replayBounceSignals(d, dir))

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected corrupt file to be removed")
	}
}

// TestReplayBounceSignals_MultipleTasksSomeSkipped: a mix of valid, archived,
// and missing tasks — signals land only for the valid ones.
func TestReplayBounceSignals_MultipleTasksSomeSkipped(t *testing.T) {
	d := openBounceTestDB(t)

	live := &model.Task{Name: "live", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(live))

	archived := &model.Task{Name: "archived", Status: model.StatusComplete}
	testutil.NoError(t, d.Add(archived))
	testutil.NoError(t, d.SetArchived(archived.ID, true))

	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	payload, _ := json.Marshal([]string{live.ID, archived.ID, "ghost-id"})
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	testutil.NoError(t, replayBounceSignals(d, dir))

	// Only the live task should have received the message.
	liveMsgs, err := d.Inbox(live.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(liveMsgs), 1)

	archMsgs, err := d.Inbox(archived.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(archMsgs), 0)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}

// TestReplayBounceSignals_IdempotentSecondRun: a second call with no file is a no-op.
func TestReplayBounceSignals_IdempotentSecondRun(t *testing.T) {
	d := openBounceTestDB(t)
	task := &model.Task{Name: "live", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(task))

	dir := t.TempDir()
	path := liveTasksAtShutdownPath(dir)
	payload, _ := json.Marshal([]string{task.ID})
	testutil.NoError(t, os.WriteFile(path, payload, 0o644))

	// First replay.
	testutil.NoError(t, replayBounceSignals(d, dir))

	// Second call: file is gone, should be silent no-op.
	testutil.NoError(t, replayBounceSignals(d, dir))

	// Only one message total.
	msgs, err := d.Inbox(task.ID, db.InboxFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
}
