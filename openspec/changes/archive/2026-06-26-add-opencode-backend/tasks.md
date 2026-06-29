# Tasks — add-opencode-backend

**Design doc:** `openspec/changes/add-opencode-backend/design.md`

Single PR. TDD: write failing tests from the deltas first, then implement. Use
`internal/testutil` assertions and `t.Run` subtests. Tests that resolve through
`$HOME`/`$XDG_DATA_HOME` MUST `t.Setenv` to a `t.TempDir()`. Verify with
`make test` per package during dev and `make pre-pr` before the PR.

## 1. Tests first (failing, from the deltas)

- [x] 1.1 `internal/agent` — `TestIsOpencodeBackend` (bare / abs-path / with-flags / non-match), mirroring `TestIsClaudeBackend`.
- [x] 1.2 `internal/agent` — `TestKnownModels`/`TestBackendModels`: `opencode` → nil (custom-only).
- [x] 1.3 `internal/agent` — `BuildCmd` cases: new opencode session omits `--session-id`; prompt rides `--prompt`; `--model provider/model` injected; resume appends `--session <id>` and drops the prompt; resume with empty SessionID starts fresh.
- [x] 1.4 `internal/agent` — `CaptureOpencodeSessionID` table tests over a `t.TempDir()` data root (`t.Setenv("XDG_DATA_HOME", …)`): SQLite `opencode.db` hit; JSON-only hit; mixed-directory rows (asserts the `directory` filter picks the right one); newest-by-updated-time wins; empty → fail-open error; malformed (`ses_` validation) rejected. Plus `CaptureSessionID`/`NeedsSessionRecapture` dispatch opencode (recapture only while SessionID empty).
- [x] 1.5 `internal/inject` (opencode pkg) — idempotency/port-change/preserve-unrelated/invalid-JSON-untouched, mirroring `codex_test.go`/`claude_test.go`.
- [x] 1.6 `internal/db` — `fixupBackends` inserts the `opencode` default (command + prompt_flag) into a DB that lacks it.

## 2. Config + DB seeding

**Depends on:** Stage 1

- [x] 2.1 `internal/config/config.go`: add `opencode` to `DefaultConfig().Backends` (`Command: "opencode"`, `PromptFlag: "--prompt"`). No new struct fields.
- [x] 2.2 Confirm `db.fixupBackends` (no code change expected) seeds it; rely on the existing newly-shipped-default insert path.

## 3. Backend detection + command construction

**Depends on:** Stage 1

- [x] 3.1 `internal/agent/agent.go`: add `IsOpencodeBackend(command)` (basename `opencode`), next to the other `Is*Backend` fns.
- [x] 3.2 `BuildCmd`: add `isOpencode`; add it to the `--model` injection gate; exclude it from start-time `--session-id` (treat like codex/pi); resume branch appends `--session <id>` (pi-shaped). Prompt already handled by the `PromptFlag` path — no new prompt branch.
- [x] 3.3 `KnownModels`: leave opencode returning nil (custom-only) — no change needed beyond the test; add an explicit comment noting opencode is intentionally custom-only.

## 4. Post-exit session capture + resume

**Depends on:** Stage 1, 3

- [x] 4.1 `internal/agent/agent.go`: add `opencodeSessionIDRe` (`^ses_[0-9A-Za-z]+$`) and `CaptureOpencodeSessionID(worktree)`:
      resolve data root from `XDG_DATA_HOME` else `~/.local/share` under `opencode`;
      SQLite first (`opencode.db`, `SELECT id FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1`, read-only, bind symlink-resolved absolute worktree);
      JSON fallback (walk `storage/session/*/*.json`, filter `directory == worktree`, max `time.updated`);
      validate `ses_` format; fail open on no match.
- [x] 4.2 Add opencode cases to `CaptureSessionID` (dispatch to 4.1) and `NeedsSessionRecapture` (recapture only while `SessionID == ""`).

## 5. Create/start + app gates

**Depends on:** Stage 3

- [x] 5.1 `internal/agent/create.go`: exclude opencode from the session-ID pre-mint (`&& !IsOpencodeBackend(...)`).
- [x] 5.2 `internal/tui/app.go`: same exclusion at the `startSession` pre-mint; add `IsOpencodeBackend` to the ctrl+r session-switcher "not Claude" guard (switcher stays Claude-only); add `kind = "opencode"` to the session-exit logging switch.

## 6. MCP injection

**Depends on:** Stage 1

- [x] 6.1 New `internal/inject/opencode/opencode.go`: `InjectGlobal(port)` writing `mcp.argus = {type:"remote", url, enabled:true}` into `~/.config/opencode/opencode.json`; idempotent; atomic temp-file write; invalid-JSON-untouched; create dir/file if missing.
- [x] 6.2 `internal/daemon/daemon.go`: call `injectopencode.InjectGlobal(actualPort)` in the post-`ListenAndServe` injection goroutine, with the same success/error slog lines as Claude/Codex.

## 7. Docs

- [x] 7.1 README Reference appendix: add opencode to the pre-configured backends list and the session-resume note (`opencode --session <id>`); confirm the marketing line already naming opencode stays accurate. No top-half change (not pillar-class).
- [x] 7.2 `context/knowledge/gotchas/misc.md` (or `daemon-rpc.md` for capture): record the non-obvious opencode gotchas — no start-time session-id (capture-style), session keyed by git root-commit/shared-across-worktrees so capture filters by `directory`, dual SQLite/JSON store with SQLite-first + fail-open, MCP uses `type:"remote"`. Update `context/knowledge/index.md` bullet counts.
- [x] 7.3 No keybinding change → no help-modal/`help_test.go` edit.

## 8. Archive (same PR, before merge)

- [x] 8.1 Fold the three deltas into the base specs under `openspec/specs/{agent-execution,mcp-injection,config-management}/` and move this change folder to `openspec/changes/archive/2026-06-26-add-opencode-backend/`. Commit on the branch.

## 9. Gate

- [x] 9.1 `make pre-pr` green (build → vet → fmt-check → lint-pr → vuln → test-cover-gate). Touched packages ≥95% where feasible.
