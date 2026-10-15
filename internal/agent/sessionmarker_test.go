package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// parentSessionMarkerNames is the spec's literal list (agent-execution:
// "Spawn environment strips a parent Claude Code session's runtime markers").
// Kept literal here, not read from the production slice, so the test pins the
// spec rather than echoing the implementation.
var parentSessionMarkerNames = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
}

// unsetForTest removes name from the process env for the test's duration,
// restoring any prior value on cleanup.
func unsetForTest(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // registers the restore
	testutil.NoError(t, os.Unsetenv(name))
}

// setParentSessionMarkers simulates a spawning process started from inside a
// Claude Code session: every listed marker carries a distinctive value.
func setParentSessionMarkers(t *testing.T) {
	t.Helper()
	for _, n := range parentSessionMarkerNames {
		t.Setenv(n, "parent-"+n)
	}
}

func TestBuildCmd_StripsParentSessionMarkers(t *testing.T) {
	seedDefaultClaude(t)
	setParentSessionMarkers(t)

	cases := []struct {
		name    string
		backend string
		// mustNotHave are the scenario's named variables; every listed marker
		// is also checked below.
		mustNotHave []string
	}{
		{"supervisor started from inside Claude Code (claude agent)", "claude",
			[]string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"}},
		{"markers are stripped for non-Claude backends too (codex agent)", "codex",
			[]string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.backend == "codex" {
				stubCodexOverlay(t, filepath.Join(t.TempDir(), "overlay"))
			}
			task := &model.Task{ID: "t1", Name: "t", Backend: tc.backend, Prompt: "go", Worktree: t.TempDir()}
			cmd, _, err := BuildCmd(task, permModeConfig(""), false)
			testutil.NoError(t, err)
			for _, n := range tc.mustNotHave {
				_, found := envLast(cmd.Env, n)
				testutil.False(t, found)
			}
			for _, n := range parentSessionMarkerNames {
				if _, found := envLast(cmd.Env, n); found {
					t.Errorf("spawned %s env still carries parent marker %s", tc.backend, n)
				}
			}
		})
	}
}

func TestBuildCmd_KeepsUserClaudeSettingsVars(t *testing.T) {
	seedDefaultClaude(t)
	setParentSessionMarkers(t)
	t.Setenv("CLAUDE_CODE_ENABLE_TELEMETRY", "1")

	task := &model.Task{ID: "t1", Name: "t", Prompt: "go", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, permModeConfig(""), false)
	testutil.NoError(t, err)
	testutil.True(t, envHas(cmd.Env, "CLAUDE_CODE_ENABLE_TELEMETRY=1"))
}

func TestBuildCmd_BackendEnvVarsWinOverMarkerStrip(t *testing.T) {
	seedDefaultClaude(t)
	setParentSessionMarkers(t)
	t.Setenv("ARGUS_TEST_EFFORT_SRC", "high")

	cfg := permModeConfig("")
	b := cfg.Backends["claude"]
	b.EnvVars = map[string]string{"CLAUDE_EFFORT": "ARGUS_TEST_EFFORT_SRC"}
	cfg.Backends["claude"] = b

	task := &model.Task{ID: "t1", Name: "t", Prompt: "go", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	v, found := envLast(cmd.Env, "CLAUDE_EFFORT")
	testutil.True(t, found)
	testutil.Equal(t, v, "high")
	testutil.False(t, envHas(cmd.Env, "CLAUDE_EFFORT=parent-CLAUDE_EFFORT"))
}

func TestSpawnBaseEnv_LogsStrippedMarkersByNameOnly(t *testing.T) {
	read := captureUXLog(t)
	base := []string{
		"PATH=/bin",
		"CLAUDECODE=secret-claudecode-value",
		"CLAUDE_CODE_CHILD_SESSION=secret-child-value",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret-token-value",
	}

	got := spawnBaseEnv(base, nil, "t1")
	testutil.DeepEqual(t, got, []string{"PATH=/bin"})

	log := read()
	testutil.Contains(t, log, "CLAUDECODE")
	testutil.Contains(t, log, "CLAUDE_CODE_CHILD_SESSION")
	testutil.Contains(t, log, "CLAUDE_CODE_MESSAGING_TOKEN")
	for _, v := range []string{"secret-claudecode-value", "secret-child-value", "secret-token-value"} {
		if strings.Contains(log, v) {
			t.Errorf("uxlog leaked marker value %q: %s", v, log)
		}
	}
}

func TestBuildCmd_ForceSessionPersistence(t *testing.T) {
	seedDefaultClaude(t)
	unsetForTest(t, "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE")

	t.Run("claude agent gets the persistence flag", func(t *testing.T) {
		task := &model.Task{ID: "t1", Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, permModeConfig(""), false)
		testutil.NoError(t, err)
		v, found := envLast(cmd.Env, "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE")
		testutil.True(t, found)
		testutil.Equal(t, v, "1")
	})

	t.Run("codex agent does not get the persistence flag", func(t *testing.T) {
		stubCodexOverlay(t, filepath.Join(t.TempDir(), "overlay"))
		cmd, _, err := BuildCmd(codexTask(t, ""), permModeConfig(""), false)
		testutil.NoError(t, err)
		_, found := envLast(cmd.Env, "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE")
		testutil.False(t, found)
	})
}

// envLast returns the value of name's last assignment in env (exec.Cmd
// semantics: later entries win).
func envLast(env []string, name string) (string, bool) {
	val, ok := "", false
	for _, kv := range env {
		if v, found := strings.CutPrefix(kv, name+"="); found {
			val, ok = v, true
		}
	}
	return val, ok
}
