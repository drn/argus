package doctor

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// surfaceActors builds the two rows the supervisor verdict is computed from: the
// TUI (this build's reference surface) and the live supervisor. Both resolve to
// the same path, so path-divergence never fires and the only thing under test is
// the hash-vs-surface decision.
func surfaceActors(tuiHash, supHash string, tui, sup [2]int) []Actor {
	return []Actor{
		{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: tuiHash, Resolved: true,
			SpawnSurface: tui[0], StreamSurface: tui[1]},
		{Role: RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: supHash, Resolved: true,
			SpawnSurface: sup[0], StreamSurface: sup[1]},
	}
}

// TestDiagnose_SupervisorJudgedOnSurface is the doctor half of the change. The
// first case is the one that matters: differing binary hashes, matching surface,
// verdict HEALTHY. That is the ~9-in-10 build that used to exit 1 and point the
// operator at a restart costing every running agent (design D5 — the exit-code
// change is the point, not a side effect).
func TestDiagnose_SupervisorJudgedOnSurface(t *testing.T) {
	tests := []struct {
		name    string
		actors  []Actor
		want    Verdict
		remHas  string
		remNone bool
	}{
		{
			name:    "differing hash but matching surface ⇒ HEALTHY",
			actors:  surfaceActors("new", "old", [2]int{1, 1}, [2]int{1, 1}),
			want:    Healthy,
			remNone: true,
		},
		{
			name:   "spawn behind ⇒ restart needed, but says running agents are unaffected",
			actors: surfaceActors("new", "old", [2]int{2, 1}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "UNAFFECTED",
		},
		{
			name:   "stream behind ⇒ restart needed, says live sessions are affected",
			actors: surfaceActors("new", "old", [2]int{1, 2}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "LIVE sessions are affected",
		},
		{
			name:   "both behind ⇒ reports the stream consequences",
			actors: surfaceActors("new", "old", [2]int{2, 2}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "LIVE sessions are affected",
		},
		{
			name:   "both behind ⇒ ALSO reports the spawn consequences",
			actors: surfaceActors("new", "old", [2]int{2, 2}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "argus MCP wiring",
		},
		{
			name:   "spawn behind ⇒ enumerates argus MCP wiring",
			actors: surfaceActors("new", "old", [2]int{2, 1}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "argus MCP wiring",
		},
		{
			name: "surface mismatch is caught even with matching hashes and no other rows",
			// Not reachable in practice (same file ⇒ same constants), but the
			// surface is the verdict signal now, so it must stand on its own
			// rather than riding on a hash pair happening to be resolvable.
			actors: surfaceActors("same", "same", [2]int{1, 2}, [2]int{1, 1}),
			want:   RestartNeeded,
			remHas: "STREAM surface",
		},
		{
			name:   "pre-v6 supervisor reports no surface ⇒ falls back to the hash",
			actors: surfaceActors("new", "old", [2]int{1, 1}, [2]int{0, 0}),
			want:   RestartNeeded,
			remHas: "older build",
		},
		{
			name:    "pre-v6 supervisor with a matching hash ⇒ healthy, never a false stale",
			actors:  surfaceActors("same", "same", [2]int{1, 1}, [2]int{0, 0}),
			want:    Healthy,
			remNone: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diagnose(tt.actors)
			testutil.Equal(t, got.Verdict, tt.want)
			if tt.remNone {
				testutil.Equal(t, got.Remediation, "")
				return
			}
			testutil.Contains(t, got.Remediation, tt.remHas)
		})
	}
}

// TestDiagnose_SurfaceCoherenceDoesNotExcuseTheDaemon pins the exemption's scope:
// only the SUPERVISOR is judged on its surface. A stale daemon at the same path
// must still be reported, because bouncing it is cheap and never touches agents.
func TestDiagnose_SurfaceCoherenceDoesNotExcuseTheDaemon(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true, SpawnSurface: 1, StreamSurface: 1},
		{Role: RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true, SpawnSurface: 1, StreamSurface: 1},
		{Role: RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "old", Resolved: true},
	})
	testutil.Equal(t, got.Verdict, RestartNeeded)
	testutil.Contains(t, got.Remediation, "argus daemon restart")
}

// TestDiagnose_PathDivergenceStillOutranksSurface pins that surface coherence
// never masks path divergence. They are different problems: divergence is about
// the INSTALL topology (a restart relaunches the same divergent file and loops),
// not about the supervisor being behind, and it is the footgun the priority order
// exists to protect.
func TestDiagnose_PathDivergenceStillOutranksSurface(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RoleTUI, ResolvedPath: "/usr/local/bin/argus", Hash: "a", Resolved: true, SpawnSurface: 1, StreamSurface: 1},
		{Role: RoleSupervisor, ResolvedPath: "/opt/homebrew/bin/argus", Hash: "b", Resolved: true, SpawnSurface: 1, StreamSurface: 1},
	})
	testutil.Equal(t, got.Verdict, PathDivergence)
}

// TestDiagnose_NoSupervisorIsUnaffected pins that an in-process-runner install
// (no supervisor row at all) still diagnoses normally.
func TestDiagnose_NoSupervisorIsUnaffected(t *testing.T) {
	got := Diagnose([]Actor{
		{Role: RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "new", Resolved: true, SpawnSurface: 1, StreamSurface: 1},
		{Role: RoleSupervisor, Resolved: false, Note: "no supervisor (in-process runner)"},
	})
	testutil.Equal(t, got.Verdict, Healthy)
}

// TestRender_ShowsSurfaceAlongsideHashes pins design D5's honesty clause: the
// verdict moved to the surface, but the table must still show BOTH signals so
// nothing became less inspectable.
func TestRender_ShowsSurfaceAlongsideHashes(t *testing.T) {
	out := Render(surfaceActors("new", "old", [2]int{1, 1}, [2]int{1, 1}))
	testutil.Contains(t, out, "surface spawn=1 stream=1")
	testutil.Contains(t, out, "HEALTHY")
	// Two rows carry a surface: the supervisor and the TUI reference.
	if n := strings.Count(out, "[surface "); n != 2 {
		t.Errorf("expected the supervisor and TUI rows to show a surface, got %d rows with one", n)
	}
	// The hashes stay visible.
	testutil.Contains(t, out, "sha:old")
	testutil.Contains(t, out, "sha:new")
}

// TestRender_UnknownSurfaceRendersAsUnknown pins that a pre-v6 supervisor's row
// says so explicitly rather than rendering a misleading "spawn=0 stream=0".
func TestRender_UnknownSurfaceRendersAsUnknown(t *testing.T) {
	out := Render(surfaceActors("same", "same", [2]int{1, 1}, [2]int{0, 0}))
	testutil.Contains(t, out, "[surface unknown]")
}

func TestActorSurfaceHelpers(t *testing.T) {
	tests := []struct {
		name  string
		a     Actor
		known bool
		str   string
	}{
		{"unreported", Actor{}, false, "unknown"},
		{"spawn only", Actor{SpawnSurface: 2}, true, "spawn=2 stream=0"},
		{"stream only", Actor{StreamSurface: 3}, true, "spawn=0 stream=3"},
		{"both", Actor{SpawnSurface: 4, StreamSurface: 5}, true, "spawn=4 stream=5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, tt.a.surfaceKnown(), tt.known)
			testutil.Equal(t, tt.a.surfaceString(), tt.str)
		})
	}
}

// TestSupervisorSurfaceSkew_Verdicts pins the classification itself, including
// that BOTH stale surfaces yield their own verdict instead of stream shadowing
// spawn (the 2026-10-01 incident: stream 1 vs 5 and spawn 6 vs 14 printed only
// the stream message, hiding that new sessions lost argus MCP).
func TestSupervisorSurfaceSkew_Verdicts(t *testing.T) {
	byRole := func(tui, sup [2]int) map[Role]Actor {
		m := map[Role]Actor{}
		for _, a := range surfaceActors("a", "b", tui, sup) {
			m[a.Role] = a
		}
		return m
	}
	tests := []struct {
		name string
		in   map[Role]Actor
		want surfaceVerdict
	}{
		{"coherent", byRole([2]int{1, 1}, [2]int{1, 1}), surfaceCoherent},
		{"spawn only", byRole([2]int{2, 1}, [2]int{1, 1}), surfaceSpawnStale},
		{"stream only", byRole([2]int{1, 2}, [2]int{1, 1}), surfaceStreamStale},
		{"both", byRole([2]int{14, 5}, [2]int{6, 1}), surfaceBothStale},
		{"unknown supervisor", byRole([2]int{1, 1}, [2]int{0, 0}), surfaceUnknown},
		{"no rows", map[Role]Actor{}, surfaceUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, supervisorSurfaceSkew(tt.in), tt.want)
		})
	}
}

// TestSupervisorSurfaceFix_BothStale pins the combined message: both
// consequence blocks and exactly one remediation line.
func TestSupervisorSurfaceFix_BothStale(t *testing.T) {
	got := supervisorSurfaceFix(Actor{ResolvedPath: "/opt/bin/argus"}, surfaceBothStale)
	testutil.Contains(t, got, "STREAM")
	testutil.Contains(t, got, "SPAWN")
	testutil.Contains(t, got, "LIVE sessions are affected")
	testutil.Contains(t, got, "argus MCP wiring")
	testutil.Contains(t, got, "/opt/bin/argus")
	testutil.Equal(t, strings.Count(got, "argus session-supervisor stop"), 1)
}
