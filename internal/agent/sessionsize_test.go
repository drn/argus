package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestSaveLoadSessionSize_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	SaveSessionSize("size-rt", 316, 82)
	cols, rows, ok := LoadSessionSize("size-rt")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, 316)
	testutil.Equal(t, rows, 82)
}

func TestSaveSessionSize_Overwrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	SaveSessionSize("size-ow", 80, 24)
	SaveSessionSize("size-ow", 190, 60)
	cols, rows, ok := LoadSessionSize("size-ow")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, 190)
	testutil.Equal(t, rows, 60)
}

func TestSaveSessionSize_RejectsNonPositive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	SaveSessionSize("size-bad", 0, 24)
	SaveSessionSize("size-bad", 80, 0)
	SaveSessionSize("size-bad", -1, -1)

	_, _, ok := LoadSessionSize("size-bad")
	testutil.Equal(t, ok, false)
}

func TestSaveSessionSize_MkdirFailure_NoPanic(t *testing.T) {
	// Point HOME at a regular file so MkdirAll under it fails.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-dir")
	testutil.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))
	t.Setenv("HOME", notADir)

	SaveSessionSize("size-mkdir-fail", 80, 24) // must not panic

	_, _, ok := LoadSessionSize("size-mkdir-fail")
	testutil.Equal(t, ok, false)
}

func TestLoadSessionSize_Missing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, _, ok := LoadSessionSize("size-missing")

	testutil.Equal(t, ok, false)
}

func TestLoadSessionSize_Corrupt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name    string
		content string
	}{
		{"garbage", "not a size"},
		{"empty", ""},
		{"zero_cols", "0 24\n"},
		{"zero_rows", "80 0\n"},
		{"negative", "-80 24\n"},
		{"missing_rows", "80\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := SessionSizePath("size-corrupt-" + tt.name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, ok := LoadSessionSize("size-corrupt-" + tt.name)
			testutil.Equal(t, ok, false)
		})
	}
}

func TestSessionSizePath_UnderSessionsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got := SessionSizePath("abc")

	testutil.Equal(t, got, filepath.Join(SessionsDir(), "abc.size"))
}

func TestStartSession_WritesSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := exec.Command("echo", "hi")
	sess, err := StartSession("size-start", cmd, 82, 316)
	testutil.NoError(t, err)
	defer sess.Stop() //nolint:errcheck

	cols, rows, ok := LoadSessionSize("size-start")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, 316)
	testutil.Equal(t, rows, 82)
}

func TestStartSession_ZeroSize_WritesDefaultsToSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := exec.Command("echo", "hi")
	sess, err := StartSession("size-start-zero", cmd, 0, 0)
	testutil.NoError(t, err)
	defer sess.Stop() //nolint:errcheck

	cols, rows, ok := LoadSessionSize("size-start-zero")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, int(DefaultTermCols))
	testutil.Equal(t, rows, int(DefaultTermRows))
}

func TestSession_Resize_UpdatesSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("size-resize", cmd, 24, 80)
	testutil.NoError(t, err)
	defer sess.Stop() //nolint:errcheck

	testutil.NoError(t, sess.Resize(82, 316))

	cols, rows, ok := LoadSessionSize("size-resize")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, 316)
	testutil.Equal(t, rows, 82)
}

func TestPruneCompleted_RemovesSessionSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	testutil.NoError(t, d.Add(&model.Task{
		ID: "prune-size", Name: "done", Status: model.StatusComplete, Project: "proj",
	}))
	SaveSessionSize("prune-size", 316, 82)

	_, err = PruneCompleted(d, PruneOptions{})
	testutil.NoError(t, err)

	if _, _, ok := LoadSessionSize("prune-size"); ok {
		t.Error("prune should remove the session size file")
	}
}

func TestSession_Resize_AfterPtmxClosed_SkipsSizeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := exec.Command("echo", "bye")
	sess, err := StartSession("size-resize-dead", cmd, 24, 80)
	testutil.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit")
	}

	// Resize after ptmx close is a no-op; the size file must keep the
	// last real PTY size so dead-session previews emulate at the width
	// the bytes were actually formatted for.
	_ = sess.Resize(10, 20)

	cols, rows, ok := LoadSessionSize("size-resize-dead")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)
}
