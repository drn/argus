## Context

`HeraPage.doRefresh` builds a model, then `Model.AnnotateRoles(tierResolver)` calls `App.resolveHeraTier` once per role. Profiling with a 200-role benchmark (15.2ms and 133k allocations per build):

- ~96% of the time is profile loading: `os.Stat` in `Loader.locate` (57%), TOML decode (39%). The tiering `ValidateName` and `agent.ResolveModel`'s own `resolveProfile` each load the same profile.
- `db.Config()` costs 59µs per call on a copy of the real database, about 12ms per 200 roles.
- One `db.Get(task)` per bound role (small).

Constraints from the gotchas: no shared task pointers across goroutines (reverted attempt 1); no bare TTL cache and no cache that `New()`'s startup refresh can poison (reverted attempt 2); the resolver must be safe on the background build goroutine under `-race`.

## Goals / Non-Goals

**Goals:** resolve once per build; identical output; no new shared state; settings changes visible on the next build.

**Non-Goals:** changing `agent.ResolveModel` or the daemon spawn path; changing the rebuild gate; caching across builds.

## Decisions

1. **Build-scoped memo, not a cross-build cache.** A factory returns a fresh closure per build; all memo maps live in that closure. Nothing outlives the build, so there is no invalidation signal to get wrong and no cross-goroutine sharing (each build owns its closure). This is the same freshness as today.
2. **Config snapshot once per build.** `Config()` is called when the factory runs; roles reuse it. `ContextPercent` and project/profile lookups read the snapshot.
3. **Memoize the tiering profile validation** keyed on `(profileName, repoDirKey)`, where `repoDirKey` is empty when `<worktree>/.argus/profiles` does not exist (one `os.Stat` per role instead of several stats plus a decode) and the directory path otherwise. Roles with an in-repo profile directory keep their own key, preserving in-repo precedence.
4. **Memoize `agent.ResolveModel`'s result** keyed on `(task.Model, task.Profile, task.Backend, task.Project, task.Archetype, repoDirKey)` plus the resolved backend name, rather than changing `agent`. Roles that share those inputs share the result.
5. **Seam shape:** `SetTierResolverFactory(func() func(*RoleView))`; `doRefresh` prefers the factory when set. Keeps the `page.go` diff small so it merges cleanly with the off-thread refresh work.

## Risks / Trade-offs

- A role's task or the profile file changing mid-build is not observed until the next build; builds are milliseconds long and this matches today's per-role read-once semantics.
- Memo key omissions would return a wrong model; a test pins that roles differing in any key field resolve separately.
