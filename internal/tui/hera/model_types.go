package hera

import heramodel "github.com/drn/argus/internal/hera/model"

// Model, OrchView, RoleView, Selection, and HeraReader are the hera
// orchestration read model. Their real definitions and BuildModel live in
// internal/hera/model (tview-free, so the daemon's REST API can share them
// without pulling terminal-UI dependencies into its binary — see
// internal/hera/model's package doc). These are Go type ALIASES (not new
// types), so hera.Model and heramodel.Model are the identical type: every
// existing external caller in internal/tui (heraactions.go, hera_tiering.go,
// commandpalette_actions.go, mergesafety.go, app.go, and their tests) keeps
// compiling unchanged against hera.X, with no forced refactor of the TUI's
// local-mode consumer.
type (
	Model      = heramodel.Model
	OrchView   = heramodel.OrchView
	RoleView   = heramodel.RoleView
	Selection  = heramodel.Selection
	HeraReader = heramodel.HeraReader
)
