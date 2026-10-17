package client

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"sync"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestBoundedStopWire(t *testing.T) {
	c, rec := newWireRecorderClient(t)
	testutil.NoError(t, c.StopScoped("a", agent.StopRerender))
	testutil.NoError(t, c.KickWidthRerender(&model.Task{ID: "a"}, config.DefaultConfig(), 30, 98))
	rec.mu.Lock()
	testutil.Equal(t, rec.boundedStops, 1)
	testutil.Equal(t, rec.ordinaryStops, 0)
	testutil.Equal(t, rec.boundedKicks, 1)
	testutil.Equal(t, rec.ordinaryKicks, 0)
	rec.stopError = "boom"
	rec.mu.Unlock()
	err := c.StopScoped("a", agent.StopRerender)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "boom")
	rec.mu.Lock()
	testutil.Equal(t, rec.ordinaryStops, 0)
	rec.mu.Unlock()
}

func TestBoundedStopHops(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sc, rec := newWireRecorderClient(t)
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	d := daemon.New(database)
	d.UseSupervisorRunner(sc)
	sock := filepath.Join(t.TempDir(), "d.sock")
	go d.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitFile(t, sock)
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	testutil.NoError(t, c.StopScoped("a", agent.StopRerender))
	testutil.NoError(t, c.KickWidthRerender(&model.Task{ID: "a"}, config.DefaultConfig(), 30, 98))
	rec.mu.Lock()
	testutil.Equal(t, rec.boundedStops, 1)
	testutil.Equal(t, rec.ordinaryStops, 0)
	testutil.Equal(t, rec.boundedKicks, 1)
	testutil.Equal(t, rec.ordinaryKicks, 0)
	rec.mu.Unlock()
	// Session-switch/revive routing stays on the ordinary verb across both hops.
	testutil.NoError(t, c.KickRerender(&model.Task{ID: "a"}, config.DefaultConfig(), 30, 98))
	rec.mu.Lock()
	testutil.Equal(t, rec.boundedKicks, 1)
	testutil.Equal(t, rec.ordinaryKicks, 1)
	rec.mu.Unlock()
}

func TestSupRerender(t *testing.T) {
	d, sc, _ := supE2E(t)
	// Bypass client fallback to prove both supervisor capabilities exist.
	for _, method := range []string{"Daemon.StopRerender", "Daemon.KickWidthRerender"} {
		t.Run(method, func(t *testing.T) {
			var resp daemon.StatusResp
			err := sc.call(method, &daemon.KickReq{TaskID: "missing"}, &resp)
			testutil.NoError(t, err)
			testutil.Contains(t, resp.Error, "session not found")
		})
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	go d.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitFile(t, sock)
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	// Both real RPC hops must recognize the verb and reach the supervisor's
	// runner lookup. The separate scope-forwarding test pins its scope value.
	err = c.StopScoped("missing", agent.StopRerender)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "session not found")
}

func TestOldWidthKick(t *testing.T) {
	server := rpc.NewServer()
	peer := &oldWidthKickPeer{}
	testutil.NoError(t, server.RegisterName("Daemon", peer))
	serverConn, clientConn := net.Pipe()
	go server.ServeCodec(jsonrpc.NewServerCodec(serverConn))
	c := &Client{rpc: jsonrpc.NewClient(clientConn), closed: make(chan struct{})}
	t.Cleanup(func() { _ = c.Close(); _ = serverConn.Close() })
	testutil.NoError(t, c.KickWidthRerender(&model.Task{ID: "a"}, config.DefaultConfig(), 30, 98))
	peer.mu.Lock()
	defer peer.mu.Unlock()
	testutil.Equal(t, peer.calls, 1)
}

type oldWidthKickPeer struct {
	mu    sync.Mutex
	calls int
}

func (p *oldWidthKickPeer) KickRerender(_ *daemon.KickReq, resp *daemon.StatusResp) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	resp.OK = true
	return nil
}

func TestWidthKickError(t *testing.T) {
	c, rec := newWireRecorderClient(t)
	rec.mu.Lock()
	rec.kickError = "boom"
	rec.mu.Unlock()
	err := c.KickWidthRerender(&model.Task{ID: "a"}, config.DefaultConfig(), 30, 98)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "boom")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	testutil.Equal(t, rec.ordinaryKicks, 0)
	testutil.Equal(t, rec.boundedKicks, 1)
}

func TestOldRerenderStop(t *testing.T) {
	// FakeRPCService exposes the old StopSession but no StopRerender. If the
	// client falls back, it must send the existing agent-only scope.
	fd := newFakeDaemon(t)
	c := fakeClient(t, fd)
	err := c.StopScoped("a", agent.StopRerender)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "fake-stop-fail")
	fd.mu.Lock()
	defer fd.mu.Unlock()
	testutil.DeepEqual(t, fd.stopScopes, []agent.StopScope{agent.StopAgentOnly})
}
