## 1. Approval and scope

- [x] 1.1 Obtain approval: coordinator message 17669 authorizes opt-in unsigned agent commits and hermetic agent Git fixtures.
- [x] 1.2 Exclude iris authentication/polling requirements; iris PR #17 shipped separately. Preserve Argus's plugin deadline.

## 2. Implementation and focused verification

- [x] 2.1 Write failing tests for config overlay/default/invalid values and child-only signing behavior, including fresh/resume, environment precedence, and synthetic failing signer.
- [x] 2.2 Implement validated config.toml setting and deterministic child-only environment override with uxlog messages.
- [x] 2.3 Isolate agent test fixture Git configuration at suite startup without disrupting the fake-daemon helper.
- [x] 2.4 Verify no host config mutation and that explicit signing still wins; cover malformed config counts and preserved unrelated entries.
- [x] 2.5 Bump supervisor spawn surface/manifest/digest, document README Reference/gotchas, and name client controls follow-up.
- [x] 2.6 Run focused race tests and hera-review plus test-adversary review; resolve findings. Full package preflight exposed inherited CODEX_HOME contamination in existing session fixtures; isolate Codex home overrides at the normal agent test entrypoint as well.

## 3. Delivery

- [x] 3.1 Archive delta into base specs before merge; validate OpenSpec locally without adding build/CI hooks.
- [ ] 3.2 Request coordinator's host gate slot; run make pre-pr once, detached with nohup/exit marker and GOTOOLCHAIN=go1.26.10.
- [ ] 3.3 Commit with commit.gpgsign=false, push/open PR via iris, state signing stopgap in PR, and wait for CI.
- [ ] 3.4 Report PR number and exact opt-in config to coordinator; leave Aaron's settings untouched.
