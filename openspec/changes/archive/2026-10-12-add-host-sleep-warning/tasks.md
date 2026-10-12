## 1. Core

- [x] 1.1 `internal/hostsleep`: pure `Diagnose`, `ParseACSleep`, `ParseHeldBy`, `Check`, async cached `Monitor`.
- [x] 1.2 `db.ReadConfigValueReadOnly` so doctor reads `api.enabled` without mutating the DB.

## 2. Surfaces

- [x] 2.1 `argus doctor` Host sleep block.
- [x] 2.2 `/api/system-metrics` `host_sleep` field.
- [x] 2.3 TUI Settings Remote API warning + rail suffix.
- [x] 2.4 Web Settings banner (bump `SW_VERSION`) + Playwright spec.
- [x] 2.5 macOS System tab banner + decode tests.

## 3. Docs & tests

- [x] 3.1 Unit tests for every layer; gotcha bullet; README (doctor + REST rows).
- [x] 3.2 Archive this change into `openspec/specs/` before merge.
