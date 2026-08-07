package pricing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestInstallDefault_MissingDirInstalls(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nested", "rates.toml")
	installed, err := InstallDefault(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, installed, true)
	testutil.Equal(t, fileExists(dest), true)
}

func TestInstallDefault_SkipsExistingWithoutOverwriting(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rates.toml")
	custom := "# hand-corrected pricing\n[models.sonnet]\ninput = 99.0\n"
	testutil.NoError(t, os.WriteFile(dest, []byte(custom), 0o644))

	installed, err := InstallDefault(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, installed, false)

	got, err := os.ReadFile(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), custom)
}

// TestInstallDefault_MkdirFailure_Errors covers the os.MkdirAll error path:
// dest's parent directory cannot be created because a FILE already occupies
// that path segment.
func TestInstallDefault_MkdirFailure_Errors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	testutil.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	dest := filepath.Join(blocker, "rates.toml") // blocker is a file, not a dir

	installed, err := InstallDefault(dest)
	if err == nil {
		t.Fatal("expected an error when dest's parent path is occupied by a file")
	}
	testutil.Equal(t, installed, false)
}

// TestInstallDefault_WriteFailure_Errors covers the os.WriteFile error path:
// dest itself is a directory, so writing the seed's bytes there fails.
func TestInstallDefault_WriteFailure_Errors(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rates.toml")
	testutil.NoError(t, os.Mkdir(dest, 0o755)) // dest exists as a DIR, not absent

	// fileExists treats a directory as "not a regular file" (info.IsDir()),
	// so InstallDefault proceeds past the never-overwrite check straight into
	// the write, which fails because dest is a directory.
	installed, err := InstallDefault(dest)
	if err == nil {
		t.Fatal("expected an error writing to a dest that is itself a directory")
	}
	testutil.Equal(t, installed, false)
}

func TestInstallDefault_SecondRunSkips(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rates.toml")
	installed, err := InstallDefault(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, installed, true)

	installed, err = InstallDefault(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, installed, false)
}

// TestEmbeddedSeed_Validates proves the bytes the embed directive actually
// captured (not merely the on-disk file) decode into a usable Table with at
// least the Claude aliases agent.KnownModels curates — extracting through
// seedFS (the same embed.FS InstallDefault reads from) rather than reading
// internal/pricing/rates.toml directly, so a future edit that breaks the
// seed fails this test, not a silent runtime surprise.
func TestEmbeddedSeed_Validates(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rates.toml")
	_, err := InstallDefault(dest)
	testutil.NoError(t, err)

	l := &Loader{LibraryPath: dest}
	table, err := l.Load()
	testutil.NoError(t, err)

	for _, alias := range []string{"opus", "sonnet", "haiku", "fable"} {
		if _, ok := table.Models[alias]; !ok {
			t.Errorf("expected embedded seed to carry a rate entry for %q", alias)
		}
	}
}
