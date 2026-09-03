package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/selfupdate"
	"github.com/drn/argus/internal/uxlog"
)

// RPCService implements the JSON-RPC methods exposed by the daemon. The
// session-scoped methods (Ping, StartSession, StopSession, StopAll,
// SessionStatus, ListSessions, HasPendingRestart, WriteInput, Resize,
// GetExitInfo) are promoted from the embedded *sessionCore so they register
// under "Daemon" exactly as before — and so the session-supervisor (P1) can
// expose the identical set by embedding the same core. The daemon-specific
// methods below stay here and reach the rest of the daemon via s.daemon.
type RPCService struct {
	*sessionCore
	daemon *Daemon
}

// logRPCErr logs an RPC handler error under the given method name and returns
// err.Error() for assignment to the response's Error field. It folds the
// repeated `resp.Error = err.Error(); slog.Error(method, "err", err)` pair
// that several handlers share into one call. Handlers whose error log carries
// extra structured fields (e.g. ClipboardSet's "task") keep their inline form.
func logRPCErr(method string, err error) string {
	slog.Error(method, "err", err)
	return err.Error()
}

// BootInfo returns the daemon's boot-time identity (binary path, content hash,
// mtime, VCS) AND — when a session-supervisor is connected — the supervisor's
// relayed identity (D1). The TUI uses this to detect when the daemon and/or
// supervisor binary is stale relative to the TUI's own build, and prompt the
// user to restart the relevant process.
//
// The daemon's own fields are written once in Daemon.New() and never mutated,
// so reading without a lock is safe. The supervisor fields are re-queried here
// at SERVE time (not cached at New()) so an independently-restarted supervisor
// reports its CURRENT identity, not whatever was captured when the daemon
// connected. A v2 supervisor (or an unreachable one) yields an empty
// SupervisorHash — reported as "unknown", NEVER as stale.
func (s *RPCService) BootInfo(_ *Empty, resp *BootInfoResp) error {
	resp.BinaryPath = s.daemon.binaryPath
	resp.BinaryMtime = s.daemon.binaryMtime
	resp.BinaryHash = s.daemon.binaryHash
	resp.VCS = s.daemon.vcs
	resp.BootedAt = s.daemon.bootedAt

	// Relay the connected supervisor's identity. supClient is nil in in-process
	// mode (no supervisor); present otherwise. Re-query Hello every call.
	if s.daemon.supClient != nil {
		resp.SupervisorPresent = true
		hello, err := s.daemon.supClient.Hello()
		if err != nil {
			// Present but unreachable — leave the hash empty (unknown). Never a
			// false stale; the TUI treats an unknown supervisor as not-stale.
			uxlog.Log("[skew] BootInfo supervisor Hello failed: %v", err)
			slog.Warn("BootInfo supervisor Hello failed", "err", err)
		} else {
			resp.SupervisorPath = hello.BinaryPath
			resp.SupervisorHash = hello.BinaryHash
			resp.SupervisorVCS = hello.VCS
			// Relay the executed-surface version too (v6). A pre-v6 supervisor
			// leaves both at zero, which the TUI reads as unknown — never stale.
			surface := hello.SupervisorSurface()
			resp.SupervisorSpawnSurface = surface.Spawn
			resp.SupervisorStreamSurface = surface.Stream
			uxlog.Log("[skew] BootInfo relayed supervisor identity: path=%s hash=%s proto=%d surface=%s (%s)",
				hello.BinaryPath, shortHashRPC(hello.BinaryHash), hello.ProtocolVersion,
				surface, CompareSupervisorSurface(surface))
		}
	}
	return nil
}

// shortHashRPC renders a content hash for logging: the first 12 hex chars, or
// "unknown" for an empty hash (a pre-v3 or unreachable supervisor).
func shortHashRPC(h string) string {
	if h == "" {
		return "unknown"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Ports returns the live MCP and REST API HTTP ports the daemon is bound to.
// Plugins (e.g. hera) call this over the Unix socket to discover the current
// ports instead of hardcoding — bindWithRetry means neither port is stable
// across restarts. Zero means that server is not enabled or failed to bind.
//
// The port fields are written once in Serve under d.mu and read here under
// the same lock; mirrors KBStatus's treatment of mcpPort.
func (s *RPCService) Ports(_ *Empty, resp *PortsResp) error {
	s.daemon.mu.Lock()
	resp.MCPPort = s.daemon.mcpPort
	resp.APIPort = s.daemon.apiPort
	s.daemon.mu.Unlock()
	return nil
}

// Shutdown initiates a graceful daemon shutdown.
func (s *RPCService) Shutdown(_ *Empty, resp *StatusResp) error {
	slog.Info("rpc.Shutdown requested")
	resp.OK = true
	go s.daemon.Shutdown()
	return nil
}

// KBSearch performs a full-text search of the knowledge base.
func (s *RPCService) KBSearch(req *KBSearchReq, resp *KBSearchResp) error {
	slog.Info("rpc.KBSearch", "query", req.Query, "limit", req.Limit)
	sanitized := kb.SanitizeQuery(req.Query)
	if sanitized == "" {
		resp.Results = nil
		return nil
	}
	results, err := s.daemon.db.KBSearch(sanitized, req.Limit)
	if err != nil {
		resp.Error = logRPCErr("rpc.KBSearch", err)
		return nil
	}
	for _, r := range results {
		resp.Results = append(resp.Results, KBSearchResult{
			Path:    r.Path,
			Title:   r.Title,
			Tier:    r.Tier,
			Snippet: r.Snippet,
			Rank:    r.Rank,
		})
	}
	slog.Info("rpc.KBSearch ok", "results", len(resp.Results))
	return nil
}

// KBIngest ingests a document into the knowledge base.
func (s *RPCService) KBIngest(req *KBIngestReq, resp *KBIngestResp) error {
	slog.Info("rpc.KBIngest", "path", req.Path)
	doc := kb.ParseDocument(req.Path, req.Content)
	doc.IngestedAt = time.Now()
	doc.ModifiedAt = time.Now()
	if err := s.daemon.db.KBUpsert(&doc); err != nil {
		resp.Error = logRPCErr("rpc.KBIngest", err)
	} else {
		slog.Info("rpc.KBIngest ok", "path", req.Path)
	}
	return nil
}

// KBList lists documents in the knowledge base.
func (s *RPCService) KBList(req *KBListReq, resp *KBListResp) error {
	slog.Info("rpc.KBList", "prefix", req.Prefix, "limit", req.Limit)
	docs, err := s.daemon.db.KBList(req.Prefix, req.Limit)
	if err != nil {
		resp.Error = logRPCErr("rpc.KBList", err)
		return nil
	}
	for _, doc := range docs {
		resp.Documents = append(resp.Documents, KBDocumentInfo{
			Path:      doc.Path,
			Title:     doc.Title,
			Tier:      doc.Tier,
			WordCount: doc.WordCount,
		})
	}
	slog.Info("rpc.KBList ok", "documents", len(resp.Documents))
	return nil
}

// UpdateSelf fetches origin and hard-resets to `origin/master`, then runs
// `go install ./...` against the configured argus source path. The combined
// command output is returned regardless of success so callers can show it to
// the user. The daemon is NOT restarted by this RPC — the caller decides.
func (s *RPCService) UpdateSelf(_ *Empty, resp *UpdateSelfResp) error {
	cfg := s.daemon.db.Config()
	slog.Info("rpc.UpdateSelf", "source", cfg.Argus.SourcePath)
	out, err := selfupdate.Run(cfg.Argus.SourcePath)
	resp.Output = out
	if err != nil {
		resp.Error = logRPCErr("rpc.UpdateSelf failed", err)
	} else {
		slog.Info("rpc.UpdateSelf ok")
	}
	return nil
}

// KBStatus returns the current state of the knowledge base.
func (s *RPCService) KBStatus(_ *Empty, resp *KBStatusResp) error {
	resp.DocumentCount = s.daemon.db.KBDocumentCount()
	cfg := s.daemon.db.Config()
	resp.VaultPath = cfg.KB.MetisVaultPath
	s.daemon.mu.Lock()
	resp.Port = s.daemon.mcpPort
	s.daemon.mu.Unlock()
	slog.Info("rpc.KBStatus", "docs", resp.DocumentCount, "vault", resp.VaultPath, "port", resp.Port)
	return nil
}

// ClipboardSet stages text for a task in the agent-staged clipboard. Used by
// agents (via MCP) to queue text for the user to copy with a single tap or
// keypress.
func (s *RPCService) ClipboardSet(req *ClipboardSetReq, resp *StatusResp) error {
	slog.Info("rpc.ClipboardSet", "task", req.TaskID, "bytes", len(req.Text))
	if err := s.daemon.clipboard.Set(req.TaskID, req.Text); err != nil {
		resp.Error = err.Error()
		slog.Error("rpc.ClipboardSet failed", "task", req.TaskID, "err", err)
		return nil
	}
	resp.OK = true
	return nil
}

// ClipboardGet returns any staged text for a task. Returns OK=false when no
// payload is staged (or it has expired).
func (s *RPCService) ClipboardGet(req *ClipboardGetReq, resp *ClipboardGetResp) error {
	text, ok := s.daemon.clipboard.Get(req.TaskID)
	resp.Text = text
	resp.OK = ok
	return nil
}

// ClipboardClear removes any staged text for a task and notifies subscribers.
func (s *RPCService) ClipboardClear(req *ClipboardClearReq, resp *StatusResp) error {
	slog.Info("rpc.ClipboardClear", "task", req.TaskID)
	s.daemon.clipboard.Clear(req.TaskID)
	resp.OK = true
	return nil
}

// ForceRecycleCoordinator kills and restarts the coordinator role bound to
// req.TaskID immediately, ignoring recycle_coord's idle gate entirely
// (add-coordinator-context-management D5's human-forced path). It is
// `argus coord-hook`'s hard-stop escalation trigger (fix-coordhook-idle-
// deadlock, Part B): once a coordinator's context_size crosses 1.5x its
// budget, the hook calls this over the daemon socket rather than trust the
// graceful path, which can be stuck waiting for idleness that never comes.
//
// Mirrors internal/tui/heraactions.go's heraDoForceRecycle (the rail's `B`
// key) exactly — same coordinator-role resolution shape as
// hera.RecycleWatcher.tickTask (ListHeraLiveBindingsByTask, first
// coordinator-kind binding found), same daemon.NewHeraRecycleRunner, same
// hera.RecycleCoord call with hera.RecycleHumanForced — so both entry points
// kill/restart identically.
func (s *RPCService) ForceRecycleCoordinator(req *TaskIDReq, resp *StatusResp) error {
	slog.Info("rpc.ForceRecycleCoordinator", "task", req.TaskID)

	bindings, err := s.daemon.db.ListHeraLiveBindingsByTask(req.TaskID)
	if err != nil {
		resp.Error = logRPCErr("rpc.ForceRecycleCoordinator",
			fmt.Errorf("list bindings for task %s: %w", req.TaskID, err))
		return nil
	}
	var coordRoleID int64
	found := false
	for _, b := range bindings {
		role, err := s.daemon.db.HeraRole(b.RoleID)
		if err != nil {
			resp.Error = logRPCErr("rpc.ForceRecycleCoordinator",
				fmt.Errorf("resolve role %d: %w", b.RoleID, err))
			return nil
		}
		if role.Kind == db.HeraKindCoordinator {
			coordRoleID = role.ID
			found = true
			break
		}
	}
	if !found {
		resp.Error = logRPCErr("rpc.ForceRecycleCoordinator",
			fmt.Errorf("no coordinator role bound to task %s", req.TaskID))
		return nil
	}

	sessionID := ""
	if task, err := s.daemon.db.Get(req.TaskID); err == nil && task != nil {
		sessionID = task.SessionID
	}

	rr := NewHeraRecycleRunner(s.daemon.db, s.runner, s.cfgFn)
	if err := hera.RecycleCoord(s.daemon.db, rr, coordRoleID, sessionID, hera.RecycleHumanForced); err != nil {
		resp.Error = logRPCErr("rpc.ForceRecycleCoordinator",
			fmt.Errorf("recycle task %s: %w", req.TaskID, err))
		return nil
	}
	resp.OK = true
	return nil
}

// SetFocused registers or clears TUI agent-view focus for a task. Called by
// the TUI's daemon client when the user enters or exits agent view so the
// reliable pane-delivery reconciler can gate auto-submits on human presence.
func (s *RPCService) SetFocused(req *SetFocusedReq, _ *Empty) error {
	if s.daemon.focusTracker == nil || req.TaskID == "" {
		return nil
	}
	s.daemon.focusTracker.SetFocused(req.TaskID, req.Focused)
	return nil
}
