## Context

The ai-ron permission hook is being built against the fixed `ARGUS_SANDBOXED=1` contract. A worktree path and persisted sandbox preference cannot prove that a particular command was wrapped. The session supervisor owns command construction across daemon restarts.

## Goals and scope

**Goals:**

- Make the environment reflect successful sandbox wrapping for every backend and launch lifecycle.
- Prevent inherited environment values, backend mappings, and cache mappings from overriding the result.
- Advertise the changed spawn behavior to daemon/supervisor compatibility checks.

**Non-goals:**

- Change network policy, including `allow network*`, sandbox grants, or sandbox failure behavior.
- Add a setting or change REST, TUI, web, or macOS behavior. All three frontends share the same launch path, so no frontend follow-up is required.
- Merge or deploy the PR.

## Decisions

- Finalize the reserved variable at the end of `BuildCmd`, after other environment sources. Remove all existing entries, then append exactly `ARGUS_SANDBOXED=1` only when the sandbox cleanup handle proves wrapping succeeded. Using configuration or task metadata would incorrectly mark fallback launches.
- Keep the result backend-independent. Fresh starts, resume, and recycle already use `BuildCmd`; tests exercise both fresh and resumed construction plus repeated launches with changed overrides.
- Bump the supervisor spawn surface and refresh its source digest. Bump the protocol version as requested for this launch-contract change; the stream surface is unchanged because existing sessions are untouched.
- Log the final wrapped/unwrapped result without exposing environment contents.

## Risks / Trade-offs

- An old supervisor cannot emit the signal → compatibility metadata identifies the stale spawn surface; consumers require the explicit variable and therefore fail closed until upgraded.
- Environment variables are inherited by descendants and can be edited by a process → this is a launcher signal for trusted hooks, not cryptographic attestation.
