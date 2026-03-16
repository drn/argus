# Knowledge Index

Structured knowledge for cross-session persistence. Each file covers a topic/domain.

| File | Topic | Key Entities | Last Updated |
|------|-------|-------------|-------------|
| code-quality.md | Refactoring patterns, daemon architecture, and deferred items | ScrollState, vtrender.go, sgrColor, borderedPanel, file splits, GitStatus pointer bug, cursor skip-header, Alt modifier keyMsgToBytes, textarea zero-value trap, textarea viewport scroll bug, worktree removal safety guard, self-managed worktrees, scroll-offset chicken-and-egg, diff panel line wrapping, ⌘ runewidth mismatch, tmux color palette, zero-dimension View() panic, vt10x cursor XOR reverse, PanelLayout extraction, worktree-first task creation regression, PanelLayout width enforcement bug, SessionProvider/SessionHandle interfaces, multi-writer pattern, nil-interface gotcha, daemon IPC protocol, RingBuffer export, daemon client, chroma background compositing, injectBg, onFinish ordering, RPC timeout wrapper, daemon file logging, deferred items | 2026-03-15 |

## Coverage Map

Which context files are captured in knowledge:

| Context File | Knowledge File(s) | Coverage |
|-------------|-------------------|----------|
| context/plans/daemon-architecture.md | code-quality.md (Daemon Architecture section) | Implementation patterns, IPC design, gotchas |
