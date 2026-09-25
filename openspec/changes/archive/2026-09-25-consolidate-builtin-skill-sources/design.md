## Context

The repository currently has two skill trees with overlapping names:

- `internal/skills/builtin/` — ten `go:embed`-ded skills, enumerated
  generically and materialized for Argus-launched Claude, Codex, Pi, and
  OpenCode children.
- `.agents/skills/` (also exposed as `.claude/skills/`) — nine overlapping
  copies plus the project-only `update-agent-models` maintenance skill.

Six overlapping bodies are byte-identical. The remaining bodies have diverged:

- `hera` lost blocking `hera_inbox(timeout_seconds=...)` guidance in the project
  copy, while the embedded copy lacks the role-evidence gate, `hera_revive`,
  worker `archetype`, diligence-profile environment guidance, and recycle-safety
  guidance.
- `hera-plan` lost the gater-provided fan-in branch map in the project copy,
  while the embedded copy lacks the coordinator-binding gate and plan-node
  `archetype` guidance.
- `argus-resolve-model` has a small two-way editorial difference, including a
  typo in the project-only cross-reference.

`hera-spawn-review` also contains a real dependency on the mirror: it reads
`.claude/skills/<review_skill>/SKILL.md` and the analogous lens paths. The
shipped `default` panel uses `hera-review`, and `customer_grade` uses
`hera-review-test-adversary`, so a blind deletion would make the panel stop.

## Goals / Non-Goals

**Goals:**

- Keep exactly one in-repo body for each Argus builtin.
- Preserve all useful guidance from both sides of the current drift.
- Make builtin lookup work from the session-scoped skill catalog on every
  supported backend.
- Preserve project-specific skills and user-configured custom review skills.
- Make accidental reintroduction of builtin mirrors fail in tests.

**Non-Goals:**

- Do not provision `update-agent-models` to ordinary Argus sessions. It is a
  repository-maintenance workflow, not runtime Argus machinery.
- Do not change project/user/plugin skill discovery or the `/api/skills`
  contract; the repository simply will not contain project mirrors of builtins.
- Do not add a new global `~/.agents/skills` installation path or broaden
  builtin visibility to ordinary manually-started CLI sessions.
- Do not change the existing Claude/Codex/Pi/OpenCode materialization
  mechanisms.
- Do not add a new cross-backend environment variable solely to locate a skill
  manifest when each backend already advertises its native skill catalog.

## Decisions

### D1 — `internal/skills/builtin/` is the sole in-repo builtin source

Every builtin body lives at
`internal/skills/builtin/<name>/SKILL.md`. The repository does not carry a
same-named mirror under `.agents/skills/` or `.claude/skills/`.

`.agents/skills/` remains a normal project-skill location. The retained
`update-agent-models` skill demonstrates that distinction: it is project-only,
not part of the embedded runtime bundle. `.claude/skills` continues to symlink to
`.agents/skills`, so deleting the overlapping directories is sufficient for both
native project discovery and Argus's project-skill autocomplete.

This direction matches the existing session-scoping design: builtins are copied
from the binary into each Argus-launched child's managed discovery location, not
installed globally for unrelated tools.

### D2 — Reconcile drift semantically, not by choosing one whole file

The canonical bodies are updated as unions with conflict resolution:

- Keep blocking `hera_inbox` and current worker-wait guidance.
- Add the role-evidence gate, `hera_revive`, worker `archetype`, diligence
  profile env guidance, delegate-with-prejudice guidance, and recycle-safety
  notes from the project copy.
- Keep the gater-provided fan-in branch map and blocker-branch self-rebase
  guidance in `hera-plan`; add the coordinator gate, profile resolution, and
  per-node archetype selection.
- Add the `argus-resolve-model` → `hera-plan` authoring-side cross-reference with
  the typo corrected.
- Remove the obsolete `argus-schedule` note claiming a repository mirror must be
  kept byte-identical; the external dotfiles twin is outside this repository's
  source-of-truth contract.

A regression test enumerates `BuiltinItems()` and fails if any embedded name
also exists beneath either repository project-skill root (`.agents/skills/` or
`.claude/skills/`), using `Lstat` so a dangling mirror is caught too. This is a
source-layout invariant, so the test intentionally reads the repository tree
while all materialization behavior remains hermetic.

### D3 — Resolve review instructions from the current session's catalog

`hera-spawn-review` resolves a named `review_skill` or lens skill by its exact,
case-sensitive ID from the current child session's native skill catalog. It uses
the session's native skill loader when available; otherwise it reads the exact
`SKILL.md` path advertised by the selected catalog entry. It does not assume the
current repository has project-skill copies or manually prefer a managed builtin
over a same-ID project override.

This gives one instruction contract to all supported delivery mechanisms:

- Claude, Pi, and OpenCode receive the Argus-managed skill directory through
  their existing per-session hooks.
- Codex receives the same bodies in its Argus-only `CODEX_HOME`.
- The current backend's native catalog precedence selects among project, user,
  plugin, and Argus-managed definitions. A project-scoped same-ID skill is an
  intentional user override when that catalog selects it; custom names remain
  selectable without a repository-specific path convention.

A configured ID absent from the current catalog, an unresolved visible
collision, or an unreadable selected manifest remains a loud failure before
spawning any finder or lens; the workflow never silently substitutes the default
or a different lens. A focused source-contract test pins exact-ID resolution,
the pre-spawn failure ordering, and the absence of any project-local manifest
path. A live OpenCode smoke separately loads both `hera-review` and
`hera-review-test-adversary` through the same inline `skills` configuration used
by Argus child launches.

### D4 — Source-layout regression coverage accompanies the prose-only behavior

Because the affected artifacts are `SKILL.md` files, ordinary Go behavior tests
cannot observe their lookup instructions. Two focused tests provide the missing
guard:

1. Every name returned by `BuiltinItems()` has no same-named directory or
   dangling symlink under either project-skill root.
2. `hera-spawn-review` instructs exact-ID resolution from the current session
   catalog, completes that step before finder spawning, preserves project-scoped
   override semantics, and contains no project-local review/lens manifest path.

These tests are intentionally narrow. Existing materialization tests continue
to cover the runtime delivery path across backends.

## Risks / Trade-offs

- **Project autocomplete loses builtin names.** Intentional: project autocomplete
  represents repository/user/plugin choices, while builtins are already injected
  into the actual Argus-launched session. A session should not need to select a
  builtin again in its prompt.
- **Ordinary manual repository sessions lose builtin discovery.** Intentional and
  consistent with the existing vendor-scoped/session-scoped delivery decision.
  `update-agent-models` remains available to manual repository work.
- **Catalog wording is procedural rather than executable.** The panel is itself
  a skill, so its source contract is the behavior. The focused test prevents the
  known project-path regression; a live OpenCode child smoke verifies that the
  exact default and corrective-lens IDs load through Argus's existing inline
  skills configuration after the mirrors are gone.
- **Custom review skills still work.** They are resolved by exact ID through the
  current backend's native source precedence rather than by a repository-specific
  hard-code; a project-scoped same-ID definition remains an intentional override
  when the catalog selects it.

## Migration Plan

No persisted-data or user-state migration is required. The embedded skills are
rewritten on the next Argus launch from the reconciled canonical bodies.
Project-local duplicates disappear from the next repository checkout; already-open
sessions may retain their startup skill catalog until restarted.

## Acceptance Criteria

- `internal/skills/builtin/` contains the only in-repo body for every builtin.
- `.agents/skills/` contains `update-agent-models` but none of the ten embedded
  builtin names.
- The canonical `hera`, `hera-plan`, and `argus-resolve-model` bodies preserve
  all unique guidance from both pre-change copies.
- `hera-spawn-review` resolves named review/lens instructions from the current
  session catalog and fails loudly when a name is absent.
- Existing builtin materialization tests and the full `make pre-pr` gate pass.
