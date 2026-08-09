package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// --- add-secrets-resolver-registry: bare-string/env:// regression guards and
// scheme-prefixed registry dispatch (Tasks 1.1, 1.7, 1.8) ---

// TestBuildCmd_EnvVarMapping_BareStringIgnoresSecretsConfig pins the
// secrets-resolution "Bare string resolves as env" scenario at BuildCmd's own
// call site (existing behavior, regression guard): a bare-string source keeps
// resolving against the process environment via the existing pluggable
// secretResolver seam, completely unaffected by a populated [secrets.op]
// block on cfg — only a scheme-prefixed source ever consults the registry.
func TestBuildCmd_EnvVarMapping_BareStringIgnoresSecretsConfig(t *testing.T) {
	const secret = "bare-string-still-resolves-via-process-env"
	t.Setenv("BARE_STRING_IGNORES_SECRETS_CFG", secret)

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "BARE_STRING_IGNORES_SECRETS_CFG"})
	cfg.Secrets = config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "env://SOME_OTHER_VAR_ENTIRELY",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatal("expected bare-string source to resolve against the process environment regardless of [secrets.op] config")
	}
	testutil.Equal(t, got, secret)
}

// TestBackendEnvVars_MappingCarriesOnlyDescriptors_NoResolvedValue pins the
// agent-execution "Mapping carries no secret value" scenario: a backend's
// EnvVars mapping holds only target-to-source descriptors, never a resolved
// secret value — BuildCmd must not write a resolved value back into it.
func TestBackendEnvVars_MappingCarriesOnlyDescriptors_NoResolvedValue(t *testing.T) {
	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "op://vault/item/field"})
	testutil.Equal(t, cfg.Backends["codex"].EnvVars["OPENAI_API_KEY"], "op://vault/item/field")

	installResolver(t, func(string) (string, bool) { return "should-never-appear-in-envvars-map", true })
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}
	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)
	_ = cmd

	// BuildCmd must not have mutated the mapping in cfg with a resolved value.
	testutil.Equal(t, cfg.Backends["codex"].EnvVars["OPENAI_API_KEY"], "op://vault/item/field")
}

// TestBuildCmd_EnvVarMapping_KeychainSourceDispatchesThroughRegistry pins the
// agent-execution "Scheme-prefixed source dispatches through the registry"
// scenario: a keychain://-prefixed EnvVars source is resolved through the
// secrets-resolution registry's keychain resolver (built fresh from the cfg
// parameter BuildCmd already receives), not treated as a bare env-var name.
// The fake secretSubprocessRunner (secretregistry_test.go) means this never
// shells out to a real `security` binary.
func TestBuildCmd_EnvVarMapping_KeychainSourceDispatchesThroughRegistry(t *testing.T) {
	ResetSecretMemoCache()
	installSubprocessRunner(t, func(_ context.Context, name string, _ []string, _ []string, _ time.Duration) (string, bool) {
		if name == "security" {
			return "keychain-resolved-value", true
		}
		return "", false
	})

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "keychain://some-service"})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY resolved via the keychain scheme; got env %v", cmd.Env)
	}
	testutil.Equal(t, got, "keychain-resolved-value")
}

// TestBuildCmd_EnvVarMapping_OpSourceDispatchesThroughRegistry extends the
// same scenario to an op://-prefixed source, backed by a [secrets.op]
// bootstrap block on cfg — proving BuildCmd threads cfg.Secrets through to
// the registry with no separate wiring/installation step.
func TestBuildCmd_EnvVarMapping_OpSourceDispatchesThroughRegistry(t *testing.T) {
	ResetSecretMemoCache()
	installSubprocessRunner(t, func(_ context.Context, name string, _ []string, _ []string, _ time.Duration) (string, bool) {
		switch name {
		case "security":
			return "bootstrap-token", true
		case "op":
			return "op-resolved-value", true
		}
		return "", false
	})

	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "op://vault/item/field"})
	cfg.Secrets = config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "keychain://op-service-account-claude",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY resolved via the op scheme; got env %v", cmd.Env)
	}
	testutil.Equal(t, got, "op-resolved-value")
}

// TestBuildCmd_EnvVarMapping_BareStringStillDispatchesThroughPluggableResolver
// pins the agent-execution "Resolver is pluggable" scenario for the env path
// specifically: swapping the resolver via SetSecretResolver still changes
// what the very next BuildCmd call resolves for a bare-string/env:// source,
// even though scheme-prefixed sources (keychain://, op://) now dispatch
// through the new secrets-resolution registry instead.
func TestBuildCmd_EnvVarMapping_BareStringStillDispatchesThroughPluggableResolver(t *testing.T) {
	installResolver(t, func(source string) (string, bool) {
		if source == "HERA_OPENAI" {
			return "still-uses-pluggable-seam", true
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
		t.Fatal("expected OPENAI_API_KEY to resolve via the pluggable resolver seam")
	}
	testutil.Equal(t, got, "still-uses-pluggable-seam")
}

// TestBuildCmd_EnvVarMapping_ExplicitEnvSchemeStillDispatchesThroughPluggableResolverUnmemoized
// pins the agent-execution "A bare string or env://-prefixed source SHALL
// resolve against the daemon's own process environment, unchanged from prior
// behavior" requirement for the EXPLICIT `env://`-prefixed form specifically
// (mirroring ...BareStringStillDispatchesThroughPluggableResolver above, which
// only covers the bare-string form). An `env://VAR` source must (a) resolve to
// the same value a bare `VAR` source would, and (b) NEVER be memoized by the
// registry's process-lifetime cache: swapping the pluggable resolver between
// two BuildCmd calls for the identical "env://HERA_OPENAI" descriptor must
// change what the SECOND call resolves. Before the fix, `strings.Contains(source,
// "://")` wrongly routed "env://HERA_OPENAI" into the registry's Resolve,
// which memoizes a successful resolve keyed on the literal descriptor string
// — so the second call would still observe the first call's stale value.
func TestBuildCmd_EnvVarMapping_ExplicitEnvSchemeStillDispatchesThroughPluggableResolverUnmemoized(t *testing.T) {
	ResetSecretMemoCache()
	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "env://HERA_OPENAI"})
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	installResolver(t, func(source string) (string, bool) {
		if source == "HERA_OPENAI" {
			return "first-value", true
		}
		return "", false
	})

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)
	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY resolved via env:// through the pluggable resolver; got env %v", cmd.Env)
	}
	testutil.Equal(t, got, "first-value")

	// Swap the resolver and rebuild with the IDENTICAL "env://HERA_OPENAI"
	// source. If it were wrongly memoized under that literal descriptor key,
	// this second call would still observe "first-value".
	installResolver(t, func(source string) (string, bool) {
		if source == "HERA_OPENAI" {
			return "second-value", true
		}
		return "", false
	})

	cmd2, cleanup2, err := BuildCmd(task, cfg, false)
	if cleanup2 != nil {
		defer cleanup2()
	}
	testutil.NoError(t, err)
	got2, ok := envValue(cmd2.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY resolved via env:// on the second BuildCmd call; got env %v", cmd2.Env)
	}
	testutil.Equal(t, got2, "second-value")
}
