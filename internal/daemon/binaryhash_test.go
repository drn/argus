package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBinaryHashFile(t *testing.T) {
	t.Run("hashes file contents", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bin")
		content := []byte("the quick brown fox")
		testutil.NoError(t, os.WriteFile(path, content, 0o600))

		got, err := BinaryHashFile(path)
		testutil.NoError(t, err)

		sum := sha256.Sum256(content)
		testutil.Equal(t, got, hex.EncodeToString(sum[:]))
	})

	t.Run("identical content yields identical hash", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		// Same bytes, written separately (mimics a deterministic rebuild that
		// produces a byte-identical binary at a different path/mtime).
		testutil.NoError(t, os.WriteFile(a, []byte("same"), 0o600))
		testutil.NoError(t, os.WriteFile(b, []byte("same"), 0o600))

		ha, err := BinaryHashFile(a)
		testutil.NoError(t, err)
		hb, err := BinaryHashFile(b)
		testutil.NoError(t, err)
		testutil.Equal(t, ha, hb)
	})

	t.Run("different content yields different hash", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		testutil.NoError(t, os.WriteFile(a, []byte("one"), 0o600))
		testutil.NoError(t, os.WriteFile(b, []byte("two"), 0o600))

		ha, err := BinaryHashFile(a)
		testutil.NoError(t, err)
		hb, err := BinaryHashFile(b)
		testutil.NoError(t, err)
		if ha == hb {
			t.Fatalf("expected different hashes, both = %s", ha)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		_, err := BinaryHashFile(filepath.Join(t.TempDir(), "does-not-exist"))
		testutil.Error(t, err)
	})
}
