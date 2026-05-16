# Spike: Per-project iMessage automation allowlist

**Date:** 2026-05-15
**Question:** How can Claude Code's Bash tool, running inside an Argus worktree of `forge`, successfully invoke `osascript ... send to participant ...` against Messages.app — scoped to one project, without globally relaxing the sandbox?
**Status:** Complete

## Summary

The handoff doc framed this as a Claude Code sandbox problem. It is not. **Argus itself is the sandboxer**: `internal/agent/agent.go:BuildCmd` wraps every agent invocation in `sandbox-exec` with an SBPL profile defined in `internal/agent/sandbox.go`. The profile is `(deny default)` and AppleEvents are not on the allowlist. Apple's own profiles use `(allow appleevent-send (appleevent-destination "com.apple.iChat"))` for exactly this case. **Recommend Path B**: extend `config.SandboxConfig` with an `AllowAppleEvents []string` field (per-project override already supported in parallel form for `DenyRead` / `ExtraWrite`) and emit `(allow appleevent-send (appleevent-destination "<bundle-id>"))` lines into the generated profile. Confidence: **High**. Effort: **M**.

## Background

- Manual `osascript ... send to participant` from raw Alacritty shells succeeds (TCC grant for `org.alacritty` → `com.apple.iChat` is in place).
- Same osascript from Claude's Bash tool fails with `-600 Application isn't running`.
- Handoff doc enumerated four candidate paths (A: Claude `.claude/settings.json`; B: Argus per-project flag/env; C: Argus MCP server; D: `shortcuts run`) and asked for a ranked recommendation.
- Per `CLAUDE.md` `internal/agent/sandbox.go` already implements per-task sandbox profile generation with `WORKTREE` / `HOME` params and a hand-curated allowlist; `internal/agent/agent.go:38-50:ResolveSandboxConfig` already merges per-project overrides on top of global config.

## Findings

### 1. Argus is the sandboxer, not Claude Code

`internal/agent/agent.go:226-340:BuildCmd` builds the final exec command. When `effectiveSandbox.Enabled && IsSandboxAvailable() && task.Worktree != ""`, it calls `WrapWithSandbox(cmdStr, profilePath, params)` (`internal/agent/sandbox.go:188-200`) which produces:

```
/usr/bin/sandbox-exec -D HOME=... -D WORKTREE=... -f <profile.sb> sh -c '<claude --flags … >'
```

So Claude Code never wraps itself; Claude inherits the Seatbelt restrictions from its parent `sandbox-exec`. Nothing in `~/.claude/settings.json` or `<repo>/.claude/settings.json` can relax this outer wrap. **Path A is structurally a dead end.**

Empirical confirmation from inside this session: `ps -p $PPID` returns `operation not permitted` (Seatbelt deny on process visibility); a nested `sandbox-exec -f /tmp/test.sb osascript ...` returns `sandbox_apply: Operation not permitted` (nested seatbelt apply is blocked by the outer profile). Both behaviors match Argus's `sandboxProfileBase` posture exactly.

### 2. The block is at SBPL `appleevent-send`, not at any of the rules Argus currently grants

`internal/agent/sandbox.go:24-92:sandboxProfileBase` currently grants:

- `(allow process*)`, `(allow signal)`, `(allow mach*)`, `(allow ipc*)`, `(allow sysctl*)`, `(allow system*)`, `(allow job-creation)`, `(allow network*)`, `(allow lsopen)`, `(allow file-read*)` — broad
- Targeted `(allow file-write* …)` for tmp / worktree / `~/.claude*` / `~/.codex` / `~/.pi` / SSH known_hosts / homebrew caches / Keychains / `.dots`

It contains **zero `appleevent-send` rules**. With `(deny default)` at the top, every AppleEvent dispatch is denied at the seatbelt layer — which is exactly what produces the `-600 Application isn't running` error. The `-600` is osascript's translation of the seatbelt deny back into AppleScript-land; the actual kernel denial is `appleevent-send`.

Direct evidence from this sandboxed shell:
- `osascript -e 'tell application "Messages" to get name'` returns `"Messages"` — this is a metadata read off CFBundleDisplayName, not a live AppleEvent.
- `osascript -e 'tell application "Messages" to get name of every service'` returns `-600` — real AppleEvent dispatch, blocked at SBPL.
- Same for `tell application "System Events"`, `tell application "Messages" to send ...`, etc.

Apple's bundled SBPL profiles (sampled from `/System/Library/Sandbox/Profiles/`) use the canonical form:

```
(allow appleevent-send (appleevent-destination "com.apple.finder"))
```

…and for broader cases `(allow appleevent-send)` with no destination clause. The bundle identifier for Messages.app is **`com.apple.iChat`** (still the legacy iChat identifier despite the rename), which is also what `~/Library/Application Support/com.apple.TCC/TCC.db` uses for the Automation grant — TCC and SBPL agree on the same bundle ID, so allow-rule and TCC entry match 1:1.

### 3. Argus's per-project sandbox plumbing is already in place

`internal/config/config.go:57-70`:

```go
type ProjectSandboxConfig struct {
    Enabled    *bool    // nil = inherit global; true/false = override
    DenyRead   []string // appended to global deny_read list
    ExtraWrite []string // appended to global extra_write list
}

type Project struct {
    Path    string
    Branch  string
    Backend string
    Sandbox ProjectSandboxConfig
}
```

`internal/agent/agent.go:38-50:ResolveSandboxConfig` already merges per-project overrides on top of `cfg.Sandbox`:

```go
if proj.Sandbox.Enabled != nil {
    result.Enabled = *proj.Sandbox.Enabled
}
result.DenyRead   = append(append([]string{}, result.DenyRead...),   proj.Sandbox.DenyRead...)
result.ExtraWrite = append(append([]string{}, result.ExtraWrite...), proj.Sandbox.ExtraWrite...)
```

DB schema (`internal/db/schema.go:32-40` + idempotent `ALTER TABLE` block at `:60-65`) has `sandbox_enabled`, `sandbox_deny_read`, `sandbox_extra_write` columns on `projects`, serialized as CSV. Settings TUI (`internal/tui/projectform.go` + `internal/tui/settings.go`) already renders editable rows for both lists. Adding an `AllowAppleEvents []string` field is a straight-line parallel extension at every layer.

### 4. The Claude Code config surface is irrelevant here

Inspected `~/.claude/settings.json` (the live one): contains `env`, `hooks`, `enabledPlugins`, `extraKnownMarketplaces`, `skipDangerousModePermissionPrompt`, `skipAutoPermissionPrompt`. Strings extracted from the Claude binary (version `2.1.143`, single 198 MB Bun-compiled executable at `~/.local/share/claude/versions/2.1.143`) confirm a Bash-tool flag `dangerouslyDisableSandbox: boolean` and a top-level `"permissions"` config key — both apply to Claude's *own* in-process safety scaffolding, not to an external sandbox-exec wrapper. None of them can change Argus's outer Seatbelt posture.

### 5. `shortcuts run` from inside the Argus sandbox is actually reachable

`shortcuts list` from this sandboxed Bash returned the user's full 17-entry shortcut list. `shortcuts run "<missing>"` returned a clean "couldn't find shortcut" error — i.e., the CLI itself executed without seatbelt rejection. The Shortcuts runner is dispatched via XPC to `WorkflowKit.BackgroundShortcutRunner`, which is its own entitled process and likely escapes the caller's AppleEvent restriction. **Unverified for actual `send` action** (I'd have to author a shortcut to test, and a working test would actually send a message — out of scope for a research spike). This route is technically viable but isn't a clean primary path: the action UI is mouse-driven, parameter passing is constrained to file input or single top-level args, and it has no clean test harness. **Keep as fallback** if Path B hits an unforeseen wrinkle.

### 6. MCP route (Path C) adds attack surface for no win

The Argus daemon runs unsandboxed and could expose `send_imessage(to, body)` as an MCP tool. But:

- All Argus-managed agents see the same MCP server (no per-project MCP gating exists today), so `send_imessage` would be reachable from every project, defeating the per-project opt-in goal.
- Forge's three-layer rule still requires `bin/forge imessage send` as the canonical entry point, so an Argus MCP tool doesn't save the forge build — it duplicates it.
- A generic `send_imessage` tool is a more tempting prompt-injection target than a sandbox-allow-rule scoped to one project's agents.

## Recommendation

**Adopt Path B: per-project SBPL `AllowAppleEvents` allow-list in Argus.**

Concrete change shape (write up in `/plan`, do not implement in this spike):

1. `internal/config/config.go`
   - Add `AllowAppleEvents []string \`toml:"allow_apple_events"\`` to `SandboxConfig`.
   - Add `AllowAppleEvents []string` to `ProjectSandboxConfig`.

2. `internal/db/projects.go` + `internal/db/schema.go`
   - Add `sandbox_allow_apple_events TEXT NOT NULL DEFAULT ''` column to `projects`. Reuse the idempotent `ALTER TABLE … ADD COLUMN` block at `schema.go:60-65`.
   - CSV-serialize identical to `DenyRead` / `ExtraWrite` (use existing `splitCSV` helper).

3. `internal/agent/agent.go:ResolveSandboxConfig`
   - Append project's `AllowAppleEvents` to result, mirroring the existing `DenyRead` / `ExtraWrite` merges.

4. `internal/agent/sandbox.go:GenerateSandboxConfig`
   - For each entry, validate as a CFBundleIdentifier (`/^[A-Za-z0-9.-]+$/` — keeps SBPL-injection-safe) and emit `(allow appleevent-send (appleevent-destination "<bundle-id>"))` after the existing `(allow file-write* (subpath WORKTREE))` block.

5. `internal/tui/projectform.go` + `internal/tui/settings.go`
   - Add an editable "Allow AppleEvents (bundle IDs)" list, identical UI pattern to "Sandbox Deny Read" / "Sandbox Extra Write".

6. HTTP API: if `/api/projects` exposes the sandbox config fields, mirror the new field there too. (Verify in `/plan` phase.)

7. Tests at each layer per the 95 % coverage floor:
   - `config_test.go`: TOML roundtrip with the new field.
   - `projects_test.go`: DB CSV roundtrip.
   - `agent_test.go`: `ResolveSandboxConfig` merges project allow list.
   - `sandbox_test.go`: emitted profile contains the expected `(allow appleevent-send …)` line; validation rejects malformed bundle IDs.
   - `projectform_test.go` + `settings_test.go`: UI roundtrip.

8. Forge-side phase 3 (separate task per handoff): add `bin/forge imessage send PHONE --body TEXT` (Thor → Actor → Client per forge architecture), wire `/imessage` skill calling that CLI, document org-policy "every send needs explicit yes" reminder in forge's `CLAUDE.md`.

9. Operator step: in Argus settings, edit the `forge` project and add `com.apple.iChat` (and any other Messages-related IDs we discover — likely none) to its Allow AppleEvents list.

**Confidence:** High — diagnosis matches symptom on first principles, profile-emit pattern matches Apple's reference profiles, and the per-project plumbing is a straight-line replication of two existing fields with full test coverage already in place.

**Effort estimate:** M — small surface area per file, but six layers (config / db / agent resolve / sandbox emit / two UI files) plus tests at each.

### Ranking summary

| Path | Verdict | Reason |
| --- | --- | --- |
| **B** Argus per-project SBPL allow rule | **Recommended** | Reuses existing per-project override plumbing; scopes capability precisely; one new SBPL rule class; clean test points; small risk surface. |
| C Argus MCP `send_imessage` tool | Reject | Cross-project leak risk, duplicates required forge CLI build, increases injection surface. |
| D Shortcuts wildcard | Hold as fallback | `shortcuts` CLI is reachable from sandbox, but action setup is GUI-only and untestable as a CI artifact. Use only if Path B hits a kernel wrinkle. |
| A Claude Code `.claude/settings.json` knob | Reject | Claude Code is not the sandboxer; no Claude config can change Argus's outer `sandbox-exec` profile. |

## Open Questions

- Does the HTTP API (`internal/api/`) expose project sandbox config today? `/plan` needs to confirm whether the new field needs to be plumbed there too.
- The Argus daemon spawns sessions on the user's behalf — does the daemon process inherit Alacritty's TCC grant (Automation → Messages), or does the daemon need its own TCC entry? Verify in `/plan` by inspecting how the daemon is started (it forks the running binary with `Setsid`; the responsible-process for TCC is determined by the launch chain). Likely a non-issue because Argus is launched from Alacritty and child processes inherit, but worth checking before the build.
- Should validation also enforce the bundle ID actually corresponds to an installed app? Probably no — validation should be syntactic only; semantic checks are a footgun (apps come and go).

## Next Steps

- [ ] User confirms Path B is the chosen direction.
- [ ] Run `/plan` to produce the implementation plan with file-by-file diffs and test list.
- [ ] Run `/dev` to implement against the plan with the 95 % coverage gate enforced.
- [ ] In a separate forge-side task (per handoff): add `bin/forge imessage send` Thor command + Actor + Client, plus `/imessage` skill, plus `CLAUDE.md` safety reminder.
- [ ] Operator step after merge: add `com.apple.iChat` to forge's Allow AppleEvents list in Argus settings.
