package daemon

import (
	"log/slog"
	"time"

	"github.com/drn/argus/internal/kb"
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
// and mtime). The TUI uses this to detect when the on-disk binary has been
// rebuilt since the daemon started, and prompt the user to restart.
//
// The fields read here are written once in Daemon.New() and never mutated
// afterward, so reading without a lock is safe — the goroutine spawn that
// runs RPC handlers happens-after New() returns.
func (s *RPCService) BootInfo(_ *Empty, resp *BootInfoResp) error {
	resp.BinaryPath = s.daemon.binaryPath
	resp.BinaryMtime = s.daemon.binaryMtime
	resp.BinaryHash = s.daemon.binaryHash
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
