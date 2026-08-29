## 1. Config

- [x] 1.1 Add `CacheDirs map[string]string` (`toml:"cache_dirs"`) to `config.Config`, doc-commented against the existing forced `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` redirect.
- [x] 1.2 Add `CacheDirs map[string]string` (`toml:"cache_dirs"`) to `config.Project`, doc-commented as merging on top of the global map.

## 2. Resolution + spawn

- [x] 2.1 Add `agent.ResolveCacheDirs(task, cfg) map[string]string` mirroring `ResolveSandboxConfig`'s merge shape (fresh map, no mutation of either input).
- [x] 2.2 Add `agent.isValidCacheSubdir(string) bool` rejecting empty, absolute, and `..`-escaping subdirectory values.
- [x] 2.3 In `BuildCmd`, after the existing GOCACHE/PLAYWRIGHT_BROWSERS_PATH force, loop over `ResolveCacheDirs` (sorted target order for determinism), skip-and-log invalid entries, `os.MkdirAll` each resolved directory, and append `TARGET=<dir>` to `cmd.Env`.

## 3. Tests

**Depends on:** Stage 2

- [x] 3.1 `ResolveCacheDirs` merge tests mirroring the `ResolveSandboxConfig` suite: global-only, project-overrides-and-adds, no-project-uses-global, does-not-mutate-global.
- [x] 3.2 `isValidCacheSubdir` table test covering empty/absolute/`.`/`..`/nested-escape/valid-nested cases.
- [x] 3.3 `TestBuildCmd_ExportsCacheDirs` (mirrors `TestBuildCmd_RedirectsBuildCaches`): configured entry appears in `cmd.Env` and its directory exists on disk.
- [x] 3.4 `TestBuildCmd_SkipsInvalidCacheDirsEntry`: a `..`-escaping entry is not exported and creates no directory outside the cache root.
- [x] 3.5 Run `go test ./internal/agent/... ./internal/config/...` and confirm everything passes.

## 4. Docs

**Depends on:** Stage 3

- [x] 4.1 Add a `[cache_dirs]` config reference section to `README.md` (mirroring `[sandbox]`) plus a `[projects.<name>.cache_dirs]` note under `[projects.<name>]`.
- [x] 4.2 Add a bullet to `context/knowledge/gotchas/worktree.md` describing the feature and why it exists (multi-GB toolchain re-provisioning per disposable worktree).
- [x] 4.3 Update `context/knowledge/index.md`'s `worktree.md` bullet count.
- [x] 4.4 Run `make pre-pr` and confirm it passes clean (or note any pre-existing unrelated failures, matching this repo's documented flake classes).

## 5. Archive

**Depends on:** Stage 4

- [x] 5.1 Run `openspec archive add-cache-dir-config` (or apply the merge-and-move by hand if the CLI is unavailable) before merge, per this repo's CLAUDE.md archiving requirement.
