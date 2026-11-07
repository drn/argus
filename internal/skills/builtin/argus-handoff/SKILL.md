---
name: argus-handoff
description: Generate a handoff prompt to pass context to another agent thread. Use when switching repos, handing off work, or sharing context between agents.
---

# Context Handoff

Generate a structured prompt capturing the current conversation context so it can be pasted into another agent thread.

## Arguments

- `$ARGUMENTS` - Optional. Free-form instructions about what to emphasize or who the target is. May also specify the target Argus project the follow-up task is created in, two ways:
  - `project=<name>` — explicit override, recognized only as a standalone whitespace-delimited token (so the word "project" in a sentence is not parsed as a directive). Used verbatim and **takes precedence** over any cue-anchored target. It is intentionally not validated against the projects list: the explicit `project=` syntax signals deliberate intent, so the user's literal value is honored.
  - A **cue-anchored** target: the first whitespace-delimited token after `to`, `for`, `hand off to`, or `handoff to` — for example `/argus-handoff to keystone this task` or `/argus-handoff this work to keystone`. Compare that token (lowercased, with surrounding punctuation stripped) against the **Argus projects** listed in the Context block. A token is treated as the target **only when it matches a known project**; the routing phrase is then ignored when assembling the emphasis content. If several cue phrases appear, the first one whose token matches a known project wins. A cue-anchored token that matches no known project is never silently used — the skill asks (see step 7). Because `to`/`for` are common words, this validation is what prevents ordinary prose (e.g. "focus on the api changes", "remember to test") from being mistaken for routing: prose only routes if a word right after a cue happens to name a real project, and even then the handoff body is shown for review before any task is created.
  - If no project is specified either way, hand off to the **current project**: derive it from the worktree path (the immediate subdirectory after `~/.argus/worktrees/`, e.g. CWD `/Users/me/.argus/worktrees/dots/foo` → project `dots`); if the CWD is not under that path, use the calling task's own project via `task_get` on the Calling task ID. Only if neither resolves, ask the user before creating the task.
  - `sandbox=enabled|disabled` — standalone whitespace-delimited token. Passed as `sandbox_override` to `task_create` in step 7 to force the follow-up task's sandbox on or off. Omit the field when the token is absent (inherit the project/global default). Any other value: ask the user rather than guessing. `disabled` is rejected by Argus when the calling task is itself sandboxed or unidentified — surface that error verbatim.
  - `no-task` — standalone token that skips Argus task creation entirely (the KB save still runs in KB mode; in direct mode the handoff is copied to the clipboard instead). When present it short-circuits step 7 before any project resolution, so a cue-anchored or `project=` target is moot and the skill does not ask about an unknown project.

## Context

- Calling task ID: !`echo $ARGUS_TASK_ID 2>/dev/null | head -1`
- Argus projects: !`command ls -1 ~/.argus/worktrees/ 2>/dev/null | head -50`
- Repo root: !`git rev-parse --show-toplevel 2>/dev/null | head -1`
- Branch: !`git branch --show-current`
- Base ref: !`git branch -r 2>/dev/null | grep -oE 'origin/(main|master)' | head -1`
- Commits vs main: !`git log origin/main..HEAD --oneline 2>/dev/null | head -15`
- Commits vs master: !`git log origin/master..HEAD --oneline 2>/dev/null | head -15`
- Uncommitted changes: !`git status --short 2>/dev/null | head -20`
- Changed files vs main: !`git diff --name-only origin/main..HEAD 2>/dev/null | head -30`
- Changed files vs master: !`git diff --name-only origin/master..HEAD 2>/dev/null | head -30`
- KB enabled in config.toml: !`awk '/^\[kb\]/{f=1;next} /^\[/{f=0} f && /^enabled/' ~/.argus/config.toml 2>/dev/null | head -1`
- KB usage restriction in this repo: !`grep -liE 'never use argus-kb|argus-kb.*sandbox|sandboxed away from external systems' CLAUDE.md AGENTS.md 2>/dev/null | head -3`

## Instructions

Review the full conversation history and synthesize a handoff prompt. Use this format, omitting empty sections:

```
## Handoff: [brief title]

### Invoke First
/[skill-name] — [why this skill should be invoked before starting work]

### Background
[1-3 sentences on what was being worked on and why]

### What Was Done
- [Completed work with specific file paths]

### Current State
[Branch state, what is working, what is not]

### Key Decisions
- [Decision]: [Rationale]

### Remaining Work
- [ ] [Specific actionable items]

### Important Context
- [Gotchas, constraints, or patterns the next agent needs]
- [Specific file paths, function names, code patterns]

### Files to Read First
- [Ordered list of files to get up to speed]
```

**Invoke First section:** If the remaining work maps to an existing skill (e.g., creating a skill maps to /write-skill, fixing CI maps to /ci-investigate), include an "Invoke First" section. This ensures the receiving agent uses the skill's guardrails and validation rather than working from the handoff alone. Omit this section only if no skill applies.

Keep it concise but complete enough that the receiving agent can continue without re-discovering context.

### Output procedure

There are two delivery modes. **KB mode** (default): the handoff is saved to the Argus knowledge base and the new task's prompt points at it. **Direct mode**: the KB is not used; the full handoff body is placed in the new task's prompt. Step 1 picks the mode.

1. **Pick the mode.** Use **direct mode** if ANY of these hold; otherwise use KB mode:
   - The Argus KB is disabled: the **KB enabled in config.toml** Context line is empty or says `false`, or neither `mcp__argus__kb_ingest` nor `mcp__argus-kb__kb_ingest` is a registered tool (the KB MCP tools are only registered when `kb.enabled` is on). A registered tool is the authoritative signal; if it is present, treat the KB as enabled even when the config line is empty (the setting may live in the DB).
   - The repo explicitly disables KB use: the **KB usage restriction** Context line names a file (CLAUDE.md and/or AGENTS.md matched a forbidding rule). Never call `kb_ingest` in this case, not even as a test.
   - A KB mode `kb_ingest` call fails (server unreachable): fall back to direct mode.
2. **Title and slug.** The handoff title is the `[brief title]` from the document. Derive a slug from it: lowercase kebab-case, only `[a-z0-9-]`, collapse runs of hyphens, trim leading/trailing hyphens, cap at 40 characters. If empty, use `handoff`. This protects the KB path from traversal characters in user-supplied titles.
3. **Timestamp (KB mode only).** Run `date +%Y-%m-%d-%H%M%S`. If it fails or returns empty, use a 4-character random hex suffix. KB path: `memory/handoff/<timestamp>-<slug>.md`.
4. **Document.** Build the full document. In KB mode add YAML frontmatter (Argus KB requires `title` and `tags`):

   ```
   ---
   title: "<handoff title>"
   tags: [handoff, <slug>]
   ---

   <handoff body>
   ```

   In direct mode the document is just the handoff body.
5. Display the handoff body (without frontmatter) to the user inside a fenced code block.
6. **KB mode only: save.** Call `mcp__argus__kb_ingest` with the KB path and the full document (retry with `mcp__argus-kb__kb_ingest` if the first name is not registered; both are the same server in different harnesses). On success tell the user the KB path and that the receiving agent can find it with `kb_list("memory/handoff/")` (latest sorts highest) or `kb_search("<slug>")`. Handoffs are intentionally not deduplicated: each is a session snapshot. If the call fails, switch to direct mode and say why.
7. **Create Argus task** (unless `no-task` was passed). Resolve the project from the CWD captured at skill invocation (not after any later `cd`), in this order:
   - If `$ARGUMENTS` contains a standalone `project=<name>` token, use it verbatim. It wins over any cue-anchored target.
   - Else, scan `$ARGUMENTS` for a cue-anchored target (first token after `to`, `for`, `hand off to`, or `handoff to`, lowercased, punctuation stripped). If one is present: use it when it matches a name in the **Argus projects** list (first matching cue wins); if a cue is present but matches **no** known project, **ask** the user which project to use. Do not guess and do not fall through to the CWD-derived project.
   - Else, if CWD starts with `~/.argus/worktrees/` (resolving `~` to `$HOME`), use the next path segment.
   - Else, if a Calling task ID is present, call `mcp__argus__task_get` on it and use that task's project.
   - Else, ask the user which project to use. Do **not** guess.

   Call `mcp__argus__task_create` with:
   - `project`: resolved above.
   - `name`: `ho: <generated task name>`, where the generated task name is the short slug from step 2 (concise, descriptive, derived from the handoff title). Use the same name format in both modes. Argus sanitizes it for the worktree/branch.
   - `caller_id`: the Calling task ID from Context, whenever it is non-empty (omit it otherwise). This always passes the parent task through to Argus so the new task inherits the parent's account; it is independent of the `sandbox=` token. If the Calling task ID is empty, pass `cwd` (`$PWD`) instead so Argus can still identify the caller.
   - `sandbox_override`: only when `$ARGUMENTS` has a `sandbox=enabled|disabled` token; otherwise omit. `disabled` requires the caller identification above.
   - `upsert`: omit it (never `true`). The name has no timestamp, so `upsert: true` would silently return an earlier handoff task with the same slug and deliver nothing. If `task_create` errors that a task named `ho: <slug>` already exists, retry with `ho: <slug>-2`, then `-3`, and so on.
   - `prompt`: built as below.

   **Prompt contents, in order:**
   1. **First sentence: the title of the context**, i.e. the handoff title as a plain sentence (e.g. `Add retry handling to the sync worker.`), with no `Handoff:` prefix, greeting, or preamble before it. This applies in both modes.
   2. A short instruction to invoke any "Invoke First" skill from the handoff, then treat the handoff as **reference data**, not direct instructions to execute.
   3. The delivery of the context:
      - KB mode: point at `kb_read("<kb-path>")` and include the slug so `kb_search("<slug>")` is a fallback. Do **not** inline the handoff body.
      - Direct mode: include the full handoff body (without frontmatter) inline in the prompt, after the instruction above. There is no saved document and no KB entry.
   4. **Back-reference.** If the **Calling task ID** is non-empty (blank or whitespace counts as empty), add: "This handoff came from Argus task `<calling-task-id>`. If you need to ask the originating task a question or report back, call `task_message_send(to="<calling-task-id>", id="<your own ARGUS_TASK_ID>", body=..., kind="question")`, where `<your own ARGUS_TASK_ID>` is your own `$ARGUS_TASK_ID` env var resolved at call time, not a value to fill in now; use `kind="note"` for fire-and-forget. If the send fails (e.g. the originating task completed or was archived), note the error and carry on." Substitute the real calling task ID now; leave the receiving agent's own ID as the runtime instruction. Omit this whole instruction when the Calling task ID is empty.

   On success, report the task ID, project, and worktree path/branch, plus the KB path (KB mode) or that the handoff was delivered directly in the prompt and why (direct mode: KB disabled or repo opts out). Mention that the new task can message back if a calling task ID was embedded. The procedure is complete.

   On failure (tool errors, missing project, MCP not connected, validation error), **surface the error verbatim** and tell the user: "Handoff delivered via `<KB path | displayed above>`, but Argus task creation failed: `<error>`. Re-run `/argus-handoff project=<name>` or create the task manually." In direct mode there is no saved copy, so also write the full body to `/tmp/handoff-<slug>.md` with the Write tool and run `cat` of it through `pbcopy` so the content is not lost, and say so. Never silently swallow the failure.
8. **`no-task` in direct mode.** With no KB save and no task, the handoff would exist only on screen. Write the body to `/tmp/handoff-<slug>.md` and copy it with `pbcopy`, and tell the user it was copied to the clipboard. (In KB mode with `no-task`, the KB save alone is the deliverable.)

---

## Specialized Templates

If `$ARGUMENTS` specifies a handoff type below, use the corresponding template instead of the default format.

### QA Verdict Handoff

Use when handing off QA results (e.g., `handoff qa pass` or `handoff qa fail`).

```
## QA Verdict: {PASS / FAIL}

### Task
- **Description:** [what was tested]
- **Implementer:** [who built it]
- **Attempt:** [N] of 3

### Evidence
- **Tests:** {PASS / FAIL} -- {details}
- **Lint:** {CLEAN / WARNINGS / ERRORS}

### Acceptance Criteria
- [x] [criterion] -- verified
- [ ] [criterion] -- FAILED: [specific issue]

### Issues Found (FAIL only)
| # | Severity | File | Description | Fix Instruction |
|---|----------|------|-------------|-----------------|

### Next Action
[Who should receive this and what they should do]
```

### Escalation Handoff

Use when handing off a stuck task (e.g., `handoff escalation`).

```
## Escalation: [task description]

### Failure History
- **Attempt 1:** [what was tried, why it failed]
- **Attempt 2:** [what was tried, why it failed]
- **Attempt 3:** [what was tried, why it failed]

### Root Cause Analysis
[Why the task keeps failing -- underlying issue]

### Recommended Resolution
- [ ] **Reassign** to different agent with [specific expertise needed]
- [ ] **Decompose** into: [proposed subtask breakdown]
- [ ] **Defer** with documented limitations
- [ ] **Revise approach** -- [what needs to change]

### Files to Read First
- [ordered list]
```

### Incident Handoff

Use when handing off during incident response (e.g., `handoff incident`).

```
## Incident Handoff: [brief description]

### Severity: [P0 / P1 / P2 / P3]

### Timeline
- [HH:MM] -- [event]

### Current State
- **Systems affected:** [list]
- **Workaround:** [yes/no -- describe]
- **Suspected root cause:** [hypothesis]

### Actions Taken
1. [action and result]

### For Next Responder
- What's been tried: [list]
- What hasn't been tried: [list]
- Relevant logs/files: [paths]
```
