package daemon

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/config"

	"github.com/drn/argus/internal/testutil"
)

// TestBootInfoRelaysSupervisorSurface pins the wire path the whole verdict rides
// on: whatever surface the supervisor reports in its Hello must reach the TUI
// through BootInfo unaltered. A relay that dropped these fields would silently
// report every supervisor as pre-v6 "unknown" — a false negative, the worst
// outcome this mechanism can produce.
func TestBootInfoRelaysSupervisorSurface(t *testing.T) {
	tests := []struct {
		name       string
		hello      *HelloResp
		helloErr   bool
		wantSpawn  int
		wantStream int
	}{
		{
			name: "v6 supervisor relays both components",
			hello: &HelloResp{
				ProtocolVersion: ProtocolVersion,
				BinaryPath:      "/opt/argus",
				BinaryHash:      "abc123",
				SpawnSurface:    7,
				StreamSurface:   9,
			},
			wantSpawn:  7,
			wantStream: 9,
		},
		{
			name: "a supervisor running this very build relays this build's surface",
			hello: &HelloResp{
				ProtocolVersion: ProtocolVersion,
				BinaryHash:      "abc123",
				SpawnSurface:    SupervisorSpawnSurface,
				StreamSurface:   SupervisorStreamSurface,
			},
			wantSpawn:  SupervisorSpawnSurface,
			wantStream: SupervisorStreamSurface,
		},
		{
			name: "pre-v6 supervisor omits both ⇒ zero, which reads as unknown not stale",
			hello: &HelloResp{
				ProtocolVersion: 5,
				BinaryPath:      "/opt/argus-old",
				BinaryHash:      "old123",
			},
			wantSpawn:  0,
			wantStream: 0,
		},
		{
			name:     "unreachable supervisor leaves the surface unreported",
			helloErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			if tt.helloErr {
				d.UseSupervisorRunner(&fakeSupClient{helloErr: errUnreachableSupervisor})
			} else {
				d.UseSupervisorRunner(&fakeSupClient{helloResp: tt.hello})
			}

			var resp BootInfoResp
			testutil.NoError(t, rpcFor(d).BootInfo(&Empty{}, &resp))
			testutil.Equal(t, resp.SupervisorSpawnSurface, tt.wantSpawn)
			testutil.Equal(t, resp.SupervisorStreamSurface, tt.wantStream)
		})
	}
}

// TestHelloResp_SupervisorSurface pins the accessor the relay reads through,
// including the pre-v6 zero value that must report as not-Known.
func TestHelloResp_SupervisorSurface(t *testing.T) {
	testutil.DeepEqual(t,
		HelloResp{SpawnSurface: 2, StreamSurface: 5}.SupervisorSurface(),
		SurfaceVersion{Spawn: 2, Stream: 5})
	testutil.Equal(t, HelloResp{}.SupervisorSurface().Known(), false)
}

// TestProtocolVersionCoversSurfaceFields is the additive-protocol guard: the R/S
// contract in types.go is to bump ProtocolVersion on ANY new optional field, and
// the surface fields are that. Pinning the floor here means a future edit that
// reverts the bump — leaving a v5 daemon claiming to speak fields it does not
// send — fails loudly.
func TestProtocolVersionCoversSurfaceFields(t *testing.T) {
	if ProtocolVersion < 6 {
		t.Fatalf("HelloResp carries SpawnSurface/StreamSurface, which landed in v6; ProtocolVersion is %d", ProtocolVersion)
	}
}

var errUnreachableSupervisor = errors.New("dial: connection refused")

// TestSup_HelloReportsSurface drives the real supervisor over its real socket and
// asserts its handshake carries the surface version this binary was compiled
// with. This is the producing end of the relay pinned above — together they cover
// supervisor → daemon → TUI.
func TestSup_HelloReportsSurface(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, sock := testSupervisor(t, config.DefaultConfig())
	go s.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { s.Shutdown() })
	waitForSocket(t, sock)

	c := dialRPC(t, sock)
	var hello HelloResp
	testutil.NoError(t, c.Call("Daemon.Hello", &Empty{}, &hello))

	testutil.DeepEqual(t, hello.SupervisorSurface(), CurrentSupervisorSurface())
	// A supervisor running this build is by definition coherent with it.
	testutil.Equal(t, CompareSupervisorSurface(hello.SupervisorSurface()), SurfaceCoherent)
}
