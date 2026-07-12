# Design — improve hera node descriptions

## Context

Two Explore passes + a live-DB read + a two-arm empirical probe established the
facts (see proposal.md). The pollution is entirely upstream of argus: the
coordinator prepends the org policy into the tool's `prompt` arg, and argus
faithfully stores + renders it. The harness injects the org policy into every
spawned session on its own, so the prepend is a redundant duplicate.

## Decisions

### D1 — Fix the input, not the output (no stripping)

Rejected: detect + strip a leading policy block before rendering. Two fatal
problems, both raised by the user:

- It bakes Thanx-specific policy text (or a heuristic keyed on it) into argus
  code — argus is a general tool; other users do not inject this policy.
- It assumes line 1 is org boilerplate, which is false for anyone not using the
  Thanx injection.

Chosen: keep the stored prompt clean at the source. The tool `prompt` param
descriptions and the hera skill instruct coordinators to pass the mission only.
The worker still receives the policy — from its own session's harness injection,
proven independent of the prompt.

### D2 — No new "mission" field

Rejected: add a `mission`/`tldr` column to `hera_roles` and render that. The user
directed that the prompt itself is the mission ("just write the prompt to the DB…
the message the coord is going to send to the worker"). A parallel field is extra
schema + plumbing + a second thing to keep in sync, for no gain once the prompt
is clean. The prompt IS the description.

### D3 — Contract via tool-param documentation, not enforcement

argus cannot (and should not) parse a prompt to reject "policy-looking" content —
that reintroduces D1's stripping problem. The guardrail is the documented tool
contract: the `prompt` param description states the mission-only rule and the
"you don't need to prepend the policy — the worker gets it from its own session"
rationale. This reaches every coordinator at the call site, not just those who
read the skill. argus keeps storing the prompt verbatim (unchanged behavior).

### D4 — Detail render shows the first few lines, wrapped, policy-agnostic

`nodeHeaderLines` currently emits 4 fixed lines (Name / Status / desc / Feeds)
with `desc = firstLine(prompt)`. Change `desc` to the first N (≈3) non-empty
lines of the prompt, wrapped to the pane width; the header grows accordingly and
the layout accommodates the extra rows. No line is skipped or classified — a
clean prompt shows its opening mission lines; a still-polluted prompt shows its
opening policy lines (the coordinator's problem, fixed by D1/D3 at the source).
Empty prompt keeps the existing `"(no description)"` placeholder.

## Risks

- **Existing planned nodes stay polluted.** Nodes authored before this change
  already have the policy in their stored prompt; this change does not rewrite
  them (no stripping). Acceptable per the user ("it's ok if it's not defined
  until the worker is live") — new/re-authored nodes read clean.
- **Header height growth.** Showing ≈3 desc lines lengthens the node header;
  the plan-view detail region must accommodate (wrap + additional rows) without
  overflowing the roster/graph split. Covered by a render test.
