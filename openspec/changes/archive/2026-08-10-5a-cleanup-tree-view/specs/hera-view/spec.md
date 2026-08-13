## MODIFIED Requirements

### Requirement: Merge-safety review popup

The Hera view SHALL provide a review popup with two sections — **NOT-SAFE** listed first, then **SAFE** — each row showing its task name and, for NOT-SAFE rows, the specific reason it wasn't confirmed merged. The popup offers three actions: **Clean safe** (the default-selected action, acting only on the SAFE section), **Clean all** (acting on every listed task, an explicit override the operator reaches only after seeing the NOT-SAFE list), and **Cancel** (no-op). Both Clean actions act immediately — this popup has no separate later step.

The popup is used by exactly two entry points: the single-role nuke (candidate set of one, Tier A only) and the global Cleanup action (candidate set of the full stuck-task backlog across all projects, Tier A and Tier B). It is NOT used by cascade nuke or clear-archived, which keep their own aggregate-count confirms.

Within each section, candidates are further grouped by their originating Hera coordinator/orchestrator: a candidate whose task most recently held a Hera role renders nested under a group header bearing that orchestrator's name, alongside every other candidate in the same section that shares it; a candidate whose task never held a Hera role (or held one for a different reason entirely) renders as a flat top-level row, never nested under a fabricated header. Grouping is a sub-structure within each NOT-SAFE/SAFE section, not a replacement for that ordering.

#### Scenario: Sections are ordered NOT-SAFE then SAFE
- **WHEN** the popup renders
- **THEN** the NOT-SAFE section appears before the SAFE section

#### Scenario: Clean safe is the default-selected action
- **WHEN** the popup opens
- **THEN** `Clean safe` is the initially focused/selected action

#### Scenario: Clean safe acts only on the SAFE section
- **WHEN** the operator chooses `Clean safe`
- **THEN** only the tasks listed under SAFE are cleaned; NOT-SAFE tasks are left untouched

#### Scenario: Clean all acts on every listed task
- **WHEN** the operator chooses `Clean all`
- **THEN** every listed task, in both sections, is cleaned

#### Scenario: Cancel performs no action
- **WHEN** the operator chooses `Cancel`
- **THEN** no task is cleaned and the popup closes

#### Scenario: Candidates sharing an originating coordinator are grouped
- **WHEN** two or more candidates in the same section most recently held a Hera role in the same orchestrator
- **THEN** they render nested under one shared group header bearing that orchestrator's name, not one header per candidate

#### Scenario: A candidate with no originating coordinator renders flat
- **WHEN** a candidate's task never held a Hera role
- **THEN** it renders as a flat top-level row in its section, not nested under any group header

#### Scenario: Grouping does not disturb NOT-SAFE-before-SAFE ordering
- **WHEN** a coordinator's only candidate in the popup is SAFE while another, ungrouped candidate is NOT-SAFE
- **THEN** the NOT-SAFE section (and its row) still renders entirely before the SAFE section (and the coordinator's group within it)

### Requirement: Global Cleanup action for the stuck-task backlog

The Hera view SHALL provide a global Cleanup action, reachable via the Ctrl+K command palette (not scoped to any coordinator/orchestrator), that opens the merge-safety review popup with every task matching the stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding) across ALL projects as its candidate set, classified via both Tier A and Tier B. Choosing a Clean action immediately deletes the chosen scope's task rows, worktrees, and branches, reusing the same guarded deletion primitive the `Ctrl+R` prune-completed flow uses — never a separate, forked deletion path, and never requiring a subsequent manual prune step.

Each candidate's most recent originating Hera orchestrator (if any) is resolved from `hera_roles`/`hera_bindings` — surviving role/orchestrator archive or nuke, since Hera never hard-deletes those rows — and carried through the REST response and the popup's tree grouping (see "Merge-safety review popup"). A task with more than one historical binding across different orchestrators resolves to the most recent by `started_at`.

#### Scenario: Cleanup lists the full cross-project backlog
- **WHEN** the global Cleanup action is opened
- **THEN** the popup's candidate set includes every task matching the stuck-task predicate across every project, not just the currently-selected coordinator's tasks

#### Scenario: First open triggers classification with a visible wait state
- **WHEN** the Cleanup action is opened and candidates exist without a cached classification
- **THEN** the popup shows a scanning/in-progress state until results are ready, rather than appearing empty or frozen

#### Scenario: Clean safe immediately deletes the safe set
- **WHEN** the operator chooses `Clean safe` in the global Cleanup popup
- **THEN** every SAFE-listed task's row, worktree, and branch are deleted immediately, using the same guarded deletion primitive as `Ctrl+R` — no separate later step is required

#### Scenario: Clean all immediately deletes everything shown
- **WHEN** the operator chooses `Clean all` in the global Cleanup popup
- **THEN** every listed task's row, worktree, and branch are deleted immediately, including NOT-SAFE ones

#### Scenario: A task that stopped qualifying is skipped, not errored
- **WHEN** a Clean action processes a task that no longer matches the stuck-task predicate or no longer passes the live-binding guard at the moment of deletion
- **THEN** that task is left untouched and the rest of the batch proceeds normally

#### Scenario: Cascade nuke and clear-archived do not use this popup
- **WHEN** `Ctrl+D` is pressed on a coordinator/orchestrator header, or `C` is pressed to clear a hidden archive
- **THEN** neither opens the merge-safety review popup — both keep their existing aggregate count-based confirm, unchanged mechanics

#### Scenario: A candidate's originating orchestrator survives role/orchestrator archive or nuke
- **WHEN** a candidate's most recent Hera role and its orchestrator have both since been archived or nuked
- **THEN** the candidate still resolves and displays that orchestrator's name — Hera's retained (never hard-deleted) role/binding rows make this possible

#### Scenario: A task with roles in two orchestrators over time resolves to the most recent
- **WHEN** a candidate's task has held Hera roles in two different orchestrators at different times, both bindings since ended
- **THEN** the candidate resolves to the orchestrator of the more recent binding, by `started_at`
