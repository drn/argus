package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/model"
)

// KBQuerier is the interface the MCP server needs from the database.
type KBQuerier interface {
	KBSearch(query string, limit int) ([]kb.SearchResult, error)
	KBGet(path string) (*kb.Document, error)
	KBList(prefix string, limit int) ([]kb.Document, error)
	KBUpsert(doc *kb.Document) error
	KBDocumentCount() int
}

// TaskCreator creates a task with worktree and starts an agent session.
// Same signature as daemon.HeadlessCreateTask (injected to avoid import cycle).
type TaskCreator func(name, prompt, project, todoPath string) (*model.Task, error)

// TaskQuerier provides read access to tasks.
type TaskQuerier interface {
	Tasks() []*model.Task
	Get(id string) (*model.Task, error)
}

// TaskStopper can stop a running agent session.
type TaskStopper interface {
	Stop(taskID string) error
}

// Server is the MCP HTTP server.
type Server struct {
	db          KBQuerier
	port        int
	httpSrv     *http.Server
	createTask  TaskCreator
	taskDB      TaskQuerier
	taskStopper TaskStopper
}

// New creates a new MCP server.
func New(db KBQuerier, port int) *Server {
	return &Server{db: db, port: port}
}

// SetTaskManager wires in task management capabilities.
// When set, the server exposes task_create, task_list, task_get, and task_stop tools.
func (s *Server) SetTaskManager(creator TaskCreator, taskDB TaskQuerier, stopper TaskStopper) {
	s.createTask = creator
	s.taskDB = taskDB
	s.taskStopper = stopper
}

// ListenAndServe starts the HTTP server. It tries port first, then port+1..port+8.
// Returns the actual port used (for injection into agent configs).
// Blocks until the server exits.
func (s *Server) ListenAndServe() (int, error) {
	mux := http.NewServeMux()
	mux.Handle("/mcp", s)

	var ln net.Listener
	var err error
	actualPort := s.port
	for i := 0; i < 9; i++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", actualPort))
		if err == nil {
			break
		}
		actualPort++
	}
	if err != nil {
		return 0, fmt.Errorf("mcp listen: %w", err)
	}

	srv := &http.Server{Handler: mux}
	s.httpSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp http serve: %v", err)
		}
	}()
	return actualPort, nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// ServeHTTP handles MCP JSON-RPC 2.0 requests at POST /mcp.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// SSE endpoint for server-initiated messages — not yet used.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, -32700, "parse error")
		return
	}

	resp := s.dispatch(&req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) dispatch(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		// No-op.
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: nil}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return errorResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params InitializeParams
	if req.Params != nil {
		json.Unmarshal(req.Params, &params) //nolint:errcheck
	}

	// Codex bug workaround: echo back the client's protocolVersion.
	protocolVersion := params.ProtocolVersion
	if protocolVersion == "" {
		protocolVersion = "2024-11-05"
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo: ServerInfo{
				Name:    "argus-kb",
				Version: "1.0.0",
			},
			Capabilities: Capabilities{
				Tools: &ToolsCapability{},
			},
		},
	}
}

// toolDefs defines the four KB tools exposed via MCP.
var toolDefs = []Tool{
	{
		Name:        "kb_search",
		Description: "Search the Argus knowledge base using full-text search. Returns ranked results with snippets.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Search query"},
				"limit": map[string]interface{}{"type": "number", "description": "Maximum results (default 10)"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "kb_read",
		Description: "Read the full content of a knowledge base document by its vault-relative path.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Vault-relative path, e.g. 'projects/thanx.md'"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name:        "kb_list",
		Description: "List documents in the knowledge base, optionally filtered by path prefix.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prefix": map[string]interface{}{"type": "string", "description": "Path prefix filter (optional)"},
				"limit":  map[string]interface{}{"type": "number", "description": "Maximum documents (default 100)"},
			},
		},
	},
	{
		Name:        "kb_ingest",
		Description: "Add or update a document in the knowledge base.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":    map[string]interface{}{"type": "string", "description": "Vault-relative path for the document"},
				"content": map[string]interface{}{"type": "string", "description": "Full markdown content to index"},
			},
			"required": []string{"path", "content"},
		},
	},
}

// taskToolDefs are exposed only when SetTaskManager has been called.
var taskToolDefs = []Tool{
	{
		Name:        "task_create",
		Description: "Create a new Argus task with a git worktree and start an agent session. Returns task ID, name, and status.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "Task name (used for branch/worktree naming). Auto-generated from prompt if omitted."},
				"prompt":  map[string]interface{}{"type": "string", "description": "Instructions for the agent"},
				"project": map[string]interface{}{"type": "string", "description": "Project name (must exist in Argus config)"},
			},
			"required": []string{"prompt", "project"},
		},
	},
	{
		Name:        "task_list",
		Description: "List Argus tasks, optionally filtered by status and/or project.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status":  map[string]interface{}{"type": "string", "description": "Filter by status: pending, in_progress, in_review, complete"},
				"project": map[string]interface{}{"type": "string", "description": "Filter by project name"},
			},
		},
	},
	{
		Name:        "task_get",
		Description: "Get details of a specific Argus task by ID.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Task ID"},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "task_stop",
		Description: "Stop a running Argus agent session. The task moves to in_review status.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Task ID to stop"},
			},
			"required": []string{"id"},
		},
	},
}

func (s *Server) handleToolsList(req *Request) *Response {
	tools := toolDefs
	if s.createTask != nil {
		tools = append(tools, taskToolDefs...)
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: tools},
	}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResp(req.ID, -32602, "invalid params")
	}

	switch params.Name {
	case "kb_search":
		return s.toolKBSearch(req.ID, params.Arguments)
	case "kb_read":
		return s.toolKBRead(req.ID, params.Arguments)
	case "kb_list":
		return s.toolKBList(req.ID, params.Arguments)
	case "kb_ingest":
		return s.toolKBIngest(req.ID, params.Arguments)
	case "task_create":
		return s.toolTaskCreate(req.ID, params.Arguments)
	case "task_list":
		return s.toolTaskList(req.ID, params.Arguments)
	case "task_get":
		return s.toolTaskGet(req.ID, params.Arguments)
	case "task_stop":
		return s.toolTaskStop(req.ID, params.Arguments)
	default:
		return errorResp(req.ID, -32601, "unknown tool: "+params.Name)
	}
}

func (s *Server) toolKBSearch(id interface{}, args json.RawMessage) *Response {
	var p struct {
		Query string  `json:"query"`
		Limit float64 `json:"limit"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	limit := int(p.Limit)
	if limit <= 0 {
		limit = 10
	}

	sanitized := kb.SanitizeQuery(p.Query)
	if sanitized == "" {
		return toolResult(id, "No results: empty query after sanitization.")
	}

	results, err := s.db.KBSearch(sanitized, limit)
	if err != nil {
		return toolError(id, fmt.Sprintf("Search failed: %v", err))
	}
	if len(results) == 0 {
		return toolResult(id, "No results found.")
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "## %d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "**Path**: %s | **Tier**: %s\n", r.Path, r.Tier)
		fmt.Fprintf(&sb, "**Snippet**: %s\n\n", r.Snippet)
	}
	return toolResult(id, sb.String())
}

func (s *Server) toolKBRead(id interface{}, args json.RawMessage) *Response {
	var p struct {
		Path string `json:"path"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Path == "" {
		return toolError(id, "path is required")
	}

	doc, err := s.db.KBGet(p.Path)
	if err != nil {
		return toolError(id, fmt.Sprintf("Document not found: %v", err))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", doc.Title)
	if len(doc.Tags) > 0 {
		fmt.Fprintf(&sb, "**Tags**: %s\n\n", strings.Join(doc.Tags, ", "))
	}
	fmt.Fprintf(&sb, "**Modified**: %s | **Words**: %d\n\n", doc.ModifiedAt.Format(time.RFC3339), doc.WordCount)
	fmt.Fprintf(&sb, "---\n\n%s", doc.Body)
	return toolResult(id, sb.String())
}

func (s *Server) toolKBList(id interface{}, args json.RawMessage) *Response {
	var p struct {
		Prefix string  `json:"prefix"`
		Limit  float64 `json:"limit"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	limit := int(p.Limit)
	if limit <= 0 {
		limit = 100
	}

	docs, err := s.db.KBList(p.Prefix, limit)
	if err != nil {
		return toolError(id, fmt.Sprintf("List failed: %v", err))
	}
	if len(docs) == 0 {
		return toolResult(id, "No documents found.")
	}

	var sb strings.Builder
	for _, doc := range docs {
		fmt.Fprintf(&sb, "- **%s** (%s) [%d words]\n", doc.Path, doc.Tier, doc.WordCount)
	}
	return toolResult(id, sb.String())
}

func (s *Server) toolKBIngest(id interface{}, args json.RawMessage) *Response {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Path == "" || p.Content == "" {
		return toolError(id, "path and content are required")
	}
	if filepath.IsAbs(p.Path) || strings.Contains(p.Path, "..") {
		return toolError(id, "invalid path: must be vault-relative with no '..' components")
	}

	doc := kb.ParseDocument(p.Path, p.Content)
	doc.IngestedAt = time.Now()
	doc.ModifiedAt = time.Now()
	if err := s.db.KBUpsert(&doc); err != nil {
		return toolError(id, fmt.Sprintf("Ingest failed: %v", err))
	}
	return toolResult(id, fmt.Sprintf("Ingested %s (%d words)", p.Path, doc.WordCount))
}

// --- task tools ---

func (s *Server) toolTaskCreate(id interface{}, args json.RawMessage) *Response {
	if s.createTask == nil {
		return toolError(id, "task management not configured")
	}

	var p struct {
		Name    string `json:"name"`
		Prompt  string `json:"prompt"`
		Project string `json:"project"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Project == "" {
		return toolError(id, "project is required")
	}
	if p.Prompt == "" {
		return toolError(id, "prompt is required")
	}

	name := p.Name
	if name == "" {
		name = sanitizeTaskName(p.Prompt)
	}

	task, err := s.createTask(name, p.Prompt, p.Project, "")
	if err != nil {
		return toolError(id, fmt.Sprintf("Failed to create task: %v", err))
	}

	return toolResult(id, fmt.Sprintf("Task created.\n\n- **ID**: %s\n- **Name**: %s\n- **Status**: %s\n- **Project**: %s\n- **Branch**: %s",
		task.ID, task.Name, task.Status.String(), task.Project, task.Branch))
}

func (s *Server) toolTaskList(id interface{}, args json.RawMessage) *Response {
	if s.taskDB == nil {
		return toolError(id, "task management not configured")
	}

	var p struct {
		Status  string `json:"status"`
		Project string `json:"project"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	tasks := s.taskDB.Tasks()
	var sb strings.Builder
	count := 0
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		if p.Status != "" && t.Status.String() != p.Status {
			continue
		}
		if p.Project != "" && t.Project != p.Project {
			continue
		}
		count++
		fmt.Fprintf(&sb, "- **%s** `%s` [%s] (%s)", t.Name, t.ID, t.Status.String(), t.Project)
		if t.Branch != "" {
			fmt.Fprintf(&sb, " branch:%s", t.Branch)
		}
		if elapsed := t.ElapsedString(); elapsed != "" {
			fmt.Fprintf(&sb, " %s", elapsed)
		}
		sb.WriteString("\n")
	}

	if count == 0 {
		return toolResult(id, "No tasks found.")
	}
	return toolResult(id, fmt.Sprintf("%d task(s):\n\n%s", count, sb.String()))
}

func (s *Server) toolTaskGet(id interface{}, args json.RawMessage) *Response {
	if s.taskDB == nil {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.ID == "" {
		return toolError(id, "id is required")
	}

	task, err := s.taskDB.Get(p.ID)
	if err != nil || task == nil {
		return toolError(id, "task not found")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", task.Name)
	fmt.Fprintf(&sb, "- **ID**: %s\n", task.ID)
	fmt.Fprintf(&sb, "- **Status**: %s\n", task.Status.String())
	fmt.Fprintf(&sb, "- **Project**: %s\n", task.Project)
	if task.Branch != "" {
		fmt.Fprintf(&sb, "- **Branch**: %s\n", task.Branch)
	}
	if task.Backend != "" {
		fmt.Fprintf(&sb, "- **Backend**: %s\n", task.Backend)
	}
	if task.PRURL != "" {
		fmt.Fprintf(&sb, "- **PR**: %s\n", task.PRURL)
	}
	if elapsed := task.ElapsedString(); elapsed != "" {
		fmt.Fprintf(&sb, "- **Elapsed**: %s\n", elapsed)
	}
	if task.Prompt != "" {
		fmt.Fprintf(&sb, "\n**Prompt**: %s\n", task.Prompt)
	}
	return toolResult(id, sb.String())
}

func (s *Server) toolTaskStop(id interface{}, args json.RawMessage) *Response {
	if s.taskStopper == nil || s.taskDB == nil {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.ID == "" {
		return toolError(id, "id is required")
	}

	task, err := s.taskDB.Get(p.ID)
	if err != nil || task == nil {
		return toolError(id, "task not found")
	}

	if task.Status != model.StatusInProgress {
		return toolError(id, fmt.Sprintf("task is not running (status: %s)", task.Status.String()))
	}

	if err := s.taskStopper.Stop(p.ID); err != nil {
		return toolError(id, fmt.Sprintf("Failed to stop task: %v", err))
	}

	return toolResult(id, fmt.Sprintf("Task %s stopped. Status: in_review.", task.Name))
}

// sanitizeTaskName generates a task name from a prompt (first 40 runes).
func sanitizeTaskName(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' {
			runes[i] = ' '
		}
	}
	return string(runes)
}

// --- helpers ---

func toolResult(id interface{}, text string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: ToolCallResult{
			Content: []Content{{Type: "text", Text: text}},
		},
	}
}

func toolError(id interface{}, text string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: ToolCallResult{
			Content: []Content{{Type: "text", Text: text}},
			IsError: true,
		},
	}
}

func errorResp(id interface{}, code int, msg string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

func writeError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(errorResp(id, code, msg)) //nolint:errcheck
}
