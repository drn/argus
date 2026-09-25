# Tasks — consolidate-builtin-skill-sources

## 1. Regression tests first

- [x] 1.1 Add a focused `internal/skills` source-layout test that enumerates `BuiltinItems()` and fails when any embedded builtin name also exists under either repository project-skill root; confirm it fails against the current mirrors.
- [x] 1.2 Add a focused `hera-spawn-review` source-contract test that requires exact-ID resolution from the current session's skill catalog, preserves project-scoped override semantics, enforces lookup before finder spawning, and rejects any project-local review/lens manifest path; confirm it fails before the skill update.

## 2. Reconcile the canonical builtin bodies

- [x] 2.1 Merge the project copy's role-evidence gate, `hera_revive`, worker `archetype`, diligence-profile env, delegate/recycle guidance into `internal/skills/builtin/hera/SKILL.md`, while retaining blocking `hera_inbox(timeout_seconds=...)` and current worker-wait behavior.
- [x] 2.2 Merge coordinator gating, profile resolution, and per-node `archetype` guidance into `internal/skills/builtin/hera-plan/SKILL.md`, while retaining the gater-provided fan-in branch map and blocker-branch self-rebase behavior.
- [x] 2.3 Add the corrected authoring-side `hera-plan` cross-reference to `internal/skills/builtin/argus-resolve-model/SKILL.md` and remove the obsolete repository-mirror note from `internal/skills/builtin/argus-schedule/SKILL.md`.
- [x] 2.4 Update `internal/skills/builtin/hera-spawn-review/SKILL.md` to resolve broad-review and lens instructions by exact ID from the current child session's native skill catalog / catalog-advertised manifest path, retaining loud pre-spawn failure for a missing name and preserving user-owned custom skill selection.

## 3. Remove project-local builtin mirrors

- [x] 3.1 Delete `.agents/skills/{argus-archive,argus-complete,argus-resolve-model,argus-schedule,hera,hera-plan,hera-review,hera-review-test-adversary,hera-spawn-review}/` (the existing `.claude/skills` symlink requires no separate deletion).
- [x] 3.2 Confirm `.agents/skills/` contains only `update-agent-models/` and that both new regression tests pass.

## 4. Update repository guidance and reference docs

- [x] 4.1 Update `AGENTS.md` to distinguish project-only `.agents/skills/` from the canonical `internal/skills/builtin/` source and remove the obsolete instruction to sync builtin copies.
- [x] 4.2 Update `.agents/skills/update-agent-models/SKILL.md` so model-example edits touch only the canonical builtin bodies, not nonexistent project mirrors.
- [x] 4.3 Replace the stale embed-drift gotcha in `context/knowledge/gotchas/misc.md` with the single-source invariant, update its generic-embed bullet, and refresh `context/knowledge/index.md` summary text.
- [x] 4.4 Update the README Reference skill lists to enumerate the complete ten-skill canonical bundle and state that it is not mirrored under `.agents/skills/`.

## 5. Verification and archive

- [x] 5.1 Run `go test ./internal/skills/...` and the directly affected skill/review tests; fix all failures.
- [x] 5.2 Run `make pre-pr`; the full build → vet → fmt-check → lint-pr → vuln → coverage gate must be clean per repository policy.
- [x] 5.3 Smoke-test the post-deletion session path: launch standalone OpenCode children with Argus's exact inline `OPENCODE_CONFIG_CONTENT={"skills":[<managed-root>]}` shape and confirm the native `skill` tool loads both `hera-review` and `hera-review-test-adversary` by exact ID without any project mirror.
- [x] 5.4 Archive this change into `openspec/changes/archive/2026-09-25-consolidate-builtin-skill-sources/` in the same PR, folding the `skill-provisioning` and `cross-vendor-review` deltas into their base specs.

## 6. Post-review hardening

- [x] 6.1 Anchor the source-layout test on the repository `go.mod` so a moved package or copied tree cannot make it pass vacuously.
- [x] 6.2 Record resolved review/lens instruction provenance (role kind, exact ID, advertised path/origin) in the step-11 report and folded capability spec.
- [x] 6.3 Tighten the skill-contract test to durable negatives, a deliberate minimal phrase set, and the full section heading; document the hand-maintained hera tool count.
- [x] 6.4 Record the observed external `argus-schedule` dotfiles drift and clarify that the skill body, not Go validation, is the pre-spawn lookup enforcement point.
