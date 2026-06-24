# Tasks

## 1. Orphaned-test reaper (primary backstop)

- [x] 1.1 Add `script/reap-orphaned-tests.sh`: enumerate processes, gate on
      PPID==1 AND `*.test`+`-test.` signature AND age ≥ `REAP_MIN_AGE_MINUTES`
      (default 10); SIGTERM then SIGKILL on survival; `--dry-run`; log to
      `REAP_LOG_FILE`.
- [x] 1.2 Add `script/install-reaper.sh`: copy reaper to `~/.local/bin`, render
      the `com.drn.argus.test-reaper` LaunchAgent plist (StartInterval =
      `REAP_INTERVAL_SECONDS`, default 300), bootstrap into the user domain
      (boot out prior first); `uninstall` subcommand; no-op off macOS.
- [x] 1.3 Verify the awk gate against synthetic `ps` rows (the sandbox blocks
      `ps`, so the reaper runs on the host; gate logic verified out-of-band):
      orphan-old → killed; live/young/non-test → skipped.

## 2. tui test event-loop teardown hardening

- [x] 2.1 Make `runApp`'s returned `stop` idempotent (`sync.Once`) and register
      it via `t.Cleanup`, so the real tview event loop is torn down even when a
      caller forgets `defer stop()`. (Do NOT close the `x/vt` emulators — that
      reintroduces a `-race` data race; see `terminal/terminalpane.go`.)
- [x] 2.2 `TestSmoke_RunAppStopIsIdempotent`: stop callable multiple times
      without panic/hang.
- [x] 2.3 `TestSmoke_RunAppCleanupTearsDownLoop`: a subtest that never calls
      stop leaves no event-loop goroutine after its cleanup fires.

## 3. Tighten the test timeout (weak, last line)

- [x] 3.1 Add `-timeout 120s` to the `test`, `test-pkg`, `test-cover`, and
      `test-cover-gate` Makefile recipes, with a comment that this is a weak,
      sleep-non-surviving guard and the reaper is the real backstop.

## 4. Docs

- [x] 4.1 Gotcha: orphaned `*.test` reaper exists + why (sleep defeats the
      monotonic `-test.timeout`); `os.Exit(m.Run())` reaps goroutines so only a
      HANG keeps a binary alive; the `x/vt` emulator-drain leak is accepted (do
      not close — `-race`). Recorded in `gotchas/daemon-rpc.md` (alongside the
      `*.test` fork-bomb backstop) and `gotchas/ui-threading.md` (runApp
      auto-cleanup).
- [x] 4.2 N/A — the README Reference appendix tracks product surfaces
      (keybindings, MCP tools, REST endpoints); a dev maintenance script isn't
      one. Documented in the script headers + the change folder + gotchas.

## 5. Gate

- [x] 5.1 `make pre-pr` green — build/vet/fmt-check/lint-pr clean,
      test-cover-gate passes (filtered 89.6% ≥ 88 floor). The only failure is
      `vuln`, on pre-existing stdlib CVEs (net/textproto + crypto/x509, go1.26.3
      → 1.26.4) in untouched files; `vuln` is continue-on-error in CI, so CI is
      green (the documented "only-vuln failure" case).
