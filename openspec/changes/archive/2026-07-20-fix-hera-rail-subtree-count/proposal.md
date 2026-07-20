## Why

A top-level coordinator's rail header renders a right-aligned `(N)` badge
computed by `liveRoleCount`: it counts the orchestrator's OWN direct roles
where `Live == true`, excluding only the coordinator. This miscounts in two
ways, confirmed against a live repro (`sherlock-land-pr-45`, whose header
showed `(10)`):

- **Archiving a role never ends its binding.** Hiding a worker into the
  coordinator's greyed-out Archive bucket (the `a` key /
  `db.ArchiveHeraRole`) only stamps `hera_roles.archived_at`; the role's
  `hera_binding.ended_at` is untouched (bindings only end via explicit
  teardown: nuke/delete/reparent/detach). So an archived role keeps
  `Live == true` forever and still counts toward the badge — 9 of
  `sherlock-land-pr-45`'s reported `(10)` were roles already sitting in its
  own `Archive (9)` bucket, not current work.
- **The badge never recurses into nested (bridged) sub-coordinators.** A
  worker row that bridges to a child orchestrator (a sub-team spawned mid-run)
  is itself counted once, but that child's own workers — and its own Archive
  bucket — are invisible to the parent's badge entirely.

The result is a number that matches neither "how many agents are genuinely
live under this coordinator" nor "how many rows would I see if I expanded
every fold and counted them myself" — it's an artifact of a binding-lifecycle
detail (`ended_at` staying null) that has nothing to do with what's rendered.

## What Changes

- Replace `liveRoleCount` (single-level, `Live`-gated) with
  `Model.SubtreeAgentCount(orchID)`: it walks the same `BridgeSubtree` used by
  the `Ctrl+D` cascade and `C` clear-archive, and counts every non-coordinator
  role (worker rows, including bridging sub-coordinator rows) across every
  orchestrator in that subtree — archived (Tier-1 hidden) roles included,
  liveness ignored entirely.
- A NUKED role/orchestrator can never inflate the count: `BuildModel` already
  excludes both from the `Model` outright (defense-in-depth, matching the
  existing "Tier-2 EOL" guard), so a role whose on-disk workspace was
  genuinely torn down is never counted — only what's still visibly present in
  the rail (live or archived-but-not-nuked) is.
- Each orchestrator's own coordinator role stays excluded (folded into its
  header); a nested sub-coordinator is counted exactly once via its parent's
  bridging worker row, never doubled against its own coordinator role in the
  child orchestrator (the same underlying task).
- No new keybinding, no new tool, no schema change — a rendering-only fix to
  one existing badge.

## Capabilities

### Modified Capabilities

- `hera-view`: the "Orchestrator and role row rendering" requirement's `(N)`
  badge semantics change from a single-level live-role count to a whole-
  subtree, archive-inclusive, liveness-agnostic agent count.
