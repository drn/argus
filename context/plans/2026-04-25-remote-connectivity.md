# Robust Remote Connectivity for Argus

**Date:** 2026-04-25
**Goal:** Make the web UI a fully capable, phone-friendly remote for Argus — real terminal rendering, full task management, push notifications, installable as a PWA — so the user can manage agents fully from their phone.

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

### Phase 1 — Real terminal rendering
Fix the screenshot. Replace the polling box with xterm.js wired to the existing SSE stream and a new resize endpoint.

- [ ] Add `POST /api/tasks/{id}/resize` (rows, cols) → `session.Resize`
- [ ] Add `GET /api/tasks/{id}/size` to query current dims (helps reconnect)
- [ ] Vendor xterm.js + fit-addon (embedded — no CDN dependency, works offline / on Tailscale)
- [ ] Rewrite the detail-view terminal:
  - Mount xterm.js into `#term`
  - Open `EventSource` on `/api/tasks/{id}/stream`
  - base64-decode each SSE chunk, `term.write()` it
  - Initial dims: fit + POST resize
  - On viewport resize / orientation change: fit + POST resize
- [ ] Wire keystrokes: `term.onData` → POST `/input` (raw bytes, no newline injection)
- [ ] Drop `loadOutput`, `stripAnsi`, polling interval
- [ ] Auto-reconnect SSE if it drops
- [ ] Initial replay still works (server-side replays ring buffer on AddWriter)

### Phase 2 — Mobile terminal UX
Make the terminal usable on a phone. Hardware keyboards exist but most usage is iOS soft keyboard.

- [ ] Virtual key row above keyboard: Esc · Tab · Ctrl · ↑ ↓ ← → · Enter
- [ ] Sticky Ctrl modifier — tap once, next key is Ctrl+key
- [ ] Long-press Ctrl for caps-lock-style sticky
- [ ] Hide system keyboard helper (autocorrect, predictions) on the input proxy
- [ ] iOS-safe focus: hidden contenteditable input that captures keys; xterm handles display
- [ ] Tap-to-focus on terminal area
- [ ] Pinch-zoom to change font size; persist in localStorage
- [ ] Two-finger drag → terminal scroll (not page scroll)

### Phase 3 — PWA + auth resilience
Make it installable and trustworthy.

- [ ] `manifest.webmanifest` (name, icons 192/512, theme color, display: standalone)
- [ ] Apple touch icon, theme-color meta
- [ ] Service worker:
  - `cache-first` for static assets (HTML, JS, manifest, icons)
  - `network-only` for `/api/*`
  - Precache on install
  - Push event handler (Phase 5 will populate)
- [ ] localStorage token survives Safari clears: also offer iOS Share Sheet "Save to Files" fallback for token
- [ ] Auto-reconnect on regained connectivity (online event)
- [ ] Connection status pill (connected / reconnecting / offline)

### Phase 4 — API gap-fill
Bring REST coverage up to TUI parity for the verbs we want on a phone.

- [ ] `POST /api/tasks/{id}/archive` (and `/unarchive`)
- [ ] `POST /api/tasks/{id}/rename` (body: `{name}`)
- [ ] `POST /api/tasks/{id}/fork` (body: `{name?, prompt?, includeContext?}`)
- [ ] `POST /api/sessions/stop-all`
- [ ] `POST /api/tasks/{id}/status` (body: `{status: "in_review"|"complete"|...}`)
- [ ] `GET /api/projects/full` → returns `[{name, path, color, default_backend}]` (current `GET /api/projects` returns names only — keep both)
- [ ] `POST /api/projects`, `PUT /api/projects/{name}`, `DELETE /api/projects/{name}`
- [ ] `GET /api/backends`, `POST`, `PUT /api/backends/{name}`, `DELETE`
- [ ] `GET /api/tasks/{id}/git/status` (untracked, modified, staged)
- [ ] `GET /api/tasks/{id}/git/diff?path=<file>&unified=1`
- [ ] `GET /api/tasks/{id}/files` (worktree file tree, max depth)
- [ ] Augment task JSON with `worktree_path`, `archived`, `repo_dir`
- [ ] `GET /api/tasks?archived=1` filter

### Phase 5 — Push notifications (idle alerts)
Wire `dirac` idle detection to Web Push so the phone alerts when a task is waiting.

- [ ] VAPID keypair generated on first run, stored in DB config
- [ ] `GET /api/push/vapid-public-key`
- [ ] `POST /api/push/subscribe` (endpoint, p256dh, auth, deviceLabel)
- [ ] `DELETE /api/push/subscribe/{id}`
- [ ] `GET /api/push/subscriptions` (list, mask endpoint)
- [ ] `dirac` idle-tracker: when a session transitions to idle, fan out push to all subs
- [ ] Service worker `push` event → notification with task name, deep link to `/?task=<id>`
- [ ] Use a Go web-push library (e.g. `github.com/SherClockHolmes/webpush-go`) — small dep
- [ ] Throttle: 1 push per task per 5 minutes; coalesce bursts

### Phase 6 — Per-device tokens
Stop sharing the master bearer.

- [ ] DB table `api_tokens(id, label, hash, last_used, created_at, revoked_at)`
- [ ] Master token (the file at `~/.argus/api-token`) still works — used to mint device tokens
- [ ] `POST /api/tokens` (label) → returns plaintext token once
- [ ] `GET /api/tokens` — list with last4 + label + last_used
- [ ] `DELETE /api/tokens/{id}` — revoke
- [ ] Auth middleware checks master OR DB token (constant-time compare against hash)
- [ ] First-time flow on phone: scan QR with master token → get device token

### Phase 7 — SPA polish
Once API supports it, expose the verbs in the UI.

- [ ] Settings page in SPA: tokens, backends, projects, push subscriptions
- [ ] Archive section in task list (matches TUI)
- [ ] Fork button in task detail
- [ ] Rename via long-press on task
- [ ] Stop-all action with confirmation
- [ ] Project picker shows colors

## Testing strategy

**Playwright mobile emulation against a real binary.**

- Build a `cmd/argus-test-server` (or use a `--headless-api` flag on `argus`) that:
  - Uses `t.TempDir()` HOME so it can't touch the real `~/.argus`
  - Boots the API on a fixed port with a known token
  - Optionally seeds a test task that runs `bash` (PTY echoes input) so the terminal is exercised
- Playwright config: iPhone 14 Pro emulation, headed mode for visual debug
- Test files in `web-tests/`:
  - `auth.spec.ts` — login flow, bad token, persistence
  - `tasks.spec.ts` — list, create, refresh, archive, rename, fork
  - `terminal.spec.ts` — terminal renders, keystrokes echo, resize on rotation, reconnect on drop
  - `pwa.spec.ts` — manifest is valid, service worker registers, offline shell loads
  - `push.spec.ts` — subscribe, mock idle event, notification fires (via service worker stub)
- Run with `npx playwright test` per phase. Don't claim a phase done until its specs are green.

## Out of scope (this round)

- Native iOS app
- Reviews tab (PR list / diff / inline comments) — defer; the desktop is better suited
- Multi-user support / RBAC
- TLS termination (still relies on Tailscale for transport security)
