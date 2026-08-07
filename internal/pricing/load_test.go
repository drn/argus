package pricing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func writeRates(t *testing.T, path, body string) {
	t.Helper()
	testutil.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestLoader_NeitherPathExists_ReturnsEmptyTable(t *testing.T) {
	l := &Loader{}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, len(table.Models), 0)
}

func TestLoader_LibraryPathOnly(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, "[models.sonnet]\ninput = 1.0\n")

	l := &Loader{LibraryPath: lib}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, table.Models["sonnet"].Input, 1.0)
}

func TestLoader_InRepoTakesPrecedenceOverLibrary(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "rates.toml")
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, repo, "[models.sonnet]\ninput = 111.0\n")
	writeRates(t, lib, "[models.sonnet]\ninput = 222.0\n")

	l := &Loader{RepoPath: repo, LibraryPath: lib}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, table.Models["sonnet"].Input, 111.0)
}

func TestLoader_FallsBackToLibraryWhenRepoAbsent(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, "[models.sonnet]\ninput = 222.0\n")

	l := &Loader{RepoPath: filepath.Join(t.TempDir(), "missing.toml"), LibraryPath: lib}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, table.Models["sonnet"].Input, 222.0)
}

// TestLoader_HandEditVisibleWithoutRestart pins the no-caching property
// (design.md Decision 3): editing the file between two Load() calls on the
// SAME Loader is visible on the second call, with no reload/invalidation
// call needed — there is no cache to invalidate.
func TestLoader_HandEditVisibleWithoutRestart(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, "[models.sonnet]\ninput = 1.0\n")

	l := &Loader{LibraryPath: lib}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, table.Models["sonnet"].Input, 1.0)

	writeRates(t, lib, "[models.sonnet]\ninput = 42.0\n")

	table, err = l.Load()
	testutil.NoError(t, err)
	testutil.Equal(t, table.Models["sonnet"].Input, 42.0)
}

func TestLoader_MalformedTOML_Errors(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, "not = valid [[[ toml")

	l := &Loader{LibraryPath: lib}
	_, err := l.Load()
	if err == nil {
		t.Fatal("expected an error decoding malformed TOML")
	}
}

func TestLoader_AllFiveClassesDecode(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, `
[models.opus]
input          = 15.0
cache_write_1h = 30.0
cache_write_5m = 18.75
cache_read     = 1.5
output         = 75.0
`)
	l := &Loader{LibraryPath: lib}
	table, err := l.Load()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, table.Models["opus"], Rate{
		Input: 15.0, CacheWrite1h: 30.0, CacheWrite5m: 18.75, CacheRead: 1.5, Output: 75.0,
	})
}
