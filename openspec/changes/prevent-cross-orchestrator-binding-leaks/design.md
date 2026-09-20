## Context

Hera currently derives a parent/child relationship from two bindings that happen to share an Argus task ID. The same heuristic drives the TUI rail and the database subtree scan used by `hera_tree_updates`. The TUI `J` detach operation likewise treats every other binding for a coordinator task as a parent link.

The shared dogfood daemon demonstrated that task identity and topology are independent: a task can be active in two orchestrators without either being the other's parent.

## Goals / Non-Goals

**Goals:**

- Persist an explicit parent relation with the owning parent-side bridge role.

- Use that relation for subtree traversal, rail nesting, and `J` detach.

- Preserve intentional TUI re-parenting and protect unrelated bindings.

**Non-Goals:**

- Repair historical live rows; the owner has already repaired the incident row.

- Add capability tokens or change agent-facing topology APIs.

## Decisions

- Store parent, child, and parent-side bridge-role identity in a dedicated relation rather than overloading bindings. Bindings describe membership; the new relation describes hierarchy.

- `ReparentCoordinator` replaces the child's prior relation atomically, then creates one parent-side worker bridge and records it. `DetachCoordinator` removes that relation and only its owned bridge role.

- The model receives explicit links with its existing role data and uses parent-role identity, never a shared task ID, to build rail relationships. This prevents unrelated concurrent orchestrators from nesting.

- Existing inferred links are not migrated. They are ambiguous by definition; only newly explicit re-parent operations create hierarchy.

## Risks / Trade-offs

- [Existing inferred trees become top-level after deployment] → This is intentional: ambiguous historical topology must not grant subtree visibility. Operators can re-parent deliberately with `J`.

- [A failed re-parent leaves partial state] → Create/delete the bridge role and relation in one database transaction.

## Migration Plan

1. Create the relation table and indexes with `CREATE TABLE IF NOT EXISTS`.

2. Release the explicit read/write paths together.

3. Roll back by removing callers; retained relation rows are harmless and remain auditable.

## Open Questions

- None.
