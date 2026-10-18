## Decisions

- **Reuse, don't fork:** `widget.DrawPendingBanner` becomes a wrapper over `widget.DrawLaunchBanner(subtitle)`.
- **State-derived:** `HeraPage.syncLaunchStatus` pushes the bound task's status (from the model, which the daemon-side spawn also updates in the DB) to each pane after every selection/refresh. `TerminalPane.launchSubtitle` combines it with the session (`nil` / alive with `TotalWritten()==0`). No in-process callbacks.
- **Opt-in per pane** (`EnableLaunchSplash`): the main agent view keeps its own `SetPending` path.
- **Cap and clock:** 45s from the first splash frame for a bound session; reset by `ResetVT` and by any session change, so a revive/kick gets a fresh window.
- **Precedence:** the closed-out banner is decided earlier in Draw; `Re-rendering…` (a repair of a session that already had output) suppresses the splash.
- **Short rects:** below the 12-row banner height only the status line is drawn.
