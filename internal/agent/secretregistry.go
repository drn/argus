package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drn/argus/internal/config"
)

// This file implements the add-secrets-resolver-registry secrets-resolution
// registry: a scheme-prefixed secret source descriptor ("env://", "keychain://",
// "op://") dispatched to a scheme-specific resolver, with process-lifetime
// success-only memoization in front of the whole thing. Resolution happens
// fresh, at the point of use (inside whichever process calls BuildCmd), never
// via os.Setenv-based ambient injection.
//
// STAGE NOTE: this stage (2) lands scheme dispatch, the env scheme (wired
// through the EXISTING secret.go seam, not a hardcoded os.LookupEnv call), the
// keychain resolver, and memoization. The op scheme, OpBootstrapStatus's real
// tri-state logic, and BuildCmd's own dispatch through this registry are a
// later stage's job — see internal/agent/secret_test.go /
// secretregistry_test.go for the full, already-committed test surface this
// stage is proving out.

// keychainCommandTimeout bounds a `security find-generic-password` subprocess
// call. Keychain lookups are local and should return near-instantly; this is
// generous headroom, not an expected steady-state latency.
const keychainCommandTimeout = 5 * time.Second

// secretSubprocessWaitDelay backstops cmd.Wait() after a process-group kill.
// The kill itself (see defaultSecretSubprocessRunner) should close the
// stdout pipe almost immediately; this only guards against a pipe that
// somehow stays open a moment longer.
const secretSubprocessWaitDelay = 2 * time.Second

// splitSecretScheme splits a source descriptor on the first "://",
// defaulting the scheme to "env" when absent — the bare-string form is a
// first-class alias for env://<descriptor>, preserving today's behavior.
func splitSecretScheme(source string) (scheme, rest string) {
	if s, r, found := strings.Cut(source, "://"); found {
		return s, r
	}
	return "env", source
}

// secretSchemeResolvers dispatches a source descriptor's scheme to its
// resolver. An unrecognized scheme is simply absent from this map — Resolve
// treats a missing entry as "not-ok", never an error. keychain/op are new,
// config-driven dispatch branches that do NOT go through the pluggable
// secretResolver seam (that seam stays env-only, preserving
// SetSecretResolver's existing contract for bare-string/env:// sources).
var secretSchemeResolvers = map[string]func(sc config.SecretsConfig, rest string) (string, bool){
	"env":      envSchemeResolve,
	"keychain": keychainSchemeResolve,
}

// envSchemeResolve resolves an env:// (or bare-string) source through the
// EXISTING swappable secretResolver var (secret.go), not a hardcoded direct
// call to envSecretResolver/os.LookupEnv — this is what preserves
// SetSecretResolver's pluggability/override contract unchanged (Task 1.8).
func envSchemeResolve(_ config.SecretsConfig, rest string) (string, bool) {
	return secretResolver(rest)
}

// keychainSchemeResolve adapts keychainResolve to the uniform
// secretSchemeResolvers signature; the keychain scheme needs no config.
func keychainSchemeResolve(_ config.SecretsConfig, rest string) (string, bool) {
	return keychainResolve(rest)
}

// keychainResolve resolves "keychain://<service>" or
// "keychain://<service>/<account>" by shelling out to `security
// find-generic-password`. A non-zero exit or empty stdout is a failed
// resolve. The actual subprocess invocation runs through the injectable
// secretSubprocessRunner seam so tests never shell out to a real `security`.
func keychainResolve(rest string) (string, bool) {
	service, account, hasAccount := strings.Cut(rest, "/")
	args := []string{"find-generic-password", "-s", service}
	if hasAccount {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")

	stdout, exitedZero := secretSubprocessRunner(context.Background(), "security", args, nil, keychainCommandTimeout)
	if !exitedZero || stdout == "" {
		return "", false
	}
	return stdout, true
}

// --- process-lifetime success-only memoization ---

var (
	secretMemoMu sync.Mutex
	secretMemo   = map[string]string{}
)

// ResetSecretMemoCache clears the process-lifetime resolve cache. Exported
// for tests only — production code never needs to clear it, since success-only
// memoization is meant to live for the process's remaining lifetime.
func ResetSecretMemoCache() {
	secretMemoMu.Lock()
	defer secretMemoMu.Unlock()
	secretMemo = map[string]string{}
}

// Resolve resolves a secret source descriptor through the scheme-prefixed
// registry (see splitSecretScheme), memoizing a SUCCESSFUL resolve for the
// remaining lifetime of the process, keyed by the exact descriptor string
// passed in. A failed resolve is never cached, so a transient failure (e.g.
// an `op`/`security` blip) can succeed on a later attempt. sc supplies any
// scheme-specific configuration a resolver needs (currently unused by env/
// keychain; the op scheme, added in a later stage, reads sc.Op from it).
func Resolve(sc config.SecretsConfig, source string) (string, bool) {
	secretMemoMu.Lock()
	if v, ok := secretMemo[source]; ok {
		secretMemoMu.Unlock()
		return v, true
	}
	secretMemoMu.Unlock()

	scheme, rest := splitSecretScheme(source)
	resolver, ok := secretSchemeResolvers[scheme]
	if !ok {
		return "", false
	}
	v, ok := resolver(sc, rest)
	if !ok {
		return "", false
	}

	secretMemoMu.Lock()
	secretMemo[source] = v
	secretMemoMu.Unlock()
	return v, true
}

// --- op bootstrap resolution status tri-state ---
//
// STAGE NOTE: OpBootstrapStatus's real RESOLVED/NOT-RESOLVED/NOT-CONFIGURED
// logic is a later stage's job (it needs the op resolver, which this stage
// deliberately does not implement — see the file header). The type and its
// constants are declared here only so internal/agent compiles against the
// already-committed secretregistry_test.go; QueryOpBootstrapStatus is a
// placeholder that always reports NOT CONFIGURED, so
// TestQueryOpBootstrapStatus's RESOLVED/NOT-RESOLVED cases are EXPECTED to
// remain red until that later stage lands.

// OpBootstrapStatus is the op bootstrap resolution status tri-state,
// surfaced by `argus doctor` and the Settings System panel.
type OpBootstrapStatus int

const (
	// OpBootstrapNotConfigured means [secrets.op] is absent or
	// bootstrap_source is empty — there is no bootstrap to attempt.
	OpBootstrapNotConfigured OpBootstrapStatus = iota
	// OpBootstrapResolved means bootstrap_source is configured and resolves.
	OpBootstrapResolved
	// OpBootstrapNotResolved means bootstrap_source is configured but fails
	// to resolve.
	OpBootstrapNotResolved
)

// QueryOpBootstrapStatus is a placeholder for a later stage's
// resolve-and-discard tri-state query; see the STAGE NOTE above.
func QueryOpBootstrapStatus(_ config.SecretsConfig) OpBootstrapStatus {
	return OpBootstrapNotConfigured
}

// --- injectable subprocess execution seam ---

// secretSubprocessRunner is the test seam for executing a resolver
// subprocess (mirrors internal/gitutil's prRunner var-swap pattern). It
// returns the command's trimmed stdout and whether the command exited with
// code 0 — NOT whether the caller should treat the result as resolved (an
// empty stdout with a zero exit is still a failed keychain/op lookup; that
// distinction is made by the scheme resolver, not here). extraEnv, when
// non-empty, is appended to a copy of the ambient environment for that
// subprocess only (used by the op resolver's bootstrap-credential handoff;
// never via os.Setenv on the calling process).
var secretSubprocessRunner = defaultSecretSubprocessRunner

// commandResolvable reports whether name is an actually-resolvable,
// executable command — via exec.LookPath, NOT os.Stat. os.Stat wrongly
// accepts a directory or a non-executable regular file as a match;
// exec.LookPath correctly rejects both while still resolving a bare name
// against PATH or an absolute/relative path directly. (Regression guard for
// the bug found and fixed in the closed, unmerged PR #928 / b6813697.)
func commandResolvable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// defaultSecretSubprocessRunner is the real subprocess-execution
// implementation behind secretSubprocessRunner. It bounds the subprocess with
// BOTH a context timeout and a process-group kill on cancellation —
// exec.CommandContext's default Cancel only signals the direct child process
// and silently fails to bound cmd.Wait() if that child forks a descendant
// that inherits and holds open the stdout/stderr pipes (proven empirically in
// PR #928/b6813697: a 300ms timeout waited the full 30s without this fix).
func defaultSecretSubprocessRunner(ctx context.Context, name string, args []string, extraEnv []string, timeout time.Duration) (string, bool) {
	if !commandResolvable(name) {
		return "", false
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	// Put the subprocess (and anything it forks) in its own process group so
	// a timeout kill can reach descendants, not just the direct child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = secretSubprocessWaitDelay
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			// Match exec.CommandContext's own default Cancel contract: a
			// process that already exited is not itself an error.
			return os.ErrProcessDone
		}
		return err
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(stdout.String()), true
}
