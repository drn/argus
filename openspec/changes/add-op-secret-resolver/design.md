## Context

`agent.BuildCmd` (`internal/agent/agent.go:852-869`) already has everything a
new resolver needs to plug into: a per-backend `EnvVars` mapping (target env
var -> opaque source descriptor, `internal/config/config.go:183-191`), a
credential-merge loop that resolves each source and appends `TARGET=value` to
the child env, and a package-level `SecretResolver` seam
(`internal/agent/secret.go`) defaulting to `envSecretResolver` =
`os.LookupEnv`. That seam and its doc comments already forward-reference an
`op`/1Password resolver as the intended production path. This change builds
that resolver and the config surface that selects it — nothing in `BuildCmd`'s
existing contract (mapping-only, never a value; unresolved = warn + leave
unset; resolver pluggable) changes.

`BuildCmd(task *model.Task, cfg config.Config, resume bool)` already takes the
**live, per-spawn** effective config as a plain parameter — `Runner.Start`
receives `cfg` from its caller, which re-derives it via
`FileLoader.Apply(base)` (mtime-cached, re-read on change) close to spawn
time. So a `[secrets]` field on `config.Config` is naturally "live" the same
way every other config.toml override already is: no daemon-reload plumbing,
no cache invalidation, no restart. This directly shapes D-LIVE below.

## Goals / Non-Goals

**Goals:**

- A second resolver mode, `"op"`, selectable via a single global
  `[secrets]` config table (config.toml-only), that resolves an `EnvVars`
  source by shelling out to `op read`.
- The 1Password object-reference *format* is entirely user-configured — no
  vault, account, or item name is hardcoded anywhere in argus, defaults, or
  seed config.
- Zero behavior change for any install that never touches `[secrets]`:
  `os.LookupEnv` stays the default, exactly as shipped in
  `add-foreign-backend-envmap`.
- Graceful degrade to the `"env"` resolver when `op` mode is selected but not
  actually usable (no template configured, `op` not resolvable) — opting in
  is never a way to break existing credential mappings.
- A per-source `op read` failure (auth, item-not-found, timeout, ...) is
  treated identically to today's unresolved-source path: target left unset,
  non-sensitive warning, agent spawn proceeds. See D-FAIL.
- Documented precondition: `op` itself must already be able to authenticate
  non-interactively from the daemon's own process environment. Argus does not
  provide, source, or manage that credential.

**Non-Goals:**

- **Bootstrapping the daemon's own process environment for `op` to
  authenticate.** No `OP_SERVICE_ACCOUNT_TOKEN`-shaped variable (or
  equivalent) is exported, read from a file, or otherwise managed by argus.
  This is one-time, host/OS-specific operator setup — launchd
  `EnvironmentVariables` (macOS), systemd `Environment=`/`EnvironmentFile=`
  (Linux), a container's env injection, or an interactive shell that already
  exports it — identical in shape to the *already-documented* problem that a
  launchd-started daemon's minimal environment doesn't carry `HERA_OPENAI`
  either (`context/knowledge/gotchas/misc.md` "Backend credential env mapping
  (env_vars)"). Every operator running ANY background daemon with ANY secrets
  backend hits this; it is not solvable in argus config, so argus's contract
  is simply: assume `op` can already authenticate, document the assumption,
  stop there.
- **No per-backend resolver override.** The resolver *mode* is one global
  daemon-level choice ("how does this daemon fetch secrets"), not a per
  `EnvVars` mapping or per-backend setting. See D-SCOPE.
- **No Settings UI, no DB column, no `argus doctor` check.** `[secrets]` is
  config.toml-only, like `[keybindings]`. A doctor advisory check (mirroring
  the existing diligence-profile-library check) and a Settings toggle are
  named, reasonable follow-ups, not built here — see Open Questions.
- **No change to the `EnvVars` mapping shape or the `SecretResolver` function
  signature.** The mapping stays target -> opaque source descriptor; the
  resolver stays `func(source string) (value string, ok bool)`.
- **No retry/backoff on a failed `op read`.** One attempt per spawn, bounded
  by a timeout; a transient failure is indistinguishable from a permanent one
  at this layer, matching the existing fail-open contract (see D-FAIL).

## Decisions

### D-SCOPE: resolver mode is global, not per-backend

The task brief floated either scope ("a `secret_resolver` setting scoped
per-backend or globally"). Going global:

- The `EnvVars` mapping already gives per-backend, per-target granularity
  (which var, from which source, for which backend). What's missing is
  purely *how a source string becomes a value* — a deployment-level fact
  ("this daemon has `op` configured this way"), not a per-backend fact. Two
  backends both using the op resolver would want the identical
  `reference_template`/`command`/`timeout`; a per-backend copy would just be
  the same three fields duplicated N times.
- A global switch also composes cleanly with D-LIVE: `cfg.Secrets` is read
  once per `BuildCmd` call regardless of how many `EnvVars` entries that
  backend carries.
- If a real need for mixed resolvers-per-backend ever shows up, it is an
  additive change (an optional per-backend override field consulted before
  the global default) — not a reason to complicate this change now.

### D-TEMPLATE: `reference_template` with a `{source}` substitution token, not a raw `op://` URI in `EnvVars`

Two ways to let the op resolver find the right 1Password item:

1. **(chosen) A global reference *template*** (e.g.
   `op://<vault>/<item>/{source}`) with the literal token `{source}`
   substituted with the `EnvVars` mapping's existing source descriptor (e.g.
   `HERA_OPENAI`) at resolve time.
2. **(rejected) Put the full `op://vault/item/field` reference directly in
   each `EnvVars` source value**, and have the resolver mode determine how
   that string is interpreted.

(1) wins because it keeps `EnvVars.source` a resolver-agnostic opaque
descriptor, exactly as `agent-execution`'s existing requirement already
states ("a source descriptor resolved at spawn time" — no resolver-specific
format baked in). Switching an install from `"env"` to `"op"` then requires
touching only the new `[secrets]` table, not rewriting every backend's
`EnvVars` mapping. (2) would work but couples the mapping's *content* to
whichever resolver happens to be active, so flipping resolvers becomes a
two-place edit and the mapping stops being the portable "mapping only" record
the base spec already promises.

### D-CONFIG-DEFAULT: no default `reference_template`, ever

`reference_template` defaults to the empty string. There is no
"example-looking" default (not even something like
`op://Shared/argus/{source}`) because a non-obviously-fake default is exactly
the kind of thing that gets used by accident, or worse, treated as
documentation of Argus's own opinion about vault layout. An empty template is
unambiguous: op mode is not actually configured, so D-DEGRADE applies. Every
user's 1Password vault/account/item structure is different (or nonexistent);
this field only ever holds an operator's own choice.

### D-DEGRADE: falling back to the env resolver, computed fresh per spawn

`resolver = "op"` is not, by itself, sufficient to activate the op resolver.
Resolver selection (a new small function in `internal/agent/secret.go`,
consulted from `BuildCmd` in place of today's direct reference to the package
`secretResolver` variable) checks, in order:

1. Is `cfg.Secrets.Resolver` (case-insensitively) `"op"`? If not — unset,
   `"env"`, or any unrecognized string — use the existing package-level
   `secretResolver` (today's default env resolver, and still the
   `SetSecretResolver` test seam). An unrecognized value is logged once (per
   spawn, not per `EnvVars` entry) as a fail-open warning naming the bad
   value, never a hard error.
2. Is `cfg.Secrets.Op.ReferenceTemplate` non-empty? If empty, log a fail-open
   degrade notice and use the env resolver.
3. Is `cfg.Secrets.Op.Command` (default `"op"`) resolvable — an absolute path
   that exists, or a bare name found via `exec.LookPath`? If not, log a
   fail-open degrade notice (naming the command, not any secret) and use the
   env resolver.
4. Otherwise, build and return the op resolver closure (D-OP-INVOCATION).

This whole check re-runs on every `BuildCmd` call using whatever `cfg` that
call received — no caching, no startup-time resolver installation, no reload
hook. See D-LIVE for why that's deliberate and why it's cheap enough to not
matter.

### D-OP-INVOCATION: `op read --no-newline <ref>`, bounded timeout, never touches stdin

The op resolver closure, for a given `source`:

```go
ref := strings.ReplaceAll(cfg.Op.ReferenceTemplate, "{source}", source)
ctx, cancel := context.WithTimeout(context.Background(), timeout)
defer cancel()
cmd := exec.CommandContext(ctx, cfg.Op.Command, "read", "--no-newline", ref)
// cmd.Stdin left nil: op must never block waiting on interactive input.
```

- **`--no-newline`** is a real `op` CLI flag specifically for this case: a
  plain `op read` appends a trailing `\n` to its stdout, which would
  otherwise silently ride along inside the resolved credential (a classic,
  hard-to-notice footgun for anything that becomes an HTTP header value).
  Belt-and-suspenders, the resolver also `strings.TrimRight(out, "\n")`s the
  captured stdout before returning it.
- **Timeout** (`cfg.Secrets.Op.TimeoutSeconds`, default 5 when zero/unset)
  bounds every single call. `BuildCmd` runs off the UI thread already (agent
  spawn is a background-goroutine operation per this repo's UI-threading
  rules), but an unbounded `op` invocation could still stall a spawn
  indefinitely if the CLI hangs waiting on, e.g., a biometric unlock prompt
  with no attached TTY to satisfy it. `cmd.Stdin` is deliberately left `nil`
  (not inherited) so `op` cannot block on stdin either; the timeout is the
  backstop for everything else (network hang against 1Password's servers,
  etc.).
- **Only stderr is read on failure**, and only its first line, capped (e.g.
  200 bytes) — see D-FAIL for why and what it's used for. **Stdout is never
  logged**, on success or failure.

### D-FAIL: an `op read` failure is treated exactly like today's unresolved-source case — not "fail louder"

The task brief asks explicitly whether the new resolver should match
`BuildCmd`'s existing "unresolved = warn + leave unset, spawn proceeds"
contract or fail louder (e.g. block the spawn, surface a blocking error). **It
matches the existing contract, with one small, safe addition:**

- Any `op read` failure — non-zero exit, timeout, empty output, `op` binary
  disappearing between D-DEGRADE's availability check and the actual call —
  resolves as `("", false)`, exactly like an `os.LookupEnv` miss. `BuildCmd`'s
  existing per-target warning (`"credential source %q did not resolve; %q
  left unset"`) fires unchanged; the agent still spawns.
- The op resolver additionally logs one extra diagnostic line of its own —
  the source descriptor plus the first line of `op`'s stderr, capped — before
  returning `false`. This is strictly additive to `BuildCmd`'s existing
  warning, not a replacement for it, and structurally cannot contain the
  secret value (the read failed to produce one). It never includes the
  expanded `op://...` reference (vault/item names), only the same `source`
  descriptor `BuildCmd` already logs today — no new information about the
  operator's vault layout leaves the log line.

**Why not fail louder** (block the spawn, surface a modal, refuse to start
the agent): this credential mapping is explicitly a *convenience* feature —
the backend CLI itself still runs and will surface its own, far more specific
auth error in-context (in the agent's own pane) if it genuinely needed the
credential. Turning a missed credential injection into an argus-level hard
failure would:

- Make an opt-in convenience feature into a hard dependency on 1Password
  being perfectly configured, for every task spawn, even for backends that
  don't use the op resolver's mapping at all.
- Break the resolver-pluggability the base spec already guarantees: multiple
  resolvers with wildly different failure characteristics (a `LookupEnv` miss
  vs. an `op` auth failure vs. a hung CLI) would all need to satisfy one
  uniform "safe to fail" contract anyway, or `BuildCmd`'s single call site
  would need per-resolver special-casing. Fail-open for every resolver keeps
  that one call site untouched.
- Regress the one thing `add-foreign-backend-envmap` already got right:
  credential injection is best-effort scaffolding around a spawn that must
  still succeed.

### D-LIVE: config-driven, re-evaluated per spawn — no `SetSecretResolver` reload wiring

`envSecretResolver`'s original doc comment framed the future production
resolver as something wired in "via `SetSecretResolver`, with NO change to
`BuildCmd`." This change does touch `BuildCmd` (D-DEGRADE's resolver-selection
call replaces the bare reference to the package `secretResolver` variable) —
a deliberate, small deviation from that comment's letter, kept in its spirit:

- `SetSecretResolver` is untouched and still the seam tests use to inject a
  fake resolver, and still exactly what's consulted for the `"env"` (default)
  path.
- The reason to deviate: `[secrets]` is *config*, and `BuildCmd` already
  receives the live, per-spawn `cfg` as a parameter (Context above). Reading
  `cfg.Secrets` directly inside `BuildCmd` means an operator's config.toml
  edit (flipping `resolver`, fixing a typo'd `reference_template`) takes
  effect on the *next spawn*, with no daemon restart and no separate
  "reload the resolver" step to remember — it rides the exact same
  "config.toml edits are picked up live on the next read" behavior every
  other config.toml override in this repo already has.
- The alternative — install a `SetSecretResolver`-wired op resolver once at
  daemon startup (and again on every config-reload tick) — would need new
  reload-hook plumbing to stay live, duplicate the same `cfg.Secrets`-reading
  logic at a different layer, and reintroduce exactly the kind of
  startup-vs-running-config divergence this repo's `binary-coherence` /
  go-install-skew gotchas already warn about elsewhere. Reading `cfg.Secrets`
  fresh inside `BuildCmd` avoids all of that for the cost of one small,
  clearly-named helper function.

### D-CONFIG-SURFACE: config.toml-only, no DB column, no Settings UI

`[secrets]` follows the `[keybindings]` precedent (`context/knowledge/index.md`
lists it as "config.toml-only, no DB rows"), not the `EnvVars`/`[hera]`
precedent (DB-backed with a config.toml overlay):

- This is a small, deployment-level, advanced/operator-facing toggle — the
  same character as `[supervisor]`/`[sandbox]` knobs, not a per-project or
  per-task setting a day-to-day Settings-menu user would reach for.
- No new schema migration, no new Settings-pane category, no new REST
  endpoint. Smaller surface area for a feature whose entire audience is "an
  operator who already knows what `op` is and already runs it."
- Named follow-ups (not silently dropped, see Open Questions): a
  `[secrets]`-aware `argus doctor` advisory check (mirroring the existing
  diligence-profile-library check's FOUND/NONE FOUND/UNKNOWN shape) and,
  if it turns out to be wanted, a read-only Settings row surfacing which
  resolver is currently active.

## Data model changes

None. `SecretsConfig`/`OpResolverConfig` are new `config.Config` fields
(config.toml-only), not DB columns. No migration.

```go
// internal/config/config.go — additive, alongside the existing Backend.EnvVars.

// SecretsConfig selects and configures the secret-resolver mode consulted by
// agent.BuildCmd when resolving a backend's EnvVars credential mapping.
// config.toml-only (no DB table, no Settings UI) — mirrors Keybindings.
type SecretsConfig struct {
	// Resolver selects the resolution strategy: "env" (default, or any
	// unset/unrecognized value) reads the daemon's own process environment
	// via os.LookupEnv — today's only behavior. "op" additionally resolves
	// via the 1Password CLI (`op read`), built from Op below. An
	// unrecognized value fails open to "env" with a logged warning.
	Resolver string           `toml:"resolver"`
	Op       OpResolverConfig `toml:"op"`
}

// OpResolverConfig configures the op ("op" mode) resolver. Every field here
// is the OPERATOR'S OWN 1Password layout — argus hardcodes no vault, account,
// or item name, and ships no default reference_template.
type OpResolverConfig struct {
	// ReferenceTemplate builds the `op read` object reference for each
	// EnvVars source descriptor: the literal token "{source}" is substituted
	// with the mapping's source string (e.g. "HERA_OPENAI"). Example:
	// "op://<vault>/<item>/{source}". Empty (the default) means op mode is
	// not actually configured — resolution degrades to the env resolver.
	ReferenceTemplate string `toml:"reference_template"`
	// Command is the `op` executable to invoke: an absolute path, or a bare
	// name resolved via PATH. Defaults to "op" when empty. Override with an
	// absolute path if the daemon runs under a minimal-PATH environment
	// (e.g. launchd) that doesn't expose "op" on PATH — the same constraint
	// already documented for HERA_OPENAI not reaching a launchd daemon's env.
	Command string `toml:"command"`
	// TimeoutSeconds bounds each `op read` invocation. Defaults to 5 when
	// zero/unset, so a hung or interactively-blocked op CLI cannot stall an
	// agent spawn indefinitely.
	TimeoutSeconds int `toml:"timeout_seconds"`
}
```

`DefaultConfig()` seeds `Secrets: SecretsConfig{Resolver: "env"}` (the `Op`
zero value; empty `ReferenceTemplate` is the "not configured" signal).

## Risks / Trade-offs

- **A misconfigured `reference_template` that happens to be non-empty but
  wrong** (e.g. references a real vault the operator doesn't have access to)
  is indistinguishable, at this layer, from "op isn't set up at all" — both
  resolve as a per-source failure via D-FAIL, not a distinct error. Accepted:
  distinguishing "wrong config" from "op declined for this item" would need
  parsing `op`'s specific error taxonomy, which is out of scope for a
  fail-open convenience resolver; the D-FAIL diagnostic line (source + `op`'s
  own stderr first line) is the debugging aid instead.
- **`op read` latency on the spawn path.** Every `EnvVars` entry resolved via
  the op resolver is one subprocess spawn + local 1Password CLI round-trip
  added to agent-start latency, bounded by the timeout. Accepted: this only
  applies to backends that actually carry an `EnvVars` mapping (today, just
  `codex`), and only when `[secrets] resolver = "op"` is explicitly set.
- **Concurrent hera worker fan-out** spawns many agents near-simultaneously,
  each potentially shelling to `op` independently with no shared lock or
  cache. Accepted: each `op read` is already a fast local CLI call in the
  success case, and 1Password's CLI is designed for concurrent invocation; a
  resolution cache is a possible future optimization, not a correctness
  requirement, and adding one now would be premature.
- **Daemon-env bootstrapping stays a manual, undocumented-in-code step** by
  design (the stated Non-Goal). Accepted risk: an operator can set `resolver
  = "op"` correctly and still get nothing but degrade-to-env or per-source
  failures if `op` itself was never given a way to authenticate — the
  precondition doc (README + gotcha note) is the mitigation, not code.

## Migration Plan

None required. `[secrets]` is a wholly new, optional config.toml table;
`DefaultConfig()`'s `Resolver: "env"` default is byte-identical to today's
only behavior. An existing `config.toml` with no `[secrets]` table parses
exactly as before (BurntSushi decode leaves an absent table as the Go zero
value, which resolves to the env resolver per D-DEGRADE step 1). Rollback is
deleting the `[secrets]` table (or setting `resolver = "env"`) — no data to
migrate back.

## Alternatives considered

- **Raw `op://...` URI directly in `EnvVars.source`** (rejected, D-TEMPLATE):
  couples the portable mapping to whichever resolver is active.
- **Per-backend resolver override** (deferred, not rejected outright,
  D-SCOPE): no concrete need yet; additive if one appears.
- **A default-but-obviously-fake `reference_template`** (rejected,
  D-CONFIG-DEFAULT): risks being used by accident or read as Argus's opinion
  on vault layout; an empty default is unambiguous.
- **Fail louder on `op read` failure** (rejected, D-FAIL): turns a
  convenience feature into a hard 1Password dependency and breaks the
  resolver-pluggability contract's uniform fail-open shape.
- **Install the resolver once via `SetSecretResolver` at daemon startup/on
  config-reload** (rejected, D-LIVE): needs new reload-hook plumbing and
  reintroduces a startup-vs-running-config divergence risk for no benefit
  over reading `cfg.Secrets` fresh inside `BuildCmd`, which is already living,
  per-spawn config.
- **DB-backed `[secrets]` with a Settings UI toggle** (deferred,
  D-CONFIG-SURFACE): bigger surface area than this operator-facing feature
  currently warrants; config.toml-only for now, named as an explicit
  follow-up rather than silently out of reach forever.

## Discovery findings

- `internal/agent/secret.go` is a tiny, self-contained file (51 lines) — the
  new resolver-selection function and the op-resolver implementation both fit
  here without disturbing `BuildCmd`'s existing shape beyond the one-line
  swap described in D-LIVE.
- `internal/config/config.go:169-192` (`Backend`) is the direct precedent for
  "holds a mapping/descriptor only, never a secret value" doc-comment
  language; `SecretsConfig`/`OpResolverConfig` reuse that framing verbatim
  where it applies.
- `Runner.Start(task, cfg, ...)` (`internal/agent/runner.go:78`) confirms
  `cfg` is already threaded fresh per spawn into `BuildCmd` — no new plumbing
  needed to make `[secrets]` "live" (D-LIVE).
- `context/knowledge/gotchas/misc.md` already documents the sibling
  launchd-minimal-env constraint for `HERA_OPENAI`; the op resolver's
  `Command` override field (absolute-path escape hatch) and the Non-Goal
  section both point back to that same documented constraint rather than
  re-solving it.
- `openspec/changes/archive/2026-06-29-add-foreign-backend-envmap/` is the
  direct predecessor change; its proposal.md's "Open design question" section
  is what this change resolves (option (b): "a future `op`-CLI resolver
  shelling to 1Password at spawn time").

## Acceptance criteria

**Config schema & defaults**

- it should default `Secrets.Resolver` to `"env"` when `[secrets]` is absent
  from config.toml
- it should parse an explicit `[secrets]` / `[secrets.op]` table from
  config.toml, overriding the default
- it should treat an empty or unrecognized `Resolver` value as `"env"`

**Resolver selection (D-DEGRADE)**

- it should use the env resolver when `Resolver` is `"env"`, empty, or
  unrecognized
- it should use the env resolver and log a degrade notice when `Resolver` is
  `"op"` but `Op.ReferenceTemplate` is empty
- it should use the env resolver and log a degrade notice when `Resolver` is
  `"op"` but `Op.Command` does not resolve (absolute path missing, or bare
  name not on PATH)
- it should build and use the op resolver when `Resolver` is `"op"`, the
  template is non-empty, and the command resolves
- it should re-evaluate resolver selection fresh on each `BuildCmd` call
  (config edits take effect on the next spawn, no restart)

**Op resolver invocation (D-OP-INVOCATION)**

- it should substitute the literal token `{source}` in `ReferenceTemplate`
  with the `EnvVars` mapping's source descriptor
- it should invoke `op read --no-newline <reference>` via the configured
  `Command`
- it should trim any trailing newline from the captured output defensively
- it should bound the invocation with `Op.TimeoutSeconds` (default 5 when
  zero/unset)
- it should never attach the daemon's stdin to the `op` subprocess

**Failure semantics (D-FAIL)**

- it should resolve as unresolved (target left unset, existing `BuildCmd`
  warning fires) on a non-zero `op` exit
- it should resolve as unresolved on a timeout
- it should resolve as unresolved on empty captured output
- it should log one additional diagnostic line naming the source descriptor
  and the first line of `op`'s stderr (size-capped), and never the resolved
  value, the full expanded reference, or stdout
- the secret value should never appear in any log line on the success path
  either

**Docs**

- README's Reference appendix documents `[secrets]` / `[secrets.op]` (keys,
  types, defaults, the `{source}` substitution rule, the daemon-env
  precondition)
- a gotcha note is added covering: config.toml-only scope, the fail-open
  degrade rule, `--no-newline`, the timeout rationale, and the explicit
  daemon-env-bootstrapping non-goal

## Open Questions

- Should a follow-up add a `[secrets]`-aware `argus doctor` advisory check
  (mirroring the existing diligence-profile-library FOUND/NONE
  FOUND/UNKNOWN shape)? Not blocking; named in Non-Goals as a reasonable,
  deferred addition.
- Should a follow-up surface the active resolver mode as a read-only
  Settings row? Not blocking; same deferred-not-dropped status.
- Is a 5-second default timeout right for `Op.TimeoutSeconds`? Reasonable
  starting point for a local CLI call; easy to revisit once there's real
  usage data, no structural reason it couldn't change later.
