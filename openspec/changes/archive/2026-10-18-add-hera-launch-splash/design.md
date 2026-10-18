## Decisions

- **Reuse, don't fork:** `widget.DrawPendingBanner` becomes a wrapper over `widget.DrawLaunchBanner(subtitle)`.
- **State-derived:** `HeraPage.syncLaunchStatus` pushes the bound task's status (from the model, which the daemon-side spawn also updates in the DB) to each pane after every selection/refresh. `TerminalPane.launchSubtitle` combines it with the session (`nil` / alive with `TotalWritten()==0`). No in-process callbacks.
- **Opt-in per pane** (`EnableLaunchSplash`): the main agent view keeps its own `SetPending` path.
- **Cap and clock:** 45s from the first splash frame for a bound session; reset by `ResetVT` and by any session change so a rebind to a different still-launching role re-arms (the nil→nil rebind relies on `ResetVT` alone).
- **First launch only:** both stages are suppressed when the task already has recorded output (non-empty on-disk session log). This keeps the splash off size-drift kill+resume (Re-rendering clears before the new session writes), fresh zero-byte handles for established agents (RemoteSession before its replay lands), and failed revives reverted to `pending`. The log is used rather than per-bind pane state because `ResetVT` wipes the latter on every rebind and revive.
- **Narrow rects:** below the wordmark width (`widget.LaunchBannerMinWidth()`) or the banner height, only the status line draws.
- **Precedence:** the closed-out banner is decided earlier in Draw; `Re-rendering…` (a repair of a session that already had output) suppresses the splash.
- **Short rects:** below the 12-row banner height only the status line is drawn.
