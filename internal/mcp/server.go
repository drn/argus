package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	Tasks() ([]*model.Task, error)
	Get(id string) (*model.Task, error)
}

// TaskStopper can stop a running agent session.
type TaskStopper interface {
	Stop(taskID string) error
}

// maxConcurrentCreates limits how many task_create calls can run concurrently
// to prevent unbounded process spawning from a misbehaving MCP client.
const maxConcurrentCreates = 5

// Server is the MCP HTTP server.
type Server struct {
	db          KBQuerier
	port        int
	vaultPath   string // Metis vault path for write-back to Obsidian
	httpSrv     *http.Server
	createTask  TaskCreator
	taskDB      TaskQuerier
	taskStopper TaskStopper
	createMu    sync.Mutex
	creating    int // number of in-flight task_create calls
}

// New creates a new MCP server.
func New(db KBQuerier, port int, vaultPath string) *Server {
	if vaultPath != "" {
		vaultPath = filepath.Clean(vaultPath)
	}
	return &Server{db: db, port: port, vaultPath: vaultPath}
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
				Name:    "argus",
				Version: "1.0.0",
			},
			Capabilities: Capabilities{
				Tools: &ToolsCapability{},
			},
			Instructions: kbInstructions,
		},
	}
}

// kbInstructions is sent to MCP clients during initialization to guide how
// agents interact with the knowledge base. Claude Code truncates this at ~2KB,
// so the most critical rules come first. Current size: ~1.8KB (~160 bytes headroom).
const kbInstructions = `Argus KB is an Obsidian-backed knowledge base indexed with FTS5. Documents are markdown files with YAML frontmatter, organized by topic in a flat folder hierarchy.

BEFORE WRITING: Always kb_search first to check if an entry already exists. Update existing documents rather than creating duplicates.

DOCUMENT SCHEMA — every document MUST have YAML frontmatter:
---
title: "Short Descriptive Title"
tags: [lowercase, kebab-case, terms]
---

The title and tags fields are required. Title should be concise (under 60 chars). Tags are a flat YAML array of lowercase kebab-case identifiers — use them for thematic clustering, not hierarchy. Hierarchy belongs in the folder path.

PATH CONVENTIONS:
- Vault-relative paths with topic folders: "thanx/data-investigation.md", "patterns/agent-frameworks.md"
- Kebab-case filenames, 2-3 words: "vendor-evaluations.md" not "list-of-all-vendor-and-tool-evaluations.md"
- File name = the topic noun, not a sentence: "hiring.md" not "how-we-hire.md"
- Group by domain (thanx/, tools/, patterns/, knowledge/) — match existing folders

CONTENT STRUCTURE:
- One topic per document. If covering multiple unrelated things, split into separate files.
- Lead with the key insight, rule, or summary — not background or preamble.
- Use ## H2 sections for subtopics. Each H2 should be independently useful.
- Bullet lists with **bold keys** for structured data (specs, criteria, evaluations).
- Cross-reference related docs with Obsidian wikilinks: [[filename]] or [[filename|display text]]
- Keep entries focused: 50-500 words is the sweet spot for retrieval quality.
- Source and date claims when possible: "— Source: website Apr 2026"

WHAT NOT TO DO:
- Don't create near-empty stubs — every entry should be immediately useful.
- Don't duplicate content across files. Cross-reference with [[wikilinks]] instead.
- Don't use inline #hashtags — put all tags in YAML frontmatter.
- Don't nest folders more than 2 levels deep.`

// toolDefs defines the four KB tools exposed via MCP.
var toolDefs = []Tool{
	{
		Name: "kb_search",
		Description: `Search the knowledge base using full-text search (BM25 ranking). Returns results with highlighted snippets. Use this BEFORE kb_ingest to check if a document already exists on the topic — update rather than duplicate.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Natural language search query. Supports stemming (e.g. 'running' matches 'run')."},
				"limit": map[string]interface{}{"type": "number", "description": "Maximum results to return (default 10, max 100)"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "kb_read",
		Description: `Read the full content of a knowledge base document by vault-relative path. Use after kb_search or kb_list to get the complete document including frontmatter, body, tags, and metadata.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Vault-relative path (e.g. 'thanx/hiring.md', 'patterns/agent-frameworks.md')"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name: "kb_list",
		Description: `List documents in the knowledge base, optionally filtered by path prefix. Use to discover what exists in a topic area before creating new entries.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prefix": map[string]interface{}{"type": "string", "description": "Path prefix to filter by (e.g. 'thanx/' for all Thanx docs, 'patterns/' for patterns)"},
				"limit":  map[string]interface{}{"type": "number", "description": "Maximum documents to return (default 100)"},
			},
		},
	},
	{
		Name: "kb_ingest",
		// Description intentionally duplicates key rules from kbInstructions —
		// not all MCP clients surface server instructions at tool-call time.
		Description: `Add or update a document in the knowledge base. The document is indexed for search and written back to the Obsidian vault.

IMPORTANT: Always kb_search first to avoid duplicates. If a document exists on the topic, kb_read it and update rather than creating a new one.

REQUIRED FORMAT: Full markdown with YAML frontmatter. Every document must have:
---
title: "Descriptive Title"
tags: [lowercase-tag, another-tag]
---

Content body here with ## sections.

PATH RULES: Use kebab-case filenames in topic folders (e.g. 'thanx/hiring.md', 'tools/vendor-evaluations.md'). Match existing folder structure — use kb_list to see current organization.

CONTENT RULES: One topic per document. Lead with the key insight. Use ## H2 for subtopics. Bold key terms in bullet lists. Cross-reference with [[wikilinks]]. Keep entries 50-500 words.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":    map[string]interface{}{"type": "string", "description": "Vault-relative path (e.g. 'thanx/data-investigation.md'). Kebab-case, 2-3 word filenames, organized in topic folders."},
				"content": map[string]interface{}{"type": "string", "description": "Full markdown with YAML frontmatter (title and tags required). See tool description for format."},
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

// taskMgmtEnabled returns true when all task management dependencies are wired.
func (s *Server) taskMgmtEnabled() bool {
	return s.createTask != nil && s.taskDB != nil && s.taskStopper != nil
}

func (s *Server) handleToolsList(req *Request) *Response {
	// Copy to avoid mutating the package-level toolDefs slice via append.
	tools := make([]Tool, len(toolDefs))
	copy(tools, toolDefs)
	if s.taskMgmtEnabled() {
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

	// Canonicalize and validate the path: must be vault-relative, no escaping.
	cleanPath := filepath.Clean(p.Path)
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
		return toolError(id, "invalid path: must be vault-relative with no '..' components")
	}
	// After Clean, verify the resolved path stays within the vault.
	if s.vaultPath != "" {
		absPath := filepath.Join(s.vaultPath, cleanPath)
		if !strings.HasPrefix(absPath, s.vaultPath+string(filepath.Separator)) && absPath != s.vaultPath {
			return toolError(id, "invalid path: escapes vault directory")
		}
	}

	doc := kb.ParseDocument(cleanPath, p.Content)
	doc.IngestedAt = time.Now()
	doc.ModifiedAt = time.Now()
	if err := s.db.KBUpsert(&doc); err != nil {
		return toolError(id, fmt.Sprintf("Ingest failed: %v", err))
	}

	// Write back to Obsidian vault so the file appears in the vault.
	if s.vaultPath != "" {
		absPath := filepath.Join(s.vaultPath, cleanPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			log.Printf("[mcp] vault write-back mkdir failed: %v", err)
		} else {
			content := kb.RenderMarkdown(&doc)
			// Atomic write: temp file + rename.
			dir := filepath.Dir(absPath)
			tmp, err := os.CreateTemp(dir, ".kb-ingest-*.tmp")
			if err != nil {
				log.Printf("[mcp] vault write-back tempfile failed: %v", err)
			} else {
				tmpName := tmp.Name()
				_, writeErr := tmp.WriteString(content)
				tmp.Close() //nolint:errcheck
				if writeErr != nil {
					log.Printf("[mcp] vault write-back write failed: %v", writeErr)
					os.Remove(tmpName) //nolint:errcheck
				} else if err := os.Rename(tmpName, absPath); err != nil {
					log.Printf("[mcp] vault write-back rename failed: %v", err)
					os.Remove(tmpName) //nolint:errcheck
				}
			}
		}
	}

	return toolResult(id, fmt.Sprintf("Ingested %s (%d words)", p.Path, doc.WordCount))
}

// --- task tools ---

func (s *Server) toolTaskCreate(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
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

	// Rate-limit concurrent task creation to prevent unbounded process spawning.
	s.createMu.Lock()
	if s.creating >= maxConcurrentCreates {
		s.createMu.Unlock()
		log.Printf("[mcp] task_create rejected: %d concurrent creates in flight", s.creating)
		return toolError(id, fmt.Sprintf("too many concurrent task creations (max %d)", maxConcurrentCreates))
	}
	s.creating++
	s.createMu.Unlock()
	defer func() {
		s.createMu.Lock()
		s.creating--
		s.createMu.Unlock()
	}()

	name := p.Name
	if name == "" {
		name = truncatePromptToName(p.Prompt)
	}

	log.Printf("[mcp] task_create name=%q project=%q", name, p.Project)
	task, err := s.createTask(name, p.Prompt, p.Project, "")
	if err != nil {
		log.Printf("[mcp] task_create failed: %v", err)
		return toolError(id, fmt.Sprintf("Failed to create task: %v", err))
	}

	log.Printf("[mcp] task_create ok: id=%s name=%s", task.ID, task.Name)
	return toolResult(id, fmt.Sprintf("Task created.\n\n- **ID**: %s\n- **Name**: %s\n- **Status**: %s\n- **Project**: %s\n- **Branch**: %s",
		task.ID, task.Name, task.Status.String(), task.Project, task.Branch))
}

func (s *Server) toolTaskList(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		Status  string `json:"status"`
		Project string `json:"project"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	tasks, err := s.taskDB.Tasks()
	if err != nil {
		return toolError(id, fmt.Sprintf("Failed to list tasks: %v", err))
	}
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
	if !s.taskMgmtEnabled() {
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
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.ID == "" {
		return toolError(id, "id is required")
	}

	// Skip the TOCTOU-prone status pre-check — let the stopper determine
	// whether the session is actually running. ErrSessionNotFound means
	// the agent already exited (or was never started).
	log.Printf("[mcp] task_stop id=%s", p.ID)
	if err := s.taskStopper.Stop(p.ID); err != nil {
		log.Printf("[mcp] task_stop failed: id=%s err=%v", p.ID, err)
		return toolError(id, fmt.Sprintf("Failed to stop task: %v", err))
	}

	return toolResult(id, fmt.Sprintf("Stop signal sent for task %s. It will transition to in_review when the agent exits.", p.ID))
}

// truncatePromptToName generates a task name from a prompt (first 40 runes).
// This is display-name truncation only — git branch sanitization happens in
// agent.CreateWorktree via sanitizeBranchName.
func truncatePromptToName(prompt string) string {
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
