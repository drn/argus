package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// This file pins the add-secrets-resolver-registry change's secrets-resolution
// delta spec: a scheme-prefixed source descriptor ("env://", "keychain://",
// "op://") dispatched through a small resolver registry, with process-lifetime
// success-only memoization and an op-bootstrap tri-state status query. It
// fails to compile until Stage 2/3 add Resolve, ResetSecretMemoCache,
// commandResolvable, secretSubprocessRunner/defaultSecretSubprocessRunner,
// OpBootstrapStatus (+ constants), and QueryOpBootstrapStatus to
// internal/agent/secretregistry.go, and config.SecretsConfig/config.OpConfig
// to internal/config/config.go — by design (Prove-It Pattern, Stage 1).
//
// No test here ever shells out to a real `security` or `op` binary: keychain
// and op resolver behavior is exercised through the injectable
// secretSubprocessRunner seam (mirroring internal/gitutil's prRunner
// pattern), except the two 1.14 regression-guard tests, which deliberately
// exercise the REAL subprocess machinery (commandResolvable and
// defaultSecretSubprocessRunner) against short-lived local shell-script
// stubs — never a real security/op install.

// writeExecStub writes an executable shell script to a temp dir and returns
// its path. The script's behavior is entirely controlled by the caller's body
// string, keeping these tests independent of any real `security`/`op`/1Password
// installation.
func writeExecStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret-stub")
	testutil.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return path
}

// installSubprocessRunner swaps the package's injectable subprocess-execution
// seam for the duration of the test, restoring the previous one on cleanup —
// mirrors installResolver (secret_test.go) and gitutil's prRunner var-swap
// pattern. No test using this ever spawns a real security/op process.
func installSubprocessRunner(t *testing.T, fn func(ctx context.Context, name string, args []string, extraEnv []string, timeout time.Duration) (string, bool)) {
	t.Helper()
	prev := secretSubprocessRunner
	secretSubprocessRunner = fn
	t.Cleanup(func() { secretSubprocessRunner = prev })
}

// --- Scheme-prefixed secret source descriptor dispatch ---

// TestResolve_BareStringUsesEnvScheme pins "Bare string resolves as env": a
// source with no "://" resolves as env://<descriptor> against the process
// environment, via the existing swappable secretResolver seam (never a
// hardcoded os.LookupEnv call — Task 2.2).
func TestResolve_BareStringUsesEnvScheme(t *testing.T) {
	ResetSecretMemoCache()
	var got string
	installResolver(t, func(source string) (string, bool) {
		got = source
		return "bare-resolved", true
	})

	v, ok := Resolve(config.SecretsConfig{}, "SOME_BARE_VAR")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "bare-resolved")
	testutil.Equal(t, got, "SOME_BARE_VAR")
}

// TestResolve_ExplicitEnvScheme pins "Explicit env scheme": env://SOME_VAR
// resolves SOME_VAR from the process environment identically to the
// bare-string form.
func TestResolve_ExplicitEnvScheme(t *testing.T) {
	ResetSecretMemoCache()
	var got string
	installResolver(t, func(source string) (string, bool) {
		got = source
		return "explicit-env-resolved", true
	})

	v, ok := Resolve(config.SecretsConfig{}, "env://SOME_VAR")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "explicit-env-resolved")
	testutil.Equal(t, got, "SOME_VAR") // the scheme prefix is stripped before dispatch
}

// TestResolve_UnrecognizedSchemeFailsClosed pins "Unrecognized scheme fails
// closed": a source naming a scheme the registry doesn't recognize fails
// (not-ok) rather than raising an error or crashing the process. The
// (string, bool) return shape itself guarantees no error is raised; this test
// additionally proves the call completes normally (no panic) and never
// consults the subprocess seam.
func TestResolve_UnrecognizedSchemeFailsClosed(t *testing.T) {
	ResetSecretMemoCache()
	installSubprocessRunner(t, func(context.Context, string, []string, []string, time.Duration) (string, bool) {
		t.Fatal("unrecognized scheme must never invoke a subprocess")
		return "", false
	})

	v, ok := Resolve(config.SecretsConfig{}, "vault://some/unsupported/scheme")

	testutil.Equal(t, ok, false)
	testutil.Equal(t, v, "")
}

// --- Keychain resolver ---

// TestResolve_Keychain_ServiceOnly pins "Service-only lookup": keychain://
// <service> runs `security find-generic-password -s <service> -w` and
// returns its output as the resolved value.
func TestResolve_Keychain_ServiceOnly(t *testing.T) {
	ResetSecretMemoCache()
	var gotName string
	var gotArgs []string
	installSubprocessRunner(t, func(_ context.Context, name string, args []string, extraEnv []string, _ time.Duration) (string, bool) {
		gotName, gotArgs = name, args
		if len(extraEnv) != 0 {
			t.Fatalf("keychain lookup must not set extra subprocess env; got %v", extraEnv)
		}
		return "svc-only-secret", true
	})

	v, ok := Resolve(config.SecretsConfig{}, "keychain://op-service-account-claude")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "svc-only-secret")
	testutil.Equal(t, gotName, "security")
	testutil.DeepEqual(t, gotArgs, []string{"find-generic-password", "-s", "op-service-account-claude", "-w"})
}

// TestResolve_Keychain_ServiceAndAccount pins "Service-and-account lookup":
// keychain://<service>/<account> runs `security find-generic-password -s
// <service> -a <account> -w`.
func TestResolve_Keychain_ServiceAndAccount(t *testing.T) {
	ResetSecretMemoCache()
	var gotArgs []string
	installSubprocessRunner(t, func(_ context.Context, name string, args []string, _ []string, _ time.Duration) (string, bool) {
		gotArgs = args
		return "svc-acct-secret", true
	})

	v, ok := Resolve(config.SecretsConfig{}, "keychain://some-service/some-account")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "svc-acct-secret")
	testutil.DeepEqual(t, gotArgs, []string{"find-generic-password", "-s", "some-service", "-a", "some-account", "-w"})
}

// TestResolve_Keychain_MissingItemFails pins "Missing Keychain item fails to
// resolve": a non-zero `security` exit, or a zero exit with empty output,
// both fail the resolve.
func TestResolve_Keychain_MissingItemFails(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		stdout     string
		exitedZero bool
	}{
		{name: "non-zero exit", source: "keychain://missing-service-nonzero-exit", stdout: "", exitedZero: false},
		{name: "zero exit but empty output", source: "keychain://missing-service-empty-output", stdout: "", exitedZero: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetSecretMemoCache()
			installSubprocessRunner(t, func(context.Context, string, []string, []string, time.Duration) (string, bool) {
				return tt.stdout, tt.exitedZero
			})

			v, ok := Resolve(config.SecretsConfig{}, tt.source)

			testutil.Equal(t, ok, false)
			testutil.Equal(t, v, "")
		})
	}
}

// --- op (1Password) resolver with self-referential bootstrap ---

// TestResolve_Op_SuccessfulReadUsesResolvedBootstrap pins "Successful op
// read" AND "Bootstrap source is itself a registry resolve": resolving
// op://<vault>/<item>/<field> first resolves [secrets.op].bootstrap_source
// (here a keychain:// descriptor) through the SAME Resolve/dispatch used for
// any other keychain:// source (proven here via the shared fake subprocess
// seam recording BOTH the keychain and the op invocation — there is no
// separate "how op authenticates" code path), then runs `op read
// op://<vault>/<item>/<field>` with [secrets.op].bootstrap_target set to the
// bootstrap value in that subprocess's environment only.
func TestResolve_Op_SuccessfulReadUsesResolvedBootstrap(t *testing.T) {
	ResetSecretMemoCache()
	type call struct {
		name     string
		args     []string
		extraEnv []string
	}
	var calls []call
	installSubprocessRunner(t, func(_ context.Context, name string, args []string, extraEnv []string, _ time.Duration) (string, bool) {
		calls = append(calls, call{name: name, args: append([]string(nil), args...), extraEnv: append([]string(nil), extraEnv...)})
		switch name {
		case "security":
			return "bootstrapped-op-token", true
		case "op":
			return "the-real-secret", true
		}
		return "", false
	})

	sc := config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "keychain://op-service-account-claude",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}

	v, ok := Resolve(sc, "op://vault/item/field")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "the-real-secret")

	if len(calls) != 2 {
		t.Fatalf("expected exactly 2 subprocess calls (keychain bootstrap + op read); got %d: %+v", len(calls), calls)
	}
	testutil.Equal(t, calls[0].name, "security")
	testutil.DeepEqual(t, calls[0].args, []string{"find-generic-password", "-s", "op-service-account-claude", "-w"})

	testutil.Equal(t, calls[1].name, "op")
	testutil.DeepEqual(t, calls[1].args, []string{"read", "op://vault/item/field"})
	testutil.DeepEqual(t, calls[1].extraEnv, []string{"OP_SERVICE_ACCOUNT_TOKEN=bootstrapped-op-token"})
}

// TestResolve_Op_BootstrapNeverSetOnCallingProcessEnv pins "Bootstrap
// credential scoped to the op subprocess only": the resolved bootstrap
// credential must never be set on the calling process's own environment via
// os.Setenv.
func TestResolve_Op_BootstrapNeverSetOnCallingProcessEnv(t *testing.T) {
	ResetSecretMemoCache()
	const target = "OP_SERVICE_ACCOUNT_TOKEN_TEST_SCOPE_GUARD"
	if _, present := os.LookupEnv(target); present {
		t.Fatalf("test precondition violated: %s already set in this process", target)
	}

	installSubprocessRunner(t, func(_ context.Context, name string, _ []string, _ []string, _ time.Duration) (string, bool) {
		switch name {
		case "security":
			return "bootstrap-value", true
		case "op":
			return "secret-value", true
		}
		return "", false
	})

	sc := config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "keychain://some-service",
		BootstrapTarget: target,
	}}

	v, ok := Resolve(sc, "op://vault/item/field-scope-guard")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "secret-value")

	if _, present := os.LookupEnv(target); present {
		t.Fatalf("bootstrap credential leaked into the calling process's own environment via %s", target)
	}
}

// TestResolve_Op_MissingBootstrapConfigFails pins "Missing bootstrap
// configuration fails the op resolve": both when [secrets.op] is absent
// entirely and when bootstrap_source is configured but fails to resolve, any
// op:// descriptor fails to resolve — and, as a fail-fast invariant, `op
// read` must never even be attempted when the bootstrap credential itself
// couldn't be obtained.
func TestResolve_Op_MissingBootstrapConfigFails(t *testing.T) {
	tests := []struct {
		name   string
		sc     config.SecretsConfig
		source string
	}{
		{
			name:   "secrets.op entirely absent",
			sc:     config.SecretsConfig{},
			source: "op://vault/item/field-absent",
		},
		{
			name: "bootstrap_source configured but fails to resolve",
			sc: config.SecretsConfig{Op: config.OpConfig{
				BootstrapSource: "keychain://nonexistent-service",
				BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
			}},
			source: "op://vault/item/field-failing-bootstrap",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetSecretMemoCache()
			var opInvoked bool
			installSubprocessRunner(t, func(_ context.Context, name string, _ []string, _ []string, _ time.Duration) (string, bool) {
				if name == "op" {
					opInvoked = true
				}
				return "", false // bootstrap (keychain) always fails in this test
			})

			v, ok := Resolve(tt.sc, tt.source)

			testutil.Equal(t, ok, false)
			testutil.Equal(t, v, "")
			if opInvoked {
				t.Fatal("op read must never be attempted when the bootstrap credential fails to resolve")
			}
		})
	}
}

// --- Process-lifetime success-only memoization ---

// TestResolve_MemoizesSuccessfulResolve pins "Second resolve of the same
// descriptor is served from cache": a call-counting fake resolver proves a
// second resolve of the same descriptor returns the cached value with zero
// additional invocations.
func TestResolve_MemoizesSuccessfulResolve(t *testing.T) {
	ResetSecretMemoCache()
	var calls int
	installResolver(t, func(string) (string, bool) {
		calls++
		return "cached-value", true
	})

	v1, ok1 := Resolve(config.SecretsConfig{}, "env://MEMO_TEST_VAR_SUCCESS")
	v2, ok2 := Resolve(config.SecretsConfig{}, "env://MEMO_TEST_VAR_SUCCESS")

	testutil.Equal(t, ok1, true)
	testutil.Equal(t, v1, "cached-value")
	testutil.Equal(t, ok2, true)
	testutil.Equal(t, v2, "cached-value")
	testutil.Equal(t, calls, 1)
}

// TestResolve_FailedResolveIsRetriedNotPoisoned pins "Failed resolve is
// retried, not poisoned": a failed resolve is never cached, so a later
// attempt for the same descriptor re-invokes the resolver.
func TestResolve_FailedResolveIsRetriedNotPoisoned(t *testing.T) {
	ResetSecretMemoCache()
	var calls int
	installResolver(t, func(string) (string, bool) {
		calls++
		return "", false
	})

	_, ok1 := Resolve(config.SecretsConfig{}, "env://MEMO_TEST_VAR_FAILURE")
	_, ok2 := Resolve(config.SecretsConfig{}, "env://MEMO_TEST_VAR_FAILURE")

	testutil.Equal(t, ok1, false)
	testutil.Equal(t, ok2, false)
	testutil.Equal(t, calls, 2)
}

// --- op bootstrap resolution status tri-state ---

// TestQueryOpBootstrapStatus covers all 3 scenarios of "op bootstrap
// resolution status tri-state": RESOLVED (configured + resolves), NOT
// RESOLVED (configured + fails), and NOT CONFIGURED ([secrets.op] absent or
// bootstrap_source empty) — computed by one resolve-and-discard of
// bootstrap_source.
func TestQueryOpBootstrapStatus(t *testing.T) {
	tests := []struct {
		name       string
		sc         config.SecretsConfig
		resolverOK bool
		want       OpBootstrapStatus
	}{
		{
			name:       "resolved: configured and resolves",
			sc:         config.SecretsConfig{Op: config.OpConfig{BootstrapSource: "env://TRISTATE_VAR", BootstrapTarget: "X"}},
			resolverOK: true,
			want:       OpBootstrapResolved,
		},
		{
			name:       "not resolved: configured but fails",
			sc:         config.SecretsConfig{Op: config.OpConfig{BootstrapSource: "env://TRISTATE_VAR", BootstrapTarget: "X"}},
			resolverOK: false,
			want:       OpBootstrapNotResolved,
		},
		{
			name: "not configured: secrets.op entirely absent",
			sc:   config.SecretsConfig{},
			want: OpBootstrapNotConfigured,
		},
		{
			name: "not configured: bootstrap_source empty",
			sc:   config.SecretsConfig{Op: config.OpConfig{BootstrapSource: "", BootstrapTarget: "X"}},
			want: OpBootstrapNotConfigured,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetSecretMemoCache()
			installResolver(t, func(string) (string, bool) { return "v", tt.resolverOK })

			got := QueryOpBootstrapStatus(tt.sc)
			testutil.Equal(t, got, tt.want)
		})
	}
}

// --- PR #928 (b6813697) regression guards: exec.LookPath, not os.Stat; and
// a process-group kill on timeout, not exec.CommandContext's default Cancel.

// TestCommandResolvable_RejectsDirectoryAndNonExecutable pins the
// exec.LookPath-vs-os.Stat regression: a directory or a non-executable
// regular file must be REJECTED (os.Stat would wrongly accept either),
// while an executable file (by absolute path) or a bare name found on PATH
// must resolve.
func TestCommandResolvable_RejectsDirectoryAndNonExecutable(t *testing.T) {
	dir := t.TempDir()

	if commandResolvable(dir) {
		t.Fatalf("commandResolvable(%q) = true for a directory, want false", dir)
	}

	nonExec := filepath.Join(dir, "not-executable")
	testutil.NoError(t, os.WriteFile(nonExec, []byte("#!/bin/sh\necho hi\n"), 0o644))
	if commandResolvable(nonExec) {
		t.Fatalf("commandResolvable(%q) = true for a non-executable file, want false", nonExec)
	}

	stub := writeExecStub(t, `echo hi`)
	if !commandResolvable(stub) {
		t.Fatalf("commandResolvable(%q) = false for an executable file, want true", stub)
	}

	if !commandResolvable("sh") {
		t.Fatal(`commandResolvable("sh") = false, want true (sh is expected on PATH)`)
	}

	if commandResolvable("definitely-not-a-real-command-xyz-123") {
		t.Fatal("commandResolvable of a nonexistent bare command = true, want false")
	}
}

// TestDefaultSecretSubprocessRunner_TimesOutDespiteForkedDescendant pins the
// process-group-kill regression: a resolver subprocess whose command forks a
// descendant that inherits stdout/stderr (here, `sh` forking `sleep`) is
// still bounded by the configured timeout, not left hanging until the
// descendant exits on its own. exec.CommandContext's default Cancel only
// signals the direct child (`sh`), which would leave `sleep` running and
// holding the stdout pipe open — silently defeating the timeout (proven
// empirically in PR #928/b6813697: a 300ms timeout waited the full 30s
// without the Setpgid+Cancel+WaitDelay fix). This calls
// defaultSecretSubprocessRunner directly (bypassing the swappable
// secretSubprocessRunner seam) so it exercises the REAL implementation, never
// a fake — and never a real security/op binary, only this local stub.
func TestDefaultSecretSubprocessRunner_TimesOutDespiteForkedDescendant(t *testing.T) {
	stub := writeExecStub(t, `sleep 30; printf 'too-late'`)

	start := time.Now()
	stdout, exitedZero := defaultSecretSubprocessRunner(context.Background(), stub, nil, nil, 300*time.Millisecond)
	elapsed := time.Since(start)

	testutil.Equal(t, exitedZero, false)
	testutil.Equal(t, stdout, "")
	if elapsed > 15*time.Second {
		t.Fatalf("expected the 300ms timeout to bound the call well under the stub's 30s sleep; took %s", elapsed)
	}
}
