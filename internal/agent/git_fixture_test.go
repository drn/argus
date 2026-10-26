package agent

import (
	"os"
	"os/exec"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// Set once before any test or t.Parallel: fixture commits must never read the
// developer's signing config. Policy tests install their own synthetic config.
func runAgentTests(m *testing.M) int {
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_COUNT":    "0",
	} {
		if err := os.Setenv(name, value); err != nil {
			panic(err)
		}
	}
	// A Codex-launched test otherwise ignores fixture HOME and reads the live
	// session database. Individual account tests set their own explicit homes.
	for _, name := range []string{"GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "CODEX_HOME", "CODEX_SQLITE_HOME"} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	return m.Run()
}

func TestAgentGitFixtureIsolation(t *testing.T) {
	testutil.Equal(t, os.Getenv("GIT_CONFIG_GLOBAL"), os.DevNull)
	testutil.Equal(t, os.Getenv("GIT_CONFIG_NOSYSTEM"), "1")
	testutil.Equal(t, os.Getenv("GIT_CONFIG_COUNT"), "0")
	_, present := os.LookupEnv("GIT_CONFIG_PARAMETERS")
	testutil.Equal(t, present, false)
	_, present = os.LookupEnv("GIT_CONFIG")
	testutil.Equal(t, present, false)
	_, present = os.LookupEnv("CODEX_HOME")
	testutil.Equal(t, present, false)
	_, present = os.LookupEnv("CODEX_SQLITE_HOME")
	testutil.Equal(t, present, false)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit", "--allow-empty", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v: %s", err, out)
		}
	}
}
