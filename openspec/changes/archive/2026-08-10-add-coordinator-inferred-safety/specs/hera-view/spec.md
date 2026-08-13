## MODIFIED Requirements

### Requirement: Merge-safety review popup

The Hera view SHALL provide a review popup with two sections — **NOT-SAFE** listed first, then **SAFE** — each row showing its task name and, for NOT-SAFE rows, the specific reason it wasn't confirmed merged. The popup offers three actions: **Clean safe** (the default-selected action, acting only on the SAFE section), **Clean all** (acting on every listed task, an explicit override the operator reaches only after seeing the NOT-SAFE list), and **Cancel** (no-op). Both Clean actions act immediately — this popup has no separate later step.

The popup is used by exactly two entry points: the single-role nuke (candidate set of one, Tier A only) and the global Cleanup action (candidate set of the full stuck-task backlog across all projects, Tier A and Tier B). It is NOT used by cascade nuke or clear-archived, which keep their own aggregate-count confirms.

Within each section, candidates are further grouped by their originating Hera coordinator/orchestrator: a candidate whose task most recently held a Hera role renders nested under a group header bearing that orchestrator's name, alongside every other candidate in the same section that shares it; a candidate whose task never held a Hera role (or held one for a different reason entirely) renders as a flat top-level row, never nested under a fabricated header. Grouping is a sub-structure within each NOT-SAFE/SAFE section, not a replacement for that ordering.

A SAFE row whose verdict was produced by the coordinator-inferred fallback (see the `merge-safety` capability) SHALL render a distinct trailing annotation marking it as inferred rather than directly confirmed. Every other SAFE row's rendering SHALL be unaffected — the annotation is additive and applies only to this one case.

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

#### Scenario: A coordinator-inferred SAFE row is visibly annotated
- **WHEN** a SAFE row's tier is the coordinator-inferred fallback
- **THEN** that row renders a distinct trailing annotation marking it as inferred, distinguishing it from a directly-confirmed SAFE row

#### Scenario: A directly-confirmed SAFE row is unaffected
- **WHEN** a SAFE row's tier is `local-ancestor` or `merged-pr` (a directly-confirmed verdict)
- **THEN** that row renders exactly as before this change, with no annotation
