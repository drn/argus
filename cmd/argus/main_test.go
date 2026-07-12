package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

// TestConfigureProcessLogging_DoesNotReachStderr is the regression guard for
// the session-supervisor's logging discipline (CLAUDE.md rule 6): the supervisor
// forks PTY children, so slog.*/log.* must land in the supervisor.log writer,
// never on the process stderr. Mirrors uxlog's
// TestSlogWithUxlogWriter_DoesNotReachStderr.
func TestConfigureProcessLogging_DoesNotReachStderr(t *testing.T) {
	// Save process-global state we mutate; restore in LIFO before assertions
	// that read the buffer so late slog calls don't race the restore.
	origSlog := slog.Default()
	origLog := log.Writer()
	origStderr := os.Stderr
	t.Cleanup(func() {
		slog.SetDefault(origSlog)
		log.SetOutput(origLog)
		os.Stderr = origStderr
	})

	// Capture anything that hits stderr during this test.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	var buf bytes.Buffer
	configureProcessLogging(&buf)

	slog.Info("slog info from supervisor process")
	slog.Error("slog error from supervisor process", "task", "test-task")
	log.Printf("stdlib log print from supervisor process")

	// Restore before reading the pipe so no further writes target it.
	slog.SetDefault(origSlog)
	log.SetOutput(origLog)
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe writer: %v", cerr)
	}
	captured, rerr := io.ReadAll(r)
	if rerr != nil {
		t.Fatalf("read captured stderr: %v", rerr)
	}
	if len(captured) != 0 {
		t.Errorf("slog/log wrote to stderr after redirect: %q", string(captured))
	}

	content := buf.String()
	for _, want := range []string{
		"slog info from supervisor process",
		"slog error from supervisor process",
		"stdlib log print from supervisor process",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected %q in supervisor log writer, got: %s", want, content)
		}
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
			testutil.Equal(t, shortHash(tt.in), tt.want)
		})
	}
}

func TestStaleDecision(t *testing.T) {
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
			testutil.Equal(t, staleDecision(tt.info, tt.tuiHash, tt.tuiHashErr, tt.tuiMtime, tt.tuiMtimeErr), tt.want)
		})
	}
}

type fakeBootInfo struct {
	resp daemon.BootInfoResp
	err  error
}

func (f fakeBootInfo) BootInfo() (daemon.BootInfoResp, error) { return f.resp, f.err }

// TestSupervisorStaleDecision covers the pure supervisor-staleness core over the
// enriched BootInfoResp. The decision is hash-based; VCS never gates.
func TestSupervisorStaleDecision(t *testing.T) {
	const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name       string
		info       daemon.BootInfoResp
		tuiHash    string
		tuiHashErr bool
		want       bool
	}{
		{"no supervisor → not stale", daemon.BootInfoResp{}, hashA, false, false},
		{"present, empty hash (old protocol) → not stale",
			daemon.BootInfoResp{SupervisorPresent: true, SupervisorHash: ""}, hashA, false, false},
		{"present, equal hash → not stale",
			daemon.BootInfoResp{SupervisorPresent: true, SupervisorHash: hashA}, hashA, false, false},
		{"present, differing hash → stale",
			daemon.BootInfoResp{SupervisorPresent: true, SupervisorHash: hashA}, hashB, false, true},
		{"present, differing hash but tui hash read error → not stale",
			daemon.BootInfoResp{SupervisorPresent: true, SupervisorHash: hashA}, "", true, false},
		{"differing VCS but equal hash → not stale (hash-based)",
			daemon.BootInfoResp{SupervisorPresent: true, SupervisorHash: hashA, SupervisorVCS: buildid.VCS{Revision: "zzz"}}, hashA, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, supervisorStaleDecision(tt.info, tt.tuiHash, tt.tuiHashErr), tt.want)
		})
	}
}

// TestEvaluateSkew exercises the full wiring (BootInfo → os.Executable →
// BinaryHashFile/stat → staleDecision/supervisorStaleDecision) against the
// running test binary, which is what os.Executable() resolves to under
// `go test`. It pins the load-bearing gating: the daemon decision honours
// checkDaemon (preExisting) while the supervisor decision fires regardless.
func TestEvaluateSkew(t *testing.T) {
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
				testutil.Equal(t, evaluateSkew(tt.prov, tt.checkDaemon).daemonStale, tt.want)
			})
		}
	})

	t.Run("supervisor checked even when checkDaemon=false", func(t *testing.T) {
		// The auto-start path passes checkDaemon=false; the supervisor must
		// still be evaluated. A differing supervisor hash ⇒ supervisorStale,
		// while the (differing) daemon hash stays gated off.
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:        "deadbeef",
			SupervisorPresent: true,
			SupervisorHash:    "cafebabe",
		}}
		got := evaluateSkew(prov, false)
		testutil.Equal(t, got.daemonStale, false)
		testutil.Equal(t, got.supervisorStale, true)
	})

	t.Run("supervisor matching hash ⇒ not stale on auto-start path", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			SupervisorPresent: true,
			SupervisorHash:    selfHash,
		}}
		got := evaluateSkew(prov, false)
		testutil.Equal(t, got.supervisorStale, false)
	})

	t.Run("old-protocol supervisor (empty hash) ⇒ not stale", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			SupervisorPresent: true,
			SupervisorHash:    "",
		}}
		got := evaluateSkew(prov, true)
		testutil.Equal(t, got.supervisorStale, false)
	})

	t.Run("identity strings populated", func(t *testing.T) {
		prov := fakeBootInfo{resp: daemon.BootInfoResp{
			BinaryHash:        "deadbeef",
			BinaryPath:        "/usr/local/bin/argus",
			SupervisorPresent: true,
			SupervisorHash:    "cafebabe",
			SupervisorPath:    "/gopath/bin/argus",
		}}
		got := evaluateSkew(prov, true)
		testutil.Contains(t, got.daemonIdentity, "/usr/local/bin/argus")
		testutil.Contains(t, got.supervisorIdentity, "/gopath/bin/argus")
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
			testutil.Equal(t, formatIdentity(tt.vcs, tt.hash, tt.path), tt.want)
		})
	}
}

var errSentinel = errors.New("boom")
