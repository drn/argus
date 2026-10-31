package pricing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
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

func TestLoader_LongContextTierDecodes(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "rates.toml")
	writeRates(t, lib, `
[models."gpt-x"]
input                  = 2.0
output                 = 10.0
long_context_threshold = 272000

[models."gpt-x".long_context]
input  = 4.0
output = 15.0

[models.flat]
input = 1.0
`)
	table, err := (&Loader{LibraryPath: lib}).Load()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, table.Models["gpt-x"], Rate{
		Input: 2.0, Output: 10.0, LongContextThreshold: 272000,
		LongContext: &Rate{Input: 4.0, Output: 15.0},
	})
	testutil.Nil(t, table.Models["flat"].LongContext)
	testutil.Equal(t, table.Models["flat"].LongContextThreshold, int64(0))
}

func TestSeed_PricesGPT61Sol(t *testing.T) {
	raw, err := seedFS.ReadFile(seedFileName)
	testutil.NoError(t, err)
	var table Table
	_, err = toml.Decode(string(raw), &table)
	testutil.NoError(t, err)
	rate, ok := table.Models["gpt-6.1-sol"]
	testutil.Equal(t, ok, true)
	testutil.Equal(t, rate.Input, 2.0)
	testutil.Equal(t, rate.CacheRead, 0.10)
	testutil.Equal(t, rate.Output, 10.0)
	testutil.Equal(t, rate.LongContextThreshold, int64(272000))
	testutil.Equal(t, rate.LongContext != nil, true)
}

func TestLoadInstalled(t *testing.T) {
	t.Run("installs the seed when absent and loads it", func(t *testing.T) {
		lib := filepath.Join(t.TempDir(), "rates.toml")
		table, err := LoadInstalled(lib, "")
		testutil.NoError(t, err)
		_, ok := table.Models["gpt-6.1-sol"]
		testutil.True(t, ok)
		_, err = os.Stat(lib)
		testutil.NoError(t, err)
	})
	t.Run("in-repo override wins", func(t *testing.T) {
		lib := filepath.Join(t.TempDir(), "rates.toml")
		wt := t.TempDir()
		testutil.NoError(t, os.MkdirAll(filepath.Join(wt, ".argus"), 0o755))
		writeRates(t, filepath.Join(wt, ".argus", "rates.toml"), "[models.only]\ninput = 1.0\n")
		table, err := LoadInstalled(lib, wt)
		testutil.NoError(t, err)
		testutil.Equal(t, len(table.Models), 1)
	})
	t.Run("install failure surfaces", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		writeRates(t, blocker, "x")
		_, err := LoadInstalled(filepath.Join(blocker, "rates.toml"), "")
		testutil.True(t, err != nil)
	})
}
