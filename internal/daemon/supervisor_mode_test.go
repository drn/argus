package daemon

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeSupClient is a no-op SupervisorClient that records whether StopAll and
// Close were invoked — enough to pin the cleanup mode-awareness invariant
// (supervisor mode must detach, NOT StopAll, so the supervisor's agents survive
// the daemon bounce). All other methods are inert stubs to satisfy the interface.
type fakeSupClient struct {
	stopAllCalled bool
	closeCalled   bool
	exitFn        func(string, ExitInfo)

	// running is the live task-ID set Running() reports. nil mirrors the real
	// client's RPC-failure return (the daemon must NOT reconcile on nil); a
	// non-nil (even empty) slice is an authoritative "these are alive" answer.
	running []string
	// getCalls records the task IDs passed to Get — the supervisor-mode startup
	// reconcile calls Get on every live ID to re-attach (arm the exit relay).
	getCalls []string

	// helloResp / helloErr override the Hello handshake reply so BootInfo-relay
	// tests can simulate a v3 supervisor (hash + VCS), a v2 supervisor (empty
	// hash ⇒ present-but-unknown), or a transport failure. When both are unset
	// Hello returns the default matching-version reply.
	helloResp *HelloResp
	helloErr  error
}

func (f *fakeSupClient) Start(*model.Task, config.Config, uint16, uint16, bool) (agent.SessionHandle, error) {
	return nil, nil
}
func (f *fakeSupClient) Stop(string) error { return nil }
func (f *fakeSupClient) StopAll()          { f.stopAllCalled = true }
func (f *fakeSupClient) Get(id string) agent.SessionHandle {
	f.getCalls = append(f.getCalls, id)
	return nil
}
func (f *fakeSupClient) Running() []string                        { return f.running }
func (f *fakeSupClient) Idle() []string                           { return nil }
func (f *fakeSupClient) RunningAndIdle() (running, idle []string) { return nil, nil }
func (f *fakeSupClient) HasSession(string) bool                   { return false }
func (f *fakeSupClient) WorkDir(string) string                    { return "" }
func (f *fakeSupClient) HasPendingRestart(string) bool            { return false }
func (f *fakeSupClient) StartOrReattach(*model.Task, config.Config, uint16, uint16, bool) (agent.SessionHandle, bool, error) {
	return nil, false, nil
}
func (f *fakeSupClient) KickRerender(*model.Task, config.Config, uint16, uint16) error { return nil }
func (f *fakeSupClient) Recycle(*model.Task, config.Config, uint16, uint16) error      { return nil }
func (f *fakeSupClient) NeedsInputIDs() []string                                       { return nil }
func (f *fakeSupClient) SetNeedsInputIDs([]string)                                     {}
func (f *fakeSupClient) OnSessionExit(fn func(string, ExitInfo))                       { f.exitFn = fn }
func (f *fakeSupClient) Hello() (HelloResp, error) {
	if f.helloErr != nil {
		return HelloResp{}, f.helloErr
	}
	if f.helloResp != nil {
		return *f.helloResp, nil
	}
	return HelloResp{ProtocolVersion: ProtocolVersion}, nil
}
func (f *fakeSupClient) Close() error { f.closeCalled = true; return nil }

// Compile-time assertion that the fake satisfies the daemon's contract.
var _ SupervisorClient = (*fakeSupClient)(nil)

// TestUseSupervisorRunner_WiresExitRelay pins that mounting a supervisor-client
// (a) swaps it in as the daemon's runner and (b) wires its exit relay to
// handleSessionExit so a relayed clean exit still flips the DB (#707 across the
// boundary).
func TestUseSupervisorRunner_WiresExitRelay(t *testing.T) {
	d, _ := testDaemon(t)
	fake := &fakeSupClient{}
	d.UseSupervisorRunner(fake)

	testutil.Equal(t, d.supClient != nil, true)
	if fake.exitFn == nil {
		t.Fatal("UseSupervisorRunner must register the exit relay")
	}

	// A relayed clean exit drives the DB flip through the wired callback.
	task := &model.Task{Name: "relayed", Status: model.StatusInProgress}
	testutil.NoError(t, d.db.Add(task))
	fake.exitFn(task.ID, ExitInfo{}) // clean
	got, err := d.db.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
}

// TestCleanup_SupervisorMode_DoesNotStopAgents pins the load-bearing invariant:
// in supervisor mode the daemon detaches (Close) on shutdown and MUST NOT StopAll
// — the supervisor owns the agent PTYs and they survive the daemon bounce (the
// whole reason the supervisor exists).
func TestCleanup_SupervisorMode_DoesNotStopAgents(t *testing.T) {
	d, _ := testDaemon(t)
	fake := &fakeSupClient{}
	d.UseSupervisorRunner(fake)

	d.cleanup()

	testutil.Equal(t, fake.closeCalled, true)
	testutil.Equal(t, fake.stopAllCalled, false)
}

// TestCleanup_InProcessMode_StopsAgents pins the OFF-mode counterpart: with no
// supervisor-client the daemon owns its in-process runner and cleanup StopAll-s
// it (byte-identical to pre-P2). We assert via a fake runner mounted directly.
func TestCleanup_InProcessMode_StopsAgents(t *testing.T) {
	d, _ := testDaemon(t)
	fake := &fakeSupClient{}
	// Mount the fake as the in-process runner WITHOUT marking supervised, so the
	// OFF cleanup branch runs (writeLiveTasksFile + StopAll).
	d.runner = fake
	testutil.Equal(t, d.supClient == nil, true)

	d.cleanup()

	testutil.Equal(t, fake.stopAllCalled, true)
	testutil.Equal(t, fake.closeCalled, false)
}
