---
name: update-agent-models
description: >-
  Refresh the curated Claude and Codex model lists (internal/agent.KnownModels and the seed
  diligence profiles) to whatever is currently real, and propagate that change through every
  place in the codebase that depends on it. Use when a task launch errors on a "default" or
  pinned Codex model, when a new-task model picker shows a model an operator says no longer
  exists, when OpenAI or Anthropic ships a model-lineup rename, or as a periodic staleness check.
  NOT a background or scheduled skill: model-id remapping and tier assignment are judgment calls
  that must run with a human present, never on an unattended loop.
disable-model-invocation: true
---

# Update Agent Models

Codex retires and renames its model identifiers on every release (its lineup went from
gpt-5-codex/gpt-5 to gpt-6-sol/gpt-6-astra/gpt-6-luna/gpt-5.6-sol/gpt-5.6-terra/gpt-5.6-luna/gpt-5.5
in one jump). Claude's CLI aliases (opus/sonnet/haiku/fable) are deliberately alias-indirected and
rarely change, but this skill checks both. `internal/agent.KnownModels(command)` is the single
source of truth both the new-task model picker and `backendAllowsModel`'s strict validation read —
when it goes stale, a task pinned to (or profile-resolved onto) a retired model silently drops its
`--model` flag or fails outright against the real CLI. This skill is a repeatable, judgment-driven
fix for that drift, not a one-time patch: expect to run it again next time either vendor renames
its lineup.

## Context

- Current codex entries in KnownModels: !`grep -A1 'IsCodexBackend(command):' internal/agent/agent.go | tail -1`
- Current claude entries in KnownModels: !`grep -A1 'IsClaudeBackend(command):' internal/agent/agent.go | tail -1`
- Seed profiles with codex model entries: !`grep -l 'codex = ' internal/profiles/seeds/*.toml 2>/dev/null | head -10`
- Local codex CLI configured default (if present): !`grep -m1 '^model = ' ~/.codex/config.toml 2>/dev/null | head -1`
- Local codex CLI's known model-availability keys (if present): !`awk '/\[tui.model_availability_nux\]/{f=1;next}/^\[/{f=0}f' ~/.codex/config.toml 2>/dev/null | head -10`
- Model ids seen in recent codex session logs (if present): !`find ~/.codex/sessions -name "rollout-*.jsonl" 2>/dev/null | sort | tail -8 | xargs grep -ohE "\"model\":\"[^\"]+\"" 2>/dev/null | sort -u | head -20`
- Claude CLI help (confirms current alias set, if the CLI is on PATH): !`claude --help 2>/dev/null | grep -iE "opus|sonnet|haiku|fable" | head -10`

## Your task

### Step 1: Confirm this is warranted

If the Context block above shows no codex config, no session logs, and no `claude --help` output,
and the user gave no other reason to believe the lists are stale (no launch error, no explicit
report of a renamed model), stop and tell the user you have no evidence of drift and ask them to
either paste the codex CLI's `/model` picker output or confirm which model failed.

### Step 2: Establish the real current model lists

**Claude:** the four stable aliases (opus, sonnet, haiku, fable) rarely change. Only touch the
Claude side of `KnownModels` if the user names a new/removed/renamed alias, or `claude --help`
output above visibly disagrees with the current four.

**Codex:** there is no reliable `codex models list` subcommand. Cross-check, in order of trust:

1. The Context block's codex config default (`model = "..."`) and `model_availability_nux` keys.
2. The Context block's recent session-log `"model":"..."` values.
3. If neither source is present (no local codex install, or a broken one), ask the user to launch
   `codex` interactively, open its `/model` "Select Model and Effort" picker, and paste or
   screenshot the list. Do not guess a `--model` slug from a display name alone (a picker showing
   "GPT-6-Sol" does not confirm the flag value is `gpt-6-sol` — corroborate against config/session
   data whenever it exists, and treat the picker text as your only source only when nothing else
   is available).

If the sources disagree, stop and ask the user which is authoritative rather than picking one.

If the sources agree the current `KnownModels` codex list is still accurate, tell the user nothing
is stale and stop here — do not touch any file.

### Step 3: Decide the tier remapping

The seed profiles pair each archetype's Claude model with a Codex model in the same line
(`models = { claude = "...", codex = "..." }`), encoding a HEAVY/LIGHT tier: opus/fable-paired
archetypes get the heavier Codex model, sonnet/haiku-paired archetypes get the lighter one. When
the real model list changes, map:

- The old heavy-tier id to whichever new id is positioned/described as the frontier or most
  capable model (e.g. "frontier intelligence for the most demanding work").
- The old light-tier id to whichever new id is positioned/described as the default or workhorse
  model (e.g. "workhorse model for coding and everyday work" — often also the CLI's own new
  configured default).

Do not substitute alphabetically or by list position alone — read each candidate's description and
match it to the tier it is replacing.

### Step 4: Apply the update across every touch-point

Update every one of the following together. Missing one leaves the repo internally inconsistent —
this is the most common failure mode of this change, so treat it as a checklist, not a suggestion:

1. **`internal/agent/agent.go`** — `KnownModels(command)`'s return value for the codex case (and
   the claude case, if Step 2 found a real change there). This is the source of truth everything
   else in this list either mirrors or validates against.
2. **`internal/profiles/seeds/*.toml`** — every `archetype.*.models.codex = "..."` entry in every
   seed file that has one (as of this writing: `default.toml`, `customer_grade.toml`; `lean.toml`
   currently has none). Apply the Step 3 tier mapping.
3. **Every test that asserts the REAL `KnownModels`/`BackendModels`/`ValidateName`/`Validate`
   output, or validates the real seed files, against the OLD literal model strings.** Find them
   with a repo-wide search for the old ids across `*_test.go` and `*.toml`, then classify each hit:
   - If it is a hardcoded expectation of `KnownModels("codex ...")` or `BackendModels(...)`'s
     actual return value (e.g. `internal/agent/models_test.go`,
     `internal/tui/newtaskform_test.go`), update it to the new list.
   - If it is a `task.Model`/`backend.Model` literal fed into `BuildCmd` and asserted to survive
     into a `--model` flag (e.g. `internal/agent/agent_test.go`,
     `internal/agent/profile_resolve_test.go`), update it to a valid new id — the old one will now
     fail `backendAllowsModel` and silently drop the flag, breaking the assertion.
   - If it is a test-local `testKnownModels`/mock function whose doc comment says it "mirrors"
     `agent.KnownModels` (e.g. `internal/profiles/validate_test.go`,
     `internal/review/seeds_test.go`) — these back tests that validate the REAL embedded seed
     files (look for `TestSeeds_EachValidates`, `TestEmbeddedSeeds_EachValidates`, or a shipped-
     profile panel-grammar test), so they must track the new list or those tests will fail the
     moment Step 4.2's seed edits land.
   - If it is a profile TOML snippet embedded directly in a test body and fed through the real
     validation path (e.g. `internal/mcp/profiles_test.go`), update it to a valid new id.
   - If it is a plain TOML-parsing fixture with no model-validation call anywhere near it (e.g.
     `internal/profiles/load_test.go`, which only exercises `Loader.Load`), leave it alone — it is
     deliberately decoupled test data, and touching it only adds noise.
4. **`internal/mcp/server.go` and `internal/mcp/hera.go`** — illustrative example strings in MCP
   tool-description doc comments (e.g. "e.g. 'gpt-5'"). Cosmetic, but keep them current so an
   agent reading the tool schema sees a real example.
5. **`internal/pricing/rates.toml`** — if its comments name example codex aliases, refresh them.
   Do not add actual pricing rows for codex unless the user asks — codex is deliberately unseeded
   there for reasons unrelated to this skill.
6. **Prose and skill docs that quote a model id as an example rather than reading it from
   `KnownModels`.** Search repo-wide for the old ids (not just `*.go`/`*.toml`) — the manual refresh
   that prompted this skill's creation missed every one of these on its first pass:
   - `README.md` — the backends-config reference table's `models` row and the diligence-profile
     "Model-naming convention" section both restate the current list; prefer rephrasing to point at
     `agent.KnownModels` over re-quoting the literal ids where the prose allows it, since a pointer
     cannot go stale the way a restated list can.
   - `context/knowledge/gotchas/tasklist-ui.md`'s model-selector bullet.
   - **`internal/skills/builtin/hera/SKILL.md`** — the canonical builtin's `model` field's
     per-backend example. There is no project-local mirror to update.
   - **`internal/skills/builtin/argus-resolve-model/SKILL.md`** (renamed from
     `resolve-archetype-model`) — the worked example's
     `foreignFlagshipHints` substring list (used to decide whether a foreign backend's model name
     should substitute to `opus` for in-session dispatch) hard-codes a Codex fragment. Pick a new
     fragment that actually appears in the new frontier-tier id's description/positioning (not by
     list position), same judgment call as Step 3's tier remapping — there is no guarantee any
     future Codex naming scheme shares a lexical "flagship" marker at all, so re-derive this by
     reading the new lineup's descriptions each time rather than assuming a pattern holds.
7. **`internal/daemon/surface.go`** — the step most likely to be skipped, and the one that will
   fail CI if it is. `internal/agent/agent.go` is declared in `SupervisorSpawnPaths`, so editing it
   changes the SHA-256 recorded in `SpawnSurfaceDigest`. Changing `KnownModels`'s codex list is a
   spawn-observable behavior change (it changes what `--model` value a newly spawned session
   receives), so:
   a. Bump `SupervisorSpawnSurface` by one and add a new history bullet to its doc comment in the
      same style as the existing entries, describing what changed and why.
   b. Run the guard test: `go test ./internal/daemon/... -run TestSupervisorSurfaceDigest`. Let it
      fail — its failure message prints the newly computed digest.
   c. Paste that computed digest into `SpawnSurfaceDigest`. Never hand-compute or guess it.
   d. Re-run the same test and confirm it now passes.
8. **`context/knowledge/gotchas/misc.md`** — add or refresh a bullet under the
   "## Model Selection (--model injection)" heading noting the old ids, the new ids, and that this
   skill is the fix procedure. Match the file's existing bullet style: a bolded one-line rule
   followed by one to three sentences of context, cross-referencing the touched file paths.

### Step 5: Verify

The sandboxed Go build cache at the default location may not be writable; redirect it first:

    export GOCACHE=<a writable scratch directory>

Then run, in order, stopping to fix and re-run on any failure:

1. `go build ./...`
2. `go vet ./...`
3. `goimports -l .` — must produce no output.
4. `golangci-lint run --new-from-rev=origin/master` — must report zero issues.
5. `go test ./...` — the full suite must be green. A failure in
   `internal/daemon` (`TestSupervisorSurfaceDigest`) or `internal/profiles`
   (`TestSeeds_EachValidates`, `TestEmbeddedSeeds_EachValidates`) almost always means Step 4 was
   applied only partially — treat it as a missed touch-point, not a flake, and go back to Step 4
   before re-running.

If you are stuck on the same failure after three fix attempts, stop and report the situation to
the user rather than continuing to retry.

### Step 6: Report

Summarize the old-to-new mapping and the list of files changed. Do not commit, push, or open a
pull request unless the user asks — this skill's job is to land a correct, verified working-tree
change, not to ship it.
