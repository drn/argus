package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// These tests wire the REAL stack end-to-end: an in-process daemon.Supervisor
// over a temp socket, a daemon-client (*Client) pointed at it, and a daemon.Daemon
// that mounts that client as its runner (UseSupervisorRunner). They prove the
// supervisor→daemon boundary actually DELIVERS the right ExitInfo so the #707
// flip lands, and that output tees supervisor→daemon→TUI. The deterministic
// matrix at the daemon's handleSessionExit sink lives in internal/daemon.
//
// Test names are deliberately short — t.TempDir() embeds the test name in the
// Unix socket path and macOS caps sun_path at 104 bytes.

// supE2E spins a Supervisor + a connected supervisor-client + a Daemon that uses
// that client. Returns (daemon, supervisor-client, db). The daemon does NOT Serve
// (not needed for the exit relay); the TEE test starts its own daemon listener.
func supE2E(t *testing.T) (*daemon.Daemon, *Client, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	supSock := filepath.Join(t.TempDir(), "s.sock")
	sup := daemon.NewSupervisor(database.Config) // cfgFn = DB config (P1 path)
	go sup.Serve(supSock)                        //nolint:errcheck
	t.Cleanup(func() { sup.Shutdown() })
	waitFile(t, supSock)

	sc, err := Connect(supSock)
	testutil.NoError(t, err)
	t.Cleanup(func() { sc.Close() }) //nolint:errcheck

	d := daemon.New(database)
	d.UseSupervisorRunner(sc) // d.runner = sc; sc.OnSessionExit → d.handleSessionExit
	return d, sc, database
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear", path)
}

func waitStatus(t *testing.T, database *db.DB, taskID string, want model.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := database.Get(taskID)
		testutil.NoError(t, err)
		if got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := database.Get(taskID)
	t.Fatalf("task %s: status %v, want %v", taskID, got.Status, want)
}

// startE2E adds an InProgress task with the given backend command and starts it
// through the supervisor-client (which RPCs StartSession to the supervisor).
func startE2E(t *testing.T, d *daemon.Daemon, sc *Client, database *db.DB, id, cmd string) *model.Task {
	t.Helper()
	bk := "be-" + id
	testutil.NoError(t, database.SetBackend(bk, config.Backend{Command: cmd}))
	task := &model.Task{ID: id, Name: id, Status: model.StatusInProgress, Backend: bk, Worktree: t.TempDir()}
	testutil.NoError(t, database.Add(task))
	// cfg arg is ignored on the wire — the supervisor resolves config via its cfgFn.
	_, err := sc.Start(task, config.Config{}, 24, 80, false)
	testutil.NoError(t, err)
	return task
}

// TestSupColdStart exercises the cold-start ordering the daemon performs in
// supervisor mode (the P4 default): no daemon, no supervisor → first launch must
// (1) fail to Connect to an absent supervisor socket, (2) take the auto-start
// path — which under `go test` the fork-bomb backstop REFUSES (ErrTestBinary)
// rather than re-exec the test binary as `session-supervisor start`; in
// production it Setsid-forks the real binary — and (3) once a supervisor is up,
// Connect succeeds, the daemon mounts it (supervisor mode ACTIVE), and a session
// round-trips through it. We never fork a real supervisor: after asserting the
// backstop we stand up an in-process Supervisor on the same socket to stand in
// for the just-started process.
func TestSupColdStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	supSock := filepath.Join(t.TempDir(), "s.sock")

	// 1. Cold: nothing is listening — Connect fails (no live supervisor).
	if _, cerr := Connect(supSock); cerr == nil {
		t.Fatal("Connect to an absent supervisor socket must fail")
	}

	// 2. The cold-start path then auto-starts one. The *.test fork-bomb backstop
	//    must refuse here — proving a test run never spawns a real supervisor.
	if _, aerr := AutoStartSupervisor(supSock); !errors.Is(aerr, ErrTestBinary) {
		t.Fatalf("AutoStartSupervisor under test = %v, want ErrTestBinary", aerr)
	}

	// 3. Stand in for the just-started supervisor: a real in-process Supervisor on
	//    the same socket. Connect now succeeds and the version handshake matches.
	sup := daemon.NewSupervisor(database.Config)
	go sup.Serve(supSock) //nolint:errcheck
	t.Cleanup(func() { sup.Shutdown() })
	waitFile(t, supSock)

	sc, err := Connect(supSock)
	testutil.NoError(t, err)
	t.Cleanup(func() { sc.Close() }) //nolint:errcheck

	hello, herr := sc.Hello()
	testutil.NoError(t, herr)
	testutil.Equal(t, daemon.SupervisorProtocolMatch(hello), true)

	// 4. Mount it: supervisor mode is now active — the daemon drives agents
	//    through the supervisor-client, not an in-process runner.
	d := daemon.New(database)
	d.UseSupervisorRunner(sc)
	testutil.Equal(t, d.Runner() == agent.SessionRunner(sc), true)

	// 5. A session started cold round-trips supervisor→daemon to Complete.
	task := startE2E(t, d, sc, database, "cold", "sh -c 'sleep 0.2'")
	waitStatus(t, database, task.ID, model.StatusComplete)
}

// TestSupClean: a clean (zero-exit) agent → the daemon flips the task Complete.
func TestSupClean(t *testing.T) {
	d, sc, database := supE2E(t)
	task := startE2E(t, d, sc, database, "clean", "sh -c 'sleep 0.3'")
	waitStatus(t, database, task.ID, model.StatusComplete)
}

// TestSupCrash: a non-zero exit → the daemon flips the task InReview, never Complete.
func TestSupCrash(t *testing.T) {
	d, sc, database := supE2E(t)
	task := startE2E(t, d, sc, database, "crash", "sh -c 'sleep 0.3; exit 1'")
	waitStatus(t, database, task.ID, model.StatusInReview)
}

// TestSupCrashRelayErr is the relay-level #707 proof: a REAL non-zero exit
// through the supervisor must deliver an ExitInfo carrying the exit error
// (Err != "", CleanExit()==false), never an empty/clean one. P2's matrix tested
// handleSessionExit with SYNTHETIC ExitInfo, so it never exercised the
// supervisor→GetExitInfo fetch — which once raced the supervisor's exit-info
// cache write: if the stream EOF (handleConn's conn close on handleStream's
// sess.Done() return) beat onFinish's cache, GetExitInfo returned "not found" →
// a zero-value ExitInfo → CleanExit()==true → the crashed task wrongly flipped
// Complete. handleStream now waits for the cache before returning, so the EOF
// the client observes is always post-cache. We wire our OWN onSessionExit to
// capture the ExitInfo the relay actually delivered and assert it carries the
// crash error.
func TestSupCrashRelayErr(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	supSock := filepath.Join(t.TempDir(), "s.sock")
	sup := daemon.NewSupervisor(database.Config)
	go sup.Serve(supSock) //nolint:errcheck
	t.Cleanup(func() { sup.Shutdown() })
	waitFile(t, supSock)

	sc, err := Connect(supSock)
	testutil.NoError(t, err)
	t.Cleanup(func() { sc.Close() }) //nolint:errcheck

	got := make(chan daemon.ExitInfo, 1)
	sc.OnSessionExit(func(_ string, info daemon.ExitInfo) { got <- info })

	bk := "be-crashrelay"
	testutil.NoError(t, database.SetBackend(bk, config.Backend{Command: "sh -c 'exit 7'"}))
	task := &model.Task{ID: "crashrelay", Name: "crashrelay", Status: model.StatusInProgress, Backend: bk, Worktree: t.TempDir()}
	testutil.NoError(t, database.Add(task))
	_, err = sc.Start(task, config.Config{}, 24, 80, false)
	testutil.NoError(t, err)

	select {
	case info := <-got:
		if info.Err == "" {
			t.Fatalf("crash relay delivered empty Err (StreamLost=%v, CleanExit=%v) — #707 violation: a crashed task would flip Complete", info.StreamLost, info.CleanExit())
		}
		testutil.Equal(t, info.StreamLost, false)
		testutil.Equal(t, info.CleanExit(), false)
	case <-time.After(10 * time.Second):
		t.Fatal("no exit relay delivered within 10s")
	}
}

// TestSupStop: an explicit stop → the daemon flips the task InReview.
func TestSupStop(t *testing.T) {
	d, sc, database := supE2E(t)
	task := startE2E(t, d, sc, database, "stop", "sh -c 'sleep 30'")
	// Give the stream a moment to subscribe, then stop through the supervisor.
	time.Sleep(200 * time.Millisecond)
	testutil.NoError(t, sc.Stop(task.ID))
	waitStatus(t, database, task.ID, model.StatusInReview)
}

// TestSupTee proves the double-proxy: bytes flow supervisor → daemon-client ring
// → daemon.handleStream → a TUI stream conn. We start a session through the
// supervisor, run the daemon's own listener, connect a SECOND client to the
// DAEMON socket, and assert it receives the agent's output.
func TestSupTee(t *testing.T) {
	d, sc, database := supE2E(t)

	// The daemon serves its own socket so a TUI client can stream from it.
	daemonSock := filepath.Join(t.TempDir(), "d.sock")
	go d.Serve(daemonSock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitFile(t, daemonSock)

	task := startE2E(t, d, sc, database, "tee", "sh -c 'sleep 0.5; echo TEE-MARKER'")

	// Connect a TUI-side client to the DAEMON and open a stream on the task.
	tui, err := Connect(daemonSock)
	testutil.NoError(t, err)
	t.Cleanup(func() { tui.Close() }) //nolint:errcheck

	sess := tui.Get(task.ID)
	if sess == nil {
		// Get is a SessionStatus probe; the daemon forwards to the supervisor.
		// Retry briefly in case the start RPC hasn't fully landed.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && sess == nil {
			time.Sleep(50 * time.Millisecond)
			sess = tui.Get(task.ID)
		}
	}
	if sess == nil {
		t.Fatal("TUI client could not resolve the session through the daemon")
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(sess.RecentOutput()), "TEE-MARKER") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("TUI client never received teed output; got %q", string(sess.RecentOutput()))
}

// TestSupHello proves the version handshake round-trips against a real
// supervisor: the daemon learns the supervisor's ProtocolVersion + binary
// identity, which it uses to feature-detect and to log skew (never auto-restart).
func TestSupHello(t *testing.T) {
	_, sc, _ := supE2E(t)
	hello, err := sc.Hello()
	testutil.NoError(t, err)
	testutil.Equal(t, hello.ProtocolVersion, daemon.ProtocolVersion)
	testutil.True(t, daemon.SupervisorProtocolMatch(hello))
	if hello.BinaryPath == "" {
		t.Error("expected a non-empty supervisor binary path in the handshake")
	}
}

// TestSupKick proves the protocol-v2 KickRerender RPC reaches the supervisor's
// real runner end-to-end (the success path; TestCliKickErr covers the v1/absent
// degradation). The kick stops the live session and queues an in-place restart,
// so the daemon must NOT flip the task to a terminal status (pendingRestart).
func TestSupKick(t *testing.T) {
	d, sc, database := supE2E(t)
	task := startE2E(t, d, sc, database, "kick", "sh -c 'sleep 30'")
	time.Sleep(200 * time.Millisecond) // let the session + stream come up

	testutil.NoError(t, sc.KickRerender(task, config.Config{}, 40, 120))

	// The supervisor reports a pending restart across the kick gap, and the task
	// stays InProgress (the queued restart bridges it — no terminal flip).
	deadline := time.Now().Add(3 * time.Second)
	sawPending := false
	for time.Now().Before(deadline) {
		if sc.HasPendingRestart(task.ID) {
			sawPending = true
			break
		}
		// Or the restart already landed and the session is alive again.
		if s := sc.Get(task.ID); s != nil {
			sawPending = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	testutil.True(t, sawPending)
	got, err := database.Get(task.ID)
	testutil.NoError(t, err)
	if got.Status != model.StatusInProgress {
		t.Fatalf("kick must not flip status; got %v", got.Status)
	}
}

// TestSupListInfo proves ListSessions relays through the supervisor-client: with a
// live supervisor session, the daemon's runner (the client) reports it via
// ListSessionInfo (the sessionInfoLister path), not the in-process Sessions() path.
func TestSupListInfo(t *testing.T) {
	d, sc, database := supE2E(t)
	task := startE2E(t, d, sc, database, "list", "sh -c 'sleep 2'")
	time.Sleep(200 * time.Millisecond)

	infos := sc.ListSessionInfo()
	found := false
	for _, s := range infos {
		if s.TaskID == task.ID && s.Alive {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected live session %s in ListSessionInfo, got %+v", task.ID, infos)
	}
}
