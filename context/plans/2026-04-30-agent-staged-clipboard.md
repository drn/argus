# Plan: Agent-staged clipboard with PWA Copy button + TUI hotkey

**Date:** 2026-04-30
**Source:** Inline spec — chat with the user on iOS clipboard limitations
**Status:** Draft
**Current Phase:** Phase 1

## Goal

Let an agent stage text via MCP that the user can one-tap (PWA) or one-keypress (TUI) to copy to the OS clipboard. Solves the iOS Safari constraint that `clipboard.writeText` requires a synchronous user gesture — the agent stages, the user does the actual write.

## Background

iOS Safari (and PWAs) reject `navigator.clipboard.writeText()` outside a real `click`/`tap` handler. An async agent stream cannot fulfil this. Workaround: the agent calls an MCP tool that stores text on the daemon; the PWA renders a Copy button next to the existing overflow `⋯` menu in the detail-header (`internal/api/static/index.html:760`); tapping the button does the synchronous `writeText` then DELETEs the staged payload. The TUI exposes the same via `ctrl+y` in agent view, reusing the existing `pbcopy` pattern at `internal/tui/app.go:300-321`.

## Requirements

### Must Have

- MCP tool `argus_clipboard_set(text, task_id?)` — agent stages text for the user.
- Daemon stores per-task payload (last-write-wins) with 5-min TTL.
- HTTP API `GET/POST/DELETE /api/clipboard` (auth via existing token middleware).
- SSE event `event: clipboard` piggybacked on existing `/api/tasks/{id}/stream` so the PWA learns of new payloads without polling.
- PWA shows a Copy button next to `⋯` when a payload exists; tap → `writeText` → DELETE → button hides.
- TUI `ctrl+y` in agent view copies the active task's pending payload via `pbcopy`, clears the payload, flashes "Copied" notice.
- iOS fallback to `navigator.share({text})` if `writeText` rejects.

### Should Have

- agentHeader hint "📋 ctrl+y to copy" when a payload is pending.
- TTL-based auto-clear (5 min) so stale buttons don't sit forever.

### Won't Do (this iteration)

- Cross-platform clipboard write in TUI — `pbcopy` is darwin-only, matches existing precedent. Defer Linux (`xclip`) / Windows (`clip.exe`) to a follow-up.
- Persistence across daemon restarts — agent re-stages on demand.
- Global (cross-task) staging — per-task only; the active task in the user's view is unambiguous.
- Multi-payload queue — last-write-wins.
- TUI hotkey outside agent view.

## Technical Approach

New `internal/clipboard/` package owns the staging store. The daemon hosts an instance and exposes it via three RPC methods (Set/Get/Clear). The HTTP API wraps the same RPC, plus a `Subscribe(taskID, fn)` callback the SSE handler uses to push live updates. The MCP server gets a thin `ClipboardSetter` interface so it can call `Set` without importing daemon types.

The PWA Copy button is a sibling of `.overflow-btn` in the existing detail-header, hidden by default; an SSE listener on `event: clipboard` toggles visibility. The button tap handler is a synchronous `clipboard.writeText` (critical — no await before the call) followed by a fire-and-forget `DELETE /api/clipboard`.

The TUI reuses the existing `pbcopy` pattern from `OnCopyPrompt`. `ctrl+y` is currently used as a PTY pass-through (`app.go:1843` in `keyToBytes`). We conditionally intercept it in agent view **only when a payload is pending** — otherwise it passes through to the PTY as today. The TUI polls `ClipboardGet` on the existing tick goroutine; cheap (local socket, in-memory map).

## Decisions

| Decision                                   | Rationale                                                                                                           |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| New `internal/clipboard/` package          | Isolates the store from daemon/api/mcp; allows independent testing. Daemon owns the instance.                       |
| Per-task scope only                        | Active task in PWA/TUI is unambiguous; global slot adds API surface for unclear value.                              |
| In-memory, no persistence                  | Daemon restart is rare; agent can always re-stage. SQLite would add migrations for ephemeral data.                  |
| Last-write-wins (no queue)                 | Simpler UX; multiple stages without copy almost always means the agent is updating, not appending.                  |
| 5-min TTL                                  | Bounds staleness; longer feels like state that should be persisted.                                                 |
| SSE piggyback on existing per-task stream  | Avoids new connection. Already has auth + reconnect. PWA only needs updates while viewing a task — perfect overlap. |
| `Subscribe(taskID, fn)` callback in store  | Lets API push live events without polling; clean lifecycle (unsub on stream close).                                 |
| Reclaim `ctrl+y` only when payload pending | Existing PTY pass-through preserved when nothing to copy. No surprise key-stealing.                                 |
| Reuse `pbcopy` (darwin-only)               | Existing precedent at `app.go:300-321`. Cross-platform is a separate concern.                                       |
| iOS fallback to `navigator.share`          | One-line addition inside the same gesture handler — covers older iOS / permission denials.                          |

## Implementation Steps

### Phase 1: Daemon clipboard store + RPC

**Status:** pending

- [ ] Create `internal/clipboard/` package — `internal/clipboard/store.go` — `Store` type with `Set(taskID, text)`, `Get(taskID) (text, ok)`, `Clear(taskID)`, `Subscribe(taskID, fn func(text string)) (unsubscribe func())`. Mutex-protected `map[string]entry`, lazy TTL check on `Get` plus periodic prune via `time.AfterFunc`. `Subscribe` notifies on next `Set`/`Clear` for that taskID; `fn("")` signals cleared.
- [ ] Tests — `internal/clipboard/store_test.go` — set/get/clear, TTL expiry (synthetic clock), subscriber fire on set+clear, unsubscribe stops delivery, concurrent Set/Get/Clear.
- [ ] Wire into daemon — `internal/daemon/daemon.go` — add `clipboard *clipboard.Store` field on `Daemon`, initialize in `New`, expose via `Clipboard() *clipboard.Store`.
- [ ] Add RPC types — `internal/daemon/types.go` — `ClipboardSetReq{TaskID, Text}`, `ClipboardGetReq{TaskID}`, `ClipboardGetResp{Text, OK}`, `ClipboardClearReq{TaskID}`.
- [ ] Add RPC methods — `internal/daemon/rpc.go` — `ClipboardSet`, `ClipboardGet`, `ClipboardClear` calling `s.daemon.clipboard.{Set,Get,Clear}`. Match the existing slog.Info bracket-prefix logging.
- [ ] Daemon test — `internal/daemon/daemon_test.go` — round-trip Set → Get → Clear via RPC client.

### Phase 2: HTTP API + SSE event

**Status:** pending

- [ ] Wire store reference into `Server` — `internal/api/server.go` — accept `*clipboard.Store` in the constructor (or via setter to mirror `SetTaskManager` pattern). Daemon passes `d.Clipboard()` when starting the API server.
- [ ] Handlers — `internal/api/handlers.go` — `handleClipboardGet` (returns `{text}` or 204), `handleClipboardSet` (POST `{text, task_id}` — primarily for testing/PWA-side staging if ever needed; not the agent path), `handleClipboardClear`.
- [ ] Routes — `internal/api/routes.go` — `GET/POST/DELETE /api/tasks/{id}/clipboard`. Reuse path-style task ID from existing routes for consistency.
- [ ] SSE integration — `internal/api/handlers.go:handleStreamOutput` — after `AddWriter`, also call `clipboard.Subscribe(id, fn)`; `fn` writes `event: clipboard\ndata: {"text":"…"}\n\n` (or `{"cleared":true}` for empty) into the same SSE stream. `defer unsubscribe()`. On stream open, emit current state if a payload exists so freshly-opened tabs catch pending staging.
- [ ] Auth — clipboard endpoints accept master OR device per existing middleware; no `requireMaster` since both stage and copy are user-driven.
- [ ] Tests — `internal/api/handlers_test.go` — POST sets, GET returns, DELETE clears; SSE delivers both `clipboard` and existing terminal frames on the same stream; TTL expiry surfaces as 204 on subsequent GET.

### Phase 3: MCP tool registration

**Status:** pending

- [ ] Define `ClipboardSetter` interface — `internal/mcp/server.go` — `Set(taskID, text string)`. Add `clipboard ClipboardSetter` field on `Server`. Add `SetClipboard(setter)` setter mirroring `SetTaskManager`.
- [ ] Tool definition — `internal/mcp/server.go` — append to `taskToolDefs` (or new `clipboardToolDefs` slice if it grows): `argus_clipboard_set` with `text` (required) and `task_id` (optional — server resolves via `cwd` like `task_archive` if omitted) parameters. Description must call out: "Stages text for the user to copy with one tap (PWA) or `ctrl+y` (TUI). Use when you have output the user will likely want to paste — code snippets, commands, generated text. Last-write-wins. The user must take an action; this does not write directly to the OS clipboard."
- [ ] Dispatch — `internal/mcp/server.go:handleToolsCall` — add `case "argus_clipboard_set"` calling a new `s.handleClipboardSet`. Reuse the `cwd → task` resolution helper used by `task_archive`.
- [ ] Wire from daemon — `internal/daemon/daemon.go` — after `mcpSrv.SetTaskManager(...)`, call `mcpSrv.SetClipboard(d.clipboard)`.
- [ ] Tests — `internal/mcp/server_test.go` — `argus_clipboard_set` invocation routes to a fake setter; cwd-resolution path works; missing task_id with no cwd returns a structured error.

### Phase 4: PWA Copy button

**Status:** pending

- [ ] CSS — `internal/api/static/index.html` `<style>` block — `.copy-btn` styled like `.overflow-btn` (44×44 tap target, same `top` offset, positioned to the left of `⋯` with an 8px gap). Add `.copy-btn.compact` matching `.overflow-btn.compact`. `display:none` by default.
- [ ] Markup — `internal/api/static/index.html:760` area — insert `<button class="copy-btn" id="btn-copy" aria-label="Copy staged text" type="button">📋</button>` immediately before the existing `<button class="overflow-btn">`. Adjust the `.detail-title` right-padding rule (~ line 171 comment) to reserve `44 + 8 + 44 + 8 = 104px` when the copy button is visible — toggled via a class on `.detail-header`.
- [ ] JS — same file — extend the existing per-task EventSource (`evtSource`) onmessage to inspect `event.type === 'clipboard'`. On set: store text on the button's dataset, show button + reserve title space. On cleared: hide. Initial fetch: `GET /api/tasks/{id}/clipboard` on detail-view open in case the SSE arrived before the page mounted.
- [ ] Tap handler — synchronous: `navigator.clipboard.writeText(textFromDataset)` inside the click listener with no await before the call. On resolved: `fetch(/api/tasks/{id}/clipboard, {method:'DELETE'})` and hide button locally (don't wait for the SSE round-trip). On rejected: try `navigator.share({text})` from the same click context; if that also fails, surface an inline error toast.
- [ ] Service worker bump — `internal/api/static/sw.js` — increment `SW_VERSION` to bust the cache.
- [ ] Manual test in iOS Safari (real device) — verify the button appears on SSE event, copy works, button disappears, and a second stage re-shows it. Run `cmd/argus-test-server/` locally for the HTTP harness.
- [ ] Playwright spec — `web-tests/clipboard.spec.ts` — fake an MCP set via `POST /api/tasks/{id}/clipboard`, assert button becomes visible, click, assert `navigator.clipboard.readText()` matches and button hides, assert a follow-up GET returns 204.

### Phase 5: TUI hotkey

**Status:** pending

- [ ] Daemon client RPC wrappers — `internal/daemon/client/client.go` — `ClipboardGet(taskID)`, `ClipboardClear(taskID)` calling the new RPC methods via `c.call`. Match the existing 2-second timeout.
- [ ] Tick poll — `internal/tui/app.go` (existing tick goroutine; grep for `tapp.QueueUpdateDraw` callsite that already runs on a ticker) — when `mode == modeAgent` and the active task ID is known, call `ClipboardGet`; cache the pending text on `App` (`a.clipboardPending string` field, mu-guarded).
- [ ] Hotkey — `internal/tui/app.go:1271` switch — add `case tcell.KeyCtrlY:` BEFORE the agent-mode PTY pass-through paths. If `a.mode == modeAgent && a.clipboardPending != ""`: spawn the existing-style goroutine that pipes to `pbcopy` (factor `OnCopyPrompt`'s body into a helper `copyToClipboard(text, notice string)`), then call `ClipboardClear`, flash `header.SetNotice("Copied")`. Return `nil`. Else fall through to PTY (the existing line 1843 mapping in `keyToBytes`).
- [ ] AgentHeader hint — `internal/tui/widget/agentheader.go` — add `SetClipboardHint(bool)`; render "📋 ctrl+y to copy" on the right side of the header bar when set. App calls `agentHeader.SetClipboardHint(a.clipboardPending != "")` whenever the cached pending text changes.
- [ ] Tests:
  - `internal/clipboard/store_test.go` — already covered.
  - `internal/tui/app_test.go` — fake `SessionProvider` returning a pending payload, simulate `ctrl+y`, assert `pbcopy` invocation (via injected exec; the existing OnCopyPrompt test, if any, is the template — otherwise gate the system call behind an injectable function).
  - `internal/tui/widget/agentheader_test.go` — hint renders/disappears.
  - `internal/tui/smoke_test.go` — agent-view enter, simulate clipboard set via fake, assert hint shown; press `ctrl+y`; assert clear RPC fired.

## Testing Strategy

- Unit tests for the store (Phase 1) — TTL, subscriber lifecycle, concurrent access.
- Daemon RPC round-trip — confirm Set→Get→Clear works over the Unix socket in `daemon_test.go` (use the existing in-process daemon harness).
- HTTP handler tests for endpoints + SSE event delivery (`handlers_test.go`).
- MCP tool tests for argument validation, cwd resolution, and dispatch (`server_test.go`).
- Playwright spec for the PWA flow (`web-tests/`), using the existing `cmd/argus-test-server/` harness with `/test/reset` to isolate runs.
- TUI smoke test exercising the full hotkey flow with `simApp`/`wireApp`/`runApp` per CLAUDE.md.
- **Edge cases:** empty text (reject at MCP level), text > some upper bound (1MB?) — clamp at the store and return an error; missing task_id with no cwd; payload set then session ends (clear on session exit? — yes, hook into `runner.onFinish`); SSE reconnect mid-payload (re-emit current state on stream open).
- All tests respect CLAUDE.md rules: `t.TempDir()` for HOME, `agent.NewRunner(nil)`, never touch real `~/.argus/`.

## Risks & Open Questions

| Risk                                                                                       | Mitigation                                                                                                                        |
| ------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| iOS rejects `writeText` despite gesture (older iOS, content too long, perms)               | `navigator.share` fallback inside the same handler; visible inline error if both fail.                                            |
| `ctrl+y` reclaim breaks agents that send Ctrl+Y to underlying TUIs (vim, emacs `yank-pop`) | Conditional intercept — only steal when `clipboardPending != ""`. Document in `gotchas/keybindings.md`.                           |
| SSE event ordering — clipboard event vs. PTY data interleaving in same stream              | Both are line-buffered framed events; SSE consumers handle event types independently. Verify in Playwright spec.                  |
| Subscribe leak if SSE handler doesn't unsubscribe on disconnect                            | Always `defer unsubscribe()` immediately after `Subscribe`. Test by cycling EventSource connections and checking goroutine count. |
| TTL-pruned payload while user is mid-tap                                                   | TTL of 5 min is generous; pruning emits `cleared` event so PWA hides button before tap. Document.                                 |
| Daemon restart loses payload                                                               | Acceptable — agent re-stages. Document in user-facing release note.                                                               |
| Per-task scoping breaks for an agent that doesn't know its task ID                         | MCP tool resolves via `cwd` like `task_archive` does today. Reuse that helper.                                                    |

- **Open:** Should `argus_clipboard_set` clamp text size at the MCP layer or the store? — Probably the store, with a documented max (1 MB seems generous). Return an error MCP-side if exceeded.
- **Open:** When a session finishes (`runner.onFinish`), should we auto-clear the task's clipboard? — Lean yes; the agent that staged it is gone. Hook into the existing onFinish callback in `daemon.New`.
- **Open:** Cross-platform clipboard for TUI — defer or include? — Defer. Existing `pbcopy` precedent already constrains us; bundling cross-platform here muddies the scope.

## Dependencies

- No new third-party Go modules. `pbcopy` invocation matches existing pattern.
- No new PWA vendor assets — uses native `navigator.clipboard` and `navigator.share`.

## Errors Encountered

| Error | Attempt | Resolution |
| ----- | ------- | ---------- |

## Estimated Scope

**Phases:** 5
**Tasks:** ~30
**Files touched:** ~12 (3 new: `internal/clipboard/store.go`, `internal/clipboard/store_test.go`, `web-tests/clipboard.spec.ts`; 9 modified across daemon, api, mcp, tui, static)

## Gotcha File Updates (per CLAUDE.md)

After implementation, capture non-obvious invariants:

- `gotchas/daemon-rpc.md` — clipboard store lives on `Daemon`, subscribers must `defer unsubscribe()` to avoid goroutine leaks.
- `gotchas/web-remote.md` — Copy button SSE event piggybacks per-task stream; PWA must fetch initial state on detail-view open because SSE may have emitted before the page mounted; `writeText` MUST be synchronous in the click handler — any `await` before it breaks iOS gesture trust.
- `gotchas/keybindings.md` — `ctrl+y` is conditionally intercepted in agent view (only when payload pending); falls through to PTY otherwise.
