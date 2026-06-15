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
			if got := shortHash(tt.in); got != tt.want {
				t.Errorf("shortHash(%q) = %q, want %q", tt.in, got, tt.want)
			}
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
			got := staleDecision(tt.info, tt.tuiHash, tt.tuiHashErr, tt.tuiMtime, tt.tuiMtimeErr)
			if got != tt.want {
				t.Errorf("staleDecision = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeBootInfo struct {
	resp daemon.BootInfoResp
	err  error
}

func (f fakeBootInfo) BootInfo() (daemon.BootInfoResp, error) { return f.resp, f.err }

// TestIsDaemonStale exercises the full wiring (BootInfo → os.Executable →
// BinaryHashFile/stat → staleDecision) against the running test binary, which
// is what os.Executable() resolves to under `go test`.
func TestIsDaemonStale(t *testing.T) {
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

	tests := []struct {
		name string
		prov fakeBootInfo
		want bool
	}{
		{"BootInfo error → not stale", fakeBootInfo{err: errSentinel}, false},
		{"matching hash → not stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryHash: selfHash}}, false},
		{"different hash → stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryHash: "deadbeef"}}, true},
		{"no hash, matching mtime → not stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryMtime: selfMtime}}, false},
		{"no hash, different mtime → stale", fakeBootInfo{resp: daemon.BootInfoResp{BinaryMtime: selfMtime.Add(time.Hour)}}, true},
		{"no hash, zero mtime → not stale", fakeBootInfo{resp: daemon.BootInfoResp{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDaemonStale(tt.prov); got != tt.want {
				t.Errorf("isDaemonStale = %v, want %v", got, tt.want)
			}
		})
	}
}

var errSentinel = errors.New("boom")
