## Context

A single model reviewing a single model's code has a systematic blind spot. Validated on Sherlock PR-45 (multi-tenant RLS/auth), scored against 54 consolidated real issues: Opus alone caught 56%; OpenAI codex alone 61% with 7 net-new real issues (missed by both Opus and Fable) and 0 false positives; **Opus + codex together = 81% (+25 pts)**. The misses are anti-correlated. Blind spots are *systematic*, not random, so the fix is to **compose a review panel by lens** (heterogeneous finders + corrective lenses), not by adding same-lineage models.

This change operationalizes that finding as a first-class, argus-native capability. Three facts shape the design:

- **The profile system already exists.** The sibling `add-diligence-profiles` change (archived, on `argus/model-tiering`) shipped `internal/profiles` (load + in-repo precedence + `extends` + validate), the `default`/`lean`/`customer_grade` profiles, project→profile binding, and spawn-time env injection (`ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL`). It **deliberately left the `[panel]` block opaque** and assigned its grammar to this chunk (D-PANEL-SEAM), and it **deferred the `mcp__argus__profile_resolve` MCP tool** to this chunk (its decision 4).

- **codex is genuinely foreign.** It runs in a PTY, interactively, and is not hera-aware — it cannot call any MCP tool (including `task_set_result`). Its review only exists as text on a terminal. But argus already tees every session's full scrollback to `~/.argus/sessions/<taskID>.log` — outside the worktree, keyed by task id, surviving teardown. So the foreign reviewer's output is *already captured*; it just lacks a structured, addressable home.

- **ralph is not the right host.** Ground truth (`ai-ron/.claude/skills/ralph-review/SKILL.md`): ralph runs a *single* reviewer per loop (up to 3 loops), and explicitly rejects the multi-reviewer pattern. Bolting multi-vendor + panel judgment + worker-spawn onto it is not clean. A hera-aware skill built for this from the start is.

## Goals / Non-Goals

**Goals:**

- **`hera-spawn-review`** — the argus-shipped, hera-aware *orchestration glue* (reachable by any argus user incl. drn): read the project's resolved profile, spawn the vendor-diverse finder set + lenses each injected with a **review instruction**, capture foreign output, run the Opus synthesizer, exercise judgment on what to fix, spawn fix-workers that obey the project's own conventions and report confidence, then re-review to clean.
- **`hera-review`** — a *default, user-overridable review instruction* (the actual "review this code" methodology encoding ralph's contract). The user brings their own review skill(s) — named in the profile/panel — and iterates them independently; `hera-review` is only the fallback when none is specified. This is the D8 decoupling.
- `mcp__argus__profile_resolve(cwd)` — the agent-facing config-fetch tool (a thin wrapper over `internal/profiles`) returning the fully-resolved profile body (archetype/rigor/panel) as structured JSON.
- The `[panel]` grammar (reviewer composition) that `hera-spawn-review` consumes and profile validation enforces, plus the `[panel]` blocks of the three shipped profiles.
- A `foreign-reviewer-capture` primitive: capture a non-hera-aware agent's review output into a structured, addressable result.
- Fix-verification as a `hera-spawn-review` phase ("does the fix work in the shipped artifact?").
- A validation harness proving the panel on PR-45 @ `cdc3a65` vs the 54-issue answer key.

**Non-Goals:**

- Modifying ralph. ralph stays untouched for solo/non-argus use; `hera-spawn-review` is the argus path and its default `hera-review` instruction reuses ralph's fix/re-review *philosophy and contract*, not its code.
- Re-platforming the ai-ron dev toolchain onto hera (retiring execute-plan, brainstorm→hera_plan). That is a separate, larger workstream; flagged, not built here.
- Gemini. Out of scope; the panel grammar leaves a clean slot for a second foreign lab later.
- The local model (pi/Qwen) leg. Already tested and dropped (0 unique catches, false positives).
- Re-implementing profile resolution. `profile_resolve` calls the existing `internal/profiles` package.

## Decisions

### D-SCOPE — Ship Fable+Opus now; defer foreign-reviewer-capture + cross-vendor validation (owner decision, 2026-07-05)

Aaron's scope call (2026-07-05): because cross-vendor value is **unmeasured** (the validation key is circular — built from Fable+Opus reviews, see D5-findings) and the operational default is Fable:high + Opus:high (D9), this chunk ships the panel machinery at the Fable+Opus default and **reserves** the foreign-vendor slot in the panel grammar. Deferred to a follow-up chunk (bundled with the codex-auth `HERA_OPENAI` fix a live codex leg needs):

- **The `foreign-reviewer-capture` primitive (D3).** Its delta spec is removed from this change; D3 below is retained as the design-of-record for the follow-up.
- **The codex leg of the panel (D2) and of fix-verification (D4).** The `[panel]` grammar still accepts `codex` as a reserved, valid id, but no shipped profile composes it and `hera-spawn-review` does not spawn it this chunk.
- **The live cross-vendor validation + vendor-neutral answer key (D5).** The Fable+Opus default is already backed by the prior bake-off (Fable:low + Opus:high = full slice coverage on PR-45); this chunk adds only a manual smoke that the glue composes/injects.

Everything else ships this chunk: `profile_resolve`, the `[panel]` grammar + injected validator, the shipped Fable+Opus profiles, the `hera-spawn-review` glue + `hera-review` default instruction + lenses, the synthesizer + single-finder adversarial gate, and fix-verification as Opus adversarial reasoning.

### D1 — Native hera-aware orchestration skill, not a ralph upgrade

The orchestration lives in a new hera-aware skill (`hera-spawn-review`, see D8) in the argus repo; ralph is left alone.

- **Why:** ground truth shows ralph is single-reviewer-per-loop and structurally resists a multi-vendor panel; a hera-aware skill is a cleaner host and gives reach (drn, non-ai-ron users). ralph keeps working solo everywhere.
- **Alternatives:** (a) upgrade ralph — rejected (structural mismatch, couples ralph to hera). (b) Hybrid where ralph orchestrates an argus primitive — rejected once ground truth showed the "orchestrator" ralph doesn't exist.

### D2 — Panel = broad vendor-diverse finders + corrective lenses → one Opus synthesizer

- **Broad finders** (the diversity engine; each reviews the full diff): **Opus + Fable + codex**. Fable is back and is a first-class broad finder. Not narrowed — the validated +25pt came from codex doing a *broad* pass, not a single lane.
- **Corrective lenses** (systematic-gap coverage; distinct prompts, model = runner): **test-adversary** (Opus — must *not* be codex, whose blind spot #2 is trusting green tests). Library-source depth is folded into the codex/deployment finder prompts, not a separate agent (YAGNI; promote later if validation shows it missed).
- **Synthesizer** (one Opus agent): normalize each finder's free-form output into the canonical schema → dedup preserving **provenance** (which finders caught each issue) → **cross-vendor confidence vote** → classify → feed downstream fix.
- **Foreign models never make the final `[AUTO-FIX]` call** — the Opus synthesizer owns it.
- **Why compose by lens:** codex's misses are systematic, so re-running a model can't cover its own gaps; a corrective lens (often on a *different* model than the one with that blind spot) can. Sonnet as a second broad reviewer is rejected — same lineage, correlated votes.

### D2a — Synthesizer confidence gate: single-finder → adversarial-verify-or-downgrade

The dangerous failure mode is auto-"fixing" working code from a hallucinated finding.

- **Corroborated (≥2 independent finders)** → high confidence → eligible for `[AUTO-FIX]` if low-risk.
- **Single-finder** → must pass an adversarial "is this real? default to not-real if uncertain" refute pass before `[AUTO-FIX]`; on refute/uncertain it is **downgraded to `[QUESTION]`** (surfaced, never silently dropped).
- **Why:** a cross-vendor panel yields more findings *and* more false positives; scrutiny scales to corroboration — cheap where safe, adversarial where risky.

### D3 — Foreign-reviewer-capture (D10 = option c), only for codex

**DEFERRED (per D-SCOPE) — design-of-record for the follow-up chunk; not built here.**

A first-class capture path: mark a session "reviewer-mode," wrap its prompt so the foreign agent emits its review between sentinels (`<<<ARGUS_REVIEW>>> … <<<END_ARGUS_REVIEW>>>`), extract that block from the already-persisted `~/.argus/sessions/<taskID>.log`, and stamp it into a structured, addressable result the synthesizer reads.

- **Only codex needs this.** Opus, Fable, and the lenses run as **in-session Claude sub-agents** (`Agent` tool, `model=opus|fable`) whose output returns directly — no capture, no worktree, no hera. codex is the sole leg that requires an argus session + capture.
- **Why option (c):** the 03-doc's recommended option (a) ("foreign worker writes `task.Result` via `task_set_result`") is infeasible — codex can't call MCP tools at all. Scraping raw scrollback (option a here) is brittle (ANSI/UI chrome); a findings-file (option b) races teardown. Sentinel-delimited extraction over the log argus already keeps is robust and reusable (gemini next).

### D4 — Fix-verification as a `hera-spawn-review` phase, reasoning-default

A distinct pass from per-area review: "does the fix work in the *shipped artifact*?" — the highest-stakes PR-45 bug (a build-stub config shipping into the image → crash-loop) was invisible to all code review and caught only by an adversarial pass over the fix PRs.

- **Default = adversarial artifact *reasoning*** (no build): read the deploy surfaces (Dockerfile, entrypoint, config precedence, what ships into the image, test-suite integrity) and trace "build → running container: is the fix present and functional?" This is exactly what caught the PR-45 bug.
- **Optional escalation = real run** via the existing `iris_run_checks` where the project ships `script/iris-check`. No new argus-Go.
- **Cross-vendor here too (DEFERRED leg):** in the full design the adversarial pass also spawns a codex reviewer via the same capture primitive (the PR-45 catch was foreign) — one primitive serving both the panel and fix-verification. Per D-SCOPE this chunk ships fix-verification as **Opus adversarial reasoning only**; the codex adversarial leg lands with the capture primitive.
- **Why a phase, not a separate skill:** it belongs in the review loop; a standalone entrypoint adds surface without benefit now.

### D5 — Validation harness (in-session judge agents), run at execution

**DEFERRED (per D-SCOPE) — the live cross-vendor validation + vendor-neutral answer key move to the follow-up chunk (a fair test needs the capture primitive + a live codex leg). This chunk relies on the prior bake-off for the shipped Fable+Opus default and adds only a manual glue-composition smoke. D5/D5-findings are retained as the design-of-record + honest measurement caveat.**

Prove `hera-spawn-review` on Sherlock PR-45 @ `cdc3a65` before adoption.

- Run the *real* `hera-spawn-review` panel + synthesizer on PR-45, produce a normalized findings list, score it.
- **Vendor-neutral answer key (load-bearing — corrected after the slice validation below).** The key MUST be built by **pooling every vendor's findings** (Opus, Fable, codex, and any future foreign lab) and adjudicating each as REAL / WAI / uncertain from *code evidence* — NOT rebuilt from Anthropic-lineage (Opus/Fable) reviews alone, and NOT with any vendor's raw report treated as the key. A single-vendor-derived key structurally cannot credit a foreign model's distinct catches, so it cannot measure cross-vendor value. Use `fable-crit-review.md` + direct code inspection as ground truth; every reviewer's out-of-key "extra-real" find gets adjudicated *into* the key, not discarded.
- **All reviewers must see the same packet.** Every config reviews the identical slice (auth + internal runtime endpoints + RLS/tenancy migrations) under one brief. Scope asymmetry invalidates the comparison — the whole point of the finding below.
- **Graceful degradation:** if the live codex leg can't authenticate (daemon lacks `HERA_OPENAI` — the known secret-sourcing follow-up), feed the synthesizer the already-captured `codex-*.md` reports and run Opus/Fable live; the harness still validates the synthesizer + composition + union lift. State the limitation in the result.
- **Metrics:** per-finder and union catch-rate (primary), precision guardrail (no FP regression vs Opus-alone), unique-catch (with provenance), cost. Output shows what each vendor found + the union, side by side.
- **No hard gate.** The harness reports; Aaron judges from the numbers.

### D5-findings — What the PR-45 slice validation actually showed (kept sharp, not smoothed)

An interim run on the auth+runtime+tenancy slice — all six configs (fable-low, fable-medium, sonnet5-xhigh, opus-high, opus-xhigh, codex) on one shared 24-file packet, scored against an interim 18-issue key — produced these results, which drive the D5 correction above:

- **codex added ZERO unique key-catches on a level playing field.** Its earlier apparent role as sole catcher of the two highest-value bugs (RLS auth-bootstrap; runtime claim-binding) was a **packet-scope artifact** — it evaporated the moment every reviewer saw the runtime endpoint + the RLS migrations (both bugs then caught 5 of 6). The only two unique catches in the study both went to **Anthropic** configs (opus-high → K8, fable-low → K15).
- **Two Anthropic configs (fable-low + opus-high) = 12/18 = the ENTIRE panel's total coverage on this slice.** Adding opus-xhigh, sonnet5-xhigh, *or* codex on top bought zero additional key catches. This is the uncomfortable core result and it stands unsmoothed.
- **BUT the interim key was single-vendor (Anthropic-derived) and auth-scoped**, so it structurally could not score codex's real out-of-key contribution — an unvalidated `user_id` claim, cross-merchant `parent_id` exposure, child tables missing RLS. "codex redundant" was true *against that key*, not a verdict on codex. This is exactly why D5's key must be vendor-neutral.
- **Effort is not monotonic:** opus-xhigh was the best single reviewer (10/18) yet added no unique catch; fable-low beat fable-medium; sonnet5-xhigh self-limited to a test-coverage lens and scored 3/18. Reviewer framing/scope dominates the effort dial.
- **Precision was near-perfect** (≈0 false positives); **6 key issues were caught by nobody**; and 5 of 6 reviewers flagged a real service-JWT replay bug that was *missing from the interim key* — evidence the key itself needs pooling.
- **Scope/limits:** n=1 slice; LLM-judge scoring carries ~±1-2 issue variance (only large gaps are robust). This does **not** overturn the full-PR finding in Context — it exposes a *measurement* requirement (the vendor-neutral key), which is the change D5 now bakes in.
- **The key is CIRCULAR — built from prior Fable + Opus reviews — so these numbers cannot decide cross-vendor value.** The 18-issue key was consolidated from Fable (`r1-auth-fable.md`, and `fable-crit-review.md` — the experiment's primary ground-truth source) + Opus (`r1-auth-opus.md`) + sweeps. Scoring configs against it measures *reproduction of prior Fable/Opus findings* and structurally cannot credit a foreign model — codex's distinct value landed as 7 out-of-key "extra-real" cross-tenant finds. Honest status: **cross-vendor value is UNMEASURED, not disproven** (pending the vendor-neutral key). "codex redundant" reflects the key's authorship, not codex's ceiling; "Fable:high is top performer" is likewise unmeasured here (Fable:high fell back — the matrix Fable is Fable:*low*).

### D9 — Operational panel default + fail-loud Fable (owner decision)

- **Highest-quality panel = Fable:high + Opus:high**, falling back to **Fable:low** when Fable:high is unavailable. Rationale: Fable is the current frontier model ("Fable leads, Opus catches its misses"); this is the strongest panel we can *actually run*. This supersedes any "codex + Anthropic" default until a vendor-neutral key measures cross-vendor value.
- **Fail-loud Fable:** the machine setting *"switch models when a message is flagged"* is DISABLED, so Fable:high now **stops instead of silently substituting Opus**. A panel can therefore trust a Fable finder is really Fable — but a panel run must handle a Fable:high *stop* (fall back to Fable:low), not a silent Opus swap.
- **Cross-vendor (codex/gemini) stays supported** by the foreign-reviewer-capture primitive — it is the mechanism that makes a fair vendor-neutral test possible — but it is **not** the justification for the capability until such a test measures a real lift.

### D6 — `profile_resolve` MCP tool: thin wrapper, opaque pass-through

`mcp__argus__profile_resolve(cwd, [profile])` resolves cwd → project → bound profile name (per-spawn `task.Profile` override > project binding > `default`) → the fully-resolved profile body, returned as structured JSON: archetype entries + `[rigor]` + `[panel]`.

- **Thin wrapper** over `profiles.Loader.ValidateName` + `Project.ResolveProfileName` — no re-implementation of load/precedence/extends/validate.
- **Runs daemon-side** (resolution reads `~/.argus/profiles`, which EPERMs inside the sandbox) — this is exactly why an MCP tool is required over an in-sandbox file read.
- **Fail-open:** a missing/invalid profile returns a structured "no profile resolved" result with the errors, not a hard error; `hera-spawn-review` then falls back to a built-in default panel.
- **Forward-compat:** archetype entries are returned **opaquely** (pass the resolved body through; do not collapse to single model/effort scalars) so a future "bounded model-menu per archetype" array flows through without breaking the contract.
- **Why MCP (not env):** env already carries the *scalars* (`ARGUS_PROFILE` etc., via 3a's D-INJECT); env can't carry the `[rigor]`/`[panel]` arrays/blocks. `profile_resolve` is the delivery vehicle for the rich body.

### D7 — The `[panel]` grammar (owned here; reconciles D-PANEL-SEAM)

The `[panel]` block schema `hera-spawn-review` consumes and profile validation enforces:

```toml
[panel]
# Broad, general-purpose finders — the vendor/lineage-diversity engine.
# Each reviews the FULL diff. Ids map to an in-session model (opus, fable) or a
# configured foreign backend (codex).
finders = ["opus", "fable", "codex"]   # lean might be ["opus", "codex"]

# The review INSTRUCTION each broad finder runs (D8). A user-owned skill name;
# defaults to the shipped "hera-review". `review_instruction` prose is an
# alternative for users who'd rather write "use /whatever to review the work".
review_skill = "hera-review"
# review_instruction = "use /my-custom-review to review the work"   # optional prose alt

# Corrective lenses — systematic-gap coverage. Distinct instructions; model = runner.
# `skill` names the lens instruction (shipped default, user-overridable).
[[panel.lens]]
name  = "test-adversary"
model = "opus"
skill = "hera-review-test-adversary"

# Synthesizer that owns the final [AUTO-FIX] call. Default "opus". hera-owned
# (the platform safety contract), NOT a user review instruction.
synthesizer = "opus"

# Fix-verification phase toggle.
fix_verification = true
```

- **Validation** (reconciling the seam): `finders` is a non-empty list of ids each resolvable to a known in-session model or configured backend; each lens has a non-empty `name` and a known `model`; `synthesizer` (if set) is a known model; `fix_verification` is a bool. `review_skill`/lens `skill` are free-form skill-name strings (existence is not validated at profile-load time — a missing skill fails loudly at spawn, and `review_instruction` prose has no skill to check); exactly one of `review_skill`/`review_instruction` may be set (else default to `hera-review`). To avoid a `profiles → review` import cycle, the panel-grammar validator is **injected** into `profiles.Validate` as a func, mirroring how `knownModels` is already injected.
- **Profile blocks filled here:** `customer_grade` = full multi-vendor panel + lenses + fix-verification; `lean` = light (e.g. `["opus", "codex"]`, no fix-verification); `default` = middle.
- **Fable-effort reliability (operational gotcha — must survive in the docs).** Fable reliably runs only at **low** effort in this harness. At **high** it *always* falls back to Opus, and at **medium** it falls back intermittently (observed on both sides). A `[panel]` that specifies a Fable finder therefore must pin it to `low`, or the panel will *silently review with Opus* while believing it ran Fable — a broken diversity assumption. The panel/profile docs and `context/knowledge/gotchas/` record this so a future composition can't unknowingly rely on Fable:high/medium. (A per-finder effort field can pin this once the effort knob is first-class; until then, a Fable finder means Fable:low.)
- **Why:** this is Decision 2 made concrete; the profile is where "customer-facing → multi-vendor, internal → lighter" and the "how many workers" leeway live.

### D8 — The review *instruction* is user-owned; hera owns the spawn/inject/synthesize glue

Split the capability along its natural seam (Aaron's design review):

- **`hera-spawn-review` (the glue, argus-shipped, reusable):** reads the resolved profile's `[panel]`, spawns the finder set (broad: Opus/Fable in-session, codex via capture) each **injected with the configured review instruction**, spawns the lens workers each injected with their lens instruction, runs the Opus synthesizer (the platform safety contract), exercises judgment on what to fix, spawns fix-workers that follow the project's conventions and report confidence, runs fix-verification, and re-reviews to clean. This is the machinery — it does not itself define *how* to review.
- **The review instruction (user-owned):** the actual "review this code" methodology a finder follows. Named in `[panel]` as `review_skill` (a skill the user brings and iterates — e.g. a high-effort and a low-effort variant, selected per profile) or as `review_instruction` prose. `hera-review` ships as the **default** instruction (encoding ralph's review contract) for when none is specified; the user overrides it freely.

- **Why:** the "spawn workers + inject instruction + capture + synthesize + fix + re-review" glue is stable, general, and worth shipping once; the "what good review looks like" instruction is exactly what a user wants to own and iterate. Decoupling them makes the capability a platform (bring-your-own review skill) and dissolves the earlier "my dev skills don't flow in" concern — the review instruction *is* a user skill now. The synthesizer stays hera-owned because it enforces the canonical schema + auto-fix discipline (safety), independent of whichever review instruction ran; it already normalizes heterogeneous finder output, so a swapped-in review skill needs no synthesizer change.
- **Alternatives:** (a) one monolithic `hera-review` skill that hard-codes the review methodology — rejected: users can't iterate the review prompt without forking the orchestration. (b) instruction as prose-only in config — supported as the `review_instruction` option, but a named skill is the primary path (versioned, iterable, testable).

## Risks / Trade-offs

- **codex can't authenticate during validation** (daemon lacks `HERA_OPENAI`) → harness degrades to captured `codex-*.md` reports and flags coord; a codex-auth failure is not a shipping blocker.
- **Panel grammar churn vs 3a's opaque seam** → contained to one field's validator + `hera-spawn-review`'s consumption; nothing else depends on panel semantics.
- **Fable's lineage** (Claude-family) could add correlated votes rather than diversity → its marginal value is measured by the harness's unique-catch metric against the *vendor-neutral* key (a Fable+Opus-derived key would flatter it).
- **Fable silently substitutes Opus at medium/high effort** → a panel naming Fable:high/medium believes it ran Fable but got Opus, corrupting both the diversity assumption and any cost accounting. Mitigation: pin Fable finders to `low`; documented in D7 + a gotchas file so composition can't rely on a broken tier.
- **Measuring cross-vendor value with a single-vendor key is invalid** → the validation harness (D5) pools all vendors' findings before adjudicating "real"; a key derived from one vendor's reviews cannot credit a foreign model and will always rank a same-lineage tier above it (the D5-findings result).
- **More findings → more false positives** → the D2a single-finder adversarial gate + foreign-never-auto-fixes guard the auto-fix path.
- **Sandbox can't read `~/.argus/profiles`** → `profile_resolve` runs daemon-side; the sandbox agent only calls the MCP tool.
- **Skill behavior isn't Go-CI-tested** → the argus-Go pieces (`profile_resolve`, capture primitive, panel validation) get Go tests; the skill's contract is recorded as a capability (specs are local docs, per project policy) and proven by the validation harness.

## Migration Plan

- Rebase the work onto `argus/model-tiering` (done); merge target is `argus/model-tiering`, never master (coord advances the integration branch). No per-chunk GitHub PR.
- argus-Go: add `profile_resolve` MCP tool; add the injected panel-grammar validator to `internal/profiles`. Direct edits; tests alongside; archive-in-PR. (The foreign-reviewer-capture path is DEFERRED per D-SCOPE — see the follow-up chunk.)
- Content: fill the `[panel]` blocks in `docs/profiles/{default,lean,customer_grade}.toml`.
- Skills: add `hera-spawn-review` (the glue) and the default `hera-review` instruction (+ shipped lens instructions) under the argus repo `.claude/skills/`.
- Rollback = revert the change; no external state.

## Alternatives considered

- **Upgrade ralph / Hybrid-with-ralph-orchestrating** — rejected (D1): ralph's orchestration doesn't match; couples ralph to hera; ground truth killed the premise.
- **Env injection for the panel/rigor body** — rejected (D6): env can't carry arrays/blocks; 3a already ruled it out for the rich body.
- **Repo-committed review config file** — rejected: reinvents the profile system that already exists; the profile is the config.
- **Scrape raw scrollback / findings-file for capture** — rejected (D3): brittle / races teardown vs sentinel extraction over the persisted log.
- **New MCP endpoint per config need** — folded into the single `profile_resolve` the seam already reserved.

## Discovery findings

- `internal/profiles` (on `argus/model-tiering`): `Loader.ValidateName(name, cfg, knownModels) (*Profile, []error)`; `Profile{Archetype map[string]Archetype, Rigor, Panel map[string]any, Name, Source}`; `Panel` is already `map[string]any` (opaque). `Project.ResolveProfileName()` gives the bound name; resolution precedence is `task.Profile > project binding > "default"` (`agent.resolveProfile`), fail-open.
- Env already injected at spawn (3a D-INJECT): `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, `ARGUS_MODEL`.
- Session log: `agent.SessionLogPath(taskID)` = `~/.argus/sessions/<taskID>.log`, full scrollback, survives teardown.
- Credential env-map (1a): codex backend seeds `OPENAI_API_KEY ← HERA_OPENAI` via `Backend.EnvVars`, applied at `agent.go:512-522`.
- Canonical archetypes include `review` and `security_review` — the archetypes whose resolved profile drives the panel.

## Acceptance criteria

**`profile_resolve` MCP tool:**

- It should resolve cwd → project → bound profile and return the full resolved body (archetype/rigor/panel) as structured JSON.
- It should honor the per-spawn `task.Profile` override over the project binding over `default`.
- It should accept an explicit profile-name argument (for testing) that bypasses cwd resolution.
- It should return a structured "no profile resolved" result (with errors), not a hard error, when the profile is missing or invalid.
- It should return archetype entries verbatim without collapsing them to single scalars.

**Panel grammar validation:**

- It should accept a `[panel]` with a non-empty `finders` list of known reviewer ids.
- It should reject a `[panel]` whose `finders` is empty or names an unknown reviewer id.
- It should reject a lens with an empty `name` or an unknown `model`.
- It should validate the panel via an injected validator without `internal/profiles` importing the review package.

**Foreign-reviewer-capture (DEFERRED — see D-SCOPE, follow-up chunk; its delta requirement was removed from this change, kept here as design-of-record):**

- It should wrap a reviewer-mode prompt so the foreign agent emits its review between the sentinels.
- It should extract the sentinel-delimited block from the session log and expose it as a structured, addressable result.
- It should survive worktree teardown (the log is outside the worktree).
- It should report a structured "no review captured" outcome when sentinels are absent.

**`hera-spawn-review` glue + review instruction (proven by the validation harness, not Go CI):**

- It should compose the panel from the resolved profile's `[panel]` block, falling back to a built-in default when no profile resolves.
- It should inject the configured `review_skill`/`review_instruction` into each broad finder, defaulting to the shipped `hera-review` instruction when none is specified.
- It should run broad finders (Opus/Fable in-session, codex via capture) plus the configured lenses (each with its lens instruction).
- It should synthesize into the canonical schema with provenance and cross-vendor confidence voting.
- It should require a single-finder finding to pass adversarial verification before `[AUTO-FIX]`, else downgrade it to `[QUESTION]`.
- It should never let a foreign model make the final `[AUTO-FIX]` call.
- It should run a fix-verification phase and re-review after fixes until clean.
- It should let a user-supplied `review_skill` override the default without touching the glue.

**Validation harness:**

- It should build a **vendor-neutral** answer key by pooling every vendor's findings and adjudicating each from code evidence — never scoring against a key derived from a single vendor's reviews.
- It should give every reviewer the **same code packet** under one brief (scope symmetry).
- It should score the panel against that key and report per-finder + union catch-rate, precision, unique-catch (with provenance), and cost, showing each vendor's finds and the union side by side.
- It should degrade to the captured codex reports when the live codex leg cannot authenticate, and flag it.

## Open Questions

- Exact sentinel strings and the structured-result home for capture (`task.Result` field vs a dedicated `task_meta` key vs an artifact) — settle in the capture impl node; leaning a dedicated result field keyed to the task.
- Whether panel-grammar validation surfaces at `validate`/Settings time (loud surface) in addition to consumption time — **RESOLVED (3b fixup, 2026-07-06):** `cmd/argus validate` now injects the real `internal/review.NewValidator(cfg)` (the same validator the daemon/MCP consumption callers inject), so `argus validate` reports a malformed `[panel]` instead of passing it clean — proven by `TestRunValidate_MalformedPanelReported` (`cmd/argus/validate_test.go`). The TUI Settings/plan-view tiering readout (`internal/tui/hera_tiering.go`) still passes `nil` — it's a display-only projection (renders applied model/effort for the plan view; not an explicit validation action the operator invokes), left as a judgment call rather than folded into this fixup. The deeper decoupling this doesn't address — "a `[panel]`-only error shouldn't fail-open the *entire* profile's archetype/rigor tiering at spawn, just the panel" — is a separate, more surgical follow-up, not built here; `profile_resolve`'s fail-open contract is deliberately unchanged.
