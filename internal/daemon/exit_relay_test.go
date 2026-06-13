package daemon

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// handleSessionExit is the single DB-side exit sink for BOTH supervisor modes:
// OFF (the in-process runner's onFinish calls it) and ON (the supervisor-client's
// OnSessionExit relay calls it with ExitInfo fetched across the boundary). These
// tests pin the #707 matrix + the StreamLost backstop + pendingRestart skip +
// the GetExitInfo cache directly at that sink, so the behavior is identical
// regardless of which mode delivered the ExitInfo. (The end-to-end proof that ON
// mode actually DELIVERS the right ExitInfo across the supervisor→daemon boundary
// lives in internal/daemon/client/sup_e2e_test.go.)

// TestSupervisorProtocolMatch pins the version-skew decision: an equal version
// matches; any mismatch does NOT (and the caller logs + proceeds — there is no
// auto-restart path, so a live supervisor's agents can never be SIGHUP'd by a
// version disagreement).
func TestSupervisorProtocolMatch(t *testing.T) {
	testutil.Equal(t, SupervisorProtocolMatch(HelloResp{ProtocolVersion: ProtocolVersion}), true)
	testutil.Equal(t, SupervisorProtocolMatch(HelloResp{ProtocolVersion: ProtocolVersion + 1}), false)
	testutil.Equal(t, SupervisorProtocolMatch(HelloResp{ProtocolVersion: 0}), false)
}

// TestHandleSessionExit_Matrix pins the terminal-status decision + the GetExitInfo
// cache for every ExitInfo shape that can cross the relay.
func TestHandleSessionExit_Matrix(t *testing.T) {
	tests := []struct {
		name       string
		info       ExitInfo
		want       model.Status // expected status after the relay
		wantStream bool         // expect cached ExitInfo.StreamLost on a TUI GetExitInfo
	}{
		// Clean self-exit (zero code) is the ONLY path to Complete (#707).
		{"clean exit -> complete", ExitInfo{}, model.StatusComplete, false},
		// Crash / non-zero / fast-fail surfaces as Err -> recoverable -> in_review.
		{"crash (non-empty Err) -> in_review", ExitInfo{Err: "exit status 1"}, model.StatusInReview, false},
		// Explicit stop -> in_review.
		{"stop -> in_review", ExitInfo{Stopped: true}, model.StatusInReview, false},
		// Stream lost (relay failed, process MAY still be alive on the supervisor)
		// -> status UNCHANGED. The ExitInfo is still cached WITH StreamLost so a
		// daemon TUI client's GetExitInfo reads StreamLost (not an empty ExitInfo
		// that CleanExit()s into a wrong Complete).
		{"stream lost -> unchanged", ExitInfo{StreamLost: true}, model.StatusInProgress, true},
		// Kick-restart in flight -> skip the flip (the restart will land); cached.
		{"pending restart -> unchanged", ExitInfo{PendingRestart: true}, model.StatusInProgress, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			task := &model.Task{Name: "x", Status: model.StatusInProgress}
			testutil.NoError(t, d.db.Add(task))

			d.handleSessionExit(task.ID, tt.info)

			got, err := d.db.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Status, tt.want)

			// The ExitInfo a TUI client would fetch via GetExitInfo.
			var resp ExitInfo
			testutil.NoError(t, d.GetExitInfo(&TaskIDReq{TaskID: task.ID}, &resp))
			testutil.Equal(t, resp.StreamLost, tt.wantStream)
		})
	}
}

// TestHandleSessionExit_HeraWorker pins that the BUG-050 worker close-out rolls
// through the relay sink too: a worker-bound clean exit lands in_review +
// ready_to_close, never Complete.
func TestHandleSessionExit_HeraWorker(t *testing.T) {
	d, _ := testDaemon(t)
	seedDaemonProject(t, d, t.TempDir())
	task := &model.Task{Name: "w", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(task))
	bindWorker(t, d, task.ID)

	d.handleSessionExit(task.ID, ExitInfo{}) // clean exit

	got, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	testutil.Equal(t, heraReadyToClose(t, d, task.ID), true)
}

// TestHandleSessionExit_Recapture proves the relay sink drives session-ID
// recapture: on a clean exit of a codex task, captureSessionIDPostExit reads the
// worktree-scoped backend state (~/.codex/state_5.sqlite) and persists the new
// SessionID. This is the daemon-side recapture (design §5) — the supervisor owns
// the process timing but the worktree state is on the shared filesystem, and the
// relay only fires after the process exited, so the daemon reads a written state.
func TestHandleSessionExit_Recapture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("codex", config.Backend{Command: "codex"}))

	wt := t.TempDir()
	task := &model.Task{Name: "cdx", Status: model.StatusInProgress, Backend: "codex", Worktree: wt}
	testutil.NoError(t, d.db.Add(task)) // SessionID empty → codex recaptures once

	// Seed a codex state_5.sqlite with a thread row keyed on the worktree cwd.
	codexDir := filepath.Join(home, ".codex")
	testutil.NoError(t, os.MkdirAll(codexDir, 0o700))
	conn, err := sql.Open("sqlite", filepath.Join(codexDir, "state_5.sqlite"))
	testutil.NoError(t, err)
	_, err = conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`)
	testutil.NoError(t, err)
	wantID := "019cff60-2cfb-7ed3-bca6-15ef06587c99"
	_, err = conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, wantID, wt, 100)
	testutil.NoError(t, err)
	conn.Close() //nolint:errcheck

	d.handleSessionExit(task.ID, ExitInfo{}) // clean exit → recapture fires (async)

	// captureSessionIDPostExit runs in a goroutine; poll for the persisted ID.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, gerr := d.db.Get(task.ID)
		testutil.NoError(t, gerr)
		if got.SessionID == wantID {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session ID not recaptured within deadline")
}

// TestHandleSessionExit_StreamLostDoesNotRecapture pins that a StreamLost relay
// short-circuits BEFORE recapture (the process may still be alive — reading its
// "post-exit" state would be premature). The codex task keeps its empty SessionID.
func TestHandleSessionExit_StreamLostDoesNotRecapture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("codex", config.Backend{Command: "codex"}))
	wt := t.TempDir()
	task := &model.Task{Name: "cdx2", Status: model.StatusInProgress, Backend: "codex", Worktree: wt}
	testutil.NoError(t, d.db.Add(task))

	// Seed state that WOULD be captured if recapture ran.
	codexDir := filepath.Join(home, ".codex")
	testutil.NoError(t, os.MkdirAll(codexDir, 0o700))
	conn, err := sql.Open("sqlite", filepath.Join(codexDir, "state_5.sqlite"))
	testutil.NoError(t, err)
	_, err = conn.Exec(`CREATE TABLE threads (id TEXT, cwd TEXT, updated_at INTEGER)`)
	testutil.NoError(t, err)
	_, err = conn.Exec(`INSERT INTO threads (id, cwd, updated_at) VALUES (?, ?, ?)`, "019cff60-2cfb-7ed3-bca6-15ef06587c99", wt, 100)
	testutil.NoError(t, err)
	conn.Close() //nolint:errcheck

	d.handleSessionExit(task.ID, ExitInfo{StreamLost: true})

	// Give any erroneous recapture goroutine a chance to run, then assert it didn't.
	time.Sleep(200 * time.Millisecond)
	got, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "")                  // not recaptured
	testutil.Equal(t, got.Status, model.StatusInProgress) // not flipped
}
