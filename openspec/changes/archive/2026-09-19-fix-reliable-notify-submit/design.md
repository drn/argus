## Context

`notify.Notifier.processOne` currently writes Ctrl+U, message text, sleeps for 50 ms, writes one CR, and immediately marks the delivery submitted. Splitting text and CR fixed the original glued-write paste bug, but a fixed delay cannot prove that a loaded recipient has consumed the text before CR arrives. The session abstraction already exposes a monotonic `TotalWritten` PTY-output counter in both in-process and supervisor-client modes, providing a process-local acknowledgment signal without parsing backend-specific screen text.

The daemon initializes the default `slog` handler to `~/.argus/daemon.log`, while `uxlog` is initialized only by TUI processes. Notify currently calls only `uxlog.Log`, so delivery behavior inside the daemon is invisible.

## Goals / non-goals

**Goals:**

- Adapt submit timing to observed recipient PTY activity rather than one fixed sleep.
- Retry standalone CR writes when no post-CR output evidence appears.
- Never mark a delivery submitted without acknowledgment evidence.
- Make delivery attempts and failures visible in `daemon.log` while retaining the TUI log trail.
- Keep cancellation, deadlines, idle/focus gates, deduplication, and per-task serialization intact.

**Non-Goals:**

- Parse Claude Code, Codex, or opencode screen content to prove semantic prompt execution.
- Add backend-specific submission commands or change the durable Hera inbox contract.
- Change REST, web, TUI, or macOS user-facing surfaces.

## Decisions

### Use monotonic PTY output as acknowledgment evidence

The notifier will read `SessionHandleIface.TotalWritten`, already implemented by both session types. After injecting text, it will wait for output activity to settle before sending CR, allowing slow input consumers to redraw the composer. After each CR it will require the output counter to advance before treating the delivery as submitted.

Alternatives considered:

- Comparing raw tail bytes is vulnerable to ring truncation and repeated redraws and adds no information beyond the monotonic counter.
- Parsing the rendered prompt to find or remove message text couples reliable delivery to backend-specific terminal layouts.
- Session idle state alone cannot distinguish a drafted prompt from an accepted prompt.

### Retry only the standalone CR with bounded backoff

When a CR receives no acknowledgment, the notifier will retry CR without rewriting the message. This preserves the already-drafted input and avoids duplicated text. A small bounded sequence of increasing acknowledgment windows accommodates delayed processing while preventing one recipient from blocking reconciliation indefinitely. If every attempt remains unacknowledged, the delivery stays pending for a later reconcile cycle instead of entering the submitted deduplication set.

### Treat text-settle timeout as degraded evidence, not a hard stop

Some terminal modes may not redraw after injected text. The notifier will log the missing pre-submit activity and still attempt CR, because refusing to submit would permanently strand those sessions. Post-CR output remains mandatory before success is recorded.

### Dual-write notify diagnostics

A package-local logging helper will preserve the existing `[notify]` `uxlog` messages and emit structured `slog` records with task and delivery IDs. The daemon's configured default handler writes these records to `daemon.log`; the TUI's configured default handler remains terminal-safe. Routine gate skips use debug severity, successful submission uses info, and write/acknowledgment failures use warn.

## Risks / trade-offs

- **PTY activity is evidence, not a semantic acknowledgment.** A cosmetic redraw could advance the counter without the CR being accepted. → Wait for injected-text activity to settle before taking the post-CR baseline, reducing the chance that delayed composer output is mistaken for submit output.
- **A successful submit could produce no immediate output.** The notifier would retry CR and retain the delivery. → Use increasing acknowledgment windows and bounded retries; later idle gating prevents most duplicate work-cycle injection, while durable inbox reads can still cancel the pending delivery.
- **Waiting can delay other recipients in one reconcile pass.** → Bound every settle/acknowledgment window and keep the existing no-lock-held submission path.
- **Dual logging can be noisy.** → Emit repeated safe-gate skips at debug, and reserve info/warn for lifecycle events operators need in daemon logs.

## Migration plan

No data migration is required. Deploying the new daemon changes only in-memory delivery behavior. Rolling back restores the prior fixed-delay protocol without persistence impact.

## Open questions

None.
