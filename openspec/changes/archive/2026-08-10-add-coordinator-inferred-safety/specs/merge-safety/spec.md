## ADDED Requirements

### Requirement: Coordinator-inferred safety for the global Cleanup pass

The global Cleanup compute pass (`internal/api`'s `runCleanupCompute`) SHALL support a bounded, one-hop fallback tier, `coordinator-inferred`, for a candidate task that classified not-safe via the existing Tier A/B evaluation AND belongs to a Hera orchestrator (per its resolved `StuckTaskCandidate.Orchestrator`). This tier exists specifically for a Hera-descended worker task folded into its coordinator's branch via a plain `git merge`, which never had — and can never retroactively be given — a standalone PR of its own, and is therefore structurally unclassifiable by Tier A or Tier B alone.

For each distinct orchestrator among such not-safe, orchestrator-bearing candidates, the system SHALL resolve that orchestrator's coordinator role's currently-bound task exactly once and classify it via the existing Tier A/B classifier (never via this same coordinator-inference logic — no chain of inference through a grandparent orchestrator). When the coordinator's own verdict is confirmed-safe, every one of that orchestrator's not-safe candidates SHALL be overridden to safe, tier `coordinator-inferred`, with a reason naming the coordinator task and citing its own tier and reason. When the coordinator's own verdict is not-safe, or its task cannot be resolved at all, every candidate under that orchestrator SHALL be left exactly as its own Tier A/B verdict reported — this fallback SHALL fail closed, never treating an unresolvable or not-safe coordinator as an error.

This tier SHALL NOT be produced by any Tier-A-only classification path (the single-role nuke, cascade nuke, or clear-archived flows) — resolving a coordinator's own verdict can require a Tier B network call, which those interactive/synchronous paths must never wait on.

#### Scenario: Coordinator confirmed safe rescues its not-safe workers

- **WHEN** a candidate classifies not-safe via Tier A/B, belongs to orchestrator O, and O's coordinator role's bound task classifies confirmed-safe
- **THEN** the candidate's verdict is overridden to safe, tier `coordinator-inferred`, with a reason naming the coordinator task and its own confirming tier/reason

#### Scenario: Coordinator not-safe leaves the candidate unchanged

- **WHEN** a candidate belongs to orchestrator O, and O's coordinator role's bound task itself classifies not-safe
- **THEN** the candidate's verdict remains exactly its own Tier A/B result — not overridden, not treated as an error

#### Scenario: Unresolvable coordinator task leaves the candidate unchanged

- **WHEN** a candidate belongs to orchestrator O, and O has no resolvable coordinator role/binding (e.g. pruned before this feature existed, or the orchestrator name does not resolve at all)
- **THEN** the candidate's verdict remains exactly its own Tier A/B result, with no error raised

#### Scenario: One coordinator classified once regardless of worker count

- **WHEN** two or more not-safe candidates in the same compute pass share the same orchestrator
- **THEN** that orchestrator's coordinator task is resolved and classified exactly once, and the resulting verdict is applied to every one of that orchestrator's candidates

#### Scenario: The inference is capped at one hop

- **WHEN** a not-safe coordinator task, resolved as the coordinator of orchestrator O, is itself associated with a further ("grandparent") orchestrator whose own coordinator would classify safe
- **THEN** the system does not look up or classify that grandparent orchestrator's coordinator, and O's not-safe candidates are NOT rescued by it

#### Scenario: Never produced by a Tier-A-only path

- **WHEN** the single-role nuke, cascade nuke, or clear-archived flow classifies a candidate
- **THEN** the resulting verdict never carries tier `coordinator-inferred`, since those paths never perform the coordinator-inference lookup
