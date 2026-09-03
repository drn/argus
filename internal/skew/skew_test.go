package skew

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

var errSentinel = errors.New("boom")

type fakeBootInfo struct {
	resp daemon.BootInfoResp
	err  error
}

func (f fakeBootInfo) BootInfo() (daemon.BootInfoResp, error) { return f.resp, f.err }

// currentSurface is what a supervisor running THIS build reports.
func currentSurface() daemon.SurfaceVersion { return daemon.CurrentSupervisorSurface() }

// supervisorInfo builds a BootInfo response for a present supervisor.
func supervisorInfo(hash string, sv daemon.SurfaceVersion) daemon.BootInfoResp {
	return daemon.BootInfoResp{
		SupervisorPresent:       true,
		SupervisorHash:          hash,
		SupervisorSpawnSurface:  sv.Spawn,
		SupervisorStreamSurface: sv.Stream,
	}
}

func TestShortHash(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"long digest truncated to 12", "0123456789abcdef0123", "0123456789ab"},
		{"exactly 12 unchanged", "0123456789ab", "0123456789ab"},
		{"short unchanged", "abc", "abc"},
		{"empty unchanged", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, ShortHash(tt.in), tt.want)
		})
	}
}

func TestDaemonStale(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	t0 := time.Unix(1_700_000_000, 0)
	t1 := time.Unix(1_700_000_500, 0)

	tests := []struct {
		name        string
		info        daemon.BootInfoResp
		tuiHash     string
		tuiHashErr  bool
		tuiMtime    time.Time
		tuiMtimeErr bool
		want        bool
	}{
		// Hash path (daemon reported a hash).
		{"hash equal → not stale", daemon.BootInfoResp{BinaryHash: hashA}, hashA, false, time.Time{}, false, false},
		{"hash differ → stale", daemon.BootInfoResp{BinaryHash: hashA}, hashB, false, time.Time{}, false, true},
		{"hash read error → not stale", daemon.BootInfoResp{BinaryHash: hashA}, "", true, time.Time{}, false, false},
		// Mtime fallback (daemon reported no hash).
		{"mtime zero → not stale", daemon.BootInfoResp{}, "", false, t0, false, false},
		{"mtime equal → not stale", daemon.BootInfoResp{BinaryMtime: t0}, "", false, t0, false, false},
		{"mtime differ → stale", daemon.BootInfoResp{BinaryMtime: t0}, "", false, t1, false, true},
		{"mtime stat error → not stale", daemon.BootInfoResp{BinaryMtime: t0}, "", false, time.Time{}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, DaemonStale(tt.info, tt.tuiHash, tt.tuiHashErr, tt.tuiMtime, tt.tuiMtimeErr), tt.want)
		})
	}
}

// TestSupervisorVerdict is the heart of this change: the supervisor is judged on
// its EXECUTED SURFACE, not the whole-binary hash. The load-bearing case is the
// first one — differing hashes, matching surface, verdict COHERENT. That is the
// ~9-in-10 build that used to be reported as skew and pointed the operator at a
// restart that kills every running agent.
func TestSupervisorVerdict(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cur := currentSurface()

	tests := []struct {
		name       string
		info       daemon.BootInfoResp
		tuiHash    string
		tuiHashErr bool
		want       daemon.SurfaceSkew
	}{
		{
			"differing binary hash but matching surface ⇒ COHERENT (the whole point)",
			supervisorInfo(hashA, cur), hashB, false, daemon.SurfaceCoherent,
		},
		{
			"no supervisor ⇒ coherent (nothing can be stale)",
			daemon.BootInfoResp{}, hashA, false, daemon.SurfaceCoherent,
		},
		{
			"matching hash and surface ⇒ coherent",
			supervisorInfo(hashA, cur), hashA, false, daemon.SurfaceCoherent,
		},
		{
			"spawn behind ⇒ spawn-stale (new sessions only)",
			supervisorInfo(hashA, daemon.SurfaceVersion{Spawn: cur.Spawn - 1, Stream: cur.Stream}),
			hashB, false, daemon.SurfaceSpawnStale,
		},
		{
			"stream behind ⇒ stream-stale (live sessions affected)",
			supervisorInfo(hashA, daemon.SurfaceVersion{Spawn: cur.Spawn, Stream: cur.Stream + 1}),
			hashB, false, daemon.SurfaceStreamStale,
		},
		{
			"pre-v6 supervisor, matching hash ⇒ unknown, never stale",
			supervisorInfo(hashA, daemon.SurfaceVersion{}), hashA, false, daemon.SurfaceUnknown,
		},
		{
			"pre-v6 supervisor, differing hash ⇒ legacy-stale (hash is its fallback signal)",
			supervisorInfo(hashA, daemon.SurfaceVersion{}), hashB, false, daemon.SurfaceLegacyStale,
		},
		{
			"pre-v6 supervisor with no hash at all ⇒ unknown",
			supervisorInfo("", daemon.SurfaceVersion{}), hashA, false, daemon.SurfaceUnknown,
		},
		{
			"pre-v6 supervisor but the TUI's own hash failed to read ⇒ unknown, never a false stale",
			supervisorInfo(hashA, daemon.SurfaceVersion{}), "", true, daemon.SurfaceUnknown,
		},
		{
			"differing VCS is display-only and never gates",
			daemon.BootInfoResp{
				SupervisorPresent:       true,
				SupervisorHash:          hashA,
				SupervisorVCS:           buildid.VCS{Revision: "zzz"},
				SupervisorSpawnSurface:  cur.Spawn,
				SupervisorStreamSurface: cur.Stream,
			},
			hashB, false, daemon.SurfaceCoherent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, SupervisorVerdict(tt.info, tt.tuiHash, tt.tuiHashErr), tt.want)
		})
	}
}

func TestSupervisorSurface(t *testing.T) {
	info := daemon.BootInfoResp{SupervisorSpawnSurface: 3, SupervisorStreamSurface: 4}
	testutil.DeepEqual(t, SupervisorSurface(info), daemon.SurfaceVersion{Spawn: 3, Stream: 4})
	testutil.Equal(t, SupervisorSurface(daemon.BootInfoResp{}).Known(), false)
}

// TestResultSurfaces pins how a verdict is presented: which tiers block the
// operator with a modal, and what the transient notice says.
func TestResultSurfaces(t *testing.T) {
	tests := []struct {
		name       string
		res        Result
		blocking   bool
		stale      bool
		noticeHas  string
		noticeNone bool
	}{
		{
			name:       "coherent says nothing and blocks nothing",
			res:        Result{Supervisor: daemon.SurfaceCoherent},
			noticeNone: true,
		},
		{
			name:       "an unknown (pre-v6) supervisor says nothing and blocks nothing",
			res:        Result{Supervisor: daemon.SurfaceUnknown},
			noticeNone: true,
		},
		{
			name:      "a spawn-only mismatch notices but never blocks",
			res:       Result{Supervisor: daemon.SurfaceSpawnStale},
			blocking:  false,
			stale:     true,
			noticeHas: "unaffected",
		},
		{
			name:      "a stream mismatch blocks — live sessions are affected",
			res:       Result{Supervisor: daemon.SurfaceStreamStale},
			blocking:  true,
			stale:     true,
			noticeHas: "live sessions are affected",
		},
		{
			name:      "a legacy-stale supervisor blocks (tier unknowable ⇒ treated as the stricter one)",
			res:       Result{Supervisor: daemon.SurfaceLegacyStale},
			blocking:  true,
			stale:     true,
			noticeHas: "predates surface-version reporting",
		},
		{
			name:      "a stale daemon blocks on its own",
			res:       Result{DaemonStale: true, Supervisor: daemon.SurfaceCoherent},
			blocking:  true,
			noticeHas: "agents are unaffected",
		},
		{
			name:      "both stale reports the supervisor's consequence",
			res:       Result{DaemonStale: true, Supervisor: daemon.SurfaceSpawnStale},
			blocking:  true,
			stale:     true,
			noticeHas: "Daemon and supervisor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, tt.res.NeedsBlockingPrompt(), tt.blocking)
			testutil.Equal(t, tt.res.SupervisorStale(), tt.stale)
			if tt.noticeNone {
				testutil.Equal(t, tt.res.Notice(), "")
				return
			}
			testutil.Contains(t, tt.res.Notice(), tt.noticeHas)
		})
	}
}

// TestEvaluate exercises the full wiring (BootInfo → os.Executable →
// BinaryHashFile/stat → the decisions) against the running test binary, which is
// what os.Executable() resolves to under `go test`. It pins the load-bearing
// gating: the daemon decision honours checkDaemon (preExisting) while the
// supervisor decision fires regardless.
func TestEvaluate(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	selfHash, err := daemon.BinaryHashFile(exe)
	testutil.NoError(t, err)
	st, err := os.Stat(exe)
	testutil.NoError(t, err)
	selfMtime := st.ModTime()
	cur := currentSurface()

	t.Run("daemon decision", func(t *testing.T) {
		tests := []struct {
			name        string
			prov        fakeBootInfo
			checkDaemon bool
			want        bool
		}{
			{"BootInfo error → not stale", fakeBootInfo{err: errSentinel}, true, false},
			{"matching hash → not stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryHash: selfHash}}, true, false},
			{"different hash → stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryHash: "deadbeef"}}, true, true},
			{"different hash but checkDaemon=false → not stale (auto-start gate)",
				fakeBootInfo{resp: daemon.BootInfoResp{BinaryHash: "deadbeef"}}, false, false},
			{"no hash, matching mtime → not stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryMtime: selfMtime}}, true, false},
			{"no hash, different mtime → stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryMtime: selfMtime.Add(time.Hour)}}, true, true},
			{"no hash, zero mtime → not stale", fakeBootInfo{resp: daemon.BootInfoResp{}}, true, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				testutil.Equal(t, Evaluate(tt.prov, tt.checkDaemon).DaemonStale, tt.want)
			})
		}
	})

	t.Run("a differing supervisor binary with a matching surface is NOT stale", func(t *testing.T) {
		// The regression this change exists to prevent: the daemon is genuinely
		// stale, the supervisor's binary hash differs too — but its surface
		// matches, so it must come back coherent instead of prompting a restart
		// that would SIGHUP every agent.
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:              "deadbeef",
			SupervisorPresent:       true,
			SupervisorHash:          "cafebabe",
			SupervisorSpawnSurface:  cur.Spawn,
			SupervisorStreamSurface: cur.Stream,
		}}
		got := Evaluate(prov, true)
		testutil.Equal(t, got.DaemonStale, true)
		testutil.Equal(t, got.Supervisor, daemon.SurfaceCoherent)
		testutil.Equal(t, got.SupervisorStale(), false)
	})

	t.Run("supervisor checked even when checkDaemon=false", func(t *testing.T) {
		// The auto-start path passes checkDaemon=false; the supervisor must
		// still be evaluated, since the TUI did not fork it.
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:              "deadbeef",
			SupervisorPresent:       true,
			SupervisorHash:          "cafebabe",
			SupervisorSpawnSurface:  cur.Spawn,
			SupervisorStreamSurface: cur.Stream + 1,
		}}
		got := Evaluate(prov, false)
		testutil.Equal(t, got.DaemonStale, false)
		testutil.Equal(t, got.Supervisor, daemon.SurfaceStreamStale)
	})

	t.Run("old-protocol supervisor with a matching hash ⇒ unknown, not stale", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:        selfHash,
			SupervisorPresent: true,
			SupervisorHash:    selfHash,
		}}
		got := Evaluate(prov, true)
		testutil.Equal(t, got.Supervisor, daemon.SurfaceUnknown)
		testutil.Equal(t, got.SupervisorStale(), false)
	})

	t.Run("supervisor present with no hash at all ⇒ unknown", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			SupervisorPresent: true,
			SupervisorHash:    "",
		}}
		got := Evaluate(prov, true)
		testutil.Equal(t, got.Supervisor, daemon.SurfaceUnknown)
	})

	t.Run("identity strings populated", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:        "deadbeef",
			BinaryPath:        "/usr/local/bin/argus",
			SupervisorPresent: true,
			SupervisorHash:    "cafebabe",
			SupervisorPath:    "/gopath/bin/argus",
		}}
		got := Evaluate(prov, true)
		testutil.Contains(t, got.DaemonIdentity, "/usr/local/bin/argus")
		testutil.Contains(t, got.SupervisorIdentity, "/gopath/bin/argus")
	})

	t.Run("BootInfo error yields an empty, harmless result", func(t *testing.T) {
		got := Evaluate(fakeBootInfo{err: errSentinel}, true)
		testutil.Equal(t, got.DaemonStale, false)
		testutil.Equal(t, got.Supervisor, daemon.SurfaceCoherent)
		testutil.Equal(t, got.Notice(), "")
		testutil.Equal(t, got.NeedsBlockingPrompt(), false)
	})
}

// TestFormatIdentity pins the display formatting: rich SHA+dirty when VCS is
// present, short content-hash fallback otherwise, and "unknown" with neither.
func TestFormatIdentity(t *testing.T) {
	tests := []struct {
		name string
		vcs  buildid.VCS
		hash string
		path string
		want string
	}{
		{"vcs present clean", buildid.VCS{Revision: "0123456789abcdef"}, "hh", "/p/argus", "0123456789ab @ /p/argus"},
		{"vcs present dirty", buildid.VCS{Revision: "0123456789abcdef", Modified: true}, "hh", "/p/argus", "0123456789ab (dirty) @ /p/argus"},
		{"hash fallback", buildid.VCS{}, "abcdef0123456789", "/p/argus", "sha:abcdef012345 @ /p/argus"},
		{"unknown", buildid.VCS{}, "", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, FormatIdentity(tt.vcs, tt.hash, tt.path), tt.want)
		})
	}
}
