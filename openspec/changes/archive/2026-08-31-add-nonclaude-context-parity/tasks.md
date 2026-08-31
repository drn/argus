## 1. Resolve open design questions

- [x] 1.1 Decide the insertion point for both the skill materialization call and the prompt-prefix assembly: a single shared helper called from inside `BuildCmd`, versus per-callsite wiring at each spawn path (`agent.CreateAndStart`, `startSession`, hera's `SpawnHeraWorker`, headless task creation) — see design.md Open Question 1. **Resolved: inside `BuildCmd` itself**, mirroring exactly where the existing Claude-only `--add-dir`/`--append-system-prompt-file` injection already lives, so every spawn path gets it for free with no per-callsite duplication.
- [ ] 1.2 Decide whether to detect opencode's `OPENCODE_DISABLE_CLAUDE_CODE*` env vars and fall back to prompt-prefixing CLAUDE.md for opencode when native compat is disabled, or accept the gap — see design.md Open Question 3. **Left open, not implemented**: the gap is accepted for this pass, not resolved.
- [x] 1.3 Decide the exact per-section rendering when a CLAUDE.md source is absent for Codex — see design.md Open Question 4. **Resolved: omit the section entirely** (no heading, no placeholder) — implemented and tested.

## 2. Vendor-scoped skill materialization

- [x] 2.1 (internal/skills) Add a second materialization function (or parameterize the existing one) writing the same embedded builtin skill bodies to `$CODEX_HOME/skills/<name>/SKILL.md` (respecting the `CODEX_HOME` env var, defaulting to `~/.codex/skills/`), idempotent and inert-under-test, mirroring `EnsureBuiltinSkills`
- [x] 2.2 Unit tests: materialized content matches the embedded set, stale directories under the new target (outside `.system/`) are removed, inert during Go tests
- [x] 2.3 (internal/inject/opencode) Extend the existing idempotent config-merge logic to ensure a `skills` array entry naming Argus's managed skills workspace is present in `~/.config/opencode/opencode.json`, preserving all other `skills` entries and unrelated keys
- [x] 2.4 Unit tests: entry added when absent, idempotent on repeat, other `skills` entries and the existing `mcp.argus` entry preserved

## 3. CLAUDE.md content readers

- [x] 3.1 Add a reader for the global `~/.claude/CLAUDE.md` file that returns cleanly (no error) when the file does not exist
- [x] 3.2 Add a reader for the repo-local `CLAUDE.md` at the task's worktree root that returns cleanly (no error) when the file does not exist
- [x] 3.3 Unit tests: file present, file absent, for both readers

## 4. Backend-differentiated prompt prefixing

- [x] 4.1 Build the Codex context-block assembly (global CLAUDE.md + repo CLAUDE.md + routing orientation), omitting any absent CLAUDE.md section cleanly per the decision from 1.3
- [x] 4.2 Build the opencode context-block assembly (routing orientation only — no CLAUDE.md)
- [x] 4.3 Wire both into the insertion point decided in 1.1, scoped to `IsCodexBackend` and `IsOpencodeBackend` respectively — explicitly excluding `IsClaudeBackend` (unchanged) and the `pi` backend (non-goal)
- [x] 4.4 Unit tests covering the agent-execution delta spec scenarios: Codex receives the full block, opencode receives routing-only (and never CLAUDE.md content), Claude is unaffected, pi is unaffected, missing CLAUDE.md files omitted cleanly for Codex

## 5. Documentation

- [x] 5.1 Add a `context/knowledge/gotchas/*.md` entry covering: why skills go through native, vendor-scoped discovery (`$CODEX_HOME/skills/` for Codex, an `opencode.json` `skills` entry for opencode) instead of a custom mechanism or the generic cross-tool `~/.agents/skills/` path, why the prompt-prefix content is backend-differentiated (opencode already natively reads CLAUDE.md as an AGENTS.md fallback — don't double-inject it), and why Codex's global `AGENTS.md` path is deliberately not used (unverified/unreliable per design.md Decision 3)
- [x] 5.2 Update the README.md Reference appendix if any sandbox-default or file-layout table needs the new `$CODEX_HOME/skills/` path or the opencode `skills` config entry noted

## 6. Validation

- [x] 6.1 `make pre-pr` passes clean (build/vet/fmt-check/lint-pr all clean; `test-cover-gate` run individually at 88.7% filtered coverage, above the 88% floor; `vuln` fails on pre-existing Go-stdlib-only CVEs per the documented CI-gate gotcha — confirmed toolchain-only, CI runs it `continue-on-error`)
- [x] 6.2 `openspec validate add-nonclaude-context-parity --strict` passes
- [ ] 6.3 Manual smoke test: spawn a real Codex session and confirm (a) it discovers argus's builtin skills via `codex debug prompt-input` or a live session and (b) its prompt carries the CLAUDE.md + routing prefix. **Not performed as a live argus-spawned task** — the native-discovery premise itself was empirically verified during design review with a locally-installed `codex-cli` (probe skill + `codex debug prompt-input`), but the fully-wired feature (an actual argus task spawning Codex with the new code path) has not been end-to-end smoke tested.
- [ ] 6.4 Manual smoke test: spawn a real opencode session and confirm (a) it discovers argus's builtin skills via the injected `skills` config entry, (b) its prompt carries routing orientation only, and (c) it still independently picks up CLAUDE.md content on its own (no duplication). **Not performed** — no opencode CLI installed in this environment; rests on opencode's official docs plus unit-level test coverage of the injection logic.
- [x] 6.5 `openspec archive add-nonclaude-context-parity` run in the same PR before merge, per this repo's CLAUDE.md workflow
