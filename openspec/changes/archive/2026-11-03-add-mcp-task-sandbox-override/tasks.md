## 1. MCP + daemon
- [ ] 1.1 `internal/mcp/server.go`: add `sandbox_override` to the `task_create` schema/description; add `SandboxOverride` to `TaskCreateInput`; parse + validate in `toolTaskCreate` (enum check before create).
- [ ] 1.2 Escalation guard: reject `"disabled"` unless caller resolved via `caller_id`/`cwd` is non-sandboxed (reuse the account-inheritance caller lookup; fail closed).
- [ ] 1.3 `internal/daemon/daemon.go:1472`: forward `SandboxOverride` into `HeadlessInput`.
- [ ] 1.4 Tests (mcp + daemon): every scenario in specs/mcp-server; table test with `t.Run`; uxlog/log line on reject.
## 2. Docs
- [x] 2.1 README MCP tools table; `gotchas/sandbox.md` bullet (escalation guard, fail-closed).
- [x] 2.2 Archive this change in the same PR (merge deltas into `openspec/specs/mcp-server/`; handoff-skill delta is skill-only — note in Non-Goals, do not create a base spec).
## 3. Skill (outside repo)
- [ ] 3.1 Update `~/.claude/skills/handoff/SKILL.md`: `sandbox=` token + pass-through in step 8.
## 4. Gate
- [ ] 4.1 `make pre-pr` clean before pushing.
