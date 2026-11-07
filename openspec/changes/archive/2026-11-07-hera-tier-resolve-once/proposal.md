## Why

Building the Hera sidebar model costs ~40ms on real data even after the index fix, and ~27ms of that is the diligence-tier readout (`resolveHeraTier`), which runs once per role. For every role it re-reads settings (`db.Config()`, ~59µs on a copy of the real 56MB database), stats the profile directories, decodes the same profile TOML file, and then decodes it a second time inside `agent.ResolveModel`. The result is identical for every role in the same project, so the work is repeated for nothing. It runs on the background build goroutine and on the synchronous forced refresh that fires when the Projects tab opens, where it shows up as a visible delay.

## What Changes

- The tier readout resolves once per model build instead of once per role: one config snapshot per build, and profile validation and model resolution memoized for the duration of that build.
- New `HeraPage.SetTierResolverFactory` seam: the page calls the factory once per build and annotates every role with the returned closure. `SetTierResolver` stays for existing callers and tests.
- No cache survives a build. Settings, project bindings and profile files are re-read on the next build exactly as today, so there is nothing to invalidate and no staleness window.
- One `uxlog` line per build reports memo hits and misses.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `hera-view`: the plan view's archetype/model readout is computed once per model build rather than once per role, with identical output.

## Impact

- Code: `internal/tui/hera_tiering.go`, `internal/tui/hera/page.go` (factory seam, ~6 lines), `internal/tui/app.go` (wiring). The `agent` package and the daemon spawn path are untouched.
- No REST/wire/keymap change; frontend parity: TUI-only readout, the web and macOS clients do not render it.
- Known, unchanged limitation: edits to `config.toml` or a profile file do not bump the DB `data_version` rebuild gate, so they appear on the next rebuild triggered by any other change or by opening the tab (forced refresh), as before.
