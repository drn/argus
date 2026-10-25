## Context

Argus infers session state by reading the PTY: idle from output silence plus a fingerprint (`internal/agent/session.go`, `needsinput.go`), needs-input from regexes, context size from re-reading the transcript in the Stop hook, plan limits from a hidden `claude /usage`. Claude Code mods (plugins of function hooks) can observe `turn.start`/`turn.complete`, `tool.check`/`tool.call`, `session.measure` and `session.start` directly. `docs/argus-bridge-proposal.md` ("Spike findings") records live-verified facts this design relies on:

- `$.http.fetch` + `$.fs.read("<HOME>/.argus/api-token")` reach the daemon from inside the sandboxed session; the API port is unstable across daemon restarts and was not recorded anywhere.
- `tool.check` verdict `ask` does not prove a dialog appeared (settings `PermissionRequest` hooks auto-decide it), and `classic.PermissionRequest` / `classic.Notification` hooks are skipped in this org (the managed plugin runs first).
- `/clear` does not re-fire `session.start`; the id must be re-read from `$.session.id()` per turn.
- Editing a `--plugin-dir` mod hot-reloads at the next turn end, re-running `register` and `session.start` and resetting module variables.

The REST API lives in the daemon; sessions are owned by the supervisor and outlive daemon bounces.

## Goals / Non-Goals

**Goals:**
- Establish the mod + daemon architecture phases 2, 3a, 3b build on: a typed event wire contract, per-task bridge state, and a single comparator seam.
- Produce hard agree/disagree numbers per signal (idle, needs-input) between screen-reading and the bridge.
- Fail open everywhere: a dead daemon, missing token or older mod never breaks a session or the daemon.

**Non-Goals:**
- Switching any behavior to the bridge (phase 2), message delivery through the mod, band/toast/slash commands (phase 3).
- Replacing the Stop hook, the hidden `/usage` probe or session-id recapture.
- Supporting non-Claude backends.

## Decisions

**D1. Mod location and loading.** Source lives in `internal/bridgemod/mod/argus-bridge/` and is embedded with explicit-file `go:embed` patterns (dot-directories such as `.claude-plugin/` are excluded by directory patterns; the gitignored `.claude-plugin/types/` symlink must never be embedded). Materialization copies the embedded files to `~/.argus/cache/bridge-mod/<sha256-prefix>/argus-bridge/` (content-addressed, so a daemon upgrade never mutates a directory a running session is loading; stale versions are not swept in phase 1). It lives under `cache/` because that is the only part of `~/.argus` a sandboxed session may write, and Claude's hot-reload writes `.claude-plugin/types/` into every `--plugin-dir` folder it loads (observed in the materialized directory). The write is a temp directory with a `.complete` marker renamed into place, and an existing hash directory is never removed: concurrent first launches racing to the same hash each get a complete directory. A real sandbox-exec load check could not be run from inside an argus sandbox (sandbox-exec cannot nest), so the location follows the evidence above, not a sandboxed run. A package-level function var `ensureBridgeModFn` mirrors `ensureSessionSkillsFn` and returns `("", nil)` under `go test`. Alternative considered: a fixed path with overwrite; rejected because a hot-reloading Claude would pick up a half-written mod mid-upgrade.

**D2. Backend capability.** `Family.BridgeMod bool` (Claude true; all others false). BuildCmd reads the registry only, never branches on backend identity. The flag is appended after the skills `--add-dir` block as ` --plugin-dir <dir>`. A `[bridge] enabled=false` config or a materialize failure skips the flag (logged), never blocks launch.

**D3. Daemon discovery.** The daemon writes `~/.argus/api-port` (mode 0600, temp file + rename) right after `ListenAndServe` returns the bound port. The mod reads `<HOME>/.argus/api-port` and `api-token` per attempt (HOME from `$.env.get("HOME")`), builds `http://127.0.0.1:<port>`, and re-reads the port after any connection failure. `ARGUS_BRIDGE_DIR`, when set, replaces `<HOME>/.argus` as the directory holding `api-token`/`api-port` (a mod-side relocation/testing hook; the daemon does not read it). Alternative: fixed port; rejected, `bindWithRetry` can move it.

**D4. Event wire contract.** `POST /api/tasks/{id}/bridge/events` accepts `{"events":[...]}`. Every event carries `type`, `seq` (monotonic within one mod `boot`), `boot` (random id minted at `register`, because a hot reload resets module state), `ts` (ms epoch, mod clock) and `session_id`. Types:

| type | extra fields |
| --- | --- |
| `session.start` | `model`, `cwd` |
| `turn.start` | `turn_id` (re-reports `session_id` when changed) |
| `turn.complete` | `turn_id`, `reason` |
| `needs_input` | `kind` (`question`/`plan`/`permission`), `confidence` (`exact` for question/plan, `pending-or-running` for permission), `tool`, `tool_use_id` |
| `cleared` | `tool_use_id` |
| `session.measure` | `context{tokens,percent,window}`, `cost_usd`, `rate_limits[{kind,percent_used,resets_at}]` |
| `heartbeat` | none (~15s) |

Subagent and teammate (`agentId`) `turn.start`/`turn.complete` are never reported: only the main loop drives busy/idle. Their needs-input IS reported (an ask or AskUserQuestion inside a subagent blocks the user just the same), without an `agentId` filter on `tool.check`/`tool.call`. The daemon dedupes by (`boot`, `seq`): a lower-or-equal seq within the same boot is dropped; a new boot resets the baseline.

**D5. Needs-input derivation in the mod.** Pure logic module `needsinput.ts` tracks pending tool calls. `AskUserQuestion` and `ExitPlanMode` are needs-input from `tool.call` pre to post. For any other tool, a `tool.check` verdict of `ask` marks it as an ask-candidate; if its `tool.call` is still pending after a ~1.5s grace (settings hooks that auto-approve finish within that), it is needs-input (`kind: permission`, `confidence: pending-or-running`); `tool.call` post clears. The permission signal is deliberately coarse: it stays pending through the tool's whole execution after approval, because the engine raises no event between approval and execution (the only tool events are `tool.call`/`check`/`describe`/`list`/`register`). So it is tagged `pending-or-running`; phase 2 trusts the bridge alone for `question`/`plan` kinds (exact) and requires scraper corroboration for `permission`. Needs-input is the set of pending-and-qualifying calls being non-empty; `needs_input` is emitted on the empty→non-empty edge and `cleared` on non-empty→empty. Alternatives rejected: relying on `classic.PermissionRequest`/`Notification` (skipped in this org).

**D6. Daemon state and comparator seam.** New package `internal/bridge` owns: wire types, `Hub` (latest `State` per task id, guarded by a mutex, bounded by pruning state silent for over an hour inside each `Observe`), and `Shadow` (comparator + counters). `api.Server` holds one `*bridge.Hub` and one `*bridge.Shadow`, and calls `Server.observeBridge` from exactly ONE place: the end of `idleWatcherTick`'s verdict computation (`push.go`), passing the final scraper idle set (after the content-idle fold-in) and the final needs-input set (`state.needsInputNow`, just computed by `detectNeedsInputTick`). A new `boot` id resets the task's busy and needs-input state (a restarted or stopped mid-turn session must not come back live still busy). Phase 2 swaps the observation seam for a source-of-truth read; the Hub and wire contract stay.

**D7. Comparison semantics.** Scraper "idle" means the screen is quiescent (it is also true while parked on a question). Bridge idle is therefore defined for comparison as `turn idle OR needs-input pending`. Needs-input compares scraper set membership against bridge `needsInput`. A task is only compared when the bridge is *live*: at least one event received and the newest event (heartbeats included) younger than 45s; otherwise it counts as `no_bridge` (never a disagreement). A disagreement must persist continuously for the settle window (default 10s, covering the scraper's ~3s lag and the 5s tick granularity) before it is counted and logged; shorter ones are counted as `settling`. The persistent disagreement logs once when it crosses the window and once on resolution with its total duration (`[bridge] disagree signal=idle task=... scraper=idle bridge=busy for=12s`). Counters per signal: `agree`, `disagree`, `settling`, `no_bridge`, one increment per task per tick. Needs-input ticks where the bridge holds a permission-kind dialog count under a separate signal, `needs_input_permission`, so their expected disagreements (a tool still running after approval) never pollute the headline `needs_input` numbers.

**D8. Fail open, never wait.** Mod: every hook body is wrapped, and no hook ever waits on delivery: `emit` enqueues and starts a single-flight background drain (later emits and the heartbeat also drain), so a daemon that accepts but never answers cannot delay a turn or dialog or blow a hook's budget. An unreachable daemon, missing token/port or retryable failure keeps events in a bounded buffer (cap 200, oldest dropped); a non-401 non-2xx also forgets the cached port so a stale port now held by another server stops receiving the token. The daemon removes `api-port` on clean shutdown (only if it still names its own port, since a newer daemon may have replaced it). `session.measure` is throttled to one send per 5s (latest held and flushed on turn end/heartbeat) and a measurement identical to the last sent is dropped. Daemon: an unknown task id returns 404, malformed body 400; nothing in the ingest path touches sessions or the DB.

**D9. Config.** `[bridge] enabled` (default true; DB key `bridge.enabled`, config.toml wins) gates both `--plugin-dir` injection and the daemon's observation logging. The endpoints stay mounted regardless so already-running sessions can still post.

## Non-Goals (frontend parity)

No TUI, web or macOS surface for `/api/bridge/*` in phase 1: viewing shadow stats is REST-only (`GET /api/bridge/shadow`, `GET /api/tasks/{id}/bridge`). A client surface (a Settings or status readout) is a named follow-up for when phase 2 starts consuming bridge state; until then this is the standing, intentional gap.

## Risks / Trade-offs

- [Mod posts from inside the sandbox using the master API token] → same trust the Stop hook already has; token is read, never logged, never sent anywhere but loopback.
- [Hot reload resets `seq`] → `boot` id makes a reload unambiguous.
- [Bridge idle vs scraper idle semantics differ around questions] → D7 definition; disagreement logs carry both raw values so analysis can re-slice.
- [Hook failure rate unknown in other orgs] → inert-without-daemon fail-open; shadow counters surface `no_bridge` separately from disagreements.
- [Embedded file set drifts from the mod dir] → a Go test enumerates `mod/argus-bridge` on disk (excluding `node_modules`, `types`, tests) and asserts each runtime file is embedded.

## Migration Plan

Ship default ON; nothing behavioral changes. Rollback: `[bridge] enabled=false`. Aaron dogfoods and reviews `GET /api/bridge/shadow` numbers; archiving this change (`openspec archive add-argus-bridge-shadow`) happens before merge, after that dogfooding. Phase 2 reads `bridge.Hub` instead of only observing.

## Open Questions

- Whether phase 2 should treat a stale bridge (no heartbeat) as "fall back to scraper" automatically (assumed yes; D7's 45s liveness window is the proposed definition).
