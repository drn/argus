## Why

Resolving uncertain permission input is appropriate for displayed status but hides disagreement in diagnostic counters and is too permissive for delivery into a session. Review of #1074 requires preserving raw permission state for those two purposes.

## What changes

- Compare raw permission input in the separate shadow diagnostic bucket.
- Restore raw input gating for delivery and claim-settlement evidence.
- Document the risk of a permission dialog that terminal inference cannot recognise.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: distinguish displayed verdicts from raw diagnostic and delivery safety signals.

## Impact

Bridge shadow comparison, daemon delivery routing/evidence, and regression tests. Displayed status remains consistent across TUI, web, macOS and push. No new settings or wire fields. Direction approved in coordinator review #17290.
