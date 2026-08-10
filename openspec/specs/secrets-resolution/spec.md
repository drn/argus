# secrets-resolution Specification

## Purpose
TBD - created by archiving change add-secrets-resolver-registry. Update Purpose after archive.
## Requirements
### Requirement: Scheme-prefixed secret source descriptor dispatch

The system SHALL provide a resolver registry that dispatches a secret source
descriptor to a resolver function based on a `scheme://` prefix. A descriptor
with no `://` SHALL be treated as `env://<descriptor>` for full backward
compatibility with the existing bare-string format. The registry SHALL
support the `env`, `keychain`, and `op` schemes. A descriptor whose scheme is
not recognized SHALL fail to resolve (return not-ok) rather than error the
caller or crash the process.

#### Scenario: Bare string resolves as env

- **WHEN** a source descriptor contains no `://`
- **THEN** it is resolved as `env://<descriptor>` against the process
  environment

#### Scenario: Explicit env scheme

- **WHEN** a source descriptor is `env://SOME_VAR`
- **THEN** it resolves `SOME_VAR` from the process environment, identically
  to the bare-string form

#### Scenario: Unrecognized scheme fails closed

- **WHEN** a source descriptor names a scheme the registry does not
  recognize
- **THEN** the resolve fails (not-ok) rather than raising an error that
  stops the caller

### Requirement: Keychain resolver

The system SHALL resolve a `keychain://<service>` source descriptor by
running `security find-generic-password -s <service> -w` and, when a
`/<account>` suffix is present (`keychain://<service>/<account>`), by
running `security find-generic-password -s <service> -a <account> -w`
instead. A non-zero exit or empty output SHALL be treated as a failed
resolve.

#### Scenario: Service-only lookup

- **WHEN** a source descriptor is `keychain://<service>` with no account
  segment
- **THEN** the resolver runs `security find-generic-password -s <service>
  -w` and returns its output as the resolved value

#### Scenario: Service-and-account lookup

- **WHEN** a source descriptor is `keychain://<service>/<account>`
- **THEN** the resolver runs `security find-generic-password -s <service>
  -a <account> -w` and returns its output as the resolved value

#### Scenario: Missing Keychain item fails to resolve

- **WHEN** the `security` command exits non-zero or produces no output for
  the given service (and account, if present)
- **THEN** the source fails to resolve

### Requirement: op (1Password) resolver with self-referential bootstrap

The system SHALL resolve an `op://<vault>/<item>/<field>` source descriptor
by first resolving `[secrets.op].bootstrap_source` through the same
registry `Resolve` function, setting the result as
`[secrets.op].bootstrap_target` in the environment of a single `op read
op://<vault>/<item>/<field>` subprocess invocation only, then returning
that subprocess's output as the resolved value. The bootstrap credential
SHALL NOT be set on the calling process's own environment. When
`[secrets.op]` is absent or its bootstrap source fails to resolve, an
`op://` descriptor SHALL fail to resolve.

#### Scenario: Successful op read

- **WHEN** an `op://<vault>/<item>/<field>` source is resolved and
  `[secrets.op].bootstrap_source` itself resolves to a value
- **THEN** the system runs `op read op://<vault>/<item>/<field>` with
  `[secrets.op].bootstrap_target` set to the bootstrap value in that
  subprocess's environment only, and returns its output

#### Scenario: Bootstrap source is itself a registry resolve

- **WHEN** `[secrets.op].bootstrap_source` is
  `keychain://op-service-account-claude`
- **THEN** the system resolves it via the keychain resolver through the
  same `Resolve` function used for any other descriptor, with no separate
  code path

#### Scenario: Bootstrap credential scoped to the op subprocess only

- **WHEN** the op resolver runs `op read`
- **THEN** the resolved bootstrap credential is present only in that
  subprocess's `cmd.Env`, never set on the calling process's own
  environment via `os.Setenv`

#### Scenario: Missing bootstrap configuration fails the op resolve

- **WHEN** `[secrets.op]` is absent from configuration or its
  `bootstrap_source` fails to resolve
- **THEN** any `op://` source descriptor fails to resolve

### Requirement: Process-lifetime success-only memoization

The system SHALL memoize a successful resolve, keyed by the exact source
descriptor string, for the remaining lifetime of the process, so repeated
resolves of the same descriptor do not repeat the underlying subprocess
call. A failed resolve SHALL NOT be memoized, so a subsequent resolve
attempt for the same descriptor can succeed once the underlying condition
(e.g. a transient `op` CLI network blip) clears.

#### Scenario: Second resolve of the same descriptor is served from cache

- **WHEN** a source descriptor has already resolved successfully once in
  this process
- **THEN** a later resolve of the identical descriptor returns the cached
  value without re-invoking the resolver

#### Scenario: Failed resolve is retried, not poisoned

- **WHEN** a source descriptor fails to resolve
- **THEN** the failure is not cached, and a later resolve attempt for the
  same descriptor re-invokes the resolver

### Requirement: op bootstrap resolution status tri-state

The system SHALL expose a queryable tri-state status for the
`[secrets.op]` bootstrap source — RESOLVED, NOT RESOLVED, or NOT
CONFIGURED — computed by doing one resolve-and-discard of
`bootstrap_source` on demand, so other capabilities (`argus doctor`'s
diagnostics, the Settings System row) can surface it without each
re-implementing the resolve-and-classify logic. NOT CONFIGURED SHALL be
reported when `[secrets.op]` is absent or `bootstrap_source` is empty,
distinctly from a configured-but-failing source.

#### Scenario: Configured and resolving

- **WHEN** `[secrets.op].bootstrap_source` is set and resolves
  successfully
- **THEN** the status is RESOLVED

#### Scenario: Configured but failing

- **WHEN** `[secrets.op].bootstrap_source` is set but fails to resolve
- **THEN** the status is NOT RESOLVED

#### Scenario: Absent configuration

- **WHEN** `[secrets.op]` is absent from configuration or
  `bootstrap_source` is empty
- **THEN** the status is NOT CONFIGURED

