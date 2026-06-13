package client

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestCliNeedsInput pins the local needs-input set used when this Client is the
// daemon's supervisor-client. It is purely local (no RPC) — needs-input is a
// daemon-side notion, not something the supervisor tracks.
func TestCliNeedsInput(t *testing.T) {
	c := &Client{closed: make(chan struct{})}
	testutil.Equal(t, len(c.NeedsInputIDs()), 0)

	c.SetNeedsInputIDs([]string{"a", "b"})
	got := c.NeedsInputIDs()
	testutil.Equal(t, len(got), 2)
	testutil.Equal(t, got[0], "a")

	// The getter returns a copy — mutating it must not corrupt internal state.
	got[0] = "mutated"
	testutil.Equal(t, c.NeedsInputIDs()[0], "a")

	// Replacing with empty clears the set.
	c.SetNeedsInputIDs(nil)
	testutil.Equal(t, len(c.NeedsInputIDs()), 0)
}

// TestCliListInfo pins ListSessionInfo — the relay the daemon's ListSessions
// uses when its runner is a supervisor-client.
func TestCliListInfo(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	infos := c.ListSessionInfo()
	testutil.Equal(t, len(infos), 2) // fake serves one alive + one idle
}

// TestCliHello pins the supervisor handshake round-trip.
func TestCliHello(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	resp, err := c.Hello()
	testutil.NoError(t, err)
	testutil.Equal(t, resp.ProtocolVersion, daemon.ProtocolVersion)
	testutil.Equal(t, resp.BinaryPath, "/fake/supervisor")
}

// TestCliSOR pins StartOrReattach's reattach branch: when the daemon reports a
// live session, it returns the existing handle with reattached=true (no Start).
func TestCliSOR(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.alive = true
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	sess, reattached, err := c.StartOrReattach(&model.Task{ID: "sor"}, config.DefaultConfig(), 24, 80, true)
	testutil.NoError(t, err)
	testutil.True(t, reattached)
	if sess == nil {
		t.Fatal("expected a session handle on reattach")
	}
}

// TestCliKickErr pins KickRerender surfacing the daemon-side resp.Error (here the
// fake has no KickRerender method, so the RPC itself errors — the daemon treats
// that as a non-fatal no-op kick, which is the v1-supervisor degradation path).
func TestCliKickErr(t *testing.T) {
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	err := c.KickRerender(&model.Task{ID: "k"}, config.DefaultConfig(), 24, 80)
	testutil.Error(t, err) // method absent on fake → rpc error
}

// TestSupCmd pins the supervisor autostart exec-build WITHOUT forking: argv is
// `<exe> session-supervisor start` and the proc is Setsid-detached. (Per the
// plan: assert the build, never spawn a real supervisor under test.)
func TestSupCmd(t *testing.T) {
	cmd, err := buildSupervisorStartCmd()
	testutil.NoError(t, err)
	args := cmd.Args
	if len(args) != 3 || args[1] != "session-supervisor" || args[2] != "start" {
		t.Fatalf("unexpected argv: %v", args)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("expected Setsid-detached SysProcAttr, got %+v", cmd.SysProcAttr)
	}
}

// TestSupAutoTestBinary pins the fork-bomb backstop: under `go test`,
// AutoStartSupervisor refuses rather than re-running the test binary.
func TestSupAutoTestBinary(t *testing.T) {
	_, err := AutoStartSupervisor("/tmp/nonexistent-supervisor.sock")
	testutil.ErrorIs(t, err, ErrTestBinary)
}
