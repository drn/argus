package agent

import "os"

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
