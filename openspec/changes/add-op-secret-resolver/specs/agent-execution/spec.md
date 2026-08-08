## ADDED Requirements

### Requirement: Configurable secret-resolver mode

The system SHALL determine, when building the agent command and resolving a
backend's credential environment mapping, which secret resolver to consult
from the live configuration passed to the command builder, re-evaluated on
every build (not cached from an earlier build or from daemon startup). The
system SHALL select the process-environment resolver when the configured mode
is absent, `"env"`, or any value the system does not recognize, SHALL log a
non-sensitive warning naming the unrecognized value when it falls into that
last case, and SHALL select the `op` resolver (see "op secret-resolver
behavior" below) only when the configured mode is `"op"` AND that resolver is
actually usable per its own availability check. Selecting the process-
environment resolver in every case above SHALL require no code change to any
existing installation's behavior.

#### Scenario: Default mode resolves via the process environment

- **WHEN** no secret-resolver mode is configured
- **THEN** the command builder resolves every credential-mapping source
  through the process-environment resolver, unchanged from prior behavior

#### Scenario: Unrecognized mode falls open to the process environment

- **WHEN** the configured secret-resolver mode is a value other than `"env"`
  or `"op"`
- **THEN** the command builder resolves through the process-environment
  resolver and logs a warning naming the unrecognized value

#### Scenario: Mode takes effect on the next build without a restart

- **WHEN** the configured secret-resolver mode changes between two command
  builds
- **THEN** the second build reflects the new mode with no daemon restart and
  no explicit reload step

### Requirement: op secret-resolver behavior

The system SHALL provide an `op` secret-resolver mode that resolves an
`EnvVars` mapping's source descriptor by invoking the 1Password CLI (`op
read`). The object reference passed to `op read` SHALL be built from a
user-configured reference template in which the literal token `{source}` is
replaced with the mapping's source descriptor; the system SHALL NOT hardcode
any vault, account, or item identifier. Before invoking `op`, the system SHALL
verify the reference template is non-empty and the configured `op` command is
resolvable (an absolute path that exists, or a bare name found on `PATH`);
when either check fails, the system SHALL fall back to the process-environment
resolver and log a non-sensitive degrade notice, rather than treating every
mapping as unresolved. When both checks pass, the system SHALL invoke `op read
--no-newline <reference>` under a bounded timeout, with no data attached to
the subprocess's standard input, and SHALL trim any trailing newline from the
captured output before returning it as the resolved value.

A failure to resolve via `op` (a non-zero exit, a timeout, or empty captured
output) SHALL be treated identically to an unresolved source under the
process-environment resolver: the target environment variable SHALL be left
unset in the child process and the command build SHALL proceed. In addition to
the existing unresolved-source warning, the system SHALL log one supplementary
diagnostic line naming the source descriptor and the first line of `op`'s own
standard-error output, size-capped; this diagnostic SHALL NOT include the
resolved value, the expanded object reference, or the subprocess's standard
output under any circumstance, including on success.

#### Scenario: Resolved via op read

- **WHEN** the `op` resolver mode is active, the reference template is
  non-empty, the `op` command resolves, and `op read` for the substituted
  reference succeeds
- **THEN** the built command's environment contains the target variable set to
  the trimmed output of `op read --no-newline`

#### Scenario: Falls back to the environment resolver when unconfigured

- **WHEN** the `op` resolver mode is selected but the reference template is
  empty, or the configured `op` command does not resolve
- **THEN** the command builder resolves every credential-mapping source
  through the process-environment resolver instead, and logs a degrade notice

#### Scenario: A failed op read leaves the target unset without blocking the spawn

- **WHEN** `op read` for a substituted reference exits non-zero, times out, or
  returns empty output
- **THEN** the target environment variable is absent from the built command's
  environment, the existing unresolved-source warning is logged, an
  additional diagnostic line names the source descriptor and `op`'s
  size-capped stderr, and the command build still succeeds

#### Scenario: The op invocation never blocks on interactive input

- **WHEN** the op resolver invokes `op read`
- **THEN** the subprocess's standard input receives no data from the daemon

#### Scenario: No resolved value or reference ever appears in a log line

- **WHEN** the op resolver resolves a source, on either the success or failure
  path
- **THEN** no log line contains the resolved secret value, the subprocess's
  standard output, or the expanded object reference
