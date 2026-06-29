# Add per-backend credential environment mapping

## Why

Argus dispatches every agent via `agent.BuildCmd`, which assembles the child
process environment by inheriting the daemon's raw environment plus the forced
terminal-capability vars and `ARGUS_TASK_ID`. There is no mechanism to give a
foreign backend the credentials it needs under a *different* env-var name than
the daemon happens to carry.

This blocks cross-vendor code review: a `codex` (OpenAI) reviewer needs
`OPENAI_API_KEY` in its child process, but the daemon environment should not be
required to already expose that exact name, and we must never store, log, or
commit the secret value.

We need a per-backend mapping that says "set the child's `OPENAI_API_KEY` from
the daemon-side source `HERA_OPENAI`" — a **mapping only**, never a value — and
a build-time merge step that resolves each source through a pluggable
secret-resolver seam and injects the resolved value into the child env. The
default resolver reads the daemon's own process environment; the seam keeps the
door open for a future 1Password/`op`-CLI resolver without touching `BuildCmd`.

Scope is `codex` only. Gemini is explicitly out of scope.

## What Changes

- **`config.Backend` gains an `EnvVars map[string]string`** field
  (TARGET_ENV_VAR -> SOURCE_DESCRIPTOR). It holds the mapping only — never a
  secret value.
- **The `backends` table gains a JSON `env_vars` column** (idempotent
  `ALTER TABLE`; read/write in `internal/db/backends.go`).
- **`BuildCmd` gains a credential-merge loop** after the existing env-append
  block: for each (target, source) it resolves the source via a secret-resolver
  seam (`func(source string) (value string, ok bool)`) and, if resolved,
  appends `target=value` to `cmd.Env`. An unresolved source sets nothing and
  logs a non-sensitive warning naming only the var (never the value).
- **The default resolver reads the daemon's own process environment**
  (`os.LookupEnv` of the source name). The resolver is pluggable via an
  exported setter so a future `op`-CLI / 1Password resolver can replace it
  without editing `BuildCmd`.
- **The `codex` default backend row is seeded with**
  `{"OPENAI_API_KEY": "HERA_OPENAI"}` (mapping only). No gemini row is added.

## Operator setup (documentation)

For a `codex` reviewer to actually receive a key, the **daemon's own
environment** must carry the source variable (default `HERA_OPENAI`). The
default resolver is `os.LookupEnv(source)`.

**Important caveat — the launchd deployment resolves nothing by default.** The
argus daemon is typically launched by launchd with a minimal environment that
will **NOT** contain `HERA_OPENAI` (the same minimal-env constraint that once
stripped `TERM` and made agents render colorless). In that deployment the
default `os.LookupEnv` resolver finds nothing, so no key is injected and the
mapping is logged as unresolved. **Cross-vendor review works today only when the
daemon is started from an environment that already carries `HERA_OPENAI`** (for
example, launched from an interactive shell that exported it). Wiring launchd to
carry the source is explicitly out of scope here.

**Intended production path (deferred follow-up).** The production resolver is an
`op`/1Password-CLI resolver that shells out at spawn time — e.g.
`op read op://claude/shell-env/HERA_OPENAI` — and drops into the existing
`agent.SetSecretResolver` seam **without touching `BuildCmd`**. The actual
production resolver, and how the daemon authenticates to 1Password without a
plaintext key in any plist or file (Aaron's secret/launchd infra + org policy),
is a SEPARATE story, out of scope for this change. This PR ships the seam and
the `os.LookupEnv` default only.

## Open design question (sent to the coordinator)

The default `os.LookupEnv` resolver may find nothing at runtime because the
launchd-started daemon's environment likely lacks `HERA_OPENAI`. The plumbing +
seam are built regardless; only the choice of *which resolver is wired as the
default* and the operator docs depend on the answer:

- (a) accept that the daemon env must carry the source (document the setup;
  launchd wiring out of scope), or
- (b) a future `op`-CLI resolver shelling to 1Password at spawn time, or
- (c) something else.

This does not block the mechanism — `BuildCmd` plumbing and the resolver seam
ship either way.

## Impact

- Affected specs: `agent-execution` (env injection), `llm-backends` (backend
  definition carries the mapping).
- Affected code: `internal/config/config.go`, `internal/db/schema.go`,
  `internal/db/backends.go`, `internal/db/migrate.go`, `internal/agent/agent.go`.
- No secret value ever enters the DB, logs, test fixtures, or git — only the
  mapping (target -> source descriptor) is persisted.
