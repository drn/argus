package daemon

import (
	"log/slog"
	"time"

	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/model"
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
		return nil
	}
	cols, rows := sess.PTYSize()
	resp.TaskID = req.TaskID
	resp.Alive = sess.Alive()
	resp.Idle = sess.IsIdle()
	resp.PID = sess.PID()
	resp.Cols = cols
	resp.Rows = rows
	resp.WorkDir = sess.WorkDir()
	resp.TotalWritten = sess.TotalWritten()
	return nil
}

// ListSessions returns info about all running sessions.
func (s *RPCService) ListSessions(_ *Empty, resp *ListResp) error {
	sessions := s.daemon.runner.Sessions()
	resp.Sessions = make([]SessionInfo, 0, len(sessions))
	for id, sess := range sessions {
		cols, rows := sess.PTYSize()
		resp.Sessions = append(resp.Sessions, SessionInfo{
			TaskID:       id,
			Alive:        sess.Alive(),
			Idle:         sess.IsIdle(),
			PID:          sess.PID(),
			Cols:         cols,
			Rows:         rows,
			WorkDir:      sess.WorkDir(),
			TotalWritten: sess.TotalWritten(),
		})
	}
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
