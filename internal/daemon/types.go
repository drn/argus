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
