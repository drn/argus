# Cross-vendor code review

## Why

A single model reviewing a single model's code has a systematic, anti-correlated blind spot: on Sherlock PR-45 (scored vs 54 real issues) Opus alone caught 56%, codex alone 61%, but **Opus + codex together 81% (+25 pts)**. This change operationalizes that finding as a native, argus-native cross-vendor review capability, composed by lens, consuming the diligence-profile system that `add-diligence-profiles` shipped (which deliberately deferred the `[panel]` grammar and the `profile_resolve` MCP tool to this chunk).

## What Changes

- **NEW `mcp__argus__profile_resolve(cwd, [profile])`** — an agent-facing MCP tool (thin wrapper over `internal/profiles`) returning the fully-resolved profile body (archetype/rigor/panel) as structured JSON, daemon-side, fail-open, archetype entries passed through opaquely.
- **`[panel]` grammar + validation** — define and validate the reviewer-panel composition block (`finders`, per-lens `name`/`model`/`skill`, `review_skill`/`review_instruction`, `synthesizer`, `fix_verification`), reconciling 3a's D-PANEL-SEAM via a validator injected into `profiles.Validate` (no `profiles → review` import cycle). Fill the `[panel]` blocks of the shipped `default`/`lean`/`customer_grade` profiles.
- **NEW `foreign-reviewer-capture` primitive** — capture a non-hera-aware agent's (codex) review output between sentinels from the already-persisted `~/.argus/sessions/<taskID>.log` into a structured, addressable result.
- **NEW `hera-spawn-review` skill** (the glue) + a default, user-overridable **`hera-review` review instruction** (+ shipped lens instructions), under the argus repo `.claude/skills/`: compose the panel, inject the review instruction, run broad finders (Opus/Fable in-session, codex via capture) + lenses, synthesize (normalize/dedup-with-provenance/cross-vendor-vote/classify; single-finder→adversarial-verify-or-downgrade; foreign never auto-fixes), run a fix-verification phase, spawn fix-workers, re-review to clean.
- **Validation harness** (in-session judge agents) proving the panel on Sherlock PR-45 @ `cdc3a65` vs a rebuilt 54-issue answer key; degrades to captured codex reports if the live codex leg can't authenticate.

Non-breaking. No per-chunk GitHub PR; lands on `argus/model-tiering`.

## Capabilities

**New Capabilities:**

- `foreign-reviewer-capture` — reviewer-mode output capture for non-hera-aware agents.
- `cross-vendor-review` — the `hera-spawn-review` orchestration + the review-instruction seam + the `[panel]` grammar it consumes.

**Modified Capabilities:**

- `diligence-profiles` — adds the `profile_resolve` MCP tool and replaces the opaque-panel seam with a validated `[panel]` grammar (via an injected validator).

## Impact

- **argus-Go:** `internal/mcp` (new `profile_resolve` tool), `internal/profiles` (injected panel validator), a new reviewer-capture path in `internal/agent`/`internal/daemon` over the existing session log, `docs/profiles/*.toml` (`[panel]` blocks).
- **Skills (argus repo `.claude/skills/`):** `hera-spawn-review`, `hera-review` (default instruction), shipped lens instructions.
- **Validation:** in-session judge agents; reads sibling Sherlock/Hera worktree answer-key + captured reports.
- **Dependencies:** the `internal/profiles` package + env injection from `add-diligence-profiles` (already on `argus/model-tiering`); the credential env-map from 1a (codex `OPENAI_API_KEY ← HERA_OPENAI`).
- **Not touched:** ralph (`ai-ron`); master (merge target is `argus/model-tiering`).
