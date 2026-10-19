## Decisions

- **Reuse, don't fork:** `widget.DrawPendingBanner` becomes a wrapper over `widget.DrawLaunchBanner(subtitle)`.
- **State-derived:** `HeraPage.syncLaunchStatus` pushes the bound task's status (from the model, which the daemon-side spawn also updates in the DB) to each pane after every selection/refresh. `TerminalPane.launchSubtitle` combines it with the session (`nil` / alive with `TotalWritten()==0`). No in-process callbacks.
- **Opt-in per pane** (`EnableLaunchSplash`): the main agent view keeps its own `SetPending` path.
- **Cap and clock:** 45s from the first splash frame for a bound session; reset by `ResetVT` and by any session change so a rebind to a different still-launching role re-arms (the nil→nil rebind relies on `ResetVT` alone).
- **First launch only:** both stages are suppressed when the task already has history: a per-task latch on the pane (set when its session handle reports `TotalWritten() > 0`, or the on-disk session log is non-empty; never cleared by `ResetVT`/`SetSession`) — checked first, so a latched task is never stat'd again. This keeps the splash off size-drift kill+resume (Re-rendering clears before the new session writes), fresh zero-byte handles for established agents (RemoteSession before its replay lands), and failed revives reverted to `pending`. The log alone is not enough: `agent.NewSession` opens it `O_TRUNC` on every spawn, so a kill+resume or revive sees a 0-byte log and 0-byte handle. Known gap: a TUI launched fresh during a respawn window cannot know the history and may briefly show STARTING.
- **Narrow rects:** below the wordmark width (`widget.LaunchBannerMinWidth()`) or the banner height, only the status line draws.
- **Precedence:** the closed-out banner is decided earlier in Draw; `Re-rendering…` (a repair of a session that already had output) suppresses the splash.
- **Short rects:** below the 12-row banner height only the status line is drawn.
