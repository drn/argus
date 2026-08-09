package doctor

import (
	"strings"
)

// SecretsBootstrapStatus classifies the [secrets.op] bootstrap-source
// resolution tri-state (add-secrets-resolver-registry). This check is
// independent of the binary-coherence Verdict and the Stop-hook and
// diligence-profile-library statuses above and never affects argus doctor's
// exit code — it exists purely to surface whether the op resolver's own
// bootstrap credential is actually resolvable, via a single resolve-and-
// discard of bootstrap_source at check time. The resolved value itself is
// never logged, returned, or otherwise surfaced here — only this tri-state.
type SecretsBootstrapStatus int

const (
	// SecretsBootstrapNotConfigured: [secrets.op] is absent or
	// bootstrap_source is empty — there is no bootstrap to attempt. Reported
	// distinctly from SecretsBootstrapNotResolved, and takes precedence over
	// it regardless of any (stray) resolved signal.
	SecretsBootstrapNotConfigured SecretsBootstrapStatus = iota
	// SecretsBootstrapResolved: bootstrap_source is configured and resolves.
	SecretsBootstrapResolved
	// SecretsBootstrapNotResolved: bootstrap_source is configured but fails
	// to resolve (e.g. a renamed Keychain item, or 1Password signed out).
	SecretsBootstrapNotResolved
)

// DiagnoseSecretsBootstrap classifies the op bootstrap-source resolution
// tri-state from whether [secrets.op].bootstrap_source is configured and,
// when it is, whether it resolved. An absent configuration is reported as
// NOT CONFIGURED regardless of resolved — a caller has no basis to attempt a
// resolve, let alone report one as having succeeded, when there is nothing
// configured to resolve.
func DiagnoseSecretsBootstrap(configured, resolved bool) SecretsBootstrapStatus {
	if !configured {
		return SecretsBootstrapNotConfigured
	}
	if !resolved {
		return SecretsBootstrapNotResolved
	}
	return SecretsBootstrapResolved
}

// RenderSecretsBootstrap builds the human-readable secrets-bootstrap status
// line printed by `argus doctor` as its own section, alongside (but
// independent of) the binary-coherence table, the Stop-hook section, and the
// profile-library section.
func RenderSecretsBootstrap(status SecretsBootstrapStatus) string {
	var b strings.Builder
	b.WriteString("\nSecrets bootstrap ([secrets.op].bootstrap_source): ")
	switch status {
	case SecretsBootstrapResolved:
		b.WriteString("RESOLVED\n")
	case SecretsBootstrapNotResolved:
		b.WriteString("NOT RESOLVED\n")
	default:
		b.WriteString("NOT CONFIGURED\n")
	}
	return b.String()
}
