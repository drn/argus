# Knowledge Index

Structured knowledge for cross-session persistence. Each file covers a topic/domain.

| File | Topic | Key Entities | Last Updated |
|------|-------|-------------|-------------|
| code-quality.md | Refactoring patterns, daemon architecture, and deferred items | ScrollState, vtrender.go, sgrColor, borderedPanel, file splits, GitStatus pointer bug, cursor skip-header, Alt modifier keyMsgToBytes, textarea zero-value trap, textarea viewport scroll bug, worktree removal safety guard, self-managed worktrees, scroll-offset chicken-and-egg, diff panel line wrapping, ⌘ runewidth mismatch, tmux color palette, zero-dimension View() panic, vt10x cursor XOR reverse, PanelLayout extraction, worktree-first task creation regression, PanelLayout width enforcement bug, SessionProvider/SessionHandle interfaces, multi-writer pattern, nil-interface gotcha, daemon IPC protocol, RingBuffer export, daemon client, chroma background compositing, injectBg, onFinish ordering, RPC timeout wrapper, daemon file logging, tab-zero-width expandTabs, daemon restart, SessionID preservation on restart, double-pointer BT pattern, AutoStart extraction, stream-failure-auto-complete bug, StreamLost flag, isSessionAlive reachable flag, client.Get race fix, daemon health check daemonFailures, uxlog debug logging, sandbox-exec SBPL profile, SBPL /tmp symlink gotcha, sandbox-exec deny-after-allow semantics, claude.json write required for startup, ~/.claude subpath write for resume, symlink write-rule bypass, daemon binary staleness requires restart, sandbox profile temp file debugging, extra_write garbage SBPL rules, settings cursor off-by-one, remote branch resolution for worktrees, daemon cleanup race, zombie prevention, killExistingDaemon, removeIfOwnedByPID, signal.Stop, Serve/Shutdown goroutine ordering, MergeChangedFiles file explorer merge pattern, resolveGitDir worktree .git write access, textarea ColumnOffset soft-wrap bug, PR URL detection, PRURL Task field, pr_url DB column, prURLRe regex, PRDetectedMsg, fast-exit LastOutput scan, 'o' key browser open, reviews tab, gh search prs field limits, enrichReviewDecisions, uxlog logging requirements, task rename (RenameTaskForm, viewRenameTask, git branch -m, worktree repair), Task.Archived, rowArchiveHeader, archiveExpanded, archiveProject, ToggleArchive, CursorOnArchiveHeader, isInArchiveSection, groupByProject, projectTasksFiltered, argusd symlink process naming, Codex backend support, Backend.ResumeCommand, resume_command DB column, backend-aware BuildCmd, backend selector new task form, backendform.go, fixupBackends --yolo to --full-auto, deferred items | 2026-03-17 |

## Coverage Map

Which context files are captured in knowledge:

| Context File | Knowledge File(s) | Coverage |
|-------------|-------------------|----------|
| context/plans/daemon-architecture.md | code-quality.md (Daemon Architecture section) | Implementation patterns, IPC design, gotchas |
| context/plans/sandbox-worktrees.md | code-quality.md (Sandbox section) | sandbox-exec SBPL profile, BuildCmd signature, sandbox config lifecycle, SBPL gotchas |
| context/research/daemon-lifecycle-flows.md | code-quality.md (Daemon Cleanup Race section) | All startup/shutdown/restart flows, goroutine ordering, race analysis |
