package daemon

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/testutil"
)

// rpcFor builds an RPCService over the given daemon exactly as Serve does, so
// BootInfo can be exercised directly without binding a socket.
func rpcFor(d *Daemon) *RPCService {
	return &RPCService{sessionCore: d.sessionCore, daemon: d}
}

// TestBootInfoOwnVCS pins that the daemon reports its OWN captured VCS identity
// in BootInfo (display-only field, alongside the existing hash/path).
func TestBootInfoOwnVCS(t *testing.T) {
	d, _ := testDaemon(t)
	d.vcs = buildid.VCS{Revision: "cafebabe", Modified: true}

	var resp BootInfoResp
	testutil.NoError(t, rpcFor(d).BootInfo(&Empty{}, &resp))
	testutil.Equal(t, resp.VCS, buildid.VCS{Revision: "cafebabe", Modified: true})
}

// TestShortHashRPC covers the log-rendering helper across its three branches:
// empty ⇒ "unknown", long ⇒ truncated to 12, short ⇒ verbatim.
func TestShortHashRPC(t *testing.T) {
	testutil.Equal(t, shortHashRPC(""), "unknown")
	testutil.Equal(t, shortHashRPC("abc123"), "abc123")
	testutil.Equal(t, shortHashRPC("0123456789abcdef0123"), "0123456789ab")
}

// TestBootInfoSupervisorRelay covers the D1 relay: the daemon re-queries the
// connected supervisor's Hello at serve time and surfaces its identity — with
// correct present/unknown handling for a missing, old, or unreachable supervisor.
func TestBootInfoSupervisorRelay(t *testing.T) {
	tests := []struct {
		name        string
		mount       func(d *Daemon) // wires (or omits) the supervisor client
		wantPresent bool
		wantPath    string
		wantHash    string
		wantVCS     buildid.VCS
	}{
		{
			name:        "no supervisor (in-process runner)",
			mount:       func(d *Daemon) {}, // supClient stays nil
			wantPresent: false,
		},
		{
			name: "v3 supervisor relays hash and vcs",
			mount: func(d *Daemon) {
				d.UseSupervisorRunner(&fakeSupClient{helloResp: &HelloResp{
					ProtocolVersion: ProtocolVersion,
					BinaryPath:      "/opt/argus",
					BinaryHash:      "abc123",
					VCS:             buildid.VCS{Revision: "feedface", Modified: false},
				}})
			},
			wantPresent: true,
			wantPath:    "/opt/argus",
			wantHash:    "abc123",
			wantVCS:     buildid.VCS{Revision: "feedface"},
		},
		{
			name: "v2 supervisor: present but hash unknown",
			mount: func(d *Daemon) {
				d.UseSupervisorRunner(&fakeSupClient{helloResp: &HelloResp{
					ProtocolVersion: 2,
					BinaryPath:      "/opt/argus-old",
					BinaryHash:      "", // pre-hash protocol
				}})
			},
			wantPresent: true,
			wantPath:    "/opt/argus-old",
			wantHash:    "", // unknown, never a false stale
		},
		{
			name: "supervisor Hello unreachable: present, hash unknown",
			mount: func(d *Daemon) {
				d.UseSupervisorRunner(&fakeSupClient{helloErr: errors.New("dial: connection refused")})
			},
			wantPresent: true,
			wantHash:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			tt.mount(d)

			var resp BootInfoResp
			testutil.NoError(t, rpcFor(d).BootInfo(&Empty{}, &resp))
			testutil.Equal(t, resp.SupervisorPresent, tt.wantPresent)
			testutil.Equal(t, resp.SupervisorPath, tt.wantPath)
			testutil.Equal(t, resp.SupervisorHash, tt.wantHash)
			testutil.Equal(t, resp.SupervisorVCS, tt.wantVCS)
		})
	}
}
