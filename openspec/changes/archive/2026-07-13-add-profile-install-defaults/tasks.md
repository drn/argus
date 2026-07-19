# Tasks: add-profile-install-defaults

**Branch target:** `argus/model-tiering` (never master).

## 1. Embeddable seeds + install function

- [x] 1.1 Move `docs/profiles/{default,lean,customer_grade}.toml` to `internal/profiles/seeds/`; update
  their "documented example, not auto-installed" header comments to describe the new install affordances
  (still not automatic on daemon startup).
- [x] 1.2 Add `internal/profiles/seeds.go`: `//go:embed seeds/*.toml` into a package-level `embed.FS`,
  plus `SeedNames = []string{"default", "lean", "customer_grade"}`.
- [x] 1.3 Repoint `internal/profiles/seeds_test.go` and `internal/review/seeds_test.go`'s `LibraryDir` at
  `internal/profiles/seeds` (was `../../docs/profiles`) — same content, same tests.
- [x] 1.4 Write failing tests for `InstallDefaults`: writes each embedded seed to an empty dir and
  reports `installed`; skips a pre-existing destination file and reports it in `skipped` WITHOUT
  modifying its bytes; each embedded seed independently passes `profiles.Validate` (extract embedded
  bytes to a temp dir, load+validate through the real `Loader` pipeline).
- [x] 1.5 Implement `internal/profiles/install.go`: `InstallDefaults(profilesDir string) (installed,
  skipped []string, err error)`, reusing the package's existing `fileExists` helper (no second path
  computation — callers still build `profilesDir` the canonical way,
  `filepath.Join(db.DataDir(), "profiles")`).

## 2. Settings-page action

**Depends on:** 1

- [x] 2.1 Write failing tests: the Hera category produces an install-profiles row; activating it (while
  not already in flight) fires the callback and marks busy; a second activation while busy is a no-op;
  `SetInstallProfilesResult` clears busy and records the result for the detail pane.
- [x] 2.2 Add `srInstallProfiles` row kind, wire it into `catHera`'s `rebuildRows`, `handleEnter`, and
  `renderRowDetail` (mirrors the existing `srUpdateArgus` busy/result pattern).
- [x] 2.3 Wire `SettingsView.OnInstallProfiles` in `internal/tui/app.go`: dispatch
  `profiles.InstallDefaults(filepath.Join(db.DataDir(), "profiles"))` off the UI thread, report back via
  `QueueUpdateDraw` + `SetInstallProfilesResult`.

## 3. CLI mirror (optional, cheap)

**Depends on:** 1

- [x] 3.1 Add `argus profiles install-defaults` in `cmd/argus/profiles.go`, mirroring `validate.go`'s
  pure-core/thin-wrapper split for testability; wire the `profiles` subcommand into `main.go`. Not wired
  into build/CI/Make.

## 4. Docs + spec archive

**Depends on:** 1, 2, 3

- [x] 4.1 Update README's seed-profiles bullet and the three `docs/profiles` path references in
  `context/knowledge/gotchas/misc.md`.
- [x] 4.2 `make pre-pr` green; bounded single-reviewer pass; archive this change on-branch (merge deltas
  into base specs, move the change folder under `openspec/changes/archive/`); push to
  `origin/argus/model-tiering`.
