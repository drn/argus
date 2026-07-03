# Add a native macOS app (ArgusMac)

## Why

Argus already ships three surfaces off a single daemon: the TUI, a Unix-socket
API for the daemon itself, and a REST + SSE API on `:7743` that a browser PWA
already consumes end to end (task CRUD, live PTY streaming, git/file views,
events, hera roster, schedules). The remote-TUI mode (`--remote URL --token`,
`internal/apiclient` + `internal/apistore`) further proves that a REST-only
frontend — with no local SQLite, no local daemon, no Unix socket — is a
first-class way to drive argus. What's missing is a native, clickable macOS
GUI in the Conductor class: a proper sidebar/detail app instead of a terminal
or a browser tab, for the same daemon the TUI already talks to.

## What Changes

- **New SwiftPM package at `macos/`** with two products:
  - **`ArgusKit`** (library) — a typed Swift API client mirroring
    `internal/apiclient`: task/project/backend/schedule/token CRUD, PTY
    streaming (`GET /api/tasks/{id}/stream` SSE + `/output` resume), input/
    resize, git/file endpoints, `/api/events/stream`, `/api/hera`. No SDK
    dependency on Go — pure HTTP + SSE over `URLSession`, Bearer-token auth,
    matching the existing REST contract byte-for-byte.
  - **`ArgusMac`** (executable) — a SwiftUI app: task rail, detail pane with
    Terminal/Diff/Files/Info tabs, a `SwiftTerm`-backed terminal view for the
    live PTY pane, native notifications, and a settings surface.
- **New `Makefile` targets**: `mac-build`, `mac-test`, `mac-run`, `mac-app`
  (assembles a runnable `.app` bundle via ad-hoc codesign — no Xcode project,
  pure `swift build`).
- **README** gains a Reference section documenting the macOS app the same way
  the web PWA is documented today (build/run instructions, feature surface,
  settings).
- **CLAUDE.md** gains a **three-surface parity rule**: any user-facing feature
  change must be evaluated against all three frontends (TUI, web app, macOS
  app), where "parity" is scoped to the REST-exposed surface; intentional gaps
  are recorded as explicit follow-ups rather than silently diverging.

**No Go behavior changes.** The daemon's REST/SSE API is the entire contract;
this change adds a consumer, not a producer. `internal/api`, `internal/apiclient`,
and `internal/apistore` are read-only reference material for this change, not
edited by it.

## Non-Goals / Follow-ups

- **Hera is read-only in the mac app**, matching the web app today: the daemon
  does not expose hera mutation actions (spawn worker, send message, plan
  mutation) over REST, only `GET /api/hera`. Wiring hera mutation endpoints
  and consuming them from all three frontends (web + macOS, TUI already has
  native Hera) is a named follow-up change, not part of this one.
- No Xcode project, no Mac App Store distribution, no Sparkle auto-update —
  ad-hoc-signed local builds only, matching how the TUI binary is built and
  run today.
- No new daemon endpoints. If a mac-app feature needs data the REST API
  doesn't expose yet, it is descoped to a follow-up rather than growing
  `internal/api` inside this change.

## Impact

- Affected specs: **macos-app** (new).
- Affected code: new `macos/` directory (SwiftPM package: `ArgusKit` +
  `ArgusMac`), `Makefile` (new `mac-*` targets), `README.md` (new Reference
  section), `CLAUDE.md` (new three-surface parity rule), `context/knowledge/`
  (new gotchas file for the mac app's SSE/reconnect/notification invariants).
- No schema change, no daemon change, no breaking change to existing surfaces.
