package daemon

import (
	"errors"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/selfupdate"
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

// BootInfo returns the daemon's boot-time identity (binary path + mtime).
// The TUI uses this to detect when the on-disk binary has been rebuilt since
// the daemon started, and prompt the user to restart.
//
// The fields read here are written once in Daemon.New() and never mutated
// afterward, so reading without a lock is safe — the goroutine spawn that
// runs RPC handlers happens-after New() returns.
func (s *RPCService) BootInfo(_ *Empty, resp *BootInfoResp) error {
	resp.BinaryPath = s.daemon.binaryPath
	resp.BinaryMtime = s.daemon.binaryMtime
	resp.BootedAt = s.daemon.bootedAt
	return nil
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
		resp.Error = err.Error()
		slog.Error("rpc.KBSearch", "err", err)
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
		resp.Error = err.Error()
		slog.Error("rpc.KBIngest", "err", err)
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
		resp.Error = err.Error()
		slog.Error("rpc.KBList", "err", err)
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
		resp.Error = err.Error()
		slog.Error("rpc.UpdateSelf failed", "err", err)
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

// LinkTasks adds ParentID to ChildID's depends_on list. Delegates to orch.Link
// so the HTTP API path runs the same cycle DFS without going through net/rpc.
func (s *RPCService) LinkTasks(req *LinkTasksReq, resp *LinkTasksResp) error {
	slog.Info("rpc.LinkTasks", "child", req.ChildID, "parent", req.ParentID)
	err := orch.Link(s.daemon.db, req.ChildID, req.ParentID)
	var ce *orch.CycleError
	if errors.As(err, &ce) {
		resp.Cycle = ce.Path
		resp.Error = ce.Error()
		return nil
	}
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// UnlinkTasks removes ParentID from ChildID's depends_on. No-op if the edge
// does not exist; cannot induce a cycle.
func (s *RPCService) UnlinkTasks(req *UnlinkTasksReq, resp *LinkTasksResp) error {
	slog.Info("rpc.UnlinkTasks", "child", req.ChildID, "parent", req.ParentID)
	if err := orch.Unlink(s.daemon.db, req.ChildID, req.ParentID); err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// GetDeps returns the one-hop neighbours of TaskID in both directions.
func (s *RPCService) GetDeps(req *DepsReq, resp *DepsResp) error {
	view, err := orch.Deps(s.daemon.db, req.TaskID)
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.Upstream = view.Upstream
	resp.Downstream = view.Downstream
	return nil
}

// ListDAG returns a minimal projection of every task matching the supplied
// filters. The client materializes edges from each node's DependsOn array.
func (s *RPCService) ListDAG(req *DAGReq, resp *DAGResp) error {
	nodes, err := orch.ListDAG(s.daemon.db, orch.DAGFilter{
		Project:         req.Project,
		PlanSlug:        req.PlanSlug,
		IncludeArchived: req.IncludeArchived,
	})
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.Nodes = make([]DAGNode, 0, len(nodes))
	for _, n := range nodes {
		resp.Nodes = append(resp.Nodes, DAGNode(n))
	}
	return nil
}

// HaltDownstream stops in_progress descendants of TaskID and archives pending
// ones. The seed task is NOT halted — see orch.HaltDownstream for the
// depswatcher-race contract.
func (s *RPCService) HaltDownstream(req *HaltDownstreamReq, resp *HaltDownstreamResp) error {
	slog.Info("rpc.HaltDownstream", "task", req.TaskID)
	report, err := orch.HaltDownstream(s.daemon.db, s.daemon.runner, req.TaskID, func(err error) bool {
		return errors.Is(err, agent.ErrSessionNotFound)
	})
	if err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.Stopped = report.Stopped
	resp.Archived = report.Archived
	resp.NotFound = report.NotFound
	slog.Info("rpc.HaltDownstream ok", "task", req.TaskID, "stopped", len(resp.Stopped), "archived", len(resp.Archived))
	return nil
}

// SetPlanSlug writes the orchestrator grouping label for a task. The daemon
// does not interpret the value — same opacity contract as Result.
func (s *RPCService) SetPlanSlug(req *SetPlanSlugReq, resp *StatusResp) error {
	slog.Info("rpc.SetPlanSlug", "task", req.TaskID, "slug", req.PlanSlug)
	if err := orch.SetPlanSlug(s.daemon.db, req.TaskID, req.PlanSlug); err != nil {
		resp.Error = err.Error()
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
