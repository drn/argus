## Context

PR #982 replaced the fixed text-to-CR delay with output-observed settling, post-CR acknowledgment, and retry. An earlier coordinator refinement was not delivered to that worker session: reliable notify also needs a content-aware decision before it writes. The observable source available to the daemon is the session ring plus PTY dimensions; the existing `agent.ScreenRenderer` already reconstructs the terminal's visible state with x/vt.

## Goals / non-goals

**Goals:**

- Protect only demonstrably active typing rather than every busy or focused session.
- Self-heal stale injected notices and abandoned input.
- Preserve abandoned non-notice text while making it explicitly non-authoritative to the recipient agent.
- Keep PR #982's output acknowledgment, CR retry, deadlines, cancellation, and serialization.

**Non-goals:**

- Query an application's private editor buffer; PTYs expose only rendered output.
- Infer content when the renderer cannot identify a supported composer prompt.
- Make backend-specific semantic claims beyond recognizable Argus/Hera notice prefixes.

## Decisions

### Extract the visible composer through the shared VT renderer

`ScreenRenderer` will expose a snapshot method that renders a bounded substantive tail, finds the latest Claude composer marker (`❯` plus NBSP), and returns text from that row through the cursor row. This reuses the same terminal reconstruction already trusted for fullscreen needs-input detection. The notifier owns one renderer and calls it from serialized reconcile processing.

When no composer marker is found, the notifier falls back to the existing idle-and-unfocused gate. Unknown terminal layouts are not treated as empty because that could overwrite live input.

### Carry composer observations on each delivery

A delivery records the last non-empty, non-notice draft and the time it was first observed unchanged. A different snapshot replaces the observation and defers. The same snapshot after the stability window is abandoned input and becomes deliverable. Empty or notice-only snapshots bypass the observation window immediately.

### Clear stale notices but preserve abandoned non-notice input

For an empty or notice-only composer, the existing Ctrl+U pre-clear runs before the current notice, removing any failed prior injection. For stable non-notice input, Ctrl+U is skipped: Argus appends a newline, a fixed warning that the preceding input was left unsubmitted and must not be acted upon, then the new notice. The resulting combined composer is submitted and acknowledged using PR #982's mechanics.

### Content decisions are daemon-visible

The notifier logs empty, notice-only, changing, stable-abandoned, and unknown-fallback decisions with task and delivery IDs. The log never includes the draft text itself, avoiding accidental capture of user input in `daemon.log`.

## Risks / trade-offs

- **Rendered composer support is initially Claude-specific.** → Unknown layouts conservatively retain idle/focus gating and are logged for diagnosis.
- **A long wrapped draft may extend beyond the visible screen.** → Snapshot from the prompt marker through the cursor row; classification needs only current change detection and recognizable injected prefixes, not full editor history.
- **An unchanged human draft may still be intentional.** → Preserve it verbatim and append a strong do-not-act annotation rather than deleting it.
- **Five-second reconcile cadence bounds typing detection granularity.** → Reuse the daemon's existing tick and require a full stability window before treating non-notice text as abandoned.

## Migration plan

No data migration is required. Composer observations are in-memory and disappear on daemon restart; after restart, non-empty human content must complete a fresh stability window.

## Open questions

None.
