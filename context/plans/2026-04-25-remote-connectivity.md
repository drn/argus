# Robust Remote Connectivity for Argus

**Date:** 2026-04-25
**Status:** Shipped in PR #480 — https://github.com/drn/argus/pull/480
**Goal:** Make the web UI a fully capable, phone-friendly remote for Argus — real terminal rendering, full task management, push notifications, installable as a PWA — so the user can manage agents fully from their phone.

## Status summary

All seven phases implemented and merged-ready. **CI green, 43 Playwright specs passing on iPhone 14 Pro emulation.**

| Phase | Items planned | Done | Deferred |
|---|---|---|---|
| 1. Real terminal | 8 | 8 | 0 |
| 2. Mobile UX | 8 | 4 | 4 (long-press lock, autocorrect-disable, contenteditable proxy, two-finger scroll) |
| 3. PWA | 7 | 6 | 1 (Files-app token fallback) |
| 4. API gap-fill | 13 | 13 | 0 |
| 5. Web Push | 9 | 9 | 0 |
| 6. Per-device tokens | 7 | 6 | 1 (QR-code mint flow) |
| 7. SPA polish | 6 | 4 | 2 (projects/backends UI in Settings, project color in picker) |

**Pinch-to-zoom font sizing** was implemented as A−/A+ buttons instead of pinch (works the same, plays nicer with iOS gestures).
**`dirac` idle hook** was implemented as an in-process polling watcher (`idleWatcher` in `internal/api/push.go`) — same outcome, decoupled from the TUI's dirac.
**Long-press rename** on tasks became a Rename button in the detail view (long-press on iOS conflicts with text selection).

## Symptoms today (the screenshot)

The mobile dashboard polls `/api/tasks/{id}/output?clean=1` every 2s and renders the result through `stripAnsi()` into a `<div>` with `white-space: pre-wrap`. This:

- Does **not** maintain a terminal grid → wrapped text gets re-wrapped by the browser, lines double up.
- Drops cursor positioning, line erase (`\r`), scroll regions, alternate-screen-buffer → status bars repeat.
- Polls instead of using the SSE stream → 2s lag, partial chunks at boundaries.
- Has no PTY size, so the agent's status bar is sized for 80x24 but the phone is rendering it free-flow.

## Architecture target

```
                Phone (PWA, installable)
                ┌──────────────────────────────┐
                │ xterm.js  ←─ SSE stream ──┐  │
                │     ↓                     │  │
                │  POST /input  ──────►     │  │
                │  POST /resize  ─────►     │  │
                │  Service Worker (push)    │  │
                └───────────────────────────┼──┘
                                            │
                                  Tailscale │
                                            │
              Argus daemon (API on 7743)    ▼
              ┌──────────────────────────────┐
              │ REST + SSE + WebPush + idle  │
              │   ├── tasks (CRUD + actions) │
              │   ├── projects/backends CRUD │
              │   ├── git status/diff/files  │
              │   ├── reviews                │
              │   ├── tokens (per-device)    │
              │   └── push subscriptions     │
              │ Runner ──► PTY sessions      │
              └──────────────────────────────┘
```

## Phases

Each phase ships independently. After each, run Playwright mobile-emulation tests.

### Phase 1 — Real terminal rendering — ✅ Done
Fix the screenshot. Replace the polling box with xterm.js wired to the existing SSE stream and a new resize endpoint.

- [x] Add `POST /api/tasks/{id}/resize` (rows, cols) → `session.Resize`
- [x] Add `GET /api/tasks/{id}/size` to query current dims (helps reconnect)
- [x] Vendor xterm.js + fit-addon (embedded — no CDN dependency, works offline / on Tailscale)
- [x] Rewrite the detail-view terminal:
  - [x] Mount xterm.js into `#term`
  - [x] Open `EventSource` on `/api/tasks/{id}/stream`
  - [x] base64-decode each SSE chunk, `term.write()` it
  - [x] Initial dims: fit + POST resize
  - [x] On viewport resize / orientation change: fit + POST resize
- [x] Wire keystrokes: `term.onData` → POST `/input` (raw bytes, no newline injection)
- [x] Drop `loadOutput`, `stripAnsi`, polling interval
- [x] Auto-reconnect SSE if it drops (EventSource auto-reconnects by default)
- [x] Initial replay still works (server-side replays ring buffer on AddWriter)

### Phase 2 — Mobile terminal UX — ⚠️ Partial
Make the terminal usable on a phone. Hardware keyboards exist but most usage is iOS soft keyboard.

- [x] Virtual key row above keyboard: Esc · Tab · Ctrl · ↑ ↓ ← → · ^C · Enter (+ A−/A+)
- [x] Sticky Ctrl modifier — tap once, next key is Ctrl+key (intercepted in `term.onData`)
- [x] Tap second time on Ctrl for caps-lock-style sticky (changed from long-press to double-tap; iOS long-press conflicts with magnifier)
- [ ] Hide system keyboard helper (autocorrect, predictions) on the input proxy — **deferred; needs real iOS device to validate**
- [ ] iOS-safe focus: hidden contenteditable input that captures keys; xterm handles display — **deferred; relying on xterm's built-in helper textarea for now**
- [x] Tap-to-focus on terminal area (xterm built-in)
- [x] Buttons to change font size; persist in localStorage (changed from pinch — A−/A+ in vkey row)
- [ ] Two-finger drag → terminal scroll (not page scroll) — **deferred; xterm-viewport scrolling works for single-finger now**

### Phase 3 — PWA + auth resilience — ✅ Mostly done
Make it installable and trustworthy.

- [x] `manifest.webmanifest` (name, icons 192/512, theme color, display: standalone)
- [x] Apple touch icon, theme-color meta
- [x] Service worker:
  - [x] `cache-first` for static assets (HTML, JS, manifest, icons)
  - [x] `network-only` for `/api/*`
  - [x] Precache on install
  - [x] Push event handler (Phase 5 wired it up)
  - [x] `notificationclick` handler with deep-link to `/?task=<id>`
- [ ] localStorage token survives Safari clears: also offer iOS Share Sheet "Save to Files" fallback for token — **deferred; minting per-device tokens (Phase 6) is the recovery path**
- [x] Auto-reconnect on regained connectivity (online event)
- [x] Connection status pill — `term-status` pill for SSE; conn-dot in header for top-level connectivity

### Phase 4 — API gap-fill — ✅ Done
Bring REST coverage up to TUI parity for the verbs we want on a phone.

- [x] `POST /api/tasks/{id}/archive` (and `/unarchive`)
- [x] `POST /api/tasks/{id}/rename` (body: `{name}`)
- [x] `POST /api/tasks/{id}/fork` (body: `{name?, prompt?, project?}`) — context-file bundling deferred to Phase 7+
- [x] `POST /api/sessions/stop-all`
- [x] `POST /api/tasks/{id}/status` (body: `{status: "in_review"|"complete"|"pending"|"in_progress"}`)
- [x] `GET /api/projects/full` → returns `[{name, path, branch, backend}]`
- [x] `POST /api/projects`, `PUT /api/projects/{name}`, `DELETE /api/projects/{name}`
- [x] `GET /api/backends`, `POST`, `PUT /api/backends/{name}`, `DELETE`
- [x] `GET /api/tasks/{id}/git/status` (status + diff stat + branch diff)
- [x] `GET /api/tasks/{id}/git/diff?path=<file>`
- [x] `GET /api/tasks/{id}/files?dir=<rel>` (worktree file listing)
- [x] Augment task JSON with `worktree_path`, `archived`, `prompt` (`repo_dir` not exposed — `worktree_path` is sufficient for the SPA)
- [x] `GET /api/tasks?archived=1` filter (also `?archived=all`)

### Phase 5 — Push notifications (idle alerts) — ✅ Done
Wire `dirac` idle detection to Web Push so the phone alerts when a task is waiting.

- [x] VAPID keypair generated on first run, stored in DB config (`push.vapid_public/private`)
- [x] `GET /api/push/vapid-public-key`
- [x] `POST /api/push/subscribe` (endpoint, p256dh, auth, label)
- [x] `DELETE /api/push/subscribe/{id}`
- [x] `GET /api/push/subscriptions` (list with masked endpoints)
- [x] Idle tracker: when a session transitions to idle, fan out push to all subs (implemented as `idleWatcher` polling `sess.IsIdle()` every 5s — same outcome as hooking `dirac` directly; decoupled from the TUI)
- [x] Service worker `push` event → notification with task name + `notificationclick` deep-link to `/?task=<id>`
- [x] Use `github.com/SherClockHolmes/webpush-go`
- [x] Throttle: 1 push per task per 5 minutes (in-memory `lastSent` map)
- [x] Auto-prune subscriptions returning HTTP 410 Gone
- [x] `POST /api/push/test` — fire test notification to all devices

### Phase 6 — Per-device tokens — ✅ Mostly done
Stop sharing the master bearer.

- [x] DB table `api_tokens(id, label, hash, last4, last_used, created_at, revoked_at)`
- [x] Master token (the file at `~/.argus/api-token`) still works — used to mint device tokens
- [x] `POST /api/tokens` (label) → returns plaintext token once
- [x] `GET /api/tokens` — list with last4 + label + last_used + created_at + revoked
- [x] `DELETE /api/tokens/{id}` — revoke (master-only)
- [x] Auth middleware checks master OR DB token (SHA-256 hash lookup); sets `X-Argus-Auth: master|device` header so mint/revoke handlers can gate on master
- [ ] First-time flow on phone: scan QR with master token → get device token — **deferred; user can paste master token once and mint a device token from the Settings tab**

### Phase 7 — SPA polish — ✅ Mostly done
Once API supports it, expose the verbs in the UI.

- [x] Settings page in SPA: tokens (mint/revoke/forget), push subscribe + test push
- [ ] Settings page: backends + projects CRUD UI — **deferred; API is there, UI is not**
- [x] Archive section in task list (Active/Archived segmented control)
- [x] Fork button in task detail
- [x] Rename via Rename button (changed from long-press; iOS conflicts with text-selection long-press)
- [x] Stop-all action with confirmation
- [ ] Project picker shows colors — **deferred; project model doesn't expose color via API yet**

## Testing strategy — ✅ Done

**Playwright mobile emulation against a real binary.**

- [x] `cmd/argus-test-server` boots API on a fixed port with a known token, HOME=tempdir, seeded `bash`-backed task that PTY-echoes input
- [x] `/test/reset` endpoint (separate listener on port+10) clears state between specs
- [x] Playwright config: iPhone 14 Pro emulation
- [x] Test files in `web-tests/tests/` (43 specs total):
  - [x] `auth.spec.ts` — login flow, bad token, persistence
  - [x] `api.spec.ts` — list, create, archive/unarchive, rename, fork, status, projects + backends CRUD, stop-all
  - [x] `terminal.spec.ts` — xterm renders, SSE connects, keystrokes echo via PTY, resize on rotation, back-button cleanup
  - [x] `vkeys.spec.ts` — virtual key sequences, sticky Ctrl, font-size persistence
  - [x] `pwa.spec.ts` — manifest is valid, icons reachable, service worker registers, PWA assets unauthenticated
  - [x] `push.spec.ts` — VAPID public key, subscribe, list (masked), delete, validation
  - [x] `tokens.spec.ts` — mint, dual-auth, master-only mint, revoke
  - [x] `settings-ui.spec.ts` — settings tab, mint via UI, revoke via UI, archive/rename/fork via UI
  - [x] `visual.spec.ts` — captures screenshots for tasks/terminal/settings views

## Out of scope (this round)

- Native iOS app
- Reviews tab (PR list / diff / inline comments) — defer; the desktop is better suited
- Multi-user support / RBAC
- TLS termination (still relies on Tailscale for transport security)

## Follow-ups for a future round

- **iOS keyboard polish** — autocorrect-disable, contenteditable proxy, two-finger scroll. Need a real iOS device to validate (Playwright emulation doesn't show the soft keyboard).
- **QR-code mint flow** — scan master token with phone camera to skip the paste step.
- **Settings tab: projects + backends CRUD UI** — the REST endpoints are in; the UI is not.
- **Project color in API** — `config.Project` doesn't carry a color field; the TUI uses palette indexing. Add and expose it.
- **iOS Files-app token fallback** — backup if Safari clears localStorage and the user lost the master token. Mitigated by per-device tokens but not eliminated.
- **Persistent push throttle** — `Manager.lastSent` is in-memory; daemon restart resets. Persist if it becomes annoying.
- **Fork context bundling** — current fork is "lite" (name/prompt/project); the TUI's fork brings recent output + git diff into `.context/` files. Move that logic to a shared package and use it from the API.
- **Real-device QA on iPhone via Tailscale** — gating the merge.
