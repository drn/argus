# Tasks — macos-app

Single PR (or a small stack). No Go behavior changes — `internal/api` is
reference material only. Verify with `swift build`/`swift test` per phase and
`make pre-pr` for the unaffected Go tree before opening/updating the PR.

## Phase A — Scaffold (ArgusKit SDK + app shell + Makefile)

- [ ] A.1 `macos/Package.swift`: SwiftPM package with `ArgusKit` (library) and
      `ArgusMac` (executable, depends on `ArgusKit` + `SwiftTerm`).
- [ ] A.2 `ArgusKit`: `ArgusClient` (Bearer-auth `URLSession` wrapper),
      `Task`/`Project`/`Backend`/`Schedule`/`Token` models decoding the
      existing REST JSON shapes (mirror `internal/apiclient` field-for-field).
- [ ] A.3 `ArgusKit`: `GET /api/tasks`, `GET /api/tasks/{id}`, `POST
      /api/tasks`, `GET /api/projects`, `GET /api/backends` — read/create
      paths only in this phase.
- [ ] A.4 `ArgusKit` unit tests (`swift test`, `URLProtocol` stub) for
      request shape, auth header, JSON decode, and 401 handling.
- [ ] A.5 `ArgusMac`: app entry point + `NavigationSplitView` shell — sidebar
      task rail (sections: active / in review / complete / archived) bound
      to `ArgusClient.listTasks()`, empty detail pane placeholder.
- [ ] A.6 `Makefile`: `mac-build` (`swift build --package-path macos`),
      `mac-test` (`swift test --package-path macos`), `mac-run` (`swift run
      --package-path macos ArgusMac`).

## Phase B — Terminal pane

**Depends on:** Phase A

- [ ] B.1 `ArgusKit`: `GET /api/tasks/{id}/output` (parse `X-Output-Total`),
      `GET /api/tasks/{id}/stream` SSE client decoding base64 frames, `POST
      /api/tasks/{id}/input`, `POST /api/tasks/{id}/resize`.
- [ ] B.2 `ArgusKit`: SSE event model for `exit` (`rerendering` flag) and
      `clipboard`; reconnect-with-backoff wrapper around the stream that
      resumes from the last-seen offset.
- [ ] B.3 `ArgusKit` tests: resume-offset math (no gap/overlap across a
      simulated reconnect), `exit`/`clipboard` event decode, backoff timing.
- [ ] B.4 `ArgusMac`: Terminal tab wiring `SwiftTerm.TerminalView` to the
      stream (feed bytes in, forward keystrokes/resizes out); tab switch
      preserves the live connection (no reconnect on tab change).
- [ ] B.5 `ArgusMac` UI test / manual smoke: open a real local task, confirm
      byte-identical scrollback vs. the TUI/web view for the same task.

## Phase C — Lifecycle + git views

**Depends on:** Phase A

- [ ] C.1 `ArgusKit`: stop / restart / resume / archive / unarchive / rename /
      delete / fork calls against their `/api/tasks/{id}/*` endpoints.
- [ ] C.2 `ArgusKit`: `GET /api/tasks/{id}/git/diff`, `GET
      /api/tasks/{id}/git/status`, `GET /api/tasks/{id}/files`, `GET
      /api/tasks/{id}/links`.
- [ ] C.3 `ArgusKit` tests for each new call (request shape, response decode,
      error surfaces on 404/missing task).
- [ ] C.4 `ArgusMac`: task action menu (rail context menu + detail toolbar)
      wired to C.1, with a native confirmation dialog gating stop/delete.
- [ ] C.5 `ArgusMac`: Diff tab (unified-diff rendering with add/remove
      styling), Files tab (worktree tree browser), Info tab (status, backend,
      PR/branch links from `links`).

## Phase D — Events, notifications, hera, schedules, settings

**Depends on:** Phase A, B

- [ ] D.1 `ArgusKit`: `GET /api/events/stream` named-event SSE client with
      since-cursor tracking and resync-on-invalid-cursor handling.
- [ ] D.2 `ArgusMac`: wire the events stream to rail updates (no polling);
      full `GET /api/tasks` resync path on cursor-invalid.
- [ ] D.3 `ArgusMac`: `UNUserNotificationCenter` integration — native
      notification + dock badge increment on `session.needs_input` /
      `session.idle` for a task not currently focused; badge clears when that
      task becomes focused or resolves.
- [ ] D.4 `ArgusKit`: `GET /api/hera` roster fetch + role-tree model.
- [ ] D.5 `ArgusMac`: Hera tab rendering the roster read-only (no mutation
      controls), matching web-app scope.
- [ ] D.6 `ArgusKit`: `GET /api/schedules`, `POST /api/schedules/{id}/run`.
- [ ] D.7 `ArgusMac`: Schedules list view with a "Run now" action.
- [ ] D.8 `ArgusMac`: Settings surface (server URL + token override,
      defaulting to `http://localhost:7743` and `~/.argus/api-token`),
      persisted via `UserDefaults`/Keychain as appropriate (token in
      Keychain, never written to a plaintext file by this app).
- [ ] D.9 Tests: events resync behavior, notification-suppressed-when-focused
      logic, hera roster decode, schedule run-now call shape.

## Phase E — `.app` bundle + docs + parity rule + archive

**Depends on:** Phase A–D

- [ ] E.1 `make mac-app`: script/target assembling `ArgusMac.app` (Info.plist,
      icon, `swift build -c release` binary) with ad-hoc codesign
      (`codesign --sign -`); verify it launches via `open` with no Gatekeeper
      override needed.
- [ ] E.2 README: new Reference section documenting the macOS app (build/run,
      feature surface, settings, how it relates to the web PWA and TUI).
- [ ] E.3 CLAUDE.md: add the three-surface frontend parity rule (TUI / web
      app / macOS app; parity scoped to the REST-exposed surface; gaps
      recorded as named follow-ups).
- [ ] E.4 `context/knowledge/gotchas/`: new file (or `misc.md` section) for
      mac-app-specific invariants — SSE reconnect/backoff + offset math,
      notification-focus suppression rule, Keychain-only token storage,
      ad-hoc codesign requirement for `mac-app`. Update
      `context/knowledge/index.md`.
- [ ] E.5 `swift test --package-path macos` green across `ArgusKit`; manual
      smoke pass of `ArgusMac` against a local daemon for each phase's
      scenarios.
- [ ] E.6 `make pre-pr` green (Go tree unaffected but must stay green).
- [ ] E.7 Archive this change: fold the delta into
      `openspec/specs/macos-app/`, move this folder to
      `openspec/changes/archive/<date>-macos-app/`, commit on the branch.
