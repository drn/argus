# Native macOS app (`macos/`) — gotchas

The `macos/` SwiftPM package (ArgusKit SDK + ArgusMac SwiftUI app) is a thin REST/SSE client of the daemon. Landmines below are Swift/SwiftPM/toolchain quirks, NOT feature descriptions.

## `swift test` silently runs ZERO tests on a CLT-only install

**On a Command Line Tools install (no Xcode), `swift test` builds the `.xctest` bundle but `swiftpm-testing-helper` cannot execute it and exits 0 without running a single test — even a deliberately failing test "passes".** The suite is therefore an **executable target** (`ArgusKitTests`) that calls swift-testing's entry point directly; run it via `make mac-test` (`swift run ArgusKitTests`), never `swift test`. A deliberately-failing canary proved the silent no-op. `make test`/CI does not cover Swift — the only real Swift gate is `make mac-test`.

## SwiftPM's sandbox cannot NEST inside an argus agent sandbox

**Every `swift build`/`swift run` needs `--disable-sandbox`.** SwiftPM sandboxes manifest/plugin compiles via `sandbox-exec`, and macOS forbids a nested sandbox — inside an argus agent sandbox (how this repo is dogfooded) the build dies with `sandbox_apply: Operation not permitted`. The flag is baked into all `mac-*` make targets.

## swift-testing on CLT needs explicit framework/rpath/plugin flags on a non-test target

**Because the suite is an executable (not a `testTarget`), SwiftPM does not auto-wire swift-testing's search paths** — `Package.swift` (`swiftTestingFlags()`) adds `-F`/`-rpath` to `<devdir>/Library/Developer/Frameworks` (Testing.framework) + `<devdir>/Library/Developer/usr/lib` (lib_TestingInterop.dylib) and `-plugin-path` to `<devdir>/usr/lib/swift/host/plugins/testing` (the `@Test`/`@Suite` macros). It **probes** candidate dev dirs with `FileManager` (honoring `DEVELOPER_DIR`) rather than shelling `xcode-select -p`, because the manifest sandbox forbids spawning subprocesses. On an Xcode install these paths are absent, the flags are skipped, and default resolution is left untouched.

## In ArgusMac always write `_Concurrency.Task { }`, never bare `Task { }`

**ArgusKit exports a `Task` model type (the argus task) that shadows Swift Concurrency's `Task`** — a bare `Task { }` in ArgusMac resolves to the wrong symbol. Every concurrency spawn/sleep/cancel in the app is fully qualified (`_Concurrency.Task`, `_Concurrency.Task.sleep`, `_Concurrency.Task.isCancelled`).

## The stream state machines' `streamOpening()` contract keeps reconnects alive

**A scheduled reconnect attempt MUST call `streamOpening()` when it starts dialing, or a failed attempt is swallowed and retries stop forever.** `scheduleReconnect()` leaves the phase in `.reconnecting`; `streamClosed()` returns nil in `.reconnecting` (so the old stream's own close does not double-book a reconnect). `streamOpening()` flips `.reconnecting → .connecting` so that when *this* attempt's stream closes, `streamClosed()` re-enters the backoff path with the grown delay. Without it, a daemon outage longer than one retry window kills reconnection permanently. `TerminalStreamSession` and `EventsStreamSession` share this exact pattern; tests pin it.

## Events consumption uses subscribe-before-snapshot fencing, client-side

**Open the `/api/events/stream` first and BUFFER decoded events, THEN fetch the `/api/tasks` snapshot as the baseline and drain the buffer on top** (`AppState.consumeEvents` / `eventBuffering`). Stream-before-snapshot closes the gap where an event committed between a snapshot and a later subscribe would be lost — mirroring the daemon's own SSE ring fencing (`internal/api/events_stream.go`). A `resync` or `unknown` event type must trigger a re-snapshot (`EventsStreamSession.effect` → `.resnapshot`), never a crash; `task.created`/`task.forked` also re-snapshot because the event lacks the full task shape.

## TerminalControllers are cached per task ID and pruned only on disappearance

**Terminal controllers are cached by task ID (`AppState.terminalControllers`) so switching tasks in the sidebar preserves scrollback**, and are torn down ONLY when the task drops out of an `/api/tasks` snapshot (`pruneTerminalControllers(keeping:)`) or the client is rebuilt. Do not tear a controller down on mere view disappearance — that loses the terminal buffer on every sidebar switch.
