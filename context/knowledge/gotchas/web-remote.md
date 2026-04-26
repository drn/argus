# Web Remote Gotchas

Non-obvious invariants for the SPA + REST API + push notifications stack.

## Auth & EventSource

- **`EventSource` cannot set custom headers** — `/api/tasks/{id}/stream` MUST accept `?token=<token>` query-param auth as a fallback. The auth middleware (`internal/api/auth.go`) checks the Bearer header first, then falls back to the query param. Removing the query-param path will break the SPA terminal silently (the EventSource will 401 forever, looking like "stream just doesn't connect").
- **PWA assets must be exempt from auth** — `/`, `/vendor/`, `/sw.js`, `/manifest.webmanifest`, `/icon-*.png`, `/apple-touch-icon.png` are all unauthenticated. The browser fetches them at install/registration time before any login UI exists. Adding auth to any of these breaks PWA install on iOS without an obvious error.
- **Skip-paths ending in `/` match by prefix; otherwise exact match.** This is what lets `"/vendor/"` cover every vendor file via one entry. Don't strip the trailing slash thinking it's cosmetic.

## Service worker

- **Never cache `/api/*`** — the service worker must use `network-only` for the API. Caching auth-bearing responses would either leak data or break dynamic state (task list, output stream). The `fetch` handler explicitly returns early for `/api/` paths.
- **Bump `SW_VERSION` on shell change** — caches keyed by version string. Without a bump the browser keeps serving the stale shell forever; users will think changes never deployed.
- **`sw.js` must NOT be aggressively cached** — the route handler sets `Cache-Control: no-cache` for `sw.js` only. Everything else gets `max-age=86400`. If `sw.js` itself caches, you can never push an update.

## xterm.js / SSE

- **base64 SSE chunks → `Uint8Array`, not string** — `term.write` accepts both, but using a string forces UTF-8 round-tripping that mangles raw bytes from agent output (status bars, ANSI sequences with high-byte content). Always decode base64 → byte array via `b64ToBytes`.
- **`fitAddon.fit()` must run after layout** — call inside `requestAnimationFrame` or via `setTimeout(0)` after `term.open(...)`. Calling synchronously yields zero/incorrect cell sizes because computed CSS isn't ready yet.
- **Resize POST is debounced (100ms) on `window.resize`** — but use `setTimeout(200)` for `orientationchange` because iOS Safari fires `orientationchange` before the viewport actually settles.
- **Empty `[]taskJSON` encodes as `null`, not `[]`** — Go's `encoding/json` will emit `null` for a nil slice. Use `make([]taskJSON, 0)` for any handler whose response slice goes through the wire (`handleListTasks` does this; if you add another, do the same). The SPA does `tasks.find(...)` and crashes on null otherwise.

## HTML escaping in `index.html`

- **`esc()` only escapes `<`, `>`, `&` — NOT `"` or `'`.** It builds via `textContent` → `innerHTML`, which is safe in element-content position but injectable in attribute position when the attribute value is double-quoted. Patterns like `data-foo="${esc(name)}"` or `<option value="${esc(p)}">` are vulnerable to attribute escape via a `"` in `name`. For untrusted strings going into attributes, prefer index-based lookup against a render-time array (see `renderedProjects` in `renderTaskList`) or a true attribute-aware escape. Project names, task IDs, etc. all flow from user-controlled DB rows.

## Mobile virtual key row

- **Sticky Ctrl is implemented in `term.onData`, not via xterm key events** — xterm's helper textarea fires onData with the un-modified character. The handler intercepts a single ASCII character when `ctrlArmed` is true and applies `code & 0x1f`. Don't try to modify `ev.ctrlKey` on the keyboard event — it's read-only.
- **Ctrl auto-clears after one keystroke unless locked** — the second tap on the Ctrl button locks (`ctrlLocked = true`); third tap clears. Without this, holding "Ctrl mode" feels stuck.

## Detail-view layout

- **`#detail-view.open` is `position: fixed; height: 100dvh` so only the xterm scrolls** — page-level scroll inside the detail view fights xterm's scrollback for touch and used to make scrolling unusable on iOS. The replacement uses a flex column where `.term-wrap` is `flex: 1`. Don't reintroduce page-flow children that grow the detail view past viewport height; nested scroll comes back the moment you do.
- **Use `100dvh`, not `100vh`, for the fixed detail view** — Safari's address bar shrinks/grows the visual viewport but `vh` reflects the layout viewport, so `100vh` puts the bottom-anchored vkey-row behind the address bar / soft keyboard. `dvh` resizes with the visible area.
- **`dvh` does NOT account for the soft keyboard — wire `visualViewport` for that** — `dvh` reflects browser chrome only. When the OS keyboard slides up, `visualViewport.height` shrinks but `dvh` doesn't, so the vkey row + detail-actions get hidden behind the keyboard. `syncVisualViewport()` writes `visualViewport.height` to the `--app-height` CSS var (consumed by `#detail-view.open { height: var(--app-height, 100dvh) }`) and translates the fixed view by `visualViewport.offsetTop` for iOS. Re-fit the terminal after the resize so xterm reflows for the new row count.
- **Action buttons live in an overflow menu (`#overflow-menu`)** — only the primary action (Stop / Resume) is inline; Rename/Fork/Archive/PR/Delete are inside a popover toggled via `#btn-overflow`. The IDs (`#btn-rename`, `#btn-archive`, etc.) are preserved on the menu items so existing Playwright selectors still work, but tests must click `#btn-overflow` first to open the menu.
- **Jump-to-input button visibility is driven by `term.onScroll`, not `term.write`** — the button appears whenever `buffer.active.viewportY < buffer.active.baseY` and hides when xterm auto-scrolls back to the bottom (which fires `onScroll`). Don't try to track scroll state from the SSE message handler — xterm's auto-follow already gets it right and `onScroll` covers manual moves.

## Web Push / VAPID

- **VAPID keys are persisted in the `config` table** — keys `push.vapid_public` and `push.vapid_private`. Regenerating them invalidates every existing subscription (the push service rejects with 401). Only delete these if you also clear `push_subscriptions`.
- **Push throttle map is in-memory only** — `idle:{taskID}` keys live in `Manager.lastSent`. Daemon restart resets all throttles, so a task that was just notified can be re-notified within 5 min. Acceptable for now; persist if it becomes annoying.
- **iOS Safari requires the page to be installed as a PWA before it will request push permission** — `Notification.requestPermission()` from a regular Safari tab on iOS silently denies. Add to Home Screen first, then open the standalone app, then tap Enable.
- **Push subscriptions returning HTTP 410 are auto-deleted** — `sendOne` checks for 410/404 from the push service and calls `DeletePushSubscriptionByEndpoint`. This is the only way to clean up after a user uninstalls the PWA.

## Per-device tokens

- **Master token is the only credential that can mint or revoke device tokens** — auth middleware sets `X-Argus-Auth: master|device` header on the request; mint/revoke handlers check for `master`. If you want to allow device-token-initiated minting, gate it behind a separate explicit capability flag, not just `auth != nil`.
- **`api_tokens.hash` is SHA-256 of plaintext, NOT bcrypt/argon** — bearer tokens already have ~256 bits of entropy from `crypto/rand`, so a single SHA-256 pass is sufficient. Don't switch to bcrypt thinking it's "more secure"; you'll add latency for no benefit.

## Test harness

- **`cmd/argus-test-server/` runs an isolated API server with `HOME=$tempdir`** — it MUST set HOME before any path resolution because `WorktreeDir()` and `db.DataDir()` resolve through `$HOME`. Without the override, the harness writes to the real `~/.argus/`.
- **`/test/reset` is a separate HTTP listener on `port+10`** — it's not under auth, so it must NOT be on the public listener. Tests call it from beforeEach to clear state between specs.
- **Playwright tests are NOT parallel** — `fullyParallel: false, workers: 1` in `playwright.config.ts`. The test server is single-tenant; concurrent tests would race on the seed task. Don't change this without rewriting the harness for multi-tenant isolation.
