package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

// envVarConfig builds a config whose "codex" backend carries the given
// credential mapping (target -> source). The backend command is bare codex so
// no Claude-only flags are injected.
func envVarConfig(envVars map[string]string) config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "codex"},
		Backends: map[string]config.Backend{
			"codex": {Command: "codex --dangerously-bypass-approvals-and-sandbox", EnvVars: envVars},
		},
	}
}

// envValue returns the value of key in a cmd.Env slice (last wins, matching
// exec.Cmd semantics) and whether it was present.
func envValue(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, kv := range env {
		if name, v, found := strings.Cut(kv, "="); found && name == key {
			val, ok = v, true
		}
	}
	return val, ok
}

// captureUXLog redirects uxlog to a fresh temp file and returns a reader for its
// contents. Restores the prior state on cleanup.
func captureUXLog(t *testing.T) func() string {
	t.Helper()
	uxlog.Close() // drop any file a prior test opened so our Init takes effect
	path := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(path))
	t.Cleanup(uxlog.Close)
	return func() string {
		b, err := os.ReadFile(path)
		testutil.NoError(t, err)
		return string(b)
	}
}

// installResolver swaps the package secret resolver for the duration of the
// test, restoring the previous one on cleanup.
func installResolver(t *testing.T, r SecretResolver) {
	t.Helper()
	prev := SetSecretResolver(r)
	t.Cleanup(func() { SetSecretResolver(prev) })
}

// A resolved source is injected into the child env under the target name.
func TestBuildCmd_EnvVarMapping_ResolvedSourceInjected(t *testing.T) {
	// A distinctive, deliberately non-credential-looking marker (no real key
	// format) so the test asserts plumbing without writing a credential string.
	const resolvedValue = "RESOLVED-marker-must-reach-child"
	installResolver(t, func(source string) (string, bool) {
		if source == "HERA_OPENAI" {
			return resolvedValue, true
		}
		return "", false
	})

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY in child env; got %v", cmd.Env)
	}
	testutil.Equal(t, got, resolvedValue)
}

// An unresolved source leaves the target unset and logs a warning that names
// the variable but carries no value.
func TestBuildCmd_EnvVarMapping_UnresolvedSourceUnsetAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return "", false })

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	if _, ok := envValue(cmd.Env, "OPENAI_API_KEY"); ok {
		t.Fatalf("expected OPENAI_API_KEY to be unset when source unresolved; got %v", cmd.Env)
	}
	log := readLog()
	testutil.Contains(t, log, "OPENAI_API_KEY")
	testutil.Contains(t, log, "HERA_OPENAI")
}

// The resolved secret value MUST NOT appear in any log line (the success path
// logs nothing about the value).
func TestBuildCmd_EnvVarMapping_ValueNeverLogged(t *testing.T) {
	const resolvedValue = "VALUE-must-never-be-logged-marker"
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return resolvedValue, true })

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	// Sanity: the value did reach the child env...
	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok || got != resolvedValue {
		t.Fatalf("expected resolved value in child env; got %q ok=%v", got, ok)
	}
	// ...but it never reached the log.
	if log := readLog(); strings.Contains(log, resolvedValue) {
		t.Fatalf("resolved value leaked into ux.log: %q", log)
	}
}

// The default resolver reads the daemon's own process environment by source
// name; an unset source resolves to nothing.
func TestBuildCmd_EnvVarMapping_DefaultResolverReadsProcessEnv(t *testing.T) {
	const secret = "from-process-env"
	t.Setenv("HERA_OPENAI_TESTSRC", secret)

	cfg := envVarConfig(map[string]string{
		"OPENAI_API_KEY": "HERA_OPENAI_TESTSRC",
		"UNSET_TARGET":   "DEFINITELY_UNSET_SOURCE_VAR_XYZ",
	})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY from process env; got %v", cmd.Env)
	}
	testutil.Equal(t, got, secret)
	if _, ok := envValue(cmd.Env, "UNSET_TARGET"); ok {
		t.Fatal("expected UNSET_TARGET to be absent (source unset)")
	}
}

// Empty target or source entries are skipped without injecting a malformed env
// entry.
func TestBuildCmd_EnvVarMapping_SkipsEmptyEntries(t *testing.T) {
	installResolver(t, func(string) (string, bool) { return "v", true })
	cfg := envVarConfig(map[string]string{"": "SOME_SOURCE", "TARGET": ""})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "=") || strings.HasPrefix(kv, "TARGET=") {
			t.Fatalf("empty-key/empty-source entry leaked into env: %q", kv)
		}
	}
}

// SetSecretResolver returns the previous resolver and a nil argument resets to
// the default process-environment resolver.
func TestSetSecretResolver_SwapAndReset(t *testing.T) {
	t.Setenv("SEAM_TEST_SRC", "env-value")

	sentinel := func(string) (string, bool) { return "sentinel", true }
	prev := SetSecretResolver(sentinel)
	t.Cleanup(func() { SetSecretResolver(prev) })

	// Active resolver is now the sentinel.
	v, ok := secretResolver("anything")
	if !ok || v != "sentinel" {
		t.Fatalf("expected sentinel resolver active; got %q ok=%v", v, ok)
	}

	// nil resets to the default process-env resolver.
	SetSecretResolver(nil)
	v, ok = secretResolver("SEAM_TEST_SRC")
	if !ok || v != "env-value" {
		t.Fatalf("expected default resolver to read process env; got %q ok=%v", v, ok)
	}
}
