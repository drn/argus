package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func writeClaudeSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestReadClaudeCleanupPeriodDaysAt(t *testing.T) {
	t.Run("configured value returned", func(t *testing.T) {
		path := writeClaudeSettings(t, `{"cleanupPeriodDays": 90}`)
		days, err := ReadClaudeCleanupPeriodDaysAt(path)
		testutil.NoError(t, err)
		if days == nil || *days != 90 {
			t.Fatalf("days = %v, want 90", days)
		}
	})

	t.Run("absent key returns nil with no error", func(t *testing.T) {
		path := writeClaudeSettings(t, `{"model": "opus"}`)
		days, err := ReadClaudeCleanupPeriodDaysAt(path)
		testutil.NoError(t, err)
		if days != nil {
			t.Fatalf("days = %v, want nil", *days)
		}
	})

	t.Run("missing file returns an error", func(t *testing.T) {
		_, err := ReadClaudeCleanupPeriodDaysAt(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("expected an error for a missing file")
		}
	})

	t.Run("malformed JSON returns an error", func(t *testing.T) {
		path := writeClaudeSettings(t, `{not json`)
		_, err := ReadClaudeCleanupPeriodDaysAt(path)
		if err == nil {
			t.Fatal("expected an error for malformed JSON")
		}
	})
}

func TestIsRetentionSweptResumeFailure(t *testing.T) {
	t.Run("signature match classifies as retention failure", func(t *testing.T) {
		out := []byte("No conversation found with session ID: 00000000-0000-0000-0000-000000000000\n")
		if !IsRetentionSweptResumeFailure(out) {
			t.Fatal("expected retention-swept failure to be recognized")
		}
	})

	t.Run("unrelated crash output does not match", func(t *testing.T) {
		out := []byte("panic: runtime error: index out of range\n")
		if IsRetentionSweptResumeFailure(out) {
			t.Fatal("expected unrelated crash output not to match")
		}
	})

	t.Run("empty output does not match", func(t *testing.T) {
		if IsRetentionSweptResumeFailure(nil) {
			t.Fatal("expected empty output not to match")
		}
	})
}
