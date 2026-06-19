## CI Gate Troubleshooting (`make pre-pr`)

`make pre-pr` mirrors `.github/workflows/ci.yml` step-for-step (build → vet → fmt-check → lint-pr → vuln → test-cover-gate). Steps run in sequence and the first failure short-circuits the rest, so a formatting miss hides downstream test/lint/coverage failures until fixed — always run the **full** `pre-pr` locally to surface everything in one pass. Per-gate failure recipes:

- **`fmt-check`** — run `make fmt` first; goimports rewrites the tree. An unformatted file is the most common CI miss.
- **`test-cover-gate`** — filtered coverage dropped below the 88% floor. Add tests for code you touched (or under-covered platform-agnostic code) until it clears; the floor ratchets up, never down. Do NOT lower `-min`. Filtered coverage drifts ~0.2% darwin↔linux from platform branches (e.g. `internal/agent/sandbox.go`) — target platform-agnostic code for reliable margin.
- **`lint-pr`** — uses `--new-from-rev=origin/master`, so it only flags issues your diff introduced (incl. `staticcheck` deprecations like `SA1019` on new lines). Fix them; no blanket `//nolint`.
- **`vuln`** — CI runs govulncheck with `continue-on-error: true`, so Go stdlib-only CVEs (`Found in: <pkg>@go1.x.y`, "Standard library") never block CI (fixable only by bumping the toolchain). Confirm the failure is toolchain-only (fails on a clean `origin/master` tree too), note it in the PR, run the remaining gates individually. Module-level findings in bumpable deps still must be fixed.
