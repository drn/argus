# Knowledge Index

Structured knowledge for cross-session persistence. Each file covers a topic/domain.

| File | Topic | Key Entities | Last Updated |
|------|-------|-------------|-------------|
| code-quality.md | Refactoring patterns, daemon architecture, and deferred items | ScrollState, vtrender.go, sgrColor, borderedPanel, file splits, GitStatus pointer bug, cursor skip-header, Alt modifier keyMsgToBytes, textarea zero-value trap, textarea viewport scroll bug, worktree removal safety guard, self-managed worktrees, scroll-offset chicken-and-egg, diff panel line wrapping, ⌘ runewidth mismatch, tmux color palette, zero-dimension View() panic, vt10x cursor XOR reverse, PanelLayout extraction, worktree-first task creation regression, PanelLayout width enforcement bug, SessionProvider/SessionHandle interfaces, multi-writer pattern, nil-interface gotcha, daemon IPC protocol, RingBuffer export, daemon client, chroma background compositing, injectBg, onFinish ordering, RPC timeout wrapper, daemon file logging, tab-zero-width expandTabs, daemon restart, SessionID preservation on restart, double-pointer BT pattern, AutoStart extraction, stream-failure-auto-complete bug, uxlog debug logging, srt allowPty, settings cursor off-by-one, remote branch resolution for worktrees, daemon cleanup race, zombie prevention, killExistingDaemon, removeIfOwnedByPID, signal.Stop, Serve/Shutdown goroutine ordering, deferred items | 2026-03-16 |

## Coverage Map

Which context files are captured in knowledge:

| Context File | Knowledge File(s) | Coverage |
|-------------|-------------------|----------|
| context/plans/daemon-architecture.md | code-quality.md (Daemon Architecture section) | Implementation patterns, IPC design, gotchas |
| context/plans/sandbox-worktrees.md | code-quality.md (Sandbox section) | srt integration, BuildCmd signature, sandbox config lifecycle |
| context/research/daemon-lifecycle-flows.md | code-quality.md (Daemon Cleanup Race section) | All startup/shutdown/restart flows, goroutine ordering, race analysis |
