## Why

Pressing `d` on a backend row in Settings updates the in-memory `defaultBackend`
shown in the UI, but `handleSetDefault` persists it under the DB config key
`default_backend`. `db.Config()` (the actual read path used to build
`cfg.Defaults.Backend` on every reload, including at startup and when spawning
a new task) only recognizes the key `defaults.backend`. The write and the read
use different keys, so the choice is silently discarded: it never survives a
restart and never affects task creation, which reads `cfg.Defaults.Backend`
directly. The remote `apistore.Store.SetConfigValue` already tolerates both
spellings, masking the mismatch there, but the local (and primary) path does
not.

## What Changes

- Persist the selected default backend under the same key `db.Config()` reads
  (`defaults.backend`), so the choice actually takes effect immediately and
  survives restart.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `settings-view`: Clarify that setting the default backend persists under the
  `defaults.backend` config key and is reflected the next time config is read.

## Impact

`internal/tui/settings.go` (`handleSetDefault`), its regression test.
