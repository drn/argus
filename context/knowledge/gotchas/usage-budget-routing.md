# Usage budget routing gotchas

Budget-aware Hera worker routing is advisory steering, not enforcement.

- **Claude `/usage` is reachable from a headless PTY-backed Claude Code subprocess.** The daemon probe relies on the rendered "Current week (all models)" line and reset timestamp from that client-side screen, without creating an Argus task or spending model tokens.

- **The probe and parser fail open by design.** Probe failures, parse drift, missing cache, stale cache, invalid fallback backend, or unavailable config all leave the worker backend empty so existing project/default backend precedence applies.

- **The v1 switch is config.toml-only.** `[hera.worker_budget]` intentionally has no TUI, web, or macOS settings surface yet; this is the named Frontend Parity non-goal for `add-usage-budget-routing`, not an overlooked UI gap.
