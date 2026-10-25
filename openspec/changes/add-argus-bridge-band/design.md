## Context

Phase 3a supplies reporting and idle message delivery. Managed organization hooks can short-circuit other mods, so AbovePrompt, toast and command hooks require early live verification.

## Goals / non-goals

**Goals:** a single-line composable role band, arrival notices, local inspection commands and settings that change running sessions.

**Non-Goals:** acknowledging mail, changing delivery, non-Claude UI mods, and archiving changes. Web SPA and macOS Bridge settings controls are the named follow-up `bridge-settings-web-macos-parity`; the REST snapshot remains reachable from every client.

## Decisions

- Fetch one authenticated read-only snapshot containing effective settings, live bridge status, bindings, unread message summaries and coordinator budget. Reading never mutates mail or delivery state.
- Cache the snapshot in a typed session atom. Rendering only reads the cache, always calls next(e), includes beneath, and uses truncate-end. Hide when unbound, disabled or unavailable.
- Refresh after turn.complete and session.measure and on a slow timer, single-flight and outside hook dispatch. Clear stale data on failed refresh. Network failures cannot block the session.
- Toast newly observed message identities once per session; baseline existing unread mail without toast. Refresh while busy so arrivals are visible independently of delivery.
- Register commands on session.start; command.run returns text from a fresh bounded snapshot or an unavailable message, without submitting a model prompt. Inbox summaries include sender, TLDR and age.
- Both settings default on, use config.toml-over-DB precedence and appear in local and remote TUI Settings. Master bridge.enabled gates visible UI.

## Risks / trade-offs

- [Managed hooks skip events] → verify early in isolated tmux and report any limitation to the coordinator.
- [Polling delays arrivals and toggles] → refresh on measurements/turn completion plus a slow timer; rendering stays network-free.
- [Unread mail survives delivery] → deduplicate arrival notifications by durable message identity, never by count.
- [Embedding misses new runtime files] → extend explicit embed patterns and retain the runtime-file coverage test.

## Verification

Live Claude Code 2.1.310 in tmux loaded the managed cc-plugin-sec-default outermost and successfully exercised AbovePrompt, toast, command.register and command.run. The bound-session check used a temporary HOME/data directory, real api.Server, agent.NewRunner(nil), real BuildCmd and bridgemod.Materialize through the materialization seam. The coordinator band showed context and configured budget, disappeared with bridge.band=false, and returned after enabling it without restarting. Local commands printed binding/status/liveness and sender/summary/age without a model turn; repeated inbox reads preserved both unread messages. A newly inserted Hera message produced an arrival toast captured from terminal output. Temporary sessions were shut down; the live daemon and supervisor were untouched.

Mounted tests exercise all four surfaces, live switches, busy-turn toasts, beneath preservation, surveys, narrow truncate-end output and event-driven refresh. Go tests exercise defaults/overlay, local TUI persistence, real remote-store round-trips (including file overrides), read-only mail, unbound tasks, multiple live bindings and missing status rows. Review strengthened disabled/unavailable tests to begin from a visible bound band, and the multi-binding test caught and fixed missing role status handling.

Final validation: plugin validate, TypeScript and 56 plugin tests pass. The single full pre-pr invocation passed build/vet/format and stopped at two lint findings, subsequently fixed; remaining stages ran separately with Go 1.26.10 and GOFLAGS=-p=1. Lint reports zero issues and the vulnerability scan reports no affected symbols. The full race/coverage run's sole failure was the untouched TestCreateWorktree_FetchesRemote: a temporary helper used to defer the gate's host fetch to iris also intercepted a test-local fetch. The helper was corrected and that test passed under race with normal git. Every other package passed (including TUI at 187s); the full-run profile clears the filtered coverage floor at 88.7%. No second full gate was run. All changes remain unarchived.
