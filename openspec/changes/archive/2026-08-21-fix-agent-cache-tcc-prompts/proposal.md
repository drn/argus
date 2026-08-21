## Why

macOS's TCC "argus would like to access data from other apps" prompt recurs because two build/test tools spawned by agents write, by default, under `~/Library/{Application Support,Containers,Caches}`: Go's build cache (`GOCACHE`, default `~/Library/Caches/go-build`) and Playwright's browser download cache (`PLAYWRIGHT_BROWSERS_PATH`, default `~/Library/Caches/ms-playwright`). TCC attributes these writes to the responsible process (the launchd-rooted `argus`), so heavy concurrent build/test activity across worktrees produces repeated prompts even though the `argus` binary itself is correctly, stably signed (`context/knowledge/gotchas/sandbox.md`). Both tools fully honor an env var override, so the prompts are avoidable at the source rather than just dismissible.

## What Changes

- `BuildCmd` (`internal/agent/agent.go`) forces `GOCACHE=~/.argus/cache/go-build` and `PLAYWRIGHT_BROWSERS_PATH=~/.argus/cache/ms-playwright` onto every spawned agent process, unconditionally — same "always force" pattern already used for `TERM`/`COLORTERM`.
- No config toggle; not opt-out per project or backend.
- No migration of existing `~/Library/Caches/go-build` or `~/Library/Caches/ms-playwright` contents — they're orphaned, not moved. New caches repopulate from scratch on first use after this ships.
- Explicitly out of scope: Chrome's crashpad write to `~/Library/Application Support/Google/Chrome` (hardcoded path, ignores `--user-data-dir`, already documented and separately handled by the sandbox's SBPL allowlist when `channel: "chrome"` is used) — a one-time grant per stable signature, not a recurring per-build churn source, so redirecting it isn't possible and isn't needed.
- Also out of scope: the periodic `Argus Code Signing` cert trust lapse (`CSSMERR_TP_NOT_TRUSTED`) requiring Aaron to be physically present to re-grant — a separate, pre-existing issue tracked independently.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `agent-execution`: adds a requirement alongside the existing "Forced terminal capability environment" requirement, forcing `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` onto every spawned agent's environment.

## Impact

- `internal/agent/agent.go` (`BuildCmd`) — two more `cmd.Env` entries, appended unconditionally next to the existing `TERM`/`COLORTERM` force.
- Test coverage: extends the existing `BuildCmd` env-assertion table test with two new cases.
- Docs: `context/knowledge/gotchas/sandbox.md` gains a bullet noting the redirect and why (next to the existing TCC/go-build/Chrome bullet).
- No API, schema, or config surface changes. No impact on remote/web/macOS clients (this is purely a spawn-time env detail, not REST-exposed).
