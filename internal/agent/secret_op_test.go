package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// writeOpStub writes an executable shell script to a temp dir and returns its
// path. The script's behavior is entirely controlled by the caller-supplied
// body, keeping these tests independent of any real `op`/1Password
// installation — never a real 1Password call.
func writeOpStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "op-stub")
	testutil.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return path
}

// --- secretResolverFor selection (design.md D-DEGRADE) ---

func TestSecretResolverFor_EnvModeUsesPackageResolver(t *testing.T) {
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{Resolver: "env"})
	v, ok := resolve("anything")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
}

func TestSecretResolverFor_EmptyModeUsesPackageResolver(t *testing.T) {
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{})
	v, ok := resolve("anything")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
}

func TestSecretResolverFor_UnrecognizedModeFallsOpenAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{Resolver: "bogus-mode"})
	v, ok := resolve("anything")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
	testutil.Contains(t, readLog(), "bogus-mode")
}

func TestSecretResolverFor_OpModeEmptyTemplateFallsBackAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{Resolver: "op"})
	v, ok := resolve("anything")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
	testutil.Contains(t, readLog(), "reference_template")
}

func TestSecretResolverFor_OpModeUnresolvableCommandFallsBackAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			ReferenceTemplate: "op://vault/item/{source}",
			Command:           "/definitely/not/a/real/binary/op-xyz",
		},
	})
	v, ok := resolve("anything")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
	testutil.Contains(t, readLog(), "/definitely/not/a/real/binary/op-xyz")
}

func TestSecretResolverFor_OpModeValidBuildsDistinctResolver(t *testing.T) {
	// Echoes its own argv (after the script name) back on stdout, so the
	// resolved "value" documents the exact invocation shape.
	stub := writeOpStub(t, `printf '%s' "$*"`)
	installResolver(t, func(string) (string, bool) { return "sentinel", true })

	resolve := secretResolverFor(config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			ReferenceTemplate: "op://vault/item/{source}",
			Command:           stub,
		},
	})
	v, ok := resolve("HERA_OPENAI")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "read --no-newline op://vault/item/HERA_OPENAI")
}

// --- op resolver invocation shape (design.md D-OP-INVOCATION) ---

func TestOpSecretResolver_TrimsTrailingNewline(t *testing.T) {
	stub := writeOpStub(t, `printf 'value-with-newline\n'`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", time.Second)

	v, ok := resolve("SRC")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "value-with-newline")
}

func TestOpSecretResolver_SubstitutesSourceToken(t *testing.T) {
	stub := writeOpStub(t, `printf '%s' "$3"`) // $1=read $2=--no-newline $3=<ref>
	resolve := opSecretResolver(stub, "op://vault/item/{source}", time.Second)

	v, ok := resolve("HERA_OPENAI")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "op://vault/item/HERA_OPENAI")
}

func TestOpSecretResolver_TimesOut(t *testing.T) {
	// The stub sleeps far longer than any reasonable test bound so a
	// generous elapsed-time margin (tolerating scheduler jitter from this
	// package's other tests spawning many concurrent real processes) still
	// clearly distinguishes "timeout enforced" from "timeout ignored."
	//
	// This specifically exercises the Setpgid/process-group-kill path: `sh`
	// forks `sleep` as a distinct child before `cmd.Cancel` fires, so killing
	// only the direct `sh` process (the pre-fix behavior) would leave `sleep`
	// running and holding the stdout pipe open, silently defeating the
	// timeout — see the "op (1Password) secret resolver" gotcha note. A
	// single-process stub (e.g. exec.Command("sleep", "30")) would NOT
	// exercise that fix at all; keep this as a multi-command shell script.
	stub := writeOpStub(t, `sleep 30; printf 'too-late'`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", 300*time.Millisecond)

	start := time.Now()
	v, ok := resolve("SRC")
	elapsed := time.Since(start)

	testutil.Equal(t, ok, false)
	testutil.Equal(t, v, "")
	if elapsed > 15*time.Second {
		t.Fatalf("expected the 300ms timeout to bound the call well under the stub's 30s sleep; took %s", elapsed)
	}
}

func TestOpSecretResolver_NonZeroExitUnresolved(t *testing.T) {
	stub := writeOpStub(t, `echo "not signed in" >&2; exit 1`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", time.Second)

	v, ok := resolve("SRC")
	testutil.Equal(t, ok, false)
	testutil.Equal(t, v, "")
}

func TestOpSecretResolver_EmptyOutputUnresolved(t *testing.T) {
	stub := writeOpStub(t, `true`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", time.Second)

	v, ok := resolve("SRC")
	testutil.Equal(t, ok, false)
	testutil.Equal(t, v, "")
}

func TestOpSecretResolver_NeverAttachesStdin(t *testing.T) {
	// cat echoes stdin back and blocks waiting for EOF on a live pipe. A nil
	// (never inherited) Stdin means the child's stdin is the null device, so
	// cat sees immediate EOF and returns long before the generous timeout
	// below — if stdin were instead attached, cat would block until the
	// timeout killed it, and elapsed would sit near the full timeout instead.
	stub := writeOpStub(t, `cat`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", 5*time.Second)

	start := time.Now()
	v, ok := resolve("SRC")
	elapsed := time.Since(start)

	testutil.Equal(t, ok, false) // empty stdin => empty output => unresolved
	testutil.Equal(t, v, "")
	if elapsed > 3*time.Second {
		t.Fatalf("expected an immediate return well under the 5s timeout (no stdin blocking); took %s", elapsed)
	}
}

func TestOpSecretResolver_FailureLogsSourceAndStderrNeverValueOrRef(t *testing.T) {
	readLog := captureUXLog(t)
	stub := writeOpStub(t, `echo "not signed in to vault" >&2; exit 1`)
	resolve := opSecretResolver(stub, "op://super-secret-vault/item/{source}", time.Second)

	_, ok := resolve("HERA_OPENAI")
	testutil.Equal(t, ok, false)

	log := readLog()
	testutil.Contains(t, log, "HERA_OPENAI")
	testutil.Contains(t, log, "not signed in to vault")
	if strings.Contains(log, "op://super-secret-vault/item/HERA_OPENAI") {
		t.Fatalf("expanded op reference must never be logged; got %q", log)
	}
}

// A successful resolve logs NOTHING at all — the most security-relevant
// acceptance criterion in design.md ("the secret value should never appear
// in any log line on the success path either"), and the one prior coverage
// left unpinned.
func TestOpSecretResolver_SuccessLogsNothing(t *testing.T) {
	readLog := captureUXLog(t)
	stub := writeOpStub(t, `printf 'quietly-resolved-value'`)
	resolve := opSecretResolver(stub, "op://vault/item/{source}", time.Second)

	v, ok := resolve("HERA_OPENAI")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "quietly-resolved-value")

	if log := readLog(); log != "" {
		t.Fatalf("expected no log output on the success path; got %q", log)
	}
}

func TestOpDiagnosticLine_CapsAtMaxLen(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := opDiagnosticLine(long)
	testutil.Equal(t, len(got), 200)
	testutil.Equal(t, got, long[:200])
}

func TestOpDiagnosticLine_TakesFirstLineOnly(t *testing.T) {
	got := opDiagnosticLine("first line\nsecond line should be dropped")
	testutil.Equal(t, got, "first line")
}

// --- opCommandResolvable (regression coverage for the os.Stat bug caught in
// review: a directory or non-executable file must NOT pass this check, or
// secretResolverFor's D-DEGRADE fallback never actually degrades — it builds
// an op resolver that then fails every single read.) ---

func TestOpCommandResolvable_RejectsDirectory(t *testing.T) {
	if opCommandResolvable(t.TempDir()) {
		t.Fatal("a directory must not be considered a resolvable command")
	}
}

func TestOpCommandResolvable_RejectsNonExecutableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-executable")
	testutil.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o644))
	if opCommandResolvable(path) {
		t.Fatal("a non-executable file must not be considered a resolvable command")
	}
}

func TestOpCommandResolvable_AcceptsAbsoluteExecutable(t *testing.T) {
	stub := writeOpStub(t, `true`)
	if !opCommandResolvable(stub) {
		t.Fatal("an absolute path to an executable file must resolve")
	}
}

func TestOpCommandResolvable_AcceptsBareNameOnPATH(t *testing.T) {
	dir := t.TempDir()
	name := "argus-op-stub-test"
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ntrue\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if !opCommandResolvable(name) {
		t.Fatal("a bare name present on PATH must resolve")
	}
}

func TestOpCommandResolvable_RejectsUnresolvableBareName(t *testing.T) {
	if opCommandResolvable("definitely-not-a-real-command-xyz") {
		t.Fatal("a bare name absent from PATH must not resolve")
	}
}

// --- secretResolverFor: missing {source} token and TimeoutSeconds wiring ---

func TestSecretResolverFor_OpModeMissingSourceTokenFallsBackAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	installResolver(t, func(string) (string, bool) { return "sentinel", true })
	stub := writeOpStub(t, `printf 'should-never-run'`)

	resolve := secretResolverFor(config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			// No "{source}" token: every EnvVars entry would otherwise
			// silently resolve to this SAME literal reference.
			ReferenceTemplate: "op://vault/item/always-the-same-field",
			Command:           stub,
		},
	})
	v, ok := resolve("ANY_SOURCE")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, v, "sentinel")
	testutil.Contains(t, readLog(), opSourceToken)
}

func TestSecretResolverFor_TimeoutSecondsOverridesDefault(t *testing.T) {
	// The stub sleeps longer than the configured 1s override but well under
	// the 5s default, so a passing test proves TimeoutSeconds actually rode
	// through to the constructed resolver rather than silently defaulting.
	stub := writeOpStub(t, `sleep 3; printf 'too-late'`)

	resolve := secretResolverFor(config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			ReferenceTemplate: "op://vault/item/{source}",
			Command:           stub,
			TimeoutSeconds:    1,
		},
	})

	start := time.Now()
	_, ok := resolve("SRC")
	elapsed := time.Since(start)

	testutil.Equal(t, ok, false)
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("expected the configured 1s timeout to bound the call well under the stub's 3s sleep; took %s", elapsed)
	}
}

// --- BuildCmd wiring (design.md D-LIVE: cfg.Secrets is read fresh per call) ---

func TestBuildCmd_SecretsOpMode_ResolvedSourceInjected(t *testing.T) {
	stub := writeOpStub(t, `printf 'op-resolved-value'`)
	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"})
	cfg.Secrets = config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			ReferenceTemplate: "op://vault/item/{source}",
			Command:           stub,
		},
	}
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
	testutil.Equal(t, got, "op-resolved-value")
}

func TestBuildCmd_SecretsOpMode_DegradesToEnvWhenUnconfigured(t *testing.T) {
	t.Setenv("HERA_OPENAI_OPDEGRADE_TESTSRC", "from-env-degrade")
	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI_OPDEGRADE_TESTSRC"})
	cfg.Secrets = config.SecretsConfig{Resolver: "op"} // no reference_template configured
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	got, ok := envValue(cmd.Env, "OPENAI_API_KEY")
	if !ok {
		t.Fatalf("expected OPENAI_API_KEY resolved via the env-resolver degrade path; got %v", cmd.Env)
	}
	testutil.Equal(t, got, "from-env-degrade")
}

func TestBuildCmd_SecretsOpMode_FailureLeavesTargetUnsetAndWarns(t *testing.T) {
	readLog := captureUXLog(t)
	stub := writeOpStub(t, `echo "not signed in" >&2; exit 1`)
	cfg := envVarConfig(map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"})
	cfg.Secrets = config.SecretsConfig{
		Resolver: "op",
		Op: config.OpResolverConfig{
			ReferenceTemplate: "op://vault/item/{source}",
			Command:           stub,
		},
	}
	task := &model.Task{Name: "review", Backend: "codex", Worktree: t.TempDir()}

	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)

	if _, ok := envValue(cmd.Env, "OPENAI_API_KEY"); ok {
		t.Fatalf("expected OPENAI_API_KEY unset on op-read failure; got %v", cmd.Env)
	}
	log := readLog()
	testutil.Contains(t, log, "OPENAI_API_KEY")
	testutil.Contains(t, log, "HERA_OPENAI")
	testutil.Contains(t, log, "not signed in")
}
