## Context

Read-only investigation on 2026-10-25 confirmed both worker and canonical Argus Git configuration inherit `commit.gpgsign=true`, `gpg.format=ssh`, and 1Password's `op-ssh-sign` from global config. Worker commits execute within the agent process tree. SSH selects the 1Password IdentityAgent independently of the service-account bootstrap exported into spawned sessions; changing Git authentication cannot fix signing.

Push/fetch run in host iris, not the worker. Argus and iris have 30-second request limits around iris's detached 300-second transfers. Coordinator message 17669 confirms the independent iris polling/authentication fix shipped in PR #17. No Argus timeout change is needed. Host process inspection was denied, so no claim is made about the running iris environment. No secrets were read or fetched.

Existing agent fixtures commit before HOME isolation. TestMain already has a process-helper mode, which must remain intact while normal tests receive hermetic Git configuration.

## Goals / non-goals

**Goals:**

- Provide an explicit opt-in for ordinary agent commits to complete without personal signing approval.
- Preserve default signing behavior and unrelated configuration.
- Keep fixture tests independent of developer signing and credentials.

**Non-goals:**

- Global Git/SSH/1Password/keychain changes, sandbox changes, authentication, or plugin timeout changes.
- Cryptographically attest unsigned feature commits; a GitHub squash signature attests the merge, not their individual provenance.
- Enforce a branch boundary. The override belongs to the process tree and survives branch switches; explicit `-S` or `-c` retains Git precedence.
- Add Settings controls. TUI/web/macOS controls are the named follow-up `agent-git-policy-settings-controls`; all launch paths already share the common launcher.

## Decisions

- `git.agent_commit_signing` accepts `inherit` (default/zero value) and `unsigned`. Invalid configured values surface an actionable file-loader error and leave the base overlay unchanged; direct invalid programmatic configuration is rejected at launch.
- Apply command-scope `commit.gpgsign=false` after backend environment mappings, preserving unrelated `GIT_CONFIG_COUNT` entries. Append the override to legacy `GIT_CONFIG_PARAMETERS` when present because Git parses that after the indexed entries. Invalid/negative/overflow counts fail launch with a credential-free error.
- Never mutate global/shared repository configuration. Persistent worktree config requires changing shared `extensions.worktreeConfig` and lifecycle management, so it was rejected. Personal signing remains the default; automation signing would require a separately approved key/trust lifecycle.
- Log policy selection and failures with `[agent-git]`, task identifier, and no credential values. Fresh/resumed/recycled launches use the same BuildCmd path. Running sessions retain their environment until restart/recycle; the supervisor must run the updated spawn surface.
- Before the normal agent test suite, ignore system/global Git config and clear inherited command-scope settings. Synthetic signer tests deliberately replace that fixture config locally. Keep TestMain's existing fake-daemon helper dispatch intact. Preflight also revealed inherited CODEX_HOME bypassing temporary HOME in existing session-capture fixtures; clear CODEX_HOME/CODEX_SQLITE_HOME in this same test-only entrypoint, leaving explicit per-test account homes available.

## Risks / tradeoffs

- Unsigned feature provenance → opt-in only; confirm repository policy allows it. Explicit signing requests still invoke the configured signer.
- Config-file overlay errors fall back to base config → surface FileLoader.Err and launch validation; document accepted values exactly.
- Environment applies to all descendants → document that this is session-scoped, not a branch protection rule.
- Stale supervisor lacks the policy → bump spawn surface and include helper files in its manifest/digest; restart supervisor after upgrading, then restart/recycle sessions.
- Shared host test contention → targeted tests first; request coordinator's gate slot, then run exactly one full `make pre-pr` detached with nohup/exit marker and `GOTOOLCHAIN=go1.26.10`.

## Migration plan

1. Aaron's approval is recorded in coordinator message 17669; implement Argus signing and fixtures only.
2. Verify with synthetic Git configuration and a failing signer; prove unsigned commits succeed, inherit/explicit signing invoke the signer, and host config files remain unchanged.
3. Archive deltas into base specs on this branch, run the coordinated gate, commit with the authorized temporary `-c commit.gpgsign=false`, push/open PR via iris, and wait for CI.
4. Aaron may opt in himself with `[git]` and `agent_commit_signing = "unsigned"` in `~/.argus/config.toml`; restart/recycle sessions after updating the supervisor. Roll back by selecting `inherit` and restarting/recycling. Do not edit Aaron's settings from this task.
