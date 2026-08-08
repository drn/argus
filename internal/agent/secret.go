package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/uxlog"
)

// SecretResolver resolves a backend credential SOURCE descriptor to its secret
// value at agent-spawn time. It returns (value, true) when the source resolves,
// or ("", false) when it cannot — in which case BuildCmd leaves the target
// variable unset and logs a non-sensitive warning naming only the variable.
//
// The returned value is a live secret: callers MUST NOT log it, persist it, or
// echo it anywhere. Only the SOURCE descriptor (the resolver's input) is safe
// to record.
type SecretResolver func(source string) (value string, ok bool)

// secretResolver is the active resolver consulted by BuildCmd. The default
// reads the daemon's own process environment (envSecretResolver). It is a
// pluggable seam: a production deployment can swap in a resolver that shells
// out to the `op` (1Password) CLI — e.g. `op read op://claude/shell-env/<src>`
// — via SetSecretResolver, with NO change to BuildCmd. (That production
// resolver, and how the daemon authenticates to 1Password without a plaintext
// key on disk, is a separate follow-up — see the add-foreign-backend-envmap
// proposal.)
var secretResolver SecretResolver = envSecretResolver

// envSecretResolver resolves a source descriptor against the daemon's own
// environment by name. os.LookupEnv distinguishes "unset" (ok=false → target
// not injected) from "set but empty" (ok=true → target injected as empty),
// which is the intended semantics.
//
// CAVEAT (documented for operators): a launchd-started daemon inherits a
// minimal environment that will NOT contain a source like HERA_OPENAI, so this
// default resolves nothing in that deployment. Cross-vendor review works today
// only when the daemon is started from an environment that already carries the
// source variable. The `op`-CLI resolver above is the intended production path.
func envSecretResolver(source string) (string, bool) {
	return os.LookupEnv(source)
}

// SetSecretResolver replaces the active secret resolver, returning the previous
// one so callers (and tests) can restore it. A nil argument resets to the
// default process-environment resolver. This is the seam through which a future
// 1Password/`op` resolver is wired without editing BuildCmd.
func SetSecretResolver(r SecretResolver) SecretResolver {
	prev := secretResolver
	if r == nil {
		r = envSecretResolver
	}
	secretResolver = r
	return prev
}

// defaultOpCommand is the `op` executable used when config.OpResolverConfig's
// Command is empty.
const defaultOpCommand = "op"

// defaultOpTimeout bounds an `op read` invocation when
// config.OpResolverConfig's TimeoutSeconds is zero/unset.
const defaultOpTimeout = 5 * time.Second

// opSourceToken is the literal substring in a configured ReferenceTemplate
// substituted with the EnvVars mapping's source descriptor.
const opSourceToken = "{source}"

// secretResolverFor selects the SecretResolver BuildCmd should consult for a
// given command build, from the live config.SecretsConfig that build's cfg
// parameter already carries — see openspec/changes/add-op-secret-resolver/
// design.md D-DEGRADE and D-LIVE. It is re-evaluated on every call (no
// caching, no daemon-startup wiring), so a config.toml edit takes effect on
// the next spawn. It never mutates the package-level secretResolver
// variable — the "env" mode (default, and any unrecognized value) always
// resolves to whatever secretResolver/SetSecretResolver currently holds, so
// the existing test seam is untouched.
func secretResolverFor(sc config.SecretsConfig) SecretResolver {
	mode := strings.ToLower(strings.TrimSpace(sc.Resolver))
	if mode == "" {
		mode = "env"
	}
	if mode != "op" {
		if mode != "env" {
			uxlog.Log("[agent] secrets.resolver %q not recognized; using the environment resolver", sc.Resolver)
		}
		return secretResolver
	}

	tmpl := sc.Op.ReferenceTemplate
	if tmpl == "" {
		uxlog.Log("[agent] secrets.resolver=\"op\" but secrets.op.reference_template is empty; falling back to the environment resolver")
		return secretResolver
	}
	if !strings.Contains(tmpl, opSourceToken) {
		// A template missing the substitution token isn't merely unresolved —
		// it silently resolves every EnvVars source to the SAME literal
		// reference, injecting a real but wrong credential rather than none
		// at all. Treated as misconfigured, same as an empty template.
		uxlog.Log("[agent] secrets.resolver=\"op\" but secrets.op.reference_template has no %q token; falling back to the environment resolver", opSourceToken)
		return secretResolver
	}

	cmdName := sc.Op.Command
	if cmdName == "" {
		cmdName = defaultOpCommand
	}
	if !opCommandResolvable(cmdName) {
		uxlog.Log("[agent] secrets.resolver=\"op\" but %q is not resolvable; falling back to the environment resolver", cmdName)
		return secretResolver
	}

	timeout := defaultOpTimeout
	if sc.Op.TimeoutSeconds > 0 {
		timeout = time.Duration(sc.Op.TimeoutSeconds) * time.Second
	}
	return opSecretResolver(cmdName, tmpl, timeout)
}

// opCommandResolvable reports whether cmdName can be executed: an absolute
// (or otherwise slash-containing) path that is itself a regular, executable
// file, or a bare name found on PATH. exec.LookPath handles both cases
// directly — for a path containing a separator it checks the file in place
// (not just existence: a directory or a non-executable file correctly
// fails), consulting PATH only for a bare name. A plain os.Stat would wrongly
// accept a directory or a non-executable file, defeating the D-DEGRADE
// fallback this check exists to gate.
func opCommandResolvable(cmdName string) bool {
	_, err := exec.LookPath(cmdName)
	return err == nil
}

// opSecretResolver returns a SecretResolver that resolves a source
// descriptor by invoking `op read --no-newline <reference>`, where
// <reference> is tmpl with the literal token "{source}" replaced by the
// source descriptor. See design.md D-OP-INVOCATION and D-FAIL.
//
// A failure (non-zero exit, timeout, or empty output) resolves as ("",
// false) — identical to an unresolved process-environment source — so
// BuildCmd's existing unresolved-source warning fires unchanged and the
// spawn proceeds. This function additionally logs ONE supplementary
// diagnostic line naming the source descriptor and the first line of `op`'s
// own stderr (size-capped): never the resolved value, the expanded
// reference, or stdout, on either the success or failure path.
func opSecretResolver(cmdName, tmpl string, timeout time.Duration) SecretResolver {
	return func(source string) (string, bool) {
		ref := strings.ReplaceAll(tmpl, opSourceToken, source)

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, cmdName, "read", "--no-newline", ref)
		// cmd.Stdin is deliberately left unset: exec.Cmd connects an unset
		// Stdin to the null device, so `op` can never block this spawn
		// waiting on interactive input.
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		// Run cmdName in its own process group and kill the whole group on
		// timeout, not just cmdName itself. exec.CommandContext's default
		// Cancel only signals the direct child; if that child ever forks a
		// descendant that inherits the stdout/stderr pipes (e.g. `op`
		// shelling out internally, or a test stub), killing only the direct
		// child leaves the descendant holding those pipes open, and
		// cmd.Wait() blocks until the descendant exits on its own — silently
		// defeating the timeout this function exists to enforce.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				// The child (and its group) already exited — Cancel raced
				// the reap. Report it the same way the stdlib's own default
				// Cancel does (os.Process.Kill returns os.ErrProcessDone in
				// this case) so Wait's error reflects "already finished," not
				// a raw ESRCH that would otherwise muddy the failure
				// diagnostic below.
				return os.ErrProcessDone
			}
			return err
		}
		// A process that escapes the group (e.g. by calling setsid itself)
		// would otherwise still hold the stdout/stderr pipes open after the
		// group kill above, leaving Wait to block until it exits on its own —
		// the same failure mode Setpgid guards against, one level deeper.
		// WaitDelay force-closes the pipes this long after Cancel fires,
		// bounding Wait unconditionally.
		cmd.WaitDelay = time.Second

		if err := cmd.Run(); err != nil {
			uxlog.Log("[agent] op resolver: read failed for source %q: %s", source, opDiagnosticLine(stderr.String()))
			return "", false
		}

		value := strings.TrimRight(stdout.String(), "\n")
		if value == "" {
			uxlog.Log("[agent] op resolver: read for source %q returned no value", source)
			return "", false
		}
		return value, true
	}
}

// opDiagnosticLine caps a subprocess's stderr to its first line, trimmed and
// size-bounded, for a non-sensitive log line. The input here is always `op`'s
// own stderr on a failed read, never the resolved value.
func opDiagnosticLine(stderr string) string {
	const maxLen = 200
	if i := strings.IndexByte(stderr, '\n'); i >= 0 {
		stderr = stderr[:i]
	}
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > maxLen {
		stderr = stderr[:maxLen]
	}
	return stderr
}
