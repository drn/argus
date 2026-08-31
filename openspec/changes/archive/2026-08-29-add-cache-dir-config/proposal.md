## Why

A build/mobile skill run inside an Argus sandbox (e.g. a repo's `/build-android` /
`/build-ios` skills) re-provisions multi-GB toolchain state from scratch in every
disposable `git worktree` — a fresh Android SDK+NDK download, a fresh CocoaPods
Specs repo clone, a fresh Yarn/npm cache — because `git worktree add` only
materializes tracked files, never gitignored machine state, and nothing shares
these directories across the many concurrent worktrees Argus creates for one
project. This was investigated and handed off from a live Argus task running
against the `breact` repo (see `context/knowledge/gotchas/worktree.md` and the
handoff note in the KB for the full investigation).

Argus already solves exactly this class of problem for two specific tools:
`BuildCmd` unconditionally forces `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` onto
every spawned agent, redirecting them from the tool's own per-worktree-relative
default into a stable, shared `~/.argus/cache/<name>` directory (see the
"Forced build/test cache environment redirect" requirement in
`agent-execution`). That fix is hardcoded to those two tools. This change
generalizes the same mechanism into an opt-in, project-configurable mapping so
any other toolchain a project depends on can be redirected the same way,
without a Go code change per tool.

Secret injection for the same class of build (e.g. Fastlane's `fastlane/.env`
credentials) is explicitly OUT OF SCOPE here — Argus already has a general
solution (`backend.EnvVars` + the secrets-resolution registry, see
`secrets-resolution`); a project just needs to configure it. Likewise,
resource-locking for exclusive hardware (one physical device, one Android
emulator) and any interactive-credential-prompt or physical-device-trust step
are explicitly OUT OF SCOPE: those steps require a human present at the moment
they fire, which an Argus sandbox cannot guarantee, so the correct behavior for
a skill encountering one is to fail fast, not be worked around. That
sandbox-residency detection (`ARGUS_TASK_ID` / the worktree path convention) is
a pre-existing, unconditionally-exported signal already used by Argus's own
builtin routing content — this change does not alter it, only documents it in
`README.md` as a contract any third-party skill can rely on. That's a docs-only
change and isn't specified here.

## What Changes

- `config.Config` gains a top-level `CacheDirs map[string]string` field
  (`cache_dirs` in `config.toml`): target environment-variable name → a
  subdirectory name created under `~/.argus/cache/`.
- `config.Project` gains a `CacheDirs map[string]string` field
  (`[projects.<name>.cache_dirs]`): merged on top of the global map for tasks
  in that project — a shared key is overridden, a new key is added.
- `agent.ResolveCacheDirs(task, cfg)` returns the effective merged mapping for
  a task, mirroring the existing `ResolveSandboxConfig` merge shape.
- `agent.BuildCmd` creates each resolved subdirectory (`os.MkdirAll`) and
  exports `TARGET=<dir>` on the spawned agent's environment, for every entry
  `ResolveCacheDirs` returns. An entry with an empty or `=`-containing target,
  or a subdir that is absolute or escapes the cache root via `..`, is skipped
  with a log line rather than failing the spawn.
- This is purely additive and opt-out-by-default: an empty/absent
  `cache_dirs` config changes nothing about today's behavior. The existing
  hardcoded `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` force is unchanged and
  unaffected.

## Capabilities

### Modified Capabilities

- `agent-execution`: adds a requirement — "Configurable shared cache directory
  redirection" — alongside the existing forced `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH`
  requirement, covering the new opt-in, project-configurable `cache_dirs`
  mechanism.

## Impact

- `internal/config/config.go` — two new map fields (`Config.CacheDirs`,
  `Project.CacheDirs`), both nil-safe zero values, no migration needed (no DB
  column; config.toml only, matching the existing `Backend.Models` precedent).
- `internal/agent/agent.go` — new `ResolveCacheDirs` + `isValidCacheSubdir`
  helpers, and an additive loop in `BuildCmd` right after the existing
  GOCACHE/PLAYWRIGHT_BROWSERS_PATH force.
- Test coverage: new `ResolveCacheDirs` merge tests (mirroring the
  `ResolveSandboxConfig` test suite) and new `BuildCmd` env-export /
  invalid-entry tests (mirroring `TestBuildCmd_RedirectsBuildCaches`).
- Docs: `README.md` gains a `[cache_dirs]` config reference section (mirroring
  the existing `[sandbox]` section) and a `[projects.<name>.cache_dirs]` note
  under `[projects.<name>]`; `context/knowledge/gotchas/worktree.md` gains a
  bullet.
- No API, schema, or DB surface changes. No impact on remote/web/macOS clients
  (this is purely a spawn-time env detail, not REST-exposed) — same shape as
  the precedent GOCACHE/PLAYWRIGHT_BROWSERS_PATH change.
