package daemon

import (
	"errors"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/selfupdate"
)

// RPCService implements the JSON-RPC methods exposed by the daemon.
type RPCService struct {
	daemon *Daemon
}

// Ping verifies the daemon is responsive.
func (s *RPCService) Ping(_ *Empty, resp *PongResp) error {
	resp.OK = true
	return nil
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

// StartSession starts a new agent session.
func (s *RPCService) StartSession(req *StartReq, resp *StartResp) error {
	slog.Info("rpc.StartSession", "task", req.TaskID, "session", req.SessionID, "project", req.Project, "resume", req.Resume, "cols", req.Cols, "rows", req.Rows, "worktree", req.Worktree)

	task := &model.Task{
		ID:        req.TaskID,
		SessionID: req.SessionID,
		Prompt:    req.Prompt,
		Project:   req.Project,
		Backend:   req.Backend,
		Worktree:  req.Worktree,
		Branch:    req.Branch,
	}

	cfg := s.daemon.db.Config()
	sess, err := s.daemon.runner.Start(task, cfg, req.Rows, req.Cols, req.Resume)
	if err != nil {
		slog.Error("rpc.StartSession failed", "task", req.TaskID, "err", err)
		resp.Error = err.Error()
		return nil
	}
	resp.PID = sess.PID()
	slog.Info("rpc.StartSession ok", "task", req.TaskID, "pid", resp.PID)
	return nil
}

// StopSession stops a running session.
func (s *RPCService) StopSession(req *TaskIDReq, resp *StatusResp) error {
	slog.Info("rpc.StopSession", "task", req.TaskID)
	if err := s.daemon.runner.Stop(req.TaskID); err != nil {
		slog.Error("rpc.StopSession failed", "task", req.TaskID, "err", err)
		resp.Error = err.Error()
		return nil
	}
	slog.Info("rpc.StopSession ok", "task", req.TaskID)
	resp.OK = true
	return nil
}

// StopAll stops all running sessions.
func (s *RPCService) StopAll(_ *Empty, resp *StatusResp) error {
	slog.Info("rpc.StopAll")
	s.daemon.runner.StopAll()
	slog.Info("rpc.StopAll ok")
	resp.OK = true
	return nil
}

// SessionStatus returns info about a single session.
func (s *RPCService) SessionStatus(req *TaskIDReq, resp *SessionInfo) error {
	sess := s.daemon.runner.Get(req.TaskID)
	if sess == nil {
		resp.TaskID = req.TaskID
		// During the brief gap between a kick-restart's old-session exit and
		// the new session's slot being filled, report Alive=true so stream
		// clients retry the connection instead of giving up and tearing down
		// their local UI state. The actual liveness will be reported once
		// the new session is in place.
		if s.daemon.runner.HasPendingRestart(req.TaskID) {
			resp.Alive = true
		}
		return nil
	}
	cols, rows := sess.PTYSize()
	initCols, initRows := sess.InitialPTYSize()
	resp.TaskID = req.TaskID
	resp.Alive = sess.Alive()
	resp.Idle = sess.IsIdle()
	resp.PID = sess.PID()
	resp.Cols = cols
	resp.Rows = rows
	resp.InitialCols = initCols
	resp.InitialRows = initRows
	resp.WorkDir = sess.WorkDir()
	resp.TotalWritten = sess.TotalWritten()
	return nil
}

// ListSessions returns info about all running sessions, plus synthetic
// Alive=true entries for tasks with a queued kick-restart but no current
// session (the brief gap between exit and Start). Without these synthetic
// entries, daemon-client reconcilers (TUI tick) see InProgress + not-running
// → mark Complete after recentStartGrace, racing the imminent restart.
func (s *RPCService) ListSessions(_ *Empty, resp *ListResp) error {
	sessions := s.daemon.runner.Sessions()
	pending := s.daemon.runner.PendingRestartIDs()
	resp.Sessions = make([]SessionInfo, 0, len(sessions)+len(pending))
	for id, sess := range sessions {
		cols, rows := sess.PTYSize()
		initCols, initRows := sess.InitialPTYSize()
		resp.Sessions = append(resp.Sessions, SessionInfo{
			TaskID:       id,
			Alive:        sess.Alive(),
			Idle:         sess.IsIdle(),
			PID:          sess.PID(),
			Cols:         cols,
			Rows:         rows,
			InitialCols:  initCols,
			InitialRows:  initRows,
			WorkDir:      sess.WorkDir(),
			TotalWritten: sess.TotalWritten(),
		})
	}
	// Synthetic entries for the kick-restart gap. Mirrors SessionStatus's
	// Alive=true synthetic when a single-task lookup hits the same window.
	for _, id := range pending {
		resp.Sessions = append(resp.Sessions, SessionInfo{
			TaskID: id,
			Alive:  true,
		})
	}
	return nil
}

// HasPendingRestart reports whether the runner has a kick-restart queued
// for this task. The TUI consults this from handleSessionExitUI so it knows
// to skip the InProgress→InReview transition while the daemon is mid-restart.
func (s *RPCService) HasPendingRestart(req *TaskIDReq, resp *PendingRestartResp) error {
	resp.Pending = s.daemon.runner.HasPendingRestart(req.TaskID)
	return nil
}

// WriteInput sends data to a session's PTY stdin.
func (s *RPCService) WriteInput(req *WriteReq, resp *StatusResp) error {
	sess := s.daemon.runner.Get(req.TaskID)
	if sess == nil {
		resp.Error = "session not found"
		return nil
	}
	if _, err := sess.WriteInput(req.Data); err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// Resize changes a session's PTY dimensions.
func (s *RPCService) Resize(req *ResizeReq, resp *StatusResp) error {
	sess := s.daemon.runner.Get(req.TaskID)
	if sess == nil {
		resp.Error = "session not found"
		return nil
	}
	if err := sess.Resize(req.Rows, req.Cols); err != nil {
		resp.Error = err.Error()
		return nil
	}
	resp.OK = true
	return nil
}

// GetExitInfo returns cached exit info for a finished session.
// Returns empty ExitInfo if the session is still running or info has expired.
func (s *RPCService) GetExitInfo(req *TaskIDReq, resp *ExitInfo) error {
	s.daemon.mu.Lock()
	info, ok := s.daemon.exitInfos[req.TaskID]
	if ok {
		delete(s.daemon.exitInfos, req.TaskID) // consume once
	}
	s.daemon.mu.Unlock()

	if ok {
		*resp = info
	}
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
