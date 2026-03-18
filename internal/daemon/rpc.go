package daemon

import (
	"log"
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

// StartSession starts a new agent session.
func (s *RPCService) StartSession(req *StartReq, resp *StartResp) error {
	log.Printf("rpc.StartSession: task=%s session=%s project=%s resume=%v pty=%dx%d worktree=%s",
		req.TaskID, req.SessionID, req.Project, req.Resume, req.Cols, req.Rows, req.Worktree)

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
		log.Printf("rpc.StartSession: FAILED task=%s err=%v", req.TaskID, err)
		return err
	}
	resp.PID = sess.PID()
	log.Printf("rpc.StartSession: OK task=%s pid=%d", req.TaskID, resp.PID)
	return nil
}

// StopSession stops a running session.
func (s *RPCService) StopSession(req *TaskIDReq, resp *StatusResp) error {
	log.Printf("rpc.StopSession: task=%s", req.TaskID)
	if err := s.daemon.runner.Stop(req.TaskID); err != nil {
		log.Printf("rpc.StopSession: FAILED task=%s err=%v", req.TaskID, err)
		resp.Error = err.Error()
		return nil
	}
	log.Printf("rpc.StopSession: OK task=%s", req.TaskID)
	resp.OK = true
	return nil
}

// StopAll stops all running sessions.
func (s *RPCService) StopAll(_ *Empty, resp *StatusResp) error {
	log.Printf("rpc.StopAll")
	s.daemon.runner.StopAll()
	log.Printf("rpc.StopAll: OK")
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
	ids := s.daemon.runner.Running()
	resp.Sessions = make([]SessionInfo, 0, len(ids))
	for _, id := range ids {
		sess := s.daemon.runner.Get(id)
		if sess == nil {
			continue
		}
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
	log.Printf("rpc.Shutdown: requested")
	resp.OK = true
	go s.daemon.Shutdown()
	return nil
}

// KBSearch performs a full-text search of the knowledge base.
func (s *RPCService) KBSearch(req *KBSearchReq, resp *KBSearchResp) error {
	log.Printf("rpc.KBSearch: query=%q limit=%d", req.Query, req.Limit)
	sanitized := kb.SanitizeQuery(req.Query)
	if sanitized == "" {
		resp.Results = nil
		return nil
	}
	results, err := s.daemon.db.KBSearch(sanitized, req.Limit)
	if err != nil {
		resp.Error = err.Error()
		log.Printf("rpc.KBSearch: error: %v", err)
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
	log.Printf("rpc.KBSearch: %d results", len(resp.Results))
	return nil
}

// KBIngest ingests a document into the knowledge base.
func (s *RPCService) KBIngest(req *KBIngestReq, resp *KBIngestResp) error {
	log.Printf("rpc.KBIngest: path=%s", req.Path)
	doc := kb.ParseDocument(req.Path, req.Content)
	doc.IngestedAt = time.Now()
	doc.ModifiedAt = time.Now()
	if err := s.daemon.db.KBUpsert(&doc); err != nil {
		resp.Error = err.Error()
		log.Printf("rpc.KBIngest: error: %v", err)
	} else {
		log.Printf("rpc.KBIngest: ok path=%s", req.Path)
	}
	return nil
}

// KBList lists documents in the knowledge base.
func (s *RPCService) KBList(req *KBListReq, resp *KBListResp) error {
	log.Printf("rpc.KBList: prefix=%q limit=%d", req.Prefix, req.Limit)
	docs, err := s.daemon.db.KBList(req.Prefix, req.Limit)
	if err != nil {
		resp.Error = err.Error()
		log.Printf("rpc.KBList: error: %v", err)
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
	log.Printf("rpc.KBList: %d documents", len(resp.Documents))
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
	log.Printf("rpc.KBStatus: docs=%d vault=%s port=%d", resp.DocumentCount, resp.VaultPath, resp.Port)
	return nil
}
