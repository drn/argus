# Cross-vendor code review

## Why

A single model reviewing a single model's code has a systematic blind spot, so a review panel should be composed **by lens** (heterogeneous finders + corrective lenses), not by stacking same-lineage models. The original cross-vendor signal (Opus + codex ≈ +25 pts on Sherlock PR-45) turned out to be **unmeasured, not proven**: the answer key was consolidated from prior Anthropic (Fable + Opus) reviews, so it is circular — it scores reproduction of Anthropic findings and structurally cannot credit a foreign model (see design D5-findings). This change therefore ships the panel machinery at the **operationally-validated Fable + Opus default** (D9), consuming the diligence-profile system that `add-diligence-profiles` shipped (which deliberately deferred the `[panel]` grammar and the `profile_resolve` MCP tool to this chunk), and **reserves** the foreign-vendor slot in the panel grammar. The `foreign-reviewer-capture` primitive and the vendor-neutral cross-vendor measurement are **deferred to a follow-up chunk** (bundled with the codex-auth `HERA_OPENAI` fix a live codex leg needs) — see design D-SCOPE.

## What Changes

- **NEW `mcp__argus__profile_resolve(cwd, [profile])`** — an agent-facing MCP tool (thin wrapper over `internal/profiles`) returning the fully-resolved profile body (archetype/rigor/panel) as structured JSON, daemon-side, fail-open, archetype entries passed through opaquely.
- **`[panel]` grammar + validation** — define and validate the reviewer-panel composition block (`finders`, per-lens `name`/`model`/`skill`, `review_skill`/`review_instruction`, `synthesizer`, `fix_verification`), reconciling 3a's D-PANEL-SEAM via a validator injected into `profiles.Validate` (no `profiles → review` import cycle). Fill the `[panel]` blocks of the shipped `default`/`lean`/`customer_grade` profiles with in-session finders (Opus/Fable). Foreign finder ids (`codex`) remain a **reserved, valid grammar slot** — accepted by validation, not composed into a shipped profile this chunk.
- **NEW `hera-spawn-review` skill** (the glue) + a default, user-overridable **`hera-review` review instruction** (+ shipped lens instructions), under the argus repo `.claude/skills/`: compose the panel, inject the review instruction, run broad finders (**Opus/Fable in-session**) + lenses, synthesize (normalize/dedup-with-provenance/corroboration-vote/classify; single-finder→adversarial-verify-or-downgrade; the synthesizer owns the final `[AUTO-FIX]` call), run a fix-verification phase (Opus adversarial reasoning), spawn fix-workers, re-review to clean.
- **Validation** — the shipped Fable + Opus default is backed by the prior bake-off (Fable:low + Opus:high = full PR-45 slice coverage); this chunk adds only a manual smoke that the glue composes/injects correctly. The vendor-neutral cross-vendor measurement is deferred with the capture primitive.

Non-breaking. No per-chunk GitHub PR; lands on `argus/model-tiering`.

## Capabilities

**New Capabilities:**

- `cross-vendor-review` — the `hera-spawn-review` orchestration + the review-instruction seam + the `[panel]` grammar it consumes (Fable + Opus finders this chunk; foreign slot reserved).

**Modified Capabilities:**

- `diligence-profiles` — adds the `profile_resolve` MCP tool and replaces the opaque-panel seam with a validated `[panel]` grammar (via an injected validator).

**Deferred to a follow-up chunk (design-of-record in D3/D5):**

- `foreign-reviewer-capture` — reviewer-mode output capture for non-hera-aware agents (codex/gemini) + the live vendor-neutral cross-vendor validation.

## Impact

- **argus-Go:** `internal/mcp` (new `profile_resolve` tool), `internal/profiles` (injected panel validator), `docs/profiles/*.toml` (`[panel]` blocks).
- **Skills (argus repo `.claude/skills/`):** `hera-spawn-review`, `hera-review` (default instruction), shipped lens instructions.
- **Validation:** the prior bake-off (Fable:low + Opus:high) backs the shipped default; a manual glue-composition smoke ships with the skill.
- **Dependencies:** the `internal/profiles` package + env injection from `add-diligence-profiles` (already on `argus/model-tiering`).
- **Not touched:** ralph (`ai-ron`); master (merge target is `argus/model-tiering`). Deferred: the reviewer-capture path in `internal/agent`/`internal/daemon` and the codex credential leg (1a's `OPENAI_API_KEY ← HERA_OPENAI`) — both land with the follow-up chunk.
