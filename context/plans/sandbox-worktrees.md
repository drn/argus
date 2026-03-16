# Plan: Sandbox Task Worktrees

**Goal:** Automatically sandbox agent processes so `--dangerously-skip-permissions` is safe — the agent can only write to its own worktree, read the project, and access approved network hosts. Worktrees auto-clean when the task completes.

## Research Summary

### Three Sandboxing Options Evaluated

| | **macOS Seatbelt (sandbox-exec)** | **nono** | **@anthropic-ai/sandbox-runtime (srt)** |
|---|---|---|---|
| **Mechanism** | Apple's MAC framework via `sandbox-exec -p <profile>` | Seatbelt (macOS) / Landlock (Linux) | Seatbelt (macOS) / bubblewrap (Linux) |
| **Maturity** | Stable OS primitive (since 10.5), but Apple-deprecated API | Alpha — "has not undergone comprehensive security audits" | Production — powers Claude Code's own `/sandbox` |
| **Cross-platform** | macOS only | macOS + Linux | macOS + Linux + WSL2 |
| **Install** | Built-in | `brew install nono` (Rust binary) | `npx @anthropic-ai/sandbox-runtime` (npm) |
| **Network control** | Profile-based (allow/deny) | Proxy-based allowlist | Proxy-based allowlist (HTTP + SOCKS5) |
| **FS control** | SBPL profile (allow/deny paths) | Allowlist paths | allowWrite/denyWrite/denyRead arrays |
| **Programmatic API** | Shell only (generate SBPL string) | Rust/Python/TS/C | TypeScript (`SandboxManager.wrapWithSandbox`) |
| **Irreversible** | Yes (child inherits) | Yes (child inherits) | Yes (child inherits) |
| **Dependencies** | None | Rust binary | Node.js + npm |

### Recommendation: **@anthropic-ai/sandbox-runtime (srt)**

**Why srt wins:**
1. **Battle-tested** — same sandbox Claude Code uses in production
2. **Cross-platform** — macOS + Linux out of the box
3. **Simple CLI wrapper** — `srt <command>` just works, no profile authoring
4. **JSON config** — easy to generate programmatically from Go
5. **Both FS and network** — proxy-based network isolation included
6. **No Rust/CGO dependency** — it's an npm package, invoked as a CLI

**Why not raw Seatbelt:**
- macOS-only, requires hand-authoring SBPL profiles, deprecated by Apple
- Would need a separate Linux solution (bubblewrap) — srt already wraps both

**Why not nono:**
- Alpha software, not audited. Cool project but too early for security-critical use
- Adds a Rust binary dependency. srt is already on the machine if Claude Code is installed

### How srt Works

```
srt --settings /tmp/argus-sandbox-XXXXX.json -- sh -c "claude --dangerously-skip-permissions ..."
```

The settings file controls everything:
```json
{
  "sandbox": {
    "enabled": true,
    "filesystem": {
      "allowWrite": ["//path/to/worktree", "//tmp"],
      "denyWrite": ["//path/to/project"],
      "denyRead": ["~/.ssh", "~/.gnupg", "~/.aws"]
    },
    "network": {
      "allowedDomains": ["api.anthropic.com", "sentry.io"]
    }
  }
}
```

- `//` prefix = absolute path from root
- `~/` prefix = relative to home
- FS writes denied by default except allowWrite paths
- Network denied by default except allowedDomains
- All child processes inherit restrictions (irreversible)

## Architecture

### Injection Point

The sandbox wraps the **shell command string** in `BuildCmd()`. Instead of:
```
sh -c "claude --dangerously-skip-permissions --session-id 'xxx' 'prompt'"
```
It becomes:
```
sh -c "npx @anthropic-ai/sandbox-runtime --settings /tmp/argus-sandbox-XXXXX.json -- claude --dangerously-skip-permissions --session-id 'xxx' 'prompt'"
```

This is the minimal-diff approach — the sandbox is a transparent wrapper around the existing command. The PTY, attach/detach, ring buffer, daemon — nothing changes.

### New Config Fields

```go
// config.go
type SandboxConfig struct {
    Enabled        bool     `toml:"enabled"`         // default: false (opt-in)
    AllowedDomains []string `toml:"allowed_domains"`  // network allowlist
    DenyRead       []string `toml:"deny_read"`        // extra deny-read paths
    ExtraWrite     []string `toml:"extra_write"`      // additional writable paths beyond worktree
}

type Config struct {
    // ... existing fields ...
    Sandbox SandboxConfig `toml:"sandbox"`
}
```

Per-project overrides are not needed initially — sandbox config is global.

### New Files

- `internal/agent/sandbox.go` — sandbox config generation and command wrapping
- `internal/agent/sandbox_test.go` — tests

### Auto-Cleanup (Already Exists)

Worktree auto-cleanup on task delete is already implemented via `removeWorktreeAndBranch()` in `internal/ui/worktree.go`. The `UIConfig.ShouldCleanupWorktrees()` flag controls it. No changes needed here.

For auto-cleanup on task **completion** (not just deletion): add an `onFinish` callback check — when the runner fires `onFinish` with exit code 0, optionally trigger cleanup. This is a separate concern from sandboxing.

## Implementation Plan

### Phase 1: Core Sandbox Wrapper

**Files:** `internal/agent/sandbox.go`, `internal/agent/sandbox_test.go`

1. **`GenerateSandboxConfig(task, projectDir, cfg) (string, func(), error)`**
   - Creates a temp file (`/tmp/argus-sandbox-XXXXX.json`) with srt settings
   - `allowWrite`: task.Worktree, `/tmp` (for build tools)
   - `denyRead`: `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube` (credentials)
   - `allowedDomains`: from `cfg.Sandbox.AllowedDomains` + defaults (`api.anthropic.com`, `statsig.anthropic.com`, `sentry.io`)
   - Returns cleanup func that removes the temp file
   - Tests: verify JSON output, verify paths, verify cleanup

2. **`WrapWithSandbox(cmdStr, settingsPath string) string`**
   - Returns `"npx @anthropic-ai/sandbox-runtime --settings " + settingsPath + " -- " + cmdStr`
   - Simple string transformation, easy to test

3. **`IsSandboxAvailable() bool`**
   - Checks if `npx @anthropic-ai/sandbox-runtime --version` succeeds (cached)
   - If not available, sandbox is silently skipped with a warning in Settings tab

### Phase 2: Integration into BuildCmd

**Files:** `internal/agent/agent.go`

4. **Modify `BuildCmd`** to accept sandbox config:
   ```go
   func BuildCmd(task *model.Task, cfg config.Config, resume bool) (*exec.Cmd, func(), error)
   ```
   - If `cfg.Sandbox.Enabled` and `IsSandboxAvailable()`:
     - Call `GenerateSandboxConfig()` to create temp settings file
     - Call `WrapWithSandbox()` to wrap the command string
     - Return cleanup func (caller must defer it after process exits)
   - If sandbox not enabled or not available: return nil cleanup func
   - **Breaking change to BuildCmd signature** — update all callers (runner.go, daemon rpc.go)

### Phase 3: Config & DB

**Files:** `internal/config/config.go`, `internal/db/schema.go`, `internal/db/migrate.go`

5. **Add `SandboxConfig` to config types**
6. **Add `sandbox_*` columns to config table** (enabled, allowed_domains, deny_read, extra_write)
7. **Add sandbox section to Settings tab** (`internal/ui/settings.go`)
   - Show enabled/disabled status
   - Show srt availability
   - List allowed domains, deny paths

### Phase 4: Cleanup on Completion

**Files:** `internal/agent/runner.go`, `internal/ui/root.go`

8. **Temp file cleanup**: The `onFinish` callback in runner already fires on process exit. Add the sandbox cleanup func call there.
9. **Optional worktree auto-cleanup on completion**: Add a `SandboxConfig.CleanupOnComplete` bool. When the runner's `onFinish` fires with exit 0, send a message to the UI to trigger `removeWorktreeAndBranch()`. This is opt-in and separate from delete-cleanup.

### Phase 5: Robustness

10. **Fallback behavior**: If srt crashes or isn't installed, the agent runs unsandboxed with a visible warning banner in the agent view.
11. **`srt` binary caching**: Instead of `npx` (slow cold start), detect if `srt` is already in PATH or cache the resolved path after first `npx` call.
12. **Validation**: On task creation, if sandbox is enabled, verify srt is available before proceeding (fail fast with clear error).

## Key Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Sandbox tool | `@anthropic-ai/sandbox-runtime` | Production-tested, cross-platform, already on machine with Claude Code |
| Injection point | `BuildCmd` command string wrapping | Minimal changes — PTY, daemon, attach all unchanged |
| Default state | Opt-in (`enabled: false`) | Breaking change to UX if enabled by default |
| Network policy | Allowlist with sensible defaults | Deny-all-then-allow is the only secure network posture |
| Credential paths | Always denied even if sandbox disabled | Defense in depth — `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube` |
| Temp settings file | `/tmp/argus-sandbox-XXXXX.json` per session | Each task gets its own sandbox config, cleaned up on exit |
| `npx` vs binary | Start with `npx`, optimize to cached binary later | `npx` works immediately; optimization is Phase 5 |

## Risk Assessment

| Risk | Severity | Mitigation |
|---|---|---|
| srt not installed | Low | Graceful fallback to unsandboxed + warning |
| srt breaks PTY behavior | Medium | Test attach/detach/resize with sandbox enabled |
| Network allowlist too restrictive | Medium | Default includes anthropic API; easy to add domains via config |
| `npx` cold start latency | Low | ~2-3s first run; Phase 5 caches binary path |
| srt profile doesn't cover all Claude Code needs | Medium | Claude Code itself uses srt, so compatibility is high |
| Linux bubblewrap not installed | Low | srt detects and reports; we surface in Settings tab |

## Out of Scope

- **Docker/devcontainer isolation** — heavier solution, different use case
- **Per-project sandbox policies** — can add later if needed
- **Custom proxy for HTTPS inspection** — enterprise feature, not needed now
- **nono integration** — revisit when it reaches v1.0
