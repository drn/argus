## 1. Resolve open design questions

- [ ] 1.1 Decide the prompt-prefix insertion point: a single shared helper called from inside `BuildCmd`, versus per-callsite wiring at each spawn path (`agent.CreateAndStart`, `startSession`, hera's `SpawnHeraWorker`, headless task creation) — see design.md Open Question 1
- [ ] 1.2 Decide where the new MCP tool is registered: joining the native `hera_*` family (gated like hera tools) versus an always-registered tool independent of hera wiring (mirroring `kb_*`) — see design.md Open Question 3
- [ ] 1.3 Decide which skill set feeds the catalog: argus's own builtin skills only (`skills.BuiltinItems()`), or the fuller project/user/plugin set (`skills.LoadSkills()`) — confirm against what a non-Claude worker can actually reach
- [ ] 1.4 Decide the exact per-section rendering when a source is absent (no global/repo CLAUDE.md, empty skill catalog) — see design.md Open Question 4

## 2. Skill catalog and full-body lookup (internal/skills)

- [ ] 2.1 Add a function rendering a `[]SkillItem` slice as a plain-text catalog (name + one-line description per entry), matching the skill-provisioning delta spec
- [ ] 2.2 Add a function resolving a single skill's full `SKILL.md` body by exact catalog name, returning a distinct not-found outcome for an unknown name
- [ ] 2.3 Unit tests: catalog rendering for a non-empty set, catalog rendering for an empty set (omittable output), body lookup for a known name, body lookup for an unknown name

## 3. CLAUDE.md content readers

- [ ] 3.1 Add a reader for the global `~/.claude/CLAUDE.md` file that returns cleanly (no error) when the file does not exist
- [ ] 3.2 Add a reader for the repo-local `CLAUDE.md` at the task's worktree root that returns cleanly (no error) when the file does not exist
- [ ] 3.3 Unit tests: file present, file absent, for both readers

## 4. Non-Claude prompt prefixing

- [ ] 4.1 Build the context-block assembly function that concatenates (global CLAUDE.md, repo CLAUDE.md, builtin routing content, skill catalog) and omits any absent section cleanly, per the decision from 1.4
- [ ] 4.2 Wire the assembly into the insertion point decided in 1.1, scoped to `IsCodexBackend` and `IsOpencodeBackend` only — explicitly excluding `IsClaudeBackend` (unchanged) and the `pi` backend (non-goal)
- [ ] 4.3 Unit tests covering the agent-execution delta spec scenarios: Codex receives the prefix, opencode receives the prefix, Claude is unaffected, pi is unaffected, missing CLAUDE.md files omitted cleanly, empty skill catalog omitted cleanly

## 5. New MCP tool: on-demand skill body read

- [ ] 5.1 Register the tool per the decision from 1.2, naming it clearly (e.g. `skill_read`)
- [ ] 5.2 Implement the handler: require `name`, call the lookup function from 2.2, return the full body or a not-found tool error
- [ ] 5.3 Unit tests covering the mcp-server delta spec scenarios: known name returns the body, missing `name` argument rejected, unknown name errors

## 6. Documentation

- [ ] 6.1 Add a `context/knowledge/gotchas/*.md` entry covering the mechanism (prompt-prefix not file-write; why; the token-cost caveat from design.md's Risks section)
- [ ] 6.2 Update the README.md Reference appendix's MCP tools table with the new tool entry

## 7. Validation

- [ ] 7.1 `make pre-pr` passes clean
- [ ] 7.2 `openspec validate add-nonclaude-context-parity --strict` passes
- [ ] 7.3 Manual smoke test: spawn a real Codex and/or opencode hera worker and confirm it receives the context block and can successfully call the new MCP tool for a skill body
- [ ] 7.4 `openspec archive add-nonclaude-context-parity` run in the same PR before merge, per this repo's CLAUDE.md workflow
