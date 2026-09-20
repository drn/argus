## Context

Profiles select one flat model alias per archetype even though task backends have disjoint model identifiers. The profile path checks the selected value for the backend, but an explicit `task.Model` bypasses that check; Hera's `model` parameter writes directly to that override. `profile_resolve` also exposes the flat value with no backend context.

## Goals / Non-Goals

**Goals:**

- Make model selection tunable per archetype and backend.
- Prevent unsupported profile and explicit-override model flags from reaching a backend CLI.
- Give non-Claude consumers a backend-specific profile result.
- Keep established fail-open semantics: an unresolved profile model falls through to the backend default, then to no flag if that default is empty.

**Non-Goals:**

- Preserve the old flat TOML or JSON shape.
- Add a migration shim for user-owned profiles.
- Curate seed aliases for backends without a stable built-in model list.

## Decisions

- Store model choices as a backend-keyed map within each archetype. This supports `claude`, `codex`, and configured backends such as `pi` or `opencode` without expanding the schema for every vendor. The flat `model` field is removed.
- Seed each archetype with Claude and Codex choices. Pi and opencode remain optional map entries because their selectable models are configuration/provider-specific rather than curated by `KnownModels`.
- Resolve the entry keyed by the selected backend's name. An absent or invalid entry is treated as an unresolved profile choice and falls through to that backend's configured default; an empty default results in no `--model`.
- Apply the existing backend allow-list to both profile-derived choices and explicit task overrides. Invalid explicit overrides fall through to the backend default rather than failing task creation.
- Add an optional backend argument to `profile_resolve` and return each archetype's resolved model for that backend. Calls without it retain the full backend-keyed data needed by generic consumers.
- Validate each `models.<backend>` value against that named configured backend's selectable values, using its configured list or its known CLI aliases. Unknown backend keys and unsupported values are validation errors.

## Risks / Trade-offs

- [Existing local profiles become invalid] → This intentional breaking change is documented; seeds are updated and no compatibility reader is retained.
- [A caller omits backend context] → Full backend-keyed data remains available, and skills will explicitly select their Claude/native-dispatch or requested-worker backend.
- [Custom backend has no curated IDs] → Its configured `models` list is authoritative; without one, entries cannot be safely validated and profile resolution falls open.

## Migration Plan

Update embedded seeds, tests, and shipped skills atomically. Users rewrite profile entries as backend-keyed maps. Rollback is a code rollback; no data migration is required.

## Open Questions

None.
