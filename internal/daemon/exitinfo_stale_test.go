package daemon

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// doneSession is a minimal SessionHandle whose only live method is Done.
type doneSession struct {
	agent.SessionHandle
	done chan struct{}
}

func newDoneSession(closed bool) *doneSession {
	s := &doneSession{done: make(chan struct{})}
	if closed {
		close(s.done)
	}
	return s
}

func (s *doneSession) Done() <-chan struct{} { return s.done }

func staleTestCore() *sessionCore {
	return newSessionCore(&fakeSupClient{}, config.DefaultConfig, make(chan struct{}))
}

func (c *sessionCore) hasExit(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.exitInfos[id]
	return ok
}

func (c *sessionCore) record(id string, owner agent.SessionHandle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordExitLocked(id, ExitInfo{Stopped: true, PendingRestart: true}, owner)
}

// TestSessionCore_ExitInfoOwnership pins the identity rules behind
// dropStaleExitInfo / awaitExitInfoCached, including the #707 branch: a session
// that already exited before its stream attached keeps its own entry.
func TestSessionCore_ExitInfoOwnership(t *testing.T) {
	t.Run("own entry kept even though the session already exited (#707)", func(t *testing.T) {
		c, sess := staleTestCore(), newDoneSession(true)
		c.record("t", sess)
		c.dropStaleExitInfo("t", sess)
		testutil.True(t, c.hasExit("t"))
	})
	t.Run("another incarnation's entry dropped", func(t *testing.T) {
		c, old, cur := staleTestCore(), newDoneSession(true), newDoneSession(false)
		c.record("t", old)
		c.dropStaleExitInfo("t", cur)
		testutil.False(t, c.hasExit("t"))
	})
	// The reviewer's pin: without identity, a Done-based rule keeps this entry
	// because the attaching session has already exited.
	t.Run("stale owner A, attach B with B's Done already closed: dropped", func(t *testing.T) {
		c, a, b := staleTestCore(), newDoneSession(true), newDoneSession(true)
		c.record("t", a)
		gen := c.dropStaleExitInfo("t", b)
		testutil.False(t, c.hasExit("t"))
		// ...and nothing is left for await to mistake for B's own.
		start := time.Now()
		c.awaitExitInfoCached("t", b, gen, 60*time.Millisecond)
		testutil.True(t, time.Since(start) >= 50*time.Millisecond)
	})
	t.Run("unowned entry falls back to Done timing", func(t *testing.T) {
		c, live := staleTestCore(), newDoneSession(false)
		c.record("t", nil)
		c.dropStaleExitInfo("t", live)
		testutil.False(t, c.hasExit("t"))

		c2, exited := staleTestCore(), newDoneSession(true)
		c2.record("t", nil)
		c2.dropStaleExitInfo("t", exited)
		testutil.True(t, c2.hasExit("t"))
	})
	// Restart-failure re-fire: onFinish runs a second time after the old session
	// left the runner's map and no replacement exists, so the entry is unowned.
	// It overwrites the owned one and must still reach the relay.
	t.Run("restart-failure re-fire is unowned and still delivered", func(t *testing.T) {
		c, old := staleTestCore(), newDoneSession(true)
		c.record("t", old)
		c.mu.Lock()
		c.recordExitLocked("t", ExitInfo{Err: "restart failed", Stopped: true}, nil)
		c.mu.Unlock()
		var info ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info))
		testutil.Equal(t, info.Err, "restart failed")
		testutil.False(t, info.PendingRestart) // not the overwritten owned entry
	})
	t.Run("await accepts an unowned entry", func(t *testing.T) {
		c, sess := staleTestCore(), newDoneSession(true)
		c.record("t", nil)
		start := time.Now()
		c.awaitExitInfoCached("t", sess, c.exitGen, 5*time.Second)
		testutil.True(t, time.Since(start) < time.Second)
	})
	t.Run("await: own entry already consumed does not wait out the bound", func(t *testing.T) {
		c, sess := staleTestCore(), newDoneSession(true)
		c.record("t", sess)
		var info ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info))
		testutil.True(t, info.Stopped)
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info)) // second read is empty
		start := time.Now()
		c.awaitExitInfoCached("t", sess, c.exitGen, 5*time.Second)
		testutil.True(t, time.Since(start) < time.Second)
	})
	t.Run("await: entry overwritten by a later incarnation does not wait out the bound", func(t *testing.T) {
		c, mine, later := staleTestCore(), newDoneSession(false), newDoneSession(true)
		gen := c.dropStaleExitInfo("t", mine) // attach: nothing cached yet
		c.record("t", later)                  // a later incarnation's exit lands first
		start := time.Now()
		c.awaitExitInfoCached("t", mine, gen, 5*time.Second)
		testutil.True(t, time.Since(start) < time.Second)
	})
	t.Run("await: an older incarnation's leftover keeps waiting", func(t *testing.T) {
		c, old, cur := staleTestCore(), newDoneSession(true), newDoneSession(true)
		c.record("t", old)
		start := time.Now()
		c.awaitExitInfoCached("t", cur, c.exitGen, 60*time.Millisecond) // attached after the write, never dropped
		testutil.True(t, time.Since(start) >= 50*time.Millisecond)
	})
	// An unowned entry that was consumed leaves a tombstone. If the next
	// incarnation attaches already-done (it crashed in the [Done, onFinish) gap)
	// and the tombstone survived, await would settle on it and the relay would
	// read a zero ExitInfo (CleanExit()==true), marking a crash Complete (#707).
	// Each half of the defence gets its own assertion.
	t.Run("unowned tombstone is dropped on attach even if the new session is done", func(t *testing.T) {
		c, b := staleTestCore(), newDoneSession(true)
		c.record("t", nil)
		var info ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info))
		c.dropStaleExitInfo("t", b)
		testutil.False(t, c.hasExit("t"))
	})
	t.Run("await is not settled by a consumed tombstone it does not own", func(t *testing.T) {
		c, b := staleTestCore(), newDoneSession(true)
		c.record("t", nil)
		var info ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info))
		start := time.Now() // tombstone left in place: no dropStale
		c.awaitExitInfoCached("t", b, c.exitGen, 60*time.Millisecond)
		testutil.True(t, time.Since(start) >= 50*time.Millisecond)
	})
	t.Run("unowned tombstone then done attach: await waits, never serves a zero ExitInfo", func(t *testing.T) {
		c, b := staleTestCore(), newDoneSession(true)
		c.record("t", nil)
		var info ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &info))
		gen := c.dropStaleExitInfo("t", b)
		start := time.Now()
		c.awaitExitInfoCached("t", b, gen, 60*time.Millisecond)
		testutil.True(t, time.Since(start) >= 50*time.Millisecond)
		// The real exit lands afterwards and is what the relay reads.
		c.mu.Lock()
		c.recordExitLocked("t", ExitInfo{Err: "crash"}, b)
		c.mu.Unlock()
		var got ExitInfo
		testutil.NoError(t, c.GetExitInfo(&TaskIDReq{TaskID: "t"}, &got))
		testutil.Equal(t, got.Err, "crash")
	})
	t.Run("overwrite replaces owner and info together", func(t *testing.T) {
		c, a, b := staleTestCore(), newDoneSession(true), newDoneSession(true)
		c.record("t", a)
		c.mu.Lock()
		c.recordExitLocked("t", ExitInfo{Err: "fresh"}, b)
		e := c.exitInfos["t"]
		c.mu.Unlock()
		testutil.Equal(t, e.info.Err, "fresh")
		testutil.True(t, e.owner == agent.SessionHandle(b))
		testutil.False(t, e.info.PendingRestart)
	})
}

// A kick/recycle restart caches the OLD incarnation's ExitInfo with
// PendingRestart=true, and nothing consumes it (clients see the gap as alive
// and just reconnect their stream). Left in place it satisfies
// awaitExitInfoCached for the replacement's REAL exit, so the stream EOF can
// beat the fresh ExitInfo to the cache and the relay's GetExitInfo consumes the
// stale PendingRestart=true entry: the daemon then skips the status flip and
// the task stays in_progress after its agent is gone. Attaching to a live
// session must therefore drop whatever exit info predates it.
func TestSup_StaleExitInfoDroppedOnAttach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, _ := testSupervisor(t, supCfg("sleep 60"))
	core := s.sessionCore
	t.Cleanup(core.runner.StopAll) // never Served, so Shutdown would block
	const id = "stale1"
	wt := t.TempDir()

	var sr StartResp
	testutil.NoError(t, core.StartSession(&StartReq{TaskID: id, Backend: "test", Worktree: wt, Rows: 24, Cols: 80}, &sr))
	testutil.Equal(t, sr.Error, "")
	first := core.runner.Get(id)
	testutil.NotNil(t, first)

	var rr StatusResp
	testutil.NoError(t, core.Recycle(&RecycleReq{TaskID: id, Backend: "test", Worktree: wt, Rows: 24, Cols: 80}, &rr))
	testutil.Equal(t, rr.Error, "")

	// Wait for the replacement to be live and the restart gap to close.
	waitFor(t, "replacement live", func() bool {
		cur := core.runner.Get(id)
		return cur != nil && cur != first && !core.runner.HasPendingRestart(id)
	})

	// Precondition: the kicked incarnation left its pending-restart ExitInfo behind.
	core.mu.Lock()
	stale, ok := core.exitInfos[id]
	core.mu.Unlock()
	testutil.True(t, ok)
	testutil.True(t, stale.info.PendingRestart)
	// The supervisor's onFinish must stamp the exiting (old) session; without it
	// production silently falls back to Done timing.
	testutil.True(t, stale.owner == first)

	// A stream client attaches to the replacement.
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	go io.Copy(io.Discard, client) //nolint:errcheck
	// handleStream consumes the header off the conn and returns when it closes.
	// The header write blocks until handleStream's decoder reads it (net.Pipe),
	// and the decoder only reads once the goroutine below is running.
	handled := make(chan struct{})
	go func() { defer close(handled); core.handleStream(server) }()
	testutil.NoError(t, json.NewEncoder(client).Encode(StreamHeader{TaskID: id}))
	waitFor(t, "stream registered", func() bool {
		core.mu.Lock()
		defer core.mu.Unlock()
		return len(core.streams[id]) == 1
	})

	core.mu.Lock()
	_, stillStale := core.exitInfos[id]
	core.mu.Unlock()
	testutil.False(t, stillStale)

	// The replacement's real exit now yields its own ExitInfo, never the stale one.
	var stop StatusResp
	testutil.NoError(t, core.StopSession(&StopReq{TaskID: id}, &stop))
	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("stream never ended after the session was stopped")
	}
	var info ExitInfo
	waitFor(t, "fresh exit info", func() bool {
		testutil.NoError(t, core.GetExitInfo(&TaskIDReq{TaskID: id}, &info))
		return info.Stopped
	})
	testutil.False(t, info.PendingRestart)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ownedSupClient is a supervisor-client that offers the owned exit relay.
type ownedSupClient struct {
	fakeSupClient
	ownedFn func(string, ExitInfo, agent.SessionHandle)
}

func (f *ownedSupClient) OnSessionExitOwned(fn func(string, ExitInfo, agent.SessionHandle)) {
	f.ownedFn = fn
}

// UseSupervisorRunner must wire the OWNED relay when the client offers it: the
// client drops the exiting session from its map before firing, so the handle
// can only reach the daemon's exit cache through this callback.
func TestDaemon_UseSupervisorRunnerWiresOwnedRelay(t *testing.T) {
	d, _ := testDaemon(t)
	c := &ownedSupClient{}
	d.UseSupervisorRunner(c)
	testutil.NotNil(t, c.ownedFn)
	sess := newDoneSession(true)
	c.ownedFn("t", ExitInfo{Stopped: true, PendingRestart: true}, sess)
	d.mu.Lock()
	e := d.exitInfos["t"]
	d.mu.Unlock()
	testutil.True(t, e.owner == agent.SessionHandle(sess))
	testutil.True(t, e.info.Stopped)
}

// The in-process (supervisor OFF) onFinish must stamp the exiting session.
func TestDaemon_InProcessExitStampsOwner(t *testing.T) {
	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: "true"}))
	task := &model.Task{Name: "owner-test", Status: model.StatusInProgress, Worktree: t.TempDir(), Backend: "test"}
	testutil.NoError(t, d.db.Add(task))
	sess, err := d.runner.Start(task, d.db.Config(), 24, 80, false)
	testutil.NoError(t, err)
	waitFor(t, "exit info recorded", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		_, ok := d.exitInfos[task.ID]
		return ok
	})
	d.mu.Lock()
	e := d.exitInfos[task.ID]
	d.mu.Unlock()
	testutil.True(t, e.owner == sess)
}
