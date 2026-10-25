# Argus bridge mod: let Claude tell argus what it's doing

## The idea

Almost everything argus knows about a live Claude session comes from reading the terminal screen. A mod running inside Claude Code could report the same facts directly, so argus stops guessing.

The mod would ship in the argus repo and load into every Claude session argus launches. Argus already builds the launch command, so it can add `--plugin-dir` itself. Today's Stop hook, by contrast, has to be added to `~/.claude/settings.json` by hand.

## What argus guesses today vs. what the mod would report

### Idle and busy

- **Today:** 3 seconds of no output, plus a screen fingerprint with the spinner stripped out, plus "esc to interrupt" on screen, plus layers of debounce streaks. It runs twice, once in the daemon and once in the TUI.
- **Where:** `internal/agent/session.go:23`, `internal/agent/needsinput.go:47`, `:632-1037`, `:753`
- **Mod:** `turn.start` reports busy and `turn.complete` reports idle. This is exact and needs no timers.

### Waiting on you (the "(?)" state)

- **Today:** three regexes on screen text: the numbered widget, the "Enter to select … Esc to" footer, and a last line ending in `?`. A long list of past bugs is noted in comments (BUG-029, 032-036, 060, 063, 065, 067, 072).
- **Where:** `needsinput.go:98-255`
- **Mod:** a `tool.check` hook sees when the verdict is "ask", which means a permission dialog is up. A `tool.call` on AskUserQuestion or ExitPlanMode means a question or plan approval is pending. When the call resolves, the mod reports that it's cleared.

### Context size and token usage

- **Today:** the Stop hook re-reads the whole transcript JSONL, retrying for up to 200ms because the file lags behind.
- **Where:** `cmd/argus/coord_hook.go:726-912`
- **Mod:** `session.measure` pushes usage figures after every turn.

### Plan usage limits

- **Today:** argus launches a hidden `claude /usage`, presses keys past the folder-trust dialog, and regexes the "% / Resets" text. It fails open when the wording drifts.
- **Where:** `internal/usagebudget/usagebudget.go:340-401`
- **Mod:** `session.measure` includes rate-limit windows. The hidden launch can go.

### Session id after `/clear`

- **Today:** after each exit, argus rescans `~/.claude/projects/<cwd>/*.jsonl` and picks the newest file.
- **Where:** `internal/daemon/daemon.go:449`, `internal/agent/resume.go:124`
- **Mod:** `session.start` reports the session id, model and working folder whenever they change.

### Delivering hera and task messages

- **Today:** argus types into the input box: render the screen, find the draft, press Ctrl+U, type the text, wait for output to settle, press Enter, retry Enter until the box empties, then restore the user's draft. Messages expire after 5 minutes.
- **Where:** `internal/notify/service.go:217-440`, `needsinput.go:433`, `internal/tui/heraactions.go:1150-1172`
- **Mod:** on `turn.complete`, or on a gentle timer while idle, the mod pulls pending messages from the daemon and submits them itself. There's no typing, and the user's draft is never touched.

### Budget nudge for coordinators

- **Today:** the Stop hook returns `decision: block` to push a recycle. This has caused an idle deadlock.
- **Where:** `coord_hook.go:229-243`
- **Mod:** add a budget section through `prompt.compose`, or show a band. Nothing gets blocked.

## Extras the screen gets for free

- A band above the prompt showing hera role, orchestrator, unread message count and budget used.
- A toast when a message arrives.
- Slash commands such as `/hera-inbox` and `/hera-status` that answer locally, without a model turn.

## What it can't fix

- **Resizing and redraw at a new width:** this happens in the terminal itself, so the kill-and-resume "rerender kick" stays (`internal/agent/rerender.go`).
- **Codex and other non-Claude backends:** mods only run in Claude Code, so screen-reading stays as their fallback.

## Proposed plan

### Phase 1: report only (shadow mode)

- The mod posts state events (busy, idle, needs-input, cleared, usage, session id, model) to the daemon.
- Argus records them next to its screen-reading verdicts and logs every disagreement. Nothing switches over yet.
- Done when the logs show the mod and the scraper disagree only where the scraper is wrong.

### Phase 2: switch the source of truth

- For Claude sessions with the mod loaded, the daemon trusts the mod's state. Screen-reading stays only for other backends.
- The usage budget reads from `session.measure` instead of the hidden `/usage` launch.
- The token-reporting part of the Stop hook retires.

### Phase 3: message delivery and screen extras

- Hera and task messages go through the mod instead of typed keystrokes.
- Add the band, toast and slash commands.

## Unknowns to confirm before building

1. **Can a mod start a turn by itself while the session is idle?** The type docs hint yes ("the plugin submits next"). Phase 3 depends on it.
2. **Can the mod reach the daemon from inside the sandbox?** The Stop hook already reaches it over HTTP with a token from `~/.argus/api-token`, so probably yes through `$.http.fetch`.
3. **Does `tool.check` fire for every permission dialog**, including ones from MCP tools and plan mode?
4. **Upstream:** `drn/argus` is the canonical repo, so this lands as a PR there.

## Decision

Should I start phase 1 now as its own worker in the argus repo, running alongside the personal-mods build?

- **Start phase 1 now (recommended):** it can't break anything running today, and the shadow logs give hard numbers on how often screen-reading is wrong.
- **Build all three phases at once:** faster, but message delivery rests on unknown #1, and a bug there breaks every hera team.
- **Wait until the personal mods ship:** no parallel work, but nothing depends on them.

## Spike findings (2026-10-24, Claude Code 2.1.310)

Tested live: a throwaway mod loaded with `claude --plugin-dir`, driven in tmux, logging every event to a file.

- **(a) Idle submit: yes.** `$.prompt.submit({ text })` while idle starts a real turn. The model reads it as "The <plugin> plugin sent a message: ..." (`asUser: true` drops that frame). A typed but unsent draft in the prompt box was left untouched across a plugin-submitted turn.
- **(b) Daemon reachable: yes.** `$.fs.read("<HOME>/.argus/api-token")` plus `$.http.fetch("http://127.0.0.1:<port>/api/...", { headers: { Authorization: "Bearer <token>" } })` returns 200 from inside the sandboxed session. `HOME` and `ARGUS_TASK_ID` come from `$.env.get`. The API port is not stable across daemon restarts (`bindWithRetry`) and nothing on disk records it today. Sessions outlive a daemon bounce through the supervisor, so the mod needs a discoverable port: the daemon should write `~/.argus/api-port` at bind time, and the mod re-reads it on every connection failure.
- **(c) `tool.check`: fires for every tool call**, MCP tools included (verdict `ask`, `approver: "person"` when a dialog would be shown), and also in bypass mode (verdict `allow`). **But `ask` does not mean a dialog appeared.** A settings `PermissionRequest` hook can auto-decide it: Aaron's global settings auto-approve Bash via `argus-permission-approve.sh` and route `ExitPlanMode` to plannotator. Mod hooks on `classic.PermissionRequest` and `classic.Notification` are **skipped** in this org's setup, because the managed `cc-plugin-sec-default` plugin runs first. So they cannot be relied on.
- **Reliable needs-input signal:** a `tool.call` that is pending (`pre` seen, `post` not yet) and whose `tool.check` verdict was `ask`, still pending after a short grace period (auto-approval hooks resolve within milliseconds to about a second). Separately, `AskUserQuestion` and `ExitPlanMode` are pending from `tool.call` pre to post: that exactly brackets the dialog, verified live for AskUserQuestion. In argus's default bypass mode, only AskUserQuestion, ExitPlanMode and MCP tools with an organization `ask` ceiling raise dialogs.
- **Events seen:** `session.start` (`cwd`, `surface`, `isInteractive`; the id comes from `$.session.id()`, the model from `$.session.model()`), `turn.start` (`turnId`, `text`), `turn.complete` (`turnId`, `reason`, `isAborted`), `session.measure` (`context.tokens/percent/window`, `cost.usd`, `rateLimits[{kind: five_hour|seven_day, percentUsed, resetsAt}]`, `changed`). `session.measure` fires at start and after every turn and tool step.
- **`/clear` does not re-fire `session.start`.** A new transcript file appears. The mod must re-read `$.session.id()` on each `turn.start` and report when it changes.
- **Hot reload:** editing a `--plugin-dir` mod reloads it at the next turn end. A reload re-runs `register` and `session.start`, and module-level variables reset.
