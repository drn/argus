## Why

Ctrl+Y in the TUI copies the agent-staged clipboard payload to the OS clipboard,
then immediately clears the staged slot — both the local cache (so the header
hint disappears) and the daemon-side store (via `ClipboardClear`). This means:

- The "ctrl+y to copy" hint vanishes the instant you copy, even though the
  underlying text is often still exactly what you want (e.g. re-copying the same
  diff/snippet into a second place, or copying again after a failed paste).
- Pressing ctrl+y a second time flashes "Nothing to copy" even though nothing
  about the situation actually changed from the user's perspective.

This clear-on-copy behavior isn't needed for correctness: the staging store
already self-cleans via a 5-minute TTL (`internal/clipboard/store.go`),
last-write-wins on the next `argus_clipboard_set` call, and an explicit clear
when the agent session exits (`internal/daemon/daemon.go`'s `handleSessionExit`).
Ctrl+Y clearing the slot on every copy is a TUI-only decision layered on top of
that lifecycle, not something the rest of the system depends on.

Separately, the existing hint (" ctrl+y to copy " right-justified in the agent
header, styled identically to the rest of the header text but bold) is easy to
miss — it doesn't stand out from the surrounding chrome, so users often don't
notice there's something staged to copy at all.

## What Changes

- **Ctrl+Y no longer clears the staged clipboard payload after copying.** It
  still copies the cached text to the OS clipboard and flashes "Copied", but
  the staged slot (local cache and daemon-side store) is left intact — the hint
  stays visible and ctrl+y can be pressed again to re-copy the same text.
  Clearing remains driven entirely by the existing lifecycle: TTL expiry,
  replacement by a newer `argus_clipboard_set` call, or the agent session
  exiting. This applies identically to the main agent view (`copyStagedClipboard`)
  and the Hera multi-pane view (`copyStagedClipboardForHeraPane`).
- **The staged-clipboard hint renders with a distinguishing highlight color**
  instead of blending into the rest of the header/border-title text, so it's
  noticeable at a glance that there's something staged to copy. Applies to both
  the main agent header's " ctrl+y to copy " hint and the Hera pane's
  "(ctrl+y copy)" border-title affordance.

## Non-Goals

- **The PWA's copy-and-clear flow is intentionally left unchanged.** The web
  client (`internal/api/static/index.html`'s `clearServerClipboard`) still
  calls `DELETE /api/tasks/{id}/clipboard` immediately after a successful
  `navigator.clipboard.writeText`, so on the web surface the Copy button and
  its hint still disappear after one use — the TUI and the web now diverge on
  this specific behavior. Per this repo's Frontend Parity rule, that gap is
  named here rather than left silent: iOS Safari's requirement that a
  clipboard write happen inside a synchronous user gesture (the reason the
  staging store exists at all) makes a lingering "still staged" web hint less
  useful there than on the TUI's PTY-only view, and unifying the two was out
  of scope for this fix. Bringing the PWA in line (or deciding it should stay
  as-is) is a tracked follow-up, not an oversight.
- The macOS app never called a clear endpoint for this feature to begin with
  (`TerminalController.swift` copies on the incoming SSE push and never issues
  a clear), so it already matched the TUI's new persist-after-copy behavior
  without any change here.

## Capabilities

### Modified Capabilities

- `clipboard-staging`: ctrl+y's TUI copy action no longer clears the staged
  payload as a side effect of copying; the staged-payload hint renders with a
  distinguishing highlight style.

## Impact

- **Modified code:**
  - `internal/tui/clipboard.go` — `copyStagedClipboard` and
    `copyStagedClipboardForHeraPane` drop the local-state clear and the
    `acc.ClipboardClear(taskID)` call; both still copy via `copyToClipboard`
    and flash "Copied".
  - `internal/tui/widget/agentheader.go` — the clipboard hint renders with a
    new highlight style instead of the plain bold header-text style.
  - `internal/tui/hera/page.go` — the `(ctrl+y copy)` border-title affordance
    picks up the same highlight treatment.
  - `internal/tui/theme/theme.go` — adds a clipboard-hint color/style constant
    (new, distinct from existing status colors) for the two call sites above.
- **No daemon/RPC/store change.** `internal/clipboard/store.go`,
  `internal/daemon/rpc.go`, and the REST clipboard endpoints are untouched —
  clearing behavior for session-exit, TTL, and last-write-wins is unchanged.
- **No new key, no rebinding.** Ctrl+Y stays bound to `keymap.ActAgentCopy`;
  only what happens after a successful copy changes.
- Specs are LOCAL DOCS only (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
