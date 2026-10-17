## Context

Fresh Claude sessions have a pinned UUID before their transcript exists. Quiet output and elapsed time do not prove that UUID can be resumed. The mission authorizes both safeguards.

## Goals / Non-goals

- Preserve a live session until its transcript exists, with deferred retries in Hera and classic views.
- Recover dead sessions consistently through the runner used by TUI, REST, and daemon relaunches.
- Respect task accounts when locating transcripts and preserve other backend behavior.
- Exclude the separate startup hang and the reason for quiet initial output.

## Decisions

- Add an optional availability callback to the family registry. Families without one retain their existing behavior. Check the exact recorded ID, not merely whether any transcript exists.
- Guard the TUI before stopping and the runner as a final defense for RPC and REST kicks. Deferred TUI outcomes invalidate cached widths and call the existing deferred callback.
- At the runner launch boundary, validate only requested resumes. Missing transcripts mint a fresh UUID and select the existing fresh-launch prompt path. Propagate the effective ID through the start RPC response so callers persist the ID actually launched.
- Preserve requested-resume background-worker cleanup using the original ID before fallback replaces it; a missing transcript does not prove detached workers are gone.
- Return the effective ID in REST resume/restart responses too, preventing the remote TUI's subsequent raw task update from restoring the stale ID. Web and macOS ignore the additive field and use daemon-persisted identity; no UI changes are needed.
- Treat inspection errors as errors rather than permission to discard conversation identity. Log deferrals and fresh-launch recovery.

## Risks / Trade-offs

- A transcript can disappear after the check; launch-time validation still avoids known missing transcripts but cannot prevent external deletion races.
- A file existing is the availability contract; this change does not validate Claude's full transcript format.
- Additive RPC response data requires updated peers to persist a recovered ID immediately; no new REST contract is needed.
