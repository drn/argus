## Database Patterns

- **New columns use `ALTER TABLE ... ADD COLUMN ... DEFAULT ''` after `CREATE TABLE IF NOT EXISTS`.** Error for duplicate column silently ignored.
- **`taskColumns` is the canonical column list.** Update `taskColumns`, `scanTask`, `Add`, and `Update` in lockstep.
- **Backend default config must be self-healing.** `fixupBackends()` runs on every `Open()` to repair outdated configs. Any `DefaultConfig()` change must be mirrored there. The `--permission-mode plan` fixup **appends** to the existing command (preserving user customizations) rather than replacing. All Claude fixup checks use `name == "claude"` (not `strings.Contains(command, "claude")`) to avoid matching user-created backends.
- **Map lookups returning `*T` become non-nil interfaces.** `Get()` must check `if sess == nil { return nil }` before returning as interface.

## Go Patterns

- **Use `charmbracelet/x/term` for raw mode** (cross-platform). `TIOCGETA` is macOS-only, `TCGETS` is Linux-only.
- **`ansi.StringWidth` returns 0 for tabs.** Expand tabs before any width math.
- **Use `ansi.Hardwrap` not `ansi.Truncate` for wrappable content.** Cache wrapped lines; invalidate on content or width change.
- **Chroma resets after every token.** Use `injectBg(s, bgEsc)` to re-apply background after each `\033[0m`.
- **Keep daemon client test names short.** macOS Unix socket paths have 104-byte limit.
- **`filepath.Walk` must return error when root is inaccessible.** Check `err != nil && path == root`.
- **CRITICAL: Tests must NEVER operate on real `~/.argus/` paths.** All worktree paths and file operations in tests MUST use `t.TempDir()`. The `testGuard` in `internal/tui/worktree.go` is a last-resort safety net, but tests should be designed correctly.
- **Declare small enums with `uint8` underlying type, not `int`.** gosec G115 flags every `int → byte/uint8/uint16` conversion as overflow risk, even for ~5-value enums where it's provably safe. Declaring `type rowKind uint8` (vs `int`) lets `byte(r.kind)` and `uint64(r.kind)` pass lint without `//nolint` hints. Cost is zero — `iota` and enum semantics are identical. CI will reject the wrong choice on the lint step.
- **Reach for `hash/fnv` from stdlib instead of inlining FNV constants.** The canonical FNV-1a 64-bit offset is `14695981039346656037`; transcription errors are easy and silent (the hash still works, just isn't canonical, which fails review). `fnv.New64a()` plus `io.WriteString(h, s)` is shorter and self-documenting.
- **`dirCompletions` reads real `/tmp/` — tests that type paths there race with `t.TempDir()` from parallel tests.** Path autocomplete in `ProjectForm` opens its dropdown when matching directories exist, and Enter on an open dropdown calls `Accept()` which replaces the typed path. CI saw `TestProjectForm_TypeAndResult` flake when another test's `Test*` tmpdir matched the typed prefix. Tests that exercise the path field must use a path whose parent doesn't exist (e.g., `/no-such-dir-xyzzz123/test`) so `dirCompletions` returns nil and the dropdown stays closed.

## Codex Integration

- **`codex resume --last` is unreliable for multi-session.** Use `codex resume --dangerously-bypass-approvals-and-sandbox <session-id>`.
- **Session ID captured post-exit from `~/.codex/state_5.sqlite`.** The `_5` suffix is codex's schema version.
- **`fixupBackends()` migrates old codex flags** (`--yolo`, `--full-auto`) to `--dangerously-bypass-approvals-and-sandbox`.
- **`ensureTopLevel` must insert before the first `[section]` header, not append.** Appending a top-level TOML key to the end of a file places it inside the last section (e.g. `[notice.model_migrations]`), causing type errors. `ensureTopLevel` also migrates previously misplaced keys.

## Knowledge Base & MCP

- **FTS5 doesn't support UPDATE.** Upsert = DELETE+INSERT in transaction.
- **FTS5 `SanitizeQuery` must strip all operators:** `" * ( ) : ^ { } - +`.
- **FTS5 + metadata JOIN avoids N+1 under mutex.** Never issue per-row `QueryRow` inside `rows.Next()` while holding `d.mu`.
- **MCP server echoes client's `protocolVersion`** — Codex workaround.
- **All config file writes should be atomic** (temp + rename).
- **KB Indexer started/stopped by daemon.** Start after MCP, stop before MCP shutdown.
- **Incremental scan compares mtime at unix-second granularity.** Sub-second edits within the same second are missed until the next fsnotify event or daemon restart. The TOCTOU window between `KBMetadataMap()` and fsnotify watcher start means changes during scan are also deferred.
- **`KBMetadataMap` must check `rows.Err()` after iteration.** A partial result without error check causes `IncrementalScan` to delete documents that weren't returned due to mid-stream DB errors.
- **Claude Code MCP entries require `"type": "http"`.** A bare `{"url": "..."}` entry in `mcpServers` fails to parse. Must be `{"type": "http", "url": "..."}`. The JSON key is `"type"`, not `"transport"` (which is the CLI flag name).
- **MCP config is injected globally only (`~/.claude.json`, `~/.codex/config.toml`), not per-worktree.** Per-worktree `.mcp.json`/`.codex/config.toml` injection was removed — it polluted git status in every project and was redundant since global config applies everywhere.
- **MCP `instructions` field in `InitializeResult` is truncated at ~2KB by Claude Code.** Put the most critical rules first. The `kb_ingest` tool description intentionally duplicates key rules from `kbInstructions` because not all MCP clients surface server instructions at tool-call time.
- **`toolDefs` slice must be copied before append in `handleToolsList`.** The package-level `var` could be corrupted if `append` reuses its backing array when adding task tools.
- **`Set*` wiring on `mcp.Server` must complete before `ListenAndServe`.** Fields like `s.taskDB`, `s.schedDB`, `s.schedRunner` are read at request time without a mutex — happens-before is guaranteed only by the daemon init order. When adding a new conditional tool family (e.g. `schedule_*`), the dependency it wraps must be constructed before the MCP block in `internal/daemon/daemon.go` so `SetXxxManager` can be called pre-listen. Reordering a later block (scheduler/pushMgr) up to before the MCP block is the standard fix.

## PR & Reviews

- **PR URL detection: scan on tick + on agent exit.** Last regex match wins. Use `RecentOutputTail(32KB)`, not full buffer.
- **`gh search prs --json` doesn't support `reviewDecision`.** Use `gh pr list --json` per-repo.
- **`SetPRs` must sort review requests before "my PRs"** — visual order must match slice order.
- **PR list has 10min cooldown.** `SetPRs` preserves cursor/selection on background refresh.

## File Explorer

- **`autoExpand` must only treat `indent == 0` dirs as expandable.** Synthetic sub-dir rows (indent > 0, IsDir) are display-only groupings from `buildChildTree`. Without the `row.indent == 0` guard, navigating onto a sub-dir row collapses the top-level parent, causing all children to disappear.
- **`CursorUp` must track `wasChild` BEFORE decrementing to distinguish entering vs exiting a folder.** `wasChild = fp.rows[fp.cursor].indent > 0` captures whether the cursor is inside a folder pre-move. Without this, `skipToLastChild` traps the cursor inside the folder when navigating up from the first child (it re-enters instead of exiting).
- **`buildChildTree` groups flat file paths into a trie and emits dirs-before-files at each level.** Sub-directories are always expanded (no interactive collapse). Only top-level directories (indent 0) toggle via `autoExpand`.
- **`skipToLastChild` scans all nested indent levels, not just immediate children.** It finds the last non-directory row before the next `indent == 0` boundary. For deeply nested trees, this lands on the deepest last file.
- **`CursorUp`/`CursorDown` must not `skipToFile` when `awaitingFetch` is true.** When `autoExpand` returns a non-empty fetch string and the cursor is on a dir row, the dir's children haven't arrived yet. Calling `skipToFile` skips over all stacked unfetched directories to the nearest file — e.g., pressing up from below 3 closed dirs jumps past all of them. The `awaitingFetch()` helper guards both directions.

## Fork Context Capture

- **PTY session logs contain `\r` (carriage return) characters that must be normalized before filtering.** Claude Code uses `\r` to overwrite status indicators in-place. Without `\r→\n` normalization, multiple screen elements concatenate on one "line" and per-line noise filters fail to match.
- **PTY session logs contain `\u00a0` (non-breaking space) that must be normalized.** Claude Code uses NBSP in tool result formatting. Without normalization, `\s+` patterns may not match as expected.
- **Long terminal lines (>120 bytes) need inline noise stripping, not just per-line filtering.** VT cell rendering concatenates the content area, status bar, separators, and prompt onto a single line with whitespace padding. `cleanLongLine` removes these inline patterns before per-line `isNoiseLine` runs.
- **Modal typeahead AC must NOT open when its field is not focused.** Two variants: (1) Calling `updateProjectAC()` in the constructor opens the dropdown immediately when input is pre-filled. (2) Async `SetBranchOptions` callback triggers `updateBranchAC()` while focus is on the prompt field — the pre-filled branch text matches itself, opening the dropdown. Fix: `updateBranchAC` gates `branchACOpen` on `f.focused == ntFieldBranch`. General rule: any `update*AC()` that sets `*ACOpen = true` must check that the corresponding field is focused.
- **`onProjectChanged()` must call `loadSkills()`.** Skill autocomplete depends on the selected project's `.claude/skills/` directory. Without reloading in `onProjectChanged`, the Enter and Down-arrow non-AC paths leave stale skills cached from the previous project. `projACAccept` delegates to `onProjectChanged` — don't duplicate the call.
- **Plugin skill discovery must follow symlinks and read `installed_plugins.json`.** Claude Code plugins ship `skills/` as a symlink into the marketplace checkout, so `filepath.WalkDir` silently skips them unless the root is resolved via `filepath.EvalSymlinks` first. Plugin names come from the manifest key (`<plugin>@<marketplace>`), not the cache directory layout. Skill IDs come from each SKILL.md's `name:` frontmatter field, not the containing directory — nested paths like `skills/merchant/font-licensing/SKILL.md` still expose a flat `<plugin>:<name>` ID.
- **`FilterSkills` is a case-insensitive substring filter, not a prefix match.** Mirrors Claude Code's slash-command picker: `/rev` matches both `review` and `cortex:review`. Both the TUI's `updateAutocomplete` and the PWA's `updateAC` rely on this so plugin-namespaced skills (`<plugin>:<name>`) surface from the bare name the user is most likely to type. Don't switch back to `strings.HasPrefix` thinking it's "more predictable" — it hides plugin items behind their plugin prefix.
- **PWA new-task AC: trigger is the token at the cursor, not the start of the prompt.** `updateAC` reads `promptEl.selectionStart`, walks left/right to whitespace, and treats that token as the trigger candidate. If the trigger were `val.startsWith('/')`, the AC would never open mid-prompt — broken for prefilled prompts (share target, dictation) and for users who write context first then add `/skill`. `selectAC` must replace exactly `[acTokenStart, acTokenEnd)` so leading text isn't clobbered. Cursor moves via mouse/arrow don't fire `input`, so AC also subscribes to `click` and `keyup` (filtered to Left/Right/Home/End — ArrowUp/Down would clobber `acIdx` while navigating the dropdown).

## MCP Task Tools

- **MCP task tools use the same `TaskCreator` injection pattern as the API.** `SetTaskManager()` wires in the creator, querier, and stopper after construction — the same circular-import-avoidance pattern. Task tools only appear in `tools/list` when `taskMgmtEnabled()` confirms all three deps are non-nil.
- **MCP `task_create` is rate-limited to 5 concurrent calls.** `maxConcurrentCreates` prevents unbounded process spawning from a misbehaving MCP client. Each HeadlessCreateTask creates a worktree + PTY process.
- **MCP `task_stop` must NOT pre-check DB status before calling `Stop()`.** TOCTOU race: the process can exit between the status read and the Stop() call, causing confusing errors. Let the stopper determine whether the session is alive.
- **MCP `task_archive` cwd resolution must compare against `worktree + separator`, not raw prefix.** Otherwise a task with worktree `…/add-tests` would match a cwd inside `…/add-tests-extra`. `resolveTask` uses `strings.HasPrefix(cwd, wt+string(filepath.Separator))` plus an exact-equals check. Longest-match wins across siblings.
- **MCP `task_archive` mirrors the TUI 'a' keybinding — archiving clears `WaitingReview`, status is untouched.** The archive flag is independent of `Status`; `/archive` does not set `StatusComplete`.

## Remote API

- **API server cannot import `daemon` package (circular import).** It uses `HeadlessCreateTask` from `daemon`, but `daemon` imports it. Fix: inject a `TaskCreator` function via closure at daemon wiring time, breaking the cycle.
- **Headless task creation uses default 24x80 PTY dimensions.** Agents format initial output for the PTY size at launch. The TUI resizes the PTY when a user opens the agent view, so headless tasks auto-correct on attach.
- **API server binds to `0.0.0.0` (not `127.0.0.1`) for Tailscale access.** MCP server uses `127.0.0.1` (local-only), but the API must be reachable over Tailscale's network interface. Auth is via bearer token from `~/.argus/api-token`.
- **`HeadlessCreateTask` must revert task to Pending on `runner.Start` failure.** Clear SessionID and zero StartedAt — same revert pattern as `startSession` in `app.go`.
- **API server only starts during daemon `Serve()` init — toggling `api.enabled` in Settings requires a daemon restart.** `SetDaemonRestarting(false)` resets `apiBootRecorded` so the "(restart required)" hint re-anchors after any restart path (manual or auto).

## Quick Add Projects

- **`scanDirectory` must `EvalSymlinks` before `.git` stat and path dedup.** Without this, symlinks in a dev directory resolve to unintended paths that get persisted as project roots for agents.
- **macOS temp paths differ pre/post `EvalSymlinks` (`/var/` vs `/private/var/`).** Tests that build `existingPaths` maps for dedup must resolve paths with `EvalSymlinks` to match what `scanDirectory` produces internally.
- **`dirPath` is `[]rune` — backspace must use `f.dirCursor--`, not `utf8.DecodeLastRuneInString`.** The utf8 function returns byte offsets, not rune offsets. For `[]rune` slices, each element is one rune, so a simple decrement is correct.
- **Directory scan must run off the tview goroutine.** `os.ReadDir` on slow filesystems (NFS, iCloud) blocks for seconds. Use `OnScan` callback → goroutine → `QueueUpdateDraw` with `SetScanResult`.
- **`dirCompletions` is the shared pure function for directory autocomplete.** Both `QuickAddForm` and `ProjectForm` call it — any change to completion logic (hidden dir filter, symlink resolution, case sensitivity) must be made in `dirCompletions`, not in the per-form `update*AC` wrappers.
- **`maybeLoadBranches` must expand tildes before passing to `OnBranchFocus`.** `acceptPathAC` calls `collapseTilde`, so the path field contains `~/...`. Go's `exec.Command` doesn't shell-expand `~`, so `cmd.Dir = "~/..."` silently fails. Use `pathValue()` which applies `expandTilde` + `TrimSpace`.
- **Form field truncation must use rune-based slicing, not byte-based.** The cursor character (U+2588) is 3 bytes in UTF-8. Byte-based `val[len(val)-maxW:]` splits it for paths where `pathLen + 3 > maxW > pathLen`, producing garbled symbols. Use `[]rune` conversion. Affects projectform, backendform, and renametask Draw methods.

## Scheduled Tasks

- **A brand-new schedule must not fire on the first tick.** `Scheduler.tickOne` only fires when `NextRunAt` is set and has passed; on the first tick it just computes `NextRunAt` and persists. Otherwise saving an `@every 1m` schedule would spawn a task the same second the user clicks Save. The first fire is always one cron interval after creation.
- **Disabled schedules still get `NextRunAt` updated.** The web/TUI surfaces preview the next-fire time even for off rows so the user can sanity-check before flipping the toggle. Skip this and the row shows a stale value forever.
- **`Scheduler.fire` reloads the row after `db.UpdateSchedule`.** It writes `LastRunAt`/`NextRunAt`/`LastTaskID`, then the surrounding `tickOne` reads the row again before falling through to the "compute next-run" branch — without the reload, the in-memory copy is stale and `tickOne` would overwrite the post-fire bookkeeping.
- **Per-fire task names include a timestamp suffix.** `agent.CreateAndStart` auto-suffixes name collisions with `-1`, `-2`, …, but a schedule that fires every minute would walk that counter forever. Suffixing with `name + "  YYYY-MM-DD HH:MM"` makes each fire a fresh worktree path.
- **The cron parser is `robfig/cron/v3` `ParseStandard` (5-field only).** 6-field (seconds-precision) expressions are rejected — Argus treats minute as the smallest unit because the scheduler ticks at one-minute intervals. Don't add `cron.Second` to the parser flags or the UI hint string in the schedule form will lie.
- **API `PUT /api/schedules/{id}` is a partial update.** All fields are pointer-typed in the request struct; nil means "leave alone". The toggle-enabled UX in the SPA depends on this — sending `{"enabled":false}` must not blank the prompt or schedule.
- **`PUT /api/schedules/{id}` recomputes `NextRunAt` anchored on `LastRunAt` only when non-zero.** `cron.Schedule.Next(time.Time{})` returns a year-0001 date which the scheduler tick reads as "due now" and would fire the schedule on the very next tick — exactly what the "no first-tick fire" invariant forbids. When the schedule has never fired (`LastRunAt.IsZero()`), anchor on `time.Now()` instead.
- **The TUI's manual run-now path must use `scheduler.FireName(name, now)`, NOT `name + " (manual)"`.** Rapid double-clicks on Run Now would otherwise hit a worktree-name collision (`agent.CreateAndStart` auto-suffixes with `-1`, `-2`, …) instead of being naturally disambiguated by the timestamp. The format also has to match `scheduler.fire()` so manually-fired and automatically-fired tasks render identically in the task list.
- **`scheduler.fire` callers MUST hold `fireMu`.** Without it, `RunNow` from an HTTP goroutine can race with the per-minute `tick()` and double-fire the same row: the tick reads the schedule, RunNow fires + persists fresh `NextRunAt`, then the tick resumes its loop with the stale copy and fires again. `tickOne` must also re-read the schedule under `fireMu` and re-check `NextRunAt > now` before calling fire.

## Task Auto-Naming (Haiku)

- **Auto-rename never touches `task.Worktree` or `task.Branch` — only `task.Name`.** Both fields are locked in at creation from the original regex slug; if you ever add code that re-derives the worktree path or branch from `Name` post-creation, the auto-rename will silently corrupt it. `task.Name` is a soft display string after creation.
- **The `claude -p` invocation MUST set both `--system-prompt` AND `--setting-sources ""`.** `--system-prompt` overrides (not appends) the default Claude Code preamble; `--setting-sources ""` is what actually disables MCP/hook loading from `~/.claude/settings.json`. Dropping either leaks tokens. `--strict-mcp-config` (with no `--mcp-config`) is the third leg — needed to suppress project-local `.mcp.json`.
- **Don't use `claude --bare`.** It's tempting (one flag instead of seven) but it disables OAuth/keychain auth and only works with `ANTHROPIC_API_KEY` — breaking users who login via the Claude.ai subscription.
- **`cmd.Dir = os.TempDir()` is required even with `--setting-sources ""`.** The `claude` CLI walks up from cwd to auto-discover `CLAUDE.md`; running from a worktree pulls the project's `CLAUDE.md` into context. Belt-and-suspenders with the settings flag.
- **Race guard is a single SQL CAS via `db.RenameIfName`, not check-then-set.** `runAutoRename` MUST use `RenameIfName(id, originalName, newName)` — a `Get`-then-`Rename` pair has a TOCTOU window where a concurrent manual rename gets clobbered. The CAS resolves the race in one `UPDATE … WHERE name=?`. If user types a name equal to the regex slug or the rename modal sets it back, auto-rename will still fire — cost of false-positive rename is low; adding a "was auto-named" boolean to the schema isn't worth it.
- **Goroutine inherits its parent process lifecycle.** TUI-initiated tasks fire `runAutoRename` inside the TUI process; if the user quits before Haiku returns, the rename is lost. Headless paths (HTTP API / MCP) run inside the daemon, so renames survive TUI restarts.
- **`AutoName` opt-in lives on each call site, not centrally.** Only set `AutoName: true` when the name was string-interpolated from `Prompt` — NOT for review-pr (`review-pr-N-…`), fork (`<src>-fork`), or multipart with attachment-derived name (the filename is already meaningful).
- **`llm.DefaultTimeout` must comfortably exceed claude CLI startup + Haiku latency.** Observed end-to-end is 6-8s (CLI startup 1-2s + model 3-6s) with occasional spikes; setting the timeout near that range produces frequent `signal: killed` failures and silent fallback to the regex slug. 30s gives ample headroom and costs nothing because the goroutine is fire-and-forget. If you ever shrink it, watch `[autoname] failed … signal: killed` in `~/.argus/daemon.log`.

## Link Extraction

- **`osc8Re` must run BEFORE `ansiRe` in `stripANSI`.** OSC 8 hyperlinks embed URLs in escape sequences (`\x1b]8;;URL\x1b\\`). If `ansiRe` strips them first, the URLs are lost. The two-pass design in `links.go:stripANSI` is intentional.
- **`tui/ansiRe` and `sanitize/ansiRe` must both handle ST-terminated OSC (`\x1b\\`).** Claude Code uses ST (not BEL) for OSC 8 hyperlinks. Missing ST support causes URLs to splice with display text.
- **SGR sequences must strip to empty; cursor movement must strip to space.** Raw PTY logs contain cursor movement codes (`\x1b[5C`, `\x1b[1B`) that position text on different screen locations. Stripping these to empty merges unrelated text into URLs. `stripANSI` uses `ReplaceAllStringFunc` to distinguish: CSI ending in `m` → empty (preserves mid-URL colors), all else → space (prevents merging).
- **`bareLinkRe` must exclude `"`, backtick, `{`, `}`, `<`.** These are never valid in URLs per RFC 3986. Without exclusion, the regex matches through quoted/JSON URL data producing garbage entries in the link picker.
