## Context

PR #874 ("Ship hera-review + hera-review-test-adversary with full routing/skill parity") shipped two of three review skills authored 2026-07-05 on the orphaned `argus/2a-skills` branch, deferring the third — `hera-spawn-review` — because it hard-depends on `mcp__argus__profile_resolve`, an `internal/review` panel-grammar package, and diligence-profile config that did not exist on master at the time.

PR #873 ("Land argus/model-tiering onto master") has since merged that entire workstream: model tiering, diligence profiles, cross-vendor review, and coordinator context management. It brought `profile_resolve`/`internal/review` onto master, closing #874's deferral blocker, and separately added a fourth skill, `resolve-archetype-model` (`.claude/skills/resolve-archetype-model/SKILL.md`) — the native-Claude-sub-agent-dispatch counterpart to `hera-spawn-review`'s archetype-resolution pattern. Neither `hera-spawn-review` nor `resolve-archetype-model` was wired into the embed/routing mechanisms by #873; both landed as `.claude/skills/` bodies only.

## Goals / Non-Goals

**Goals:**
- Ship `hera-spawn-review` and `resolve-archetype-model` with full parity to the existing five embedded skills: an embedded skill body (guaranteed `--add-dir` delivery) and an embedded routing directive (guaranteed `--append-system-prompt-file` delivery).
- Extend the two existing embed mechanisms with the minimum change needed — both `BuiltinItems()` and `BuiltinContent()` already iterate their embedded directory trees generically; no whitelist array to edit.
- Leave no self-contradicting routing content: fix `hera-review.md`'s stale "`hera-spawn-review` has not shipped yet" claim in the same PR that ships `hera-spawn-review.md`.

**Non-Goals:**
- Re-verifying or re-litigating the model-tiering merge (#873) itself — it is treated as already-landed, trusted infrastructure. This change only confirms its two specific blocking symbols (`toolProfileResolve`, `internal/review`) are present, not a full audit of that PR.
- Building a generic glob-based embed mechanism to replace the current hardcoded-directory-tree approach. Two more embedded files fit the existing shape.
- Any change to `hera-spawn-review`'s or `resolve-archetype-model`'s actual SKILL.md prose — both are copied verbatim from `.claude/skills/`, byte-identical, per the existing embed-drift convention.

## Decisions

### Re-confirm the blocker is gone by reading the code, not the tool list

Per the existing gotcha ("a live MCP tool in this session's list is not proof a dependency shipped — this session's dogfood daemon may run ahead of master"), the check was: `grep -n "toolProfileResolve\|profile_resolve" internal/mcp/profiles.go` (present, wired to `case "profile_resolve":` in `internal/mcp/server.go`) and `ls internal/review/` (present: `panel.go`, `panel_test.go`, `seeds_test.go`, with `knownInSessionModels` defined in `panel.go`). Both confirmed present in the current worktree's checked-out `master`, not inferred from a live tool call.

### Copy verbatim, no wiring change

Same reasoning as #874: `BuiltinItems()`/`EnsureBuiltinSkills()` (`internal/skills/builtin.go`) and `BuiltinContent()` (`internal/routing/routing.go`) both iterate their embedded directory trees generically (`fs.ReadDir` + sort by name) — adding `internal/skills/builtin/{hera-spawn-review,resolve-archetype-model}/SKILL.md` and `internal/routing/builtin/{hera-spawn-review,resolve-archetype-model}.md` is the entire change; no code in either package needs to move.

### Fix `hera-review.md`'s stale deferral claim

`internal/routing/builtin/hera-review.md`'s closing paragraph said `hera-spawn-review`, "the panel orchestrator that spawns multiple reviewers and synthesizes across them, has not shipped yet — don't expect multi-finder behavior until it lands." Shipping `hera-spawn-review.md` in the same PR without touching this sentence would land two contradictory routing sections in the same commit. Updated it to point at the new `hera-spawn-review` section instead of asserting non-existence.

### Author two new routing directives distinctly, not by copying `hera-review.md`'s wording

`hera-spawn-review` is panel orchestration (multi-reviewer fan-out + synthesis); `resolve-archetype-model` is a native-sub-agent-dispatch model-resolution convention, not directly user-invocable the way `/hera-review` is. Each directive was written from its own `SKILL.md` frontmatter/body rather than adapting `hera-review.md`'s phrasing, so each accurately describes what the skill actually does and when to reach for it.

## Risks / Trade-offs

- [Risk] Same known gaps as the #874 precedent apply unchanged here: embed-drift between `.claude/skills/*` and `internal/skills/builtin/*` on any future edit to either skill, and zero test coverage for the real `--add-dir`/materialization happy path under the `isTestBinary()` short-circuit. Neither is introduced or worsened by this change; both remain flagged, pre-existing gaps.

## Migration Plan

Fully additive — no migration. `make pre-pr` gates the merge; no schema or data changes.

## Open Questions

None — the coordinator's task brief for this change already specified the exact scope (embed both skills, fix the stale claim, extend tests/docs, archive before PR) and instructed to stop and report back only if either skill turned out to have an additional hidden dependency beyond `profile_resolve`/`internal/review` — confirmed neither does.
