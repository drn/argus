# Tasks: add-foreign-backend-envmap

## 1. Config

- [ ] 1.1 Add `EnvVars map[string]string` (toml `env_vars`) to `config.Backend`
      in `internal/config/config.go` with a doc comment stating it holds the
      mapping only, never a secret value.
- [ ] 1.2 Seed `EnvVars: {"OPENAI_API_KEY": "HERA_OPENAI"}` on the `codex`
      default backend in `DefaultConfig()`. Do NOT add a gemini row.

## 2. Persistence (DB)

- [ ] 2.1 Add `env_vars TEXT NOT NULL DEFAULT ''` to the `backends` CREATE TABLE
      and an idempotent `ALTER TABLE backends ADD COLUMN env_vars ...` in
      `internal/db/schema.go`.
- [ ] 2.2 Read `env_vars` in `DB.Backends()` (JSON-unmarshal into `EnvVars`),
      mirroring the existing care that the SELECT omits `resume_command`.
- [ ] 2.3 Write `env_vars` in `DB.SetBackend()` (JSON-marshal `EnvVars`).
- [ ] 2.4 Seed/propagate `env_vars` in `seedDefaults` (INSERT) and
      `fixupBackends` (fill when the existing row's mapping is empty; never
      clobber a user-customized mapping). Raw-SQL path, mirroring existing code.

## 3. Build-time injection + resolver seam (agent)

- [ ] 3.1 Add a `SecretResolver func(source string) (value string, ok bool)`
      type, a package-level default resolver backed by `os.LookupEnv`, and an
      exported `SetSecretResolver` so the default can be replaced without
      touching `BuildCmd`.
- [ ] 3.2 In `BuildCmd`, after the `ARGUS_TASK_ID` append, add a merge loop over
      `backend.EnvVars`: resolve each source; on success append `target=value`;
      on failure skip and `uxlog.Log` a non-sensitive warning naming only the
      var (never the value).

## 4. Tests (TDD — write first)

- [ ] 4.1 `BuildCmd` sets the target var from a resolved source.
- [ ] 4.2 `BuildCmd` with an unresolved source: target var NOT set + warning
      logged WITHOUT the value.
- [ ] 4.3 The secret value never appears in any log line.
- [ ] 4.4 The secret value never appears in a persisted backend row (DB
      round-trip stores only the mapping).
- [ ] 4.5 DB round-trip: `SetBackend`/`Backends` preserves `EnvVars`.
- [ ] 4.6 `seedDefaults`/`fixupBackends` seed the codex mapping; fixup does not
      clobber a customized mapping.
- [ ] 4.7 Resolver seam: installing an alternate resolver changes resolution
      without editing `BuildCmd`.

## 5. Docs + archive

- [ ] 5.1 Document operator setup (how the daemon obtains the source var) in the
      proposal / a short note.
- [ ] 5.2 Add a gotcha note (secret never persisted/logged; mapping-only).
- [ ] 5.3 `make pre-pr` clean.
- [ ] 5.4 Archive the change in this same PR (merge deltas into base specs, move
      change folder to `openspec/changes/archive/`).
- [ ] 5.5 Open PR via `iris_gh_pr_create`.
