package daemon

// StartReq is the RPC request to start a new agent session.
type StartReq struct {
	TaskID    string
	SessionID string
	Prompt    string
	Project   string
	Backend   string
	Worktree  string
	Branch    string
	Rows      uint16
	Cols      uint16
	Resume    bool
}

// StartResp is the RPC response from starting a session.
type StartResp struct {
	PID int
}

// TaskIDReq is an RPC request that identifies a single task.
type TaskIDReq struct {
	TaskID string
}

// StatusResp is a generic success/error RPC response.
type StatusResp struct {
	OK    bool
	Error string
}

// SessionInfo describes the state of a running session.
type SessionInfo struct {
	TaskID       string
	Alive        bool
	Idle         bool
	PID          int
	Cols         int
	Rows         int
	WorkDir      string
	TotalWritten uint64
}

// WriteReq is the RPC request to send input to a session's PTY.
type WriteReq struct {
	TaskID string
	Data   []byte
}

// ResizeReq is the RPC request to resize a session's PTY.
type ResizeReq struct {
	TaskID string
	Rows   uint16
	Cols   uint16
}

// StreamHeader is sent by the client on a stream connection to subscribe
// to a session's output.
type StreamHeader struct {
	TaskID string `json:"task_id"`
}

// ListResp is the RPC response for listing all sessions.
type ListResp struct {
	Sessions []SessionInfo
}

// PongResp is the RPC response for a Ping request.
type PongResp struct {
	OK bool
}

// Empty is a placeholder for RPC methods that take no arguments.
type Empty struct{}

// KBSearchReq is the RPC request to search the knowledge base.
type KBSearchReq struct {
	Query string
	Limit int
}

// KBSearchResp is the RPC response from a KB search.
type KBSearchResp struct {
	Results []KBSearchResult
	Error   string
}

// KBSearchResult is a KB search result returned over RPC.
// (Mirrors kb.SearchResult but avoids importing the kb package in types.go.)
type KBSearchResult struct {
	Path    string
	Title   string
	Tier    string
	Snippet string
	Rank    float64
}

// KBIngestReq is the RPC request to ingest a document into the knowledge base.
type KBIngestReq struct {
	Path    string
	Content string
}

// KBIngestResp is the RPC response from a KB ingest.
type KBIngestResp struct {
	Error string
}

// KBListReq is the RPC request to list documents in the knowledge base.
type KBListReq struct {
	Prefix string
	Limit  int
}

// KBListResp is the RPC response from a KB list.
type KBListResp struct {
	Documents []KBDocumentInfo
	Error     string
}

// KBDocumentInfo summarises a KB document (no body).
type KBDocumentInfo struct {
	Path      string
	Title     string
	Tier      string
	WordCount int
}

// KBStatusResp is the RPC response for a KB status query.
type KBStatusResp struct {
	DocumentCount int
	VaultPath     string
	Port          int
}
