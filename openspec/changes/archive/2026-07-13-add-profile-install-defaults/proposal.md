# Proposal: Install default diligence profile seeds

## Why

`internal/profiles/load.go` only *reads* `~/.argus/profiles/` — it never populates it. On a fresh
install that directory doesn't exist, so `mcp__argus__profile_resolve` fails "not found" (a clean
fail-open by design) and every archetype-aware spawn silently loses its model tiering until an operator
manually copies the example TOML files into place. This was discovered live on Aaron's own dogfood
machine. The three example profiles already ship in the repo (`docs/profiles/*.toml`), but there is no
one-click way to get them onto a fresh machine, and a binary installed from a release tarball has no
`docs/profiles/` to copy from at all — the examples must be embedded in the binary itself.

## What Changes

- **Seed profiles become embeddable:** move `docs/profiles/{default,lean,customer_grade}.toml` to
  `internal/profiles/seeds/` and embed them via `go:embed`, so installing the seeds never depends on a
  git checkout being present (a release-binary install works identically to a from-source build). A test
  extracts the embedded bytes and runs them through the real `profiles.Validate` pipeline, so a future
  edit to a seed file that breaks validation fails the build's test suite, not a silent runtime surprise.
- **`profiles.InstallDefaults(profilesDir string)`:** writes each embedded seed into `profilesDir` that
  isn't already present at the destination. Never overwrites an existing file (an operator may have
  customized it) — a skipped file is reported, not silently ignored.
- **Settings-page action:** a new "Install Default Profiles" row under the Hera settings category
  (alongside the existing Hera enable/disable toggle — the natural home, since diligence profiles are the
  model-tiering mechanism Hera plan-DAG nodes consult) that runs the install and reports which profiles
  were installed vs. already present. Follows the same busy-state / async-result pattern as the existing
  "Update Argus" action (`OnUpdateArgus` → goroutine → `QueueUpdateDraw` → `SetUpdateResult`).
- **CLI mirror:** `argus profiles install-defaults`, consistent with the existing `argus validate`
  operator-tooling pattern (not wired into the Go build/CI/Make, per this repo's specs-are-local-docs
  policy).

**Explicitly out of scope** (confirmed with Aaron): no automatic seeding on daemon startup. This is a
user-triggered convenience only — the fail-open "not found" behavior is unchanged for an operator who
never triggers it.

## Capabilities

**Modified Capabilities**

- `diligence-profiles` — seed profiles move to an embeddable location and gain a programmatic install
  affordance (`InstallDefaults`) alongside the existing "documented examples, copy by hand" story.
- `settings-view` — new Hera-category action row for triggering the install.

## Impact

- **Code:** `internal/profiles/seeds/*.toml` (moved from `docs/profiles/`), `internal/profiles/seeds.go`
  (embed), `internal/profiles/install.go` (`InstallDefaults`), `cmd/argus/profiles.go` (CLI subcommand),
  `internal/tui/settings.go` (new row kind + detail pane), `internal/tui/app.go` (wiring).
- **Docs:** README's "Seed profiles" bullet (path + new install affordances), three `docs/profiles`
  path references in `context/knowledge/gotchas/misc.md`.
- **No schema change, no new DB columns.**
- **Branch:** lands on `argus/model-tiering` (never master), per this workstream's convention.
