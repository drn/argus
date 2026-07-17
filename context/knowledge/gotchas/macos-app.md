# Native macOS app (`macos/`) — gotchas

The `macos/` SwiftPM package (ArgusKit SDK + Argus SwiftUI app) is a thin REST/SSE client of the daemon. Landmines below are Swift/SwiftPM/toolchain quirks, NOT feature descriptions.

## `swift test` silently runs ZERO tests on a CLT-only install

**On a Command Line Tools install (no Xcode), `swift test` builds the `.xctest` bundle but `swiftpm-testing-helper` cannot execute it and exits 0 without running a single test — even a deliberately failing test "passes".** The suite is therefore an **executable target** (`ArgusKitTests`) that calls swift-testing's entry point directly; run it via `make mac-test` (`swift run ArgusKitTests`), never `swift test`. A deliberately-failing canary proved the silent no-op. `make test`/CI does not cover Swift — the only real Swift gate is `make mac-test`.

## SwiftPM's sandbox cannot NEST inside an argus agent sandbox

**Every `swift build`/`swift run` needs `--disable-sandbox`.** SwiftPM sandboxes manifest/plugin compiles via `sandbox-exec`, and macOS forbids a nested sandbox — inside an argus agent sandbox (how this repo is dogfooded) the build dies with `sandbox_apply: Operation not permitted`. The flag is baked into all `mac-*` make targets.

## swift-testing on CLT needs explicit framework/rpath/plugin flags on a non-test target

**Because the suite is an executable (not a `testTarget`), SwiftPM does not auto-wire swift-testing's search paths** — `Package.swift` (`swiftTestingFlags()`) adds `-F`/`-rpath` to `<devdir>/Library/Developer/Frameworks` (Testing.framework) + `<devdir>/Library/Developer/usr/lib` (lib_TestingInterop.dylib) and `-plugin-path` to `<devdir>/usr/lib/swift/host/plugins/testing` (the `@Test`/`@Suite` macros). It **probes** candidate dev dirs with `FileManager` (honoring `DEVELOPER_DIR`) rather than shelling `xcode-select -p`, because the manifest sandbox forbids spawning subprocesses. On an Xcode install these paths are absent, the flags are skipped, and default resolution is left untouched.

## In Argus always write `_Concurrency.Task { }`, never bare `Task { }`

**ArgusKit exports a `Task` model type (the argus task) that shadows Swift Concurrency's `Task`** — a bare `Task { }` in Argus resolves to the wrong symbol. Every concurrency spawn/sleep/cancel in the app is fully qualified (`_Concurrency.Task`, `_Concurrency.Task.sleep`, `_Concurrency.Task.isCancelled`).

## The stream state machines' `streamOpening()` contract keeps reconnects alive

**A scheduled reconnect attempt MUST call `streamOpening()` when it starts dialing, or a failed attempt is swallowed and retries stop forever.** `scheduleReconnect()` leaves the phase in `.reconnecting`; `streamClosed()` returns nil in `.reconnecting` (so the old stream's own close does not double-book a reconnect). `streamOpening()` flips `.reconnecting → .connecting` so that when *this* attempt's stream closes, `streamClosed()` re-enters the backoff path with the grown delay. Without it, a daemon outage longer than one retry window kills reconnection permanently. `TerminalStreamSession` and `EventsStreamSession` share this exact pattern; tests pin it — and the same growth ladder is unaffected by the connected signal below because a dial that FAILS before the response never fires it.

## An open-but-silent SSE stream must render live, not "connecting" forever

**Phase only reaches `.live` on a NEW frame, so an idle agent (no output for minutes) would spin on "connecting" indefinitely — the stream wrappers therefore emit a synthetic connected signal the instant the HTTP response validates (2xx), before any frame.** `ArgusClient.stream(onOpen:)` fires the callback right after status validation; `terminalStream` yields it as `TerminalEvent.connected` (flows through `TerminalStreamSession.handle` → `.connecting`/`.reconnecting` → `.live`, resets backoff, `.ended` stays ended, offset untouched, no actions), and `eventsStream` yields `EventStreamItem.connected` (AppState calls `EventsStreamSession.streamConnected()`, same transition). It is NOT decoded from an SSEvent and must not advance the cursor/offset.

## Events consumption uses subscribe-before-snapshot fencing, client-side

**Open the `/api/events/stream` first and BUFFER decoded events, THEN fetch the `/api/tasks` snapshot as the baseline and drain the buffer on top** (`AppState.consumeEvents` / `eventBuffering`). Stream-before-snapshot closes the gap where an event committed between a snapshot and a later subscribe would be lost — mirroring the daemon's own SSE ring fencing (`internal/api/events_stream.go`). A `resync` or `unknown` event type must trigger a re-snapshot (`EventsStreamSession.effect` → `.resnapshot`), never a crash; `task.created`/`task.forked` also re-snapshot because the event lacks the full task shape.

## TerminalControllers are cached per task ID and pruned only on disappearance

**Terminal controllers are cached by task ID (`AppState.terminalControllers`) so switching tasks in the sidebar preserves scrollback**, and are torn down ONLY when the task drops out of an `/api/tasks` snapshot (`pruneTerminalControllers(keeping:)`) or the client is rebuilt. Do not tear a controller down on mere view disappearance — that loses the terminal buffer on every sidebar switch.

## `bytes.lines` swallows empty lines — never parse SSE with it

**Swift's `AsyncLineSequence` (`URLSession.AsyncBytes.lines`) silently drops empty lines, and SSE dispatches events on the blank line — parsed through it, NO event ever dispatches**: frames coalesce into one giant `\n`-joined payload that fails base64 at stream end, the resume cursor stagnates, and overlapping replays paint spliced garbage (stray `m`/`s`/`f` SGR terminators) over the terminal. `ArgusClient.stream` iterates raw bytes through `ByteLineSplitter` (preserves empties, strips `\r`, flushes trailing line); tests pin the empty-line property. Empirically verified: `"a\n\nb\n\n\nc\n"` yields `[a] [b] [c]` through `.lines`.

## SwiftTerm's TerminalView never takes first responder under SwiftUI hosting

**`TerminalView.mouseDown` only drives selection/mouse-reporting — it never calls `makeFirstResponder`, so a SwiftUI-hosted terminal is READ-ONLY (keystrokes never reach `keyDown`)**; a plain AppKit window's responder wiring masks this, `NSViewRepresentable` does not. `FocusTakingTerminalView` (TerminalTab.swift) overrides `mouseDown` (focus-then-super) and `viewDidMoveToWindow` (async focus grab on mount) so typing works on open and after clicking away and back.

## Ctrl+Z (0x1A) is stripped from outbound terminal input — never forward it

**A literal Ctrl+Z byte reaching the agent orphans the session, so the macOS app strips `0x1A` from ALL keyboard input before `POST /input`.** Claude Code's CLI runs its own background-session supervisor; a Ctrl+Z byte makes it reparent the session out of argus's process tree permanently and invisibly (argus's stop path can never signal it again). The TUI already guards this by never forwarding Ctrl+Z to the PTY (it remaps the key to a pane-zoom/fullscreen toggle). The macOS app has no analogous zoom surface, so it **swallows** the byte rather than remapping — the intent (Ctrl+Z never reaches the session) is mirrored, not the mechanism. The filter is a pure `ArgusKit.TerminalInput.sanitize(_:)` helper (in ArgusKit so it's testable from the `ArgusKitTests` target, which can't import `Argus`), called at the single outbound chokepoint `TerminalCoordinator.send`; it strips only `0x1A` (other control bytes — Ctrl+C `0x03`, Ctrl+Y `0x19`, ESC `0x1B` — pass through untouched) and logs each drop.

## `open Foo.app` does not pass environment variables

**The `ARGUS_MAC_SELECT_TASK` / `ARGUS_MAC_INITIAL_TAB` launch hooks only work when the bundle binary is executed directly** (`macos/dist/Argus.app/Contents/MacOS/Argus`) — `open(1)` launches via launchd and drops the caller's environment.

## No Ctrl+Z interception before the PTY — a real parity gap, not yet an incident

**A literal Ctrl+Z (`0x1A`) typed into `FocusTakingTerminalView`/SwiftTerm and forwarded to `/api/tasks/{id}/input` is Claude Code's own "background this session" command — it detaches the conversation onto Claude Code's per-user supervisor, permanently outside argus's process tree and un-stoppable via any argus stop action** (see gotchas/daemon-rpc.md "Claude Code's own background-session supervisor", gotchas/hera-view.md "Ctrl+Z fullscreen + suspended-pane revive"). The TUI hard-swallows `Ctrl+Z` before it ever reaches an agent's PTY, in both the classic agent view and native Hera view; Argus has no equivalent — a repo-wide grep of `macos/` finds zero Ctrl+Z-specific handling, so SwiftTerm's default key forwarding sends the raw byte straight through. Needs the same swallow-before-forward guard as the TUI (or a server-side strip in `handleWriteInput`) before this surface is safe to leave attached to a live worker unattended.
