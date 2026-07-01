package doctor

import (
	"testing"

	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/testutil"
)

// healthyActors builds a fully-coherent set: PATH argus, argusd target,
// go-install target, daemon, supervisor, and TUI all resolve to the same file
// with the same content hash.
func healthyActors() []Actor {
	const path = "/opt/bin/argus"
	const hash = "aaaaaaaaaaaaaaaa"
	mk := func(r Role) Actor {
		return Actor{Role: r, ResolvedPath: path, Hash: hash, Resolved: true}
	}
	return []Actor{
		mk(RolePathArgus),
		mk(RoleArgusdTarget),
		mk(RoleGoInstall),
		mk(RoleDaemon),
		mk(RoleSupervisor),
		mk(RoleTUI),
	}
}

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name    string
		actors  []Actor
		want    Verdict
		wantRem bool // remediation text expected (non-empty)
	}{
		{
			name:   "healthy: all resolve to same file with matching hash",
			actors: healthyActors(),
			want:   Healthy,
		},
		{
			name: "restart-needed: daemon on old bytes at the same path",
			actors: []Actor{
				{Role: RolePathArgus, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
				{Role: RoleArgusdTarget, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
				{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
				{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
			},
			want:    RestartNeeded,
			wantRem: true,
		},
		{
			name: "restart-needed: supervisor on old bytes names the agent-interrupt caveat",
			actors: []Actor{
				{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
				{Role: RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
			},
			want:    RestartNeeded,
			wantRem: true,
		},
		{
			name: "path-divergence: argusd target and PATH argus are different files",
			actors: []Actor{
				{Role: RolePathArgus, ResolvedPath: "/usr/local/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleArgusdTarget, ResolvedPath: "/opt/homebrew/bin/argus", Hash: "bbb", Resolved: true},
			},
			want:    PathDivergence,
			wantRem: true,
		},
		{
			name: "path-divergence: a running process resolves to a different file than the TUI",
			actors: []Actor{
				{Role: RoleTUI, ResolvedPath: "/usr/local/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleDaemon, ResolvedPath: "/opt/homebrew/bin/argus", Hash: "bbb", Resolved: true},
			},
			want:    PathDivergence,
			wantRem: true,
		},
		{
			name: "path-divergence takes precedence over restart-needed (a restart would loop)",
			actors: []Actor{
				// diverging disk anchors → footgun
				{Role: RolePathArgus, ResolvedPath: "/usr/local/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleArgusdTarget, ResolvedPath: "/opt/homebrew/bin/argus", Hash: "bbb", Resolved: true},
				// and separately a same-path hash mismatch that would read as restart-needed
				{Role: RoleTUI, ResolvedPath: "/usr/local/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleDaemon, ResolvedPath: "/usr/local/bin/argus", Hash: "old", Resolved: true},
			},
			want:    PathDivergence,
			wantRem: true,
		},
		{
			name: "unknown-degrades: an unresolvable go-install row does not abort or flag",
			actors: []Actor{
				{Role: RolePathArgus, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleArgusdTarget, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleGoInstall, Resolved: false, Note: "go not found"},
			},
			want: Healthy,
		},
		{
			name: "unknown-degrades: no daemon running leaves daemon/supervisor unknown, verdict healthy",
			actors: []Actor{
				{Role: RolePathArgus, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleArgusdTarget, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleDaemon, Resolved: false, Note: "no daemon"},
				{Role: RoleSupervisor, Resolved: false, Note: "no daemon"},
			},
			want: Healthy,
		},
		{
			name: "unknown-degrades: present-but-unknown supervisor (old protocol, empty hash) is never stale",
			actors: []Actor{
				{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "aaa", Resolved: true},
				// supervisor present but pre-v3: path known, hash empty ⇒ not resolved for the verdict
				{Role: RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: "", Resolved: false, Note: "old protocol"},
			},
			want: Healthy,
		},
		{
			name:   "empty input degrades to healthy rather than aborting",
			actors: nil,
			want:   Healthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diagnose(tt.actors)
			testutil.Equal(t, got.Verdict, tt.want)
			if tt.wantRem {
				if got.Remediation == "" {
					t.Errorf("expected non-empty remediation for %v", got.Verdict)
				}
			} else {
				testutil.Equal(t, got.Remediation, "")
			}
		})
	}
}

func TestDiagnose_RestartRemediationMentionsDaemonCommand(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
		{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
	})
	testutil.Equal(t, got.Verdict, RestartNeeded)
	testutil.Contains(t, got.Remediation, "argus daemon restart")
}

func TestDiagnose_SupervisorRestartWarnsAboutAgents(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
		{Role: RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
	})
	testutil.Equal(t, got.Verdict, RestartNeeded)
	testutil.Contains(t, got.Remediation, "agent")
}

func TestDiagnose_PathDivergenceRemediationNamesBothPaths(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RolePathArgus, ResolvedPath: "/usr/local/bin/argus", Hash: "aaa", Resolved: true},
		{Role: RoleArgusdTarget, ResolvedPath: "/opt/homebrew/bin/argus", Hash: "bbb", Resolved: true},
	})
	testutil.Equal(t, got.Verdict, PathDivergence)
	testutil.Contains(t, got.Remediation, "/usr/local/bin/argus")
	testutil.Contains(t, got.Remediation, "/opt/homebrew/bin/argus")
}

func TestRoleString(t *testing.T) {
	roles := []Role{RolePathArgus, RoleArgusdTarget, RoleGoInstall, RoleDaemon, RoleSupervisor, RoleTUI, Role(99)}
	for _, r := range roles {
		if r.String() == "" {
			t.Errorf("Role(%d).String() is empty", int(r))
		}
	}
}

func TestVerdictString(t *testing.T) {
	for _, v := range []Verdict{Healthy, RestartNeeded, PathDivergence, Verdict(99)} {
		if v.String() == "" {
			t.Errorf("Verdict(%d).String() is empty", int(v))
		}
	}
}

func TestRender(t *testing.T) {
	t.Run("healthy render lists every role and a healthy verdict", func(t *testing.T) {
		out := Render(healthyActors())
		testutil.Contains(t, out, "PATH argus")
		testutil.Contains(t, out, "daemon")
		testutil.Contains(t, out, "supervisor")
		testutil.Contains(t, out, "Verdict")
		testutil.Contains(t, out, Healthy.String())
	})

	t.Run("rich identity shows commit SHA and dirty flag when VCS present", func(t *testing.T) {
		out := Render([]Actor{{
			Role:         RoleTUI,
			ResolvedPath: "/opt/bin/argus",
			Hash:         "deadbeefdeadbeef",
			VCS:          buildid.VCS{Revision: "abcdef1234567890", Modified: true},
			Resolved:     true,
		}})
		testutil.Contains(t, out, "abcdef123456") // short SHA
		testutil.Contains(t, out, "dirty")
		testutil.Contains(t, out, "/opt/bin/argus")
	})

	t.Run("hash fallback shown when VCS info absent", func(t *testing.T) {
		out := Render([]Actor{{
			Role:         RoleDaemon,
			ResolvedPath: "/opt/bin/argus",
			Hash:         "0123456789abcdef",
			Resolved:     true,
		}})
		testutil.Contains(t, out, "0123456789ab") // short content hash
	})

	t.Run("unresolved row renders as unknown with its note", func(t *testing.T) {
		out := Render([]Actor{{
			Role:     RoleSupervisor,
			Resolved: false,
			Note:     "no supervisor",
		}})
		testutil.Contains(t, out, "unknown")
		testutil.Contains(t, out, "no supervisor")
	})

	t.Run("non-healthy render includes the remediation text", func(t *testing.T) {
		out := Render([]Actor{
			{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true},
			{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
		})
		testutil.Contains(t, out, RestartNeeded.String())
		testutil.Contains(t, out, "argus daemon restart")
	})
}
