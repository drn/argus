## Context

The archived `namespace-hera-worker-branches` and `fix-coordinator-branch-namespace-collision` changes cover worker namespacing and freshly spawned coordinators. An existing task becoming a coordinator retains its flat branch. All three worker/materialize primitives already share `resolveOrchestratorBranchNamespace`.

## Goals / non-goals

- Unblock workers and planned subcoordinators under any orchestrator whose namespace root is a local branch leaf in the target source repo.
- Preserve all existing branches, task rows, names, API fields, and frontend behavior.
- Avoid schema changes, branch renaming, remote fetches, and changing the generic `CreateWorktree` collision contract.

## Decisions

- Pass the target project into the existing resolver and read local heads with `git for-each-ref`. This sees packed refs and worktrees sharing the same source repository; filesystem checks would miss packed refs. One snapshot avoids a separate process for every fallback attempt.
- Use the sanitized orchestrator name normally. When blocked, try `<trimmed-name>-t<base36-orchestrator-id>-<base36-attempt>`, incrementing the attempt from 1, reserving suffix space within the 30-character sanitizer limit. Candidate selection is deterministic for an orchestrator and repository ref snapshot and repeated spawns retain the same prefix while the blocking leaves remain. No namespace persistence is added: removing a blocking leaf permits later spawns to use the ordinary prefix; already-created branches remain untouched.
- Resolve per target repository, not per coordinator worktree: cross-project workers can face different local refs. Apply equally to ad-hoc workers, planned workers, and subcoordinators using the parent orchestrator. A promoted worker uses this same resolver when spawning its children.
- DB misses retain the empty/flat fallback. Config or Git lookup failures retain the ordinary namespace and log the skipped check; genuine creation errors retain transactional cleanup. Log successful collision resolution with original and selected prefixes.
- Fresh coordinator creation keeps its existing namespace contract. No MCP/API/client changes are required; this is shared server-side spawn behavior.

## Risks / trade-offs

- A branch created after the snapshot can still race the worktree creation. Existing Git errors and transactional cleanup remain authoritative; no cross-process ref locking is introduced.
- A leaf named `argus` blocks every `argus/...` branch and cannot be solved by changing the orchestrator segment. This remains an ordinary Git failure.
- Deleting a blocking leaf can change the namespace of later workers. Persistence and repository-wide namespace ownership are outside this fix; existing branches are never migrated.

## Migration plan

Deploy normally; only newly created branches change. Roll back the binary to restore previous resolution. No data migration.
