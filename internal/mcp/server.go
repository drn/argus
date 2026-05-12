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
	KBDelete(path string) error
	KBDocumentCount() int
}

// TaskCreateInput is the payload the MCP server hands to the daemon-injected
// task creator. Adding orchestration fields (BaseBranch, DependsOn, …) here
// extends what the MCP `task_create` tool can pass through without churning
// any function signatures.
type TaskCreateInput struct {
	Name     string
	Prompt   string
	Project  string
	AutoName bool

	// BaseBranch overrides the project default start point for the worktree.
	// Empty string falls back to projCfg.Branch (today: master/main). Used
	// for stacked-PR workflows where each sub-task branches off the previous
	// task's branch.
	BaseBranch string

	// DependsOn is the list of task IDs whose status must reach `complete`
	// before this task's agent session is auto-started. Empty / nil starts
	// the session immediately (legacy behaviour).
	DependsOn []string

	// PlanSlug is the orchestrator-supplied grouping label so the DAG view
	// can scope a stack without traversing depends_on reachability. Opaque
	// to the daemon — same contract as Result.
	PlanSlug string
}

// TaskCreator creates a task with worktree and starts an agent session.
// Same call shape used by daemon.HeadlessCreateTask (the daemon wraps it to
// avoid an import cycle on the mcp package).
type TaskCreator func(input TaskCreateInput) (*model.Task, error)

// TaskStore provides read and write access to tasks.
type TaskStore interface {
	Tasks() ([]*model.Task, error)
	Get(id string) (*model.Task, error)
	Update(t *model.Task) error
	// Rename updates only the name column — used by task_rename to avoid
	// racing with concurrent status changes from the agent process.
	Rename(id, name string) error
	// SetResult updates only the result column. Used by task_set_result so
	// a concurrent agent status flip (StatusComplete on exit) can't be
	// clobbered by an Update-based read-then-write round trip.
	SetResult(id, result string) error
	// FindByNameProject returns the first non-archived task matching the
	// (name, project) pair, or (nil, nil) when no such task exists. Used by
	// the task_create idempotency check inside the createMu critical
	// section so a duplicate detection doesn't need to scan every task in
	// memory each call. Backed by an indexed SQL query in db.DB.
	FindByNameProject(name, project string) (*model.Task, error)
	// SetDependsOn, SetPlanSlug, and SetArchived are partial-column writes
	// used by orch.Link / Unlink / SetPlanSlug / HaltDownstream to avoid
	// clobbering a concurrent status flip via a stale full-row Update.
	// Same pattern as SetResult / Rename. *db.DB satisfies all three.
	SetDependsOn(id string, deps []string) error
	SetPlanSlug(id, slug string) error
	SetArchived(id string, archived bool) error
}

// TaskStopper can stop a running agent session.
type TaskStopper interface {
	Stop(taskID string) error
}

// ClipboardSetter stages text in the agent-staged clipboard for the given
// task. Used by the argus_clipboard_set MCP tool. Defined as an interface so
// the mcp package doesn't depend on the clipboard package directly.
type ClipboardSetter interface {
	Set(taskID, text string) error
}

// ScheduleStore provides read+write access to scheduled tasks. The signature
// matches *db.DB so the daemon passes its DB handle directly.
type ScheduleStore interface {
	Schedules() ([]*model.ScheduledTask, error)
	GetSchedule(id string) (*model.ScheduledTask, error)
	AddSchedule(s *model.ScheduledTask) error
	UpdateSchedule(s *model.ScheduledTask) error
	DeleteSchedule(id string) error
}

// ScheduleRunner fires a schedule out-of-cycle. Subset of *scheduler.Scheduler.
type ScheduleRunner interface {
	RunNow(id string) (*model.Task, error)
}

// maxConcurrentCreates limits how many task_create calls can run concurrently
// to prevent unbounded process spawning from a misbehaving MCP client.
const maxConcurrentCreates = 5

// maxTaskNameRunes caps task display names. SQLite TEXT is unbounded and
// the request body limit (4 MiB) would let pathologically long names through;
// 200 runes comfortably covers any human-typeable name across every UI.
const maxTaskNameRunes = 200

// maxDependsOnEntries caps how many upstream task IDs a single task_create
// may declare. A misbehaving orchestrator submitting tens of thousands of
// IDs would force one DB Get per entry at validation time and again on
// every task_get for the duplicated unresolvedDeps loop. 100 is two orders
// of magnitude above any reasonable stacked-PR plan.
const maxDependsOnEntries = 100

// maxDepGraphTraversal bounds the DFS used to detect cycles in the deps
// graph at task_create time. A real DAG visits each task at most once;
// the cap exists to bound a misbehaving DB (impossible cycles in already-
// persisted rows) so the validation can't hang the create path.
const maxDepGraphTraversal = 10000

// Server is the MCP HTTP server.
type Server struct {
	db          KBQuerier
	port        int
	vaultPath   string // Metis vault path for write-back to Obsidian
	httpSrv     *http.Server
	createTask  TaskCreator
	taskDB      TaskStore
	taskStopper TaskStopper
	clipboard   ClipboardSetter // optional; set via SetClipboard
	schedDB     ScheduleStore   // optional; set via SetScheduleManager
	schedRunner ScheduleRunner  // optional; set via SetScheduleManager
	createMu    sync.Mutex
	creating    int // number of in-flight task_create calls
	// creatingKeys tracks (project, name) pairs currently being created so
	// two concurrent task_create calls with the same key can't both pass
	// the duplicate-row check and race to insert. Guarded by createMu.
	creatingKeys map[string]bool

	// shutdownCtx is canceled by Shutdown so long-lived GET/SSE handlers
	// can return promptly instead of blocking httpSrv.Shutdown forever.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// New creates a new MCP server.
func New(db KBQuerier, port int, vaultPath string) *Server {
	if vaultPath != "" {
		vaultPath = filepath.Clean(vaultPath)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		db:             db,
		port:           port,
		vaultPath:      vaultPath,
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		creatingKeys:   make(map[string]bool),
	}
}

// SetTaskManager wires in task management capabilities.
// When set, the server exposes task_create, task_list, task_get, task_stop,
// and task_archive tools.
func (s *Server) SetTaskManager(creator TaskCreator, taskDB TaskStore, stopper TaskStopper) {
	s.createTask = creator
	s.taskDB = taskDB
	s.taskStopper = stopper
}

// SetClipboard wires the agent-staged clipboard. When set (and SetTaskManager
// has also been called so cwd-resolution works), the server exposes the
// argus_clipboard_set tool.
func (s *Server) SetClipboard(setter ClipboardSetter) {
	s.clipboard = setter
}

// SetScheduleManager wires schedule CRUD + run-now capabilities. When set, the
// server exposes schedule_list, schedule_create, schedule_update,
// schedule_delete, and schedule_run_now tools. Must be called before
// ListenAndServe (Set* fields are read at request time without a mutex).
func (s *Server) SetScheduleManager(store ScheduleStore, runner ScheduleRunner) {
	s.schedDB = store
	s.schedRunner = runner
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

// Shutdown gracefully stops the HTTP server. Cancels the server-wide context
// first so any active GET/SSE handlers unblock and return — otherwise
// httpSrv.Shutdown waits indefinitely for in-flight handlers to finish.
// shutdownCancel is always set in New(), so no nil guard is needed.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownCancel()
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// sseKeepaliveInterval is how often the GET/SSE handler emits comment-only
// keepalive frames. Short enough that idle proxies/intermediaries don't drop
// the connection, long enough not to be wasteful on a single-user local
// daemon. var (not const) so tests can shrink it to verify the streaming
// loop is alive without 30 s of dead time.
var sseKeepaliveInterval = 30 * time.Second

// maxRequestBodyBytes caps POST /mcp request size to bound memory use under
// a misbehaving client. 4 MiB is generous for JSON-RPC; tool arguments are
// typically a few KB.
const maxRequestBodyBytes = 4 * 1024 * 1024

// ServeHTTP routes incoming requests on the single MCP endpoint per the
// Streamable HTTP transport spec: POST carries client-to-server JSON-RPC,
// GET is a long-lived SSE channel for server-initiated messages, DELETE
// terminates a session (no-op here — Argus does not track sessions).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGET(w, r)
	case http.MethodPost:
		s.handlePOST(w, r)
	case http.MethodDelete:
		// No session state to release; acknowledge so clients that send
		// DELETE on transport close don't see an error.
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGET holds open the server-to-client SSE stream required by the MCP
// Streamable HTTP transport. Codex `rmcp` (and similar clients) open this
// stream right after `initialize` and treat early closure as a fatal
// "transport channel closed" error. Argus does not currently emit
// server-initiated messages, so the handler just blocks on the request
// context (client disconnect) or s.shutdownCtx (server shutdown), emitting
// SSE comment frames every sseKeepaliveInterval to defeat idle-connection
// timeouts in any intermediaries.
func (s *Server) handleGET(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()

	reqCtx := r.Context()
	for {
		select {
		case <-reqCtx.Done():
			return
		case <-s.shutdownCtx.Done():
			return
		case <-ticker.C:
			// Silent return on write error: the only meaningful failure
			// here is the client disconnecting mid-frame, which is not
			// actionable and would only spam logs. Matches the SSE write
			// pattern in internal/api.
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handlePOST processes JSON-RPC requests and notifications. Per the
// Streamable HTTP spec, requests (with `id`) get a JSON response; pure
// notifications (no `id`) get HTTP 202 Accepted with an empty body — not a
// JSON-RPC response with a null id, which is malformed and trips strict
// clients like Codex rmcp.
func (s *Server) handlePOST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Probe for the presence of an "id" field before structured unmarshal —
	// json.RawMessage on an absent field is nil, but that collides with
	// `"id": null`, so use a generic map to distinguish the two cases.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		writeError(w, nil, -32700, "parse error")
		return
	}
	_, hasID := probe["id"]

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, -32700, "parse error")
		return
	}

	if !hasID {
		// Notification: dispatch for any side effect; the returned
		// Response (always Result: nil for the only notification we
		// recognize, notifications/initialized) is intentionally
		// discarded — the wire reply is 202 Accepted with empty body.
		_ = s.dispatch(&req)
		w.WriteHeader(http.StatusAccepted)
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
		// No-op. Return value is discarded by handlePOST on the
		// notification path (req.ID is nil), so this Response is only
		// ever observed if a buggy client sends an id with this method.
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

// toolDefs defines the KB tools exposed via MCP.
var toolDefs = []Tool{
	{
		Name:        "kb_search",
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
		Name:        "kb_read",
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
		Name:        "kb_list",
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
		Name:        "kb_delete",
		Description: `Delete a document from the knowledge base by vault-relative path. Also removes the file from the Obsidian vault if it exists. Use kb_search or kb_list first to confirm the path.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Vault-relative path of the document to delete (e.g. 'thanx/hiring.md')"},
			},
			"required": []string{"path"},
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
		Name: "task_create",
		Description: `Create a new Argus task with a git worktree and start an agent session. Returns task ID, name, and status.

Orchestration: pass ` + "`base_branch`" + ` to branch off a non-default ref (stacked-PR workflows), and ` + "`depends_on`" + ` to declare a list of upstream task IDs that must reach status=complete before this task's agent session is auto-started. While blocked, the task stays in ` + "`pending`" + ` with its worktree already prepared; the depswatcher inside the daemon starts the session within one tick of every dep completing.

Idempotency: when ` + "`name`" + ` is supplied (not auto-generated from prompt) and a non-archived task with the same (name, project) already exists, the call errors unless ` + "`upsert: true`" + ` is set — in which case the existing task is returned unchanged. This keeps an orchestrator that restarts mid-loop from double-firing sub-tasks.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Task name (used for branch/worktree naming). Auto-generated from prompt if omitted; idempotency check is skipped for auto-generated names."},
				"prompt":      map[string]interface{}{"type": "string", "description": "Instructions for the agent"},
				"project":     map[string]interface{}{"type": "string", "description": "Project name (must exist in Argus config)"},
				"base_branch": map[string]interface{}{"type": "string", "description": "Optional. Start point for the new worktree's branch (e.g. 'argus/parent-task'). Resolves to origin/<ref> / upstream/<ref> if no local match. Empty = project default (master/main)."},
				"depends_on":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional. Upstream task IDs whose status must reach 'complete' before this task's agent starts. The worktree is still created immediately."},
				"plan_slug":   map[string]interface{}{"type": "string", "description": "Optional. Orchestrator grouping label for the DAG view; opaque to the daemon. Tasks sharing the same plan_slug render as one stack."},
				"upsert":      map[string]interface{}{"type": "boolean", "description": "Optional. If true and a non-archived task with the same (name, project) exists, return that task instead of erroring."},
			},
			"required": []string{"prompt", "project"},
		},
	},
	{
		Name:        "task_list",
		Description: "List Argus tasks, optionally filtered by status, project, and/or plan_slug. Returned rows include the plan_slug column for DAG-view scoping.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status":    map[string]interface{}{"type": "string", "description": "Filter by status: pending, in_progress, in_review, complete"},
				"project":   map[string]interface{}{"type": "string", "description": "Filter by project name"},
				"plan_slug": map[string]interface{}{"type": "string", "description": "Filter by orchestrator stack label (set on each sub-task via task_create / task_set_plan_slug)"},
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
	{
		Name: "task_archive",
		Description: `Archive or unarchive an Argus task. Archived tasks move to the Archive section of the task list.

The agent process does not know its own task ID, so the task is resolved from the working directory: pass ` + "`cwd`" + ` and Argus finds the task whose worktree matches. If ` + "`archived`" + ` is omitted, the current archive state is toggled.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":       map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd":      map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
				"archived": map[string]interface{}{"type": "boolean", "description": "Explicit archived state. If omitted, the current value is toggled."},
			},
		},
	},
	{
		Name: "task_rename",
		Description: `Rename an Argus task. Updates only the display name — branch and worktree paths stay locked to the original slug.

Pass ` + "`id`" + ` to target a task explicitly, or ` + "`cwd`" + ` to resolve the task from a working directory inside its worktree (the agent process does not know its own task ID). ` + "`name`" + ` is required.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":   map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd":  map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
				"name": map[string]interface{}{"type": "string", "description": "New display name. Whitespace-trimmed; must be non-empty."},
			},
			"required": []string{"name"},
		},
	},
	{
		Name: "task_complete",
		Description: `Mark an Argus task as complete. Sets status to "complete" and stamps EndedAt.

The agent process does not know its own task ID, so the task is resolved from the working directory: pass ` + "`cwd`" + ` and Argus finds the task whose worktree matches. Does NOT stop a running agent session — call ` + "`task_stop`" + ` first if needed. No-op when the task is already complete.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":  map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd": map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
			},
		},
	},
	{
		Name: "task_set_result",
		Description: `Persist a structured JSON result blob for this task so the orchestrator can read what the agent produced (PR URL, branch SHA, milestone label, success/failure). The daemon does NOT inspect the payload — schema is the orchestrator's contract with sub-tasks.

The agent process must identify itself by passing either ` + "`id`" + ` (sub-tasks should use the ` + "`ARGUS_TASK_ID`" + ` env var exported into every worktree) or ` + "`cwd`" + ` (Argus resolves to the task whose worktree the cwd lives under, longest-prefix wins). At least one is required. Last write wins. Payload capped at 64 KiB.

Conventional shape for stacked-PR flows:
` + "```json\n{\n  \"pr_url\": \"https://github.com/org/repo/pull/123\",\n  \"pr_number\": 123,\n  \"branch_sha\": \"abc1234\",\n  \"milestone\": \"M3\"\n}\n```" + `

Failure convention: write ` + "`{\"failed\": true, \"reason\": \"...\"}`" + ` before calling ` + "`task_complete`" + ` so the orchestrator can intervene. depswatcher does NOT auto-cascade failures — it only starts dependents whose deps are all status=complete, so a failed-but-complete dep WILL unblock its children on the next watcher tick (up to ~1 minute later). The orchestrator interprets ` + "`failed`" + ` and stops the rest of the stack: use ` + "`task_stop`" + ` for any downstream task that has already started (depswatcher beat the orchestrator to it) and ` + "`task_archive`" + ` for any downstream task still in ` + "`pending`" + ` (no live session yet — ` + "`task_stop`" + ` would error with session-not-found).`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":     map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd":    map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
				"result": map[string]interface{}{"type": "object", "description": "Structured result. JSON object, opaque to the daemon. Up to 64 KiB serialized."},
			},
			"required": []string{"result"},
		},
	},
}

// maxTaskResultBytes caps the serialized result payload. 64 KiB is plenty
// for a structured stacked-PR record (PR URL, SHA, milestone, optional
// failure reason) while keeping a misbehaving agent from filling the
// tasks table with multi-megabyte blobs. SQLite TEXT itself is unbounded.
const maxTaskResultBytes = 64 * 1024

// clipboardToolDefs are exposed only when SetClipboard has been called AND
// task management is enabled (the tool needs cwd-resolution to find the
// caller's task ID).
var clipboardToolDefs = []Tool{
	{
		Name: "argus_clipboard_set",
		Description: `Stage text for the user to copy with one tap (PWA Copy button) or one keypress (TUI ctrl+y). Use when you have produced output the user will likely want to paste — code snippets, generated text, commands, URLs.

The agent process does not know its own task ID, so the task is resolved from the working directory: pass ` + "`cwd`" + ` (or omit it and Argus uses the agent's PWD when available). Last-write-wins: a second call replaces the first. Payload expires after 5 minutes if not copied. Maximum text size is 1 MiB.

This does NOT write directly to the OS clipboard — it stages text the user can then copy with a single user gesture. iOS Safari requires a user tap for clipboard writes; this tool is the workaround.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{"type": "string", "description": "Text to stage for the user to copy. Up to 1 MiB."},
				"id":   map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd":  map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
			},
			"required": []string{"text"},
		},
	},
}

// taskMgmtEnabled returns true when all task management dependencies are wired.
func (s *Server) taskMgmtEnabled() bool {
	return s.createTask != nil && s.taskDB != nil && s.taskStopper != nil
}

// clipboardEnabled returns true when both the clipboard setter and task
// management are wired (cwd resolution requires task management).
func (s *Server) clipboardEnabled() bool {
	return s.clipboard != nil && s.taskMgmtEnabled()
}

// scheduleMgmtEnabled returns true when both schedule store and runner are wired.
func (s *Server) scheduleMgmtEnabled() bool {
	return s.schedDB != nil && s.schedRunner != nil
}

// scheduleToolDefs are exposed only when SetScheduleManager has been called.
var scheduleToolDefs = []Tool{
	{
		Name:        "schedule_list",
		Description: `List recurring scheduled tasks. Each row fires a fresh Argus task at its cron expression. Returns name, project, schedule, enabled, next_run_at, last_run_at, and last_error if present.`,
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "schedule_create",
		Description: `Create a scheduled task. Pass either ` + "`schedule`" + ` (cron expression for recurring runs) OR ` + "`run_once_at`" + ` (RFC3339 UTC timestamp for a single future run) — exactly one. The cron expression is parsed by robfig/cron/v3 ParseStandard: 5-field cron (e.g. "0 9 * * 1-5" for 9am weekdays UTC), descriptors (@hourly, @daily, @weekly, @monthly, @yearly), or @every <duration> (e.g. "@every 1h"). Minimum cron resolution is one minute. One-shot rows fire once at run_once_at then auto-disable (the row stays in the list with enabled=false for inspection). Project must match an existing Argus project. New schedules default to enabled.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Display name. Each fire suffixes this with the UTC timestamp."},
				"project":     map[string]interface{}{"type": "string", "description": "Project name (must exist in Argus config)."},
				"prompt":      map[string]interface{}{"type": "string", "description": "Instructions delivered to the agent at each fire."},
				"schedule":    map[string]interface{}{"type": "string", "description": "Cron expression. Mutually exclusive with run_once_at."},
				"run_once_at": map[string]interface{}{"type": "string", "description": "RFC3339 UTC timestamp (e.g. \"2026-05-17T14:00:00Z\"). Must be in the future. Mutually exclusive with schedule."},
				"backend":     map[string]interface{}{"type": "string", "description": "Optional backend override for this schedule (e.g. 'claude-haiku')."},
				"enabled":     map[string]interface{}{"type": "boolean", "description": "Optional. Defaults to true."},
			},
			"required": []string{"name", "project", "prompt"},
		},
	},
	{
		Name:        "schedule_update",
		Description: `Partial update of a scheduled task. Only fields you pass are changed; omit a field to leave it as-is. Changing the cadence (schedule or run_once_at) recomputes next_run_at. To convert between recurring and one-shot, set the new field — the other clears automatically. Passing both schedule and run_once_at non-empty in the same call is rejected with an error.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":          map[string]interface{}{"type": "string", "description": "Schedule ID (from schedule_list)."},
				"name":        map[string]interface{}{"type": "string", "description": "Display name. Each fire suffixes this with the UTC timestamp."},
				"project":     map[string]interface{}{"type": "string", "description": "Project name (must exist in Argus config)."},
				"prompt":      map[string]interface{}{"type": "string", "description": "Instructions delivered to the agent at each fire."},
				"schedule":    map[string]interface{}{"type": "string", "description": "Cron expression. Pass empty string to clear when switching to a one-shot."},
				"run_once_at": map[string]interface{}{"type": "string", "description": "RFC3339 UTC timestamp. Pass empty string to clear when switching to a recurring schedule."},
				"backend":     map[string]interface{}{"type": "string", "description": "Backend override (e.g. 'claude-haiku'). Empty string clears the override."},
				"enabled":     map[string]interface{}{"type": "boolean", "description": "Toggle on/off without resending the prompt. Re-enabling a one-shot whose RunOnceAt is in the past does NOT cause it to fire again — LastRunAt is the definitive guard."},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "schedule_delete",
		Description: `Delete a scheduled task. The row is removed; in-flight task instances already created by previous fires are unaffected.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Schedule ID."},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "schedule_run_now",
		Description: `Fire a schedule immediately, out of cycle. Creates a fresh task with the schedule's prompt and project. Bookkeeping is updated so the next regular tick will not double-fire. Note: run-now does NOT send the cron-tick push notification — use it as an explicit user action only.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Schedule ID."},
			},
			"required": []string{"id"},
		},
	},
}

func (s *Server) handleToolsList(req *Request) *Response {
	// Copy to avoid mutating the package-level toolDefs slice via append.
	tools := make([]Tool, len(toolDefs))
	copy(tools, toolDefs)
	if s.taskMgmtEnabled() {
		tools = append(tools, taskToolDefs...)
		tools = append(tools, linkingToolDefs...)
	}
	if s.clipboardEnabled() {
		tools = append(tools, clipboardToolDefs...)
	}
	if s.scheduleMgmtEnabled() {
		tools = append(tools, scheduleToolDefs...)
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
	case "kb_delete":
		return s.toolKBDelete(req.ID, params.Arguments)
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
	case "task_archive":
		return s.toolTaskArchive(req.ID, params.Arguments)
	case "task_rename":
		return s.toolTaskRename(req.ID, params.Arguments)
	case "task_complete":
		return s.toolTaskComplete(req.ID, params.Arguments)
	case "task_set_result":
		return s.toolTaskSetResult(req.ID, params.Arguments)
	case "task_link":
		return s.toolTaskLink(req.ID, params.Arguments)
	case "task_unlink":
		return s.toolTaskUnlink(req.ID, params.Arguments)
	case "task_deps":
		return s.toolTaskDeps(req.ID, params.Arguments)
	case "task_halt_downstream":
		return s.toolTaskHaltDownstream(req.ID, params.Arguments)
	case "task_set_plan_slug":
		return s.toolTaskSetPlanSlug(req.ID, params.Arguments)
	case "argus_clipboard_set":
		return s.toolClipboardSet(req.ID, params.Arguments)
	case "schedule_list":
		return s.toolScheduleList(req.ID, params.Arguments)
	case "schedule_create":
		return s.toolScheduleCreate(req.ID, params.Arguments)
	case "schedule_update":
		return s.toolScheduleUpdate(req.ID, params.Arguments)
	case "schedule_delete":
		return s.toolScheduleDelete(req.ID, params.Arguments)
	case "schedule_run_now":
		return s.toolScheduleRunNow(req.ID, params.Arguments)
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

func (s *Server) toolKBDelete(id interface{}, args json.RawMessage) *Response {
	var p struct {
		Path string `json:"path"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Path == "" {
		return toolError(id, "path is required")
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

	if err := s.db.KBDelete(cleanPath); err != nil {
		log.Printf("[mcp] kb_delete failed: path=%s err=%v", cleanPath, err)
		return toolError(id, fmt.Sprintf("Delete failed: %v", err))
	}

	// Remove from Obsidian vault if configured.
	if s.vaultPath != "" {
		absPath := filepath.Join(s.vaultPath, cleanPath)
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			log.Printf("[mcp] vault delete failed for %s: %v", cleanPath, err)
			return toolResult(id, fmt.Sprintf("Deleted %s from index (warning: vault file removal failed — re-index may restore it)", cleanPath))
		}
	}

	log.Printf("[mcp] kb_delete ok: path=%s", cleanPath)
	return toolResult(id, fmt.Sprintf("Deleted %s", cleanPath))
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
		Name       string   `json:"name"`
		Prompt     string   `json:"prompt"`
		Project    string   `json:"project"`
		BaseBranch string   `json:"base_branch"`
		DependsOn  []string `json:"depends_on"`
		PlanSlug   string   `json:"plan_slug"`
		Upsert     bool     `json:"upsert"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Project == "" {
		return toolError(id, "project is required")
	}
	if p.Prompt == "" {
		return toolError(id, "prompt is required")
	}

	// Cap depends_on at create time. The 4 MiB request body limit would
	// otherwise let an array of tens of thousands of IDs through, and the
	// per-tick unresolvedDeps loop in task_get would then linear-scan that
	// list on every read. 100 is well above any realistic stacked-PR plan.
	if len(p.DependsOn) > maxDependsOnEntries {
		return toolError(id, fmt.Sprintf("depends_on exceeds %d entries (got %d)", maxDependsOnEntries, len(p.DependsOn)))
	}

	// Validate depends_on entries reference real, non-archived tasks before
	// we spawn a worktree. An archived dep that never reached complete
	// would block the dependent permanently; rejecting at create time gives
	// the orchestrator a clear error instead of a mystery hang.
	for _, depID := range p.DependsOn {
		if strings.TrimSpace(depID) == "" {
			return toolError(id, "depends_on contains an empty task ID")
		}
		dep, err := s.taskDB.Get(depID)
		if err != nil || dep == nil {
			return toolError(id, fmt.Sprintf("depends_on references unknown task: %s", depID))
		}
		if dep.Archived {
			return toolError(id, fmt.Sprintf("depends_on references archived task: %s (%s)", depID, dep.Name))
		}
	}

	// Cycle check: DFS the transitive dep graph from each direct dep. The
	// new task has no ID yet, so only a cycle in the persisted rows can be
	// reachable. Defensive: cycles shouldn't exist if every prior create
	// passed this check, but a direct DB write or future task-update path
	// could introduce one. We refuse the create on EITHER a found cycle
	// OR an unverifiable graph (more nodes than maxDepGraphTraversal can
	// walk), so a pathological graph cannot bypass validation by being
	// large enough to exhaust the visit budget.
	cyclePath, cycleErr := s.detectCycle(p.DependsOn)
	if cycleErr != nil {
		return toolError(id, fmt.Sprintf("depends_on validation: %v", cycleErr))
	}
	if cyclePath != nil {
		return toolError(id, fmt.Sprintf("depends_on would form a cycle: %s", strings.Join(cyclePath, " -> ")))
	}

	autoName := p.Name == ""
	name := p.Name
	if name == "" {
		name = truncatePromptToName(p.Prompt)
	}

	// Atomic rate-limit + idempotency check. Both happen inside one
	// critical section so two concurrent task_create calls with the same
	// (name, project) cannot both pass. The (project, name) key is held
	// in creatingKeys for the duration of the slow create path; a duplicate
	// concurrent call sees the in-flight key and errors instead of racing.
	// Length-prefix the two components so a project/name containing the
	// separator byte cannot collide with a different (project, name) pair.
	// SQLite TEXT rejects NUL bytes so the practical collision space is
	// already zero, but the length-prefix encoding removes the assumption
	// from the call site.
	idempotencyKey := fmt.Sprintf("%d:%s|%d:%s", len(p.Project), p.Project, len(name), name)
	s.createMu.Lock()
	if s.creating >= maxConcurrentCreates {
		s.createMu.Unlock()
		log.Printf("[mcp] task_create rejected: %d concurrent creates in flight", s.creating)
		return toolError(id, fmt.Sprintf("too many concurrent task creations (max %d)", maxConcurrentCreates))
	}
	// Idempotency by (name, project): an orchestrator that restarts mid-loop
	// must not double-fire sub-tasks. Only user-typed names are gated —
	// auto-generated names from prompts already disambiguate via content.
	// Upsert=true short-circuits the duplicate check by returning the
	// existing row; otherwise we fail loud so the caller can decide.
	if !autoName {
		if s.creatingKeys[idempotencyKey] {
			s.createMu.Unlock()
			return toolError(id, fmt.Sprintf("task with name=%q project=%q is already being created (concurrent task_create)", name, p.Project))
		}
		existing, lookupErr := s.lookupExistingTaskLocked(name, p.Project)
		if lookupErr != nil {
			s.createMu.Unlock()
			return toolError(id, fmt.Sprintf("lookup existing task: %v", lookupErr))
		}
		if existing != nil {
			s.createMu.Unlock()
			if p.Upsert {
				log.Printf("[mcp] task_create upsert hit: id=%s name=%q project=%q", existing.ID, name, p.Project)
				return toolResult(id, formatTaskCreatedSummary(existing, "Task already exists (upsert)"))
			}
			return toolError(id, fmt.Sprintf("task already exists with name=%q project=%q id=%s (pass upsert:true to reuse)", name, p.Project, existing.ID))
		}
		s.creatingKeys[idempotencyKey] = true
	}
	s.creating++
	s.createMu.Unlock()
	defer func() {
		s.createMu.Lock()
		s.creating--
		if !autoName {
			delete(s.creatingKeys, idempotencyKey)
		}
		s.createMu.Unlock()
	}()

	log.Printf("[mcp] task_create name=%q project=%q auto=%v base=%q deps=%v", name, p.Project, autoName, p.BaseBranch, p.DependsOn)
	task, err := s.createTask(TaskCreateInput{
		Name:       name,
		Prompt:     p.Prompt,
		Project:    p.Project,
		AutoName:   autoName,
		BaseBranch: strings.TrimSpace(p.BaseBranch),
		DependsOn:  p.DependsOn,
		PlanSlug:   strings.TrimSpace(p.PlanSlug),
	})
	if err != nil {
		log.Printf("[mcp] task_create failed: %v", err)
		return toolError(id, fmt.Sprintf("Failed to create task: %v", err))
	}

	log.Printf("[mcp] task_create ok: id=%s name=%s status=%s deps=%v", task.ID, task.Name, task.Status, task.DependsOn)
	return toolResult(id, formatTaskCreatedSummary(task, "Task created"))
}

// lookupExistingTaskLocked returns the first non-archived task with the
// given (name, project), or (nil, nil) when no such task exists. MUST be
// called with s.createMu held — the lock guarantees no concurrent
// task_create can insert a colliding row between this read and the
// caller's slot reservation in creatingKeys. Delegates to the store's
// FindByNameProject (indexed SQL query in db.DB) rather than scanning
// every row in memory.
func (s *Server) lookupExistingTaskLocked(name, project string) (*model.Task, error) {
	return s.taskDB.FindByNameProject(name, project)
}

// detectCycle DFS-walks the transitive dep graph starting from depIDs.
// Returns:
//   - (path, nil) when a cycle is found — path is "A -> B -> ... -> A" style.
//   - (nil, nil)  when the reachable graph is acyclic and small enough to
//     fully validate.
//   - (nil, err)  when validation could not complete because the visit
//     budget (maxDepGraphTraversal) was exhausted before exploring every
//     reachable node. The caller MUST refuse the create on err — silently
//     allowing it would let a pathological graph bypass the cycle gate.
//
// The new task is not yet persisted, so the only cycles detectable are
// ones already in the stored graph. Defensive against direct DB tampering
// or a future task-update path that doesn't re-validate.
//
// Algorithm: standard DFS with a per-node visited set + a recursion-stack
// set. The order of checks inside dfs is intentional:
//  1. inStack first — a true cycle is detected immediately, never
//     suppressed by the visit cap.
//  2. visited second — already-explored subgraphs short-circuit (diamond
//     DAGs visit shared descendants once).
//  3. visit-budget last — applies only to genuinely new nodes; bounds
//     worst-case work without preempting cycle detection.
//
// The returned cycle path is a defensive copy so sibling descents on a
// non-fully-explored graph cannot corrupt it via shared slice backing.
func (s *Server) detectCycle(depIDs []string) ([]string, error) {
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	var visits int
	var capExceeded bool

	var dfs func(id string, path []string) []string
	dfs = func(id string, path []string) []string {
		if inStack[id] {
			// Cycle: clone the path + duplicate id so the returned slice
			// cannot alias the recursion's shared backing array.
			cycle := make([]string, len(path)+1)
			copy(cycle, path)
			cycle[len(path)] = id
			return cycle
		}
		if visited[id] {
			return nil
		}
		visits++
		if visits > maxDepGraphTraversal {
			capExceeded = true
			return nil
		}
		inStack[id] = true
		visited[id] = true
		task, err := s.taskDB.Get(id)
		if err == nil && task != nil {
			for _, next := range task.DependsOn {
				if cycle := dfs(next, append(path, id)); cycle != nil {
					return cycle
				}
			}
		}
		inStack[id] = false
		return nil
	}

	for _, depID := range depIDs {
		if cycle := dfs(depID, nil); cycle != nil {
			return cycle, nil
		}
		// Once the visit budget is exhausted, every later dfs call would
		// either short-circuit on `visited` (for nodes touched by the
		// exhausted subtree) or trip `visits > maxDepGraphTraversal` on
		// its very first new node. Either way no useful work is possible
		// and we will return the cap-exceeded error below — break out so
		// the refusal path is reached without burning bookkeeping. This
		// is a semantic narrowing: a cycle reachable ONLY from a later
		// dep is no longer detected, but the create is still refused
		// (capExceeded triggers the error), so the safety invariant
		// "never approve an unverified graph" holds.
		if capExceeded {
			break
		}
	}
	if capExceeded {
		return nil, fmt.Errorf("dependency graph exceeds %d-node validation cap; refusing to create without verifying acyclicity", maxDepGraphTraversal)
	}
	return nil, nil
}

// formatTaskCreatedSummary renders the task_create response in a stable
// markdown shape so both the create path and the upsert path produce
// matching output. Surfaces orchestration fields (base_branch, depends_on,
// status) so the orchestrator can verify what landed without a follow-up
// task_get.
func formatTaskCreatedSummary(task *model.Task, heading string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s.\n\n", heading)
	fmt.Fprintf(&b, "- **ID**: %s\n", task.ID)
	fmt.Fprintf(&b, "- **Name**: %s\n", task.Name)
	fmt.Fprintf(&b, "- **Status**: %s\n", task.Status.String())
	fmt.Fprintf(&b, "- **Project**: %s\n", task.Project)
	if task.Branch != "" {
		fmt.Fprintf(&b, "- **Branch**: %s\n", task.Branch)
	}
	if task.BaseBranch != "" {
		fmt.Fprintf(&b, "- **Base branch**: %s\n", task.BaseBranch)
	}
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&b, "- **Depends on**: %s\n", strings.Join(task.DependsOn, ", "))
		fmt.Fprintf(&b, "- **Note**: blocked until every dep reaches status=complete; depswatcher will auto-start the agent.\n")
	}
	return b.String()
}

func (s *Server) toolTaskList(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		Status   string `json:"status"`
		Project  string `json:"project"`
		PlanSlug string `json:"plan_slug"`
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
		if p.PlanSlug != "" && t.PlanSlug != p.PlanSlug {
			continue
		}
		count++
		fmt.Fprintf(&sb, "- **%s** `%s` [%s] (%s)", t.Name, t.ID, t.Status.String(), t.Project)
		if t.Branch != "" {
			fmt.Fprintf(&sb, " branch:%s", t.Branch)
		}
		if t.PlanSlug != "" {
			fmt.Fprintf(&sb, " plan:%s", t.PlanSlug)
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
	if task.BaseBranch != "" {
		fmt.Fprintf(&sb, "- **Base branch**: %s\n", task.BaseBranch)
	}
	if task.Backend != "" {
		fmt.Fprintf(&sb, "- **Backend**: %s\n", task.Backend)
	}
	if elapsed := task.ElapsedString(); elapsed != "" {
		fmt.Fprintf(&sb, "- **Elapsed**: %s\n", elapsed)
	}
	// Surface dependency state so the orchestrator can poll one task and
	// see whether it's stuck waiting on upstream sub-tasks. blocked_by is
	// the live subset of depends_on that has not yet reached complete;
	// when empty (or DependsOn was empty to begin with), nothing renders.
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&sb, "- **Depends on**: %s\n", strings.Join(task.DependsOn, ", "))
		blocked := s.unresolvedDeps(task.DependsOn)
		if len(blocked) > 0 {
			fmt.Fprintf(&sb, "- **Blocked by**: %s\n", strings.Join(blocked, ", "))
		}
	}
	if task.Result != "" {
		fmt.Fprintf(&sb, "\n**Result**:\n```json\n%s\n```\n", task.Result)
	}
	if task.Prompt != "" {
		fmt.Fprintf(&sb, "\n**Prompt**: %s\n", task.Prompt)
	}
	return toolResult(id, sb.String())
}

// unresolvedDeps returns the subset of depIDs whose tasks have not reached
// status=complete. Missing tasks (deleted while a dependent waited) are
// also considered unresolved so the orchestrator sees the broken link
// instead of silently treating the dep as satisfied. Errors enumerating
// individual tasks are swallowed — the dep stays listed as blocking so
// the caller errs on the safe side.
func (s *Server) unresolvedDeps(depIDs []string) []string {
	var blocked []string
	for _, depID := range depIDs {
		dep, err := s.taskDB.Get(depID)
		if err != nil || dep == nil {
			blocked = append(blocked, depID+" (missing)")
			continue
		}
		if dep.Status != model.StatusComplete {
			blocked = append(blocked, depID)
		}
	}
	return blocked
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

func (s *Server) toolTaskArchive(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID       string `json:"id"`
		Cwd      string `json:"cwd"`
		Archived *bool  `json:"archived"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Read-then-write is not atomic — a concurrent /archive call or a
	// TUI 'a' keypress racing this handler can blindly re-flip the toggle.
	// Acceptable for a single-user local daemon; callers wanting determinism
	// should pass an explicit `archived` bool instead of relying on toggle.
	newArchived := !task.Archived
	if p.Archived != nil {
		newArchived = *p.Archived
	}

	// No-op: report current state without a DB write.
	if newArchived == task.Archived {
		state := "unarchived"
		if task.Archived {
			state = "archived"
		}
		return toolResult(id, fmt.Sprintf("Task %s (%s) already %s.", task.ID, task.Name, state))
	}

	task.SetArchived(newArchived)
	if err := s.taskDB.Update(task); err != nil {
		log.Printf("[mcp] task_archive failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to archive task: %v", err))
	}

	action := "Archived"
	if !newArchived {
		action = "Unarchived"
	}
	log.Printf("[mcp] task_archive ok: id=%s archived=%v", task.ID, newArchived)
	return toolResult(id, fmt.Sprintf("%s task %s (%s).", action, task.ID, task.Name))
}

func (s *Server) toolTaskRename(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID   string `json:"id"`
		Cwd  string `json:"cwd"`
		Name string `json:"name"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	name := strings.TrimSpace(p.Name)
	if name == "" {
		return toolError(id, "name is required")
	}
	// Defensive cap: the request body limit (4 MiB) is the only other
	// ceiling, and a 4 MiB task name would render unusably across every UI
	// surface. 200 runes comfortably covers any human-typeable name.
	if len([]rune(name)) > maxTaskNameRunes {
		return toolError(id, fmt.Sprintf("name exceeds %d runes", maxTaskNameRunes))
	}

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	if name == task.Name {
		return toolResult(id, fmt.Sprintf("Task %s (%s) already named %q.", task.ID, task.Name, name))
	}

	prev := task.Name
	if err := s.taskDB.Rename(task.ID, name); err != nil {
		log.Printf("[mcp] task_rename failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to rename task: %v", err))
	}

	log.Printf("[mcp] task_rename ok: id=%s prev=%q new=%q", task.ID, prev, name)
	return toolResult(id, fmt.Sprintf("Renamed task %s: %q → %q.", task.ID, prev, name))
}

func (s *Server) toolTaskComplete(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	if task.Status == model.StatusComplete {
		return toolResult(id, fmt.Sprintf("Task %s (%s) already complete.", task.ID, task.Name))
	}

	// Read-then-write is not atomic — two concurrent task_complete calls can
	// both read non-complete state and both stamp EndedAt; the second wins
	// with a slightly later timestamp. Acceptable for a single-user local
	// daemon.
	prev := task.Status
	task.SetStatus(model.StatusComplete)
	if err := s.taskDB.Update(task); err != nil {
		log.Printf("[mcp] task_complete failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to mark task complete: %v", err))
	}

	log.Printf("[mcp] task_complete ok: id=%s prev=%s", task.ID, prev)
	return toolResult(id, fmt.Sprintf("Marked task %s (%s) as complete.", task.ID, task.Name))
}

// toolTaskSetResult persists the agent-supplied JSON result blob on a task.
// The daemon never inspects the shape — that is the orchestrator's contract.
// We re-encode the user-supplied object via json.Marshal so trailing
// whitespace, ordering, or odd integer formatting from the wire payload
// don't survive into the DB row (deterministic storage). Last write wins;
// the 64 KiB cap is enforced on the serialized form.
func (s *Server) toolTaskSetResult(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}

	var p struct {
		ID     string          `json:"id"`
		Cwd    string          `json:"cwd"`
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if len(p.Result) == 0 {
		return toolError(id, "result is required")
	}
	// Accept only JSON objects — arrays, scalars, and bare strings would
	// make the orchestrator contract ambiguous (which field is the PR
	// URL?). Strict object check keeps the contract obvious.
	var obj map[string]interface{}
	if err := json.Unmarshal(p.Result, &obj); err != nil {
		return toolError(id, fmt.Sprintf("result must be a JSON object: %v", err))
	}
	canonical, err := json.Marshal(obj)
	if err != nil {
		return toolError(id, fmt.Sprintf("re-encode result: %v", err))
	}
	if len(canonical) > maxTaskResultBytes {
		return toolError(id, fmt.Sprintf("result exceeds %d bytes (got %d)", maxTaskResultBytes, len(canonical)))
	}

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	if err := s.taskDB.SetResult(task.ID, string(canonical)); err != nil {
		log.Printf("[mcp] task_set_result failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to set result: %v", err))
	}

	log.Printf("[mcp] task_set_result ok: id=%s bytes=%d", task.ID, len(canonical))
	return toolResult(id, fmt.Sprintf("Result stored for task %s (%s). %d bytes.", task.ID, task.Name, len(canonical)))
}

// toolClipboardSet stages text for the user to copy. Resolves the task via
// explicit id or via cwd (matching against task worktree paths).
func (s *Server) toolClipboardSet(id interface{}, args json.RawMessage) *Response {
	if !s.clipboardEnabled() {
		return toolError(id, "clipboard not configured")
	}

	var p struct {
		Text string `json:"text"`
		ID   string `json:"id"`
		Cwd  string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Text == "" {
		return toolError(id, "text is required")
	}

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	if err := s.clipboard.Set(task.ID, p.Text); err != nil {
		log.Printf("[mcp] clipboard_set failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to stage text: %v", err))
	}

	log.Printf("[mcp] clipboard_set ok: id=%s bytes=%d", task.ID, len(p.Text))
	return toolResult(id, fmt.Sprintf("Staged %d bytes for task %s (%s). The user will see a Copy button (PWA) or ctrl+y hint (TUI).", len(p.Text), task.ID, task.Name))
}

// resolveTask finds a task by explicit ID, or by matching cwd against
// task worktree paths (longest prefix wins, separator-guarded so siblings
// don't collide). Archived tasks are included in the lookup so unarchive
// from inside an archived worktree works. Returns an error if neither
// input is provided or no match is found.
//
// Callers must guarantee s.taskMgmtEnabled() — this method dereferences
// s.taskDB without a nil check.
func (s *Server) resolveTask(taskID, cwd string) (*model.Task, error) {
	if taskID != "" {
		t, err := s.taskDB.Get(taskID)
		if err != nil || t == nil {
			return nil, fmt.Errorf("task not found: %s", taskID)
		}
		return t, nil
	}
	if cwd == "" {
		return nil, fmt.Errorf("provide id or cwd")
	}
	cleanCwd := filepath.Clean(cwd)
	tasks, err := s.taskDB.Tasks()
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	var best *model.Task
	var bestLen int
	for _, t := range tasks {
		if t.Worktree == "" {
			continue
		}
		wt := filepath.Clean(t.Worktree)
		if cleanCwd != wt && !strings.HasPrefix(cleanCwd, wt+string(filepath.Separator)) {
			continue
		}
		if len(wt) > bestLen {
			best = t
			bestLen = len(wt)
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no task matches cwd: %s", cwd)
	}
	return best, nil
}

// --- Schedule tool handlers ---

// formatScheduleTime renders a schedule timestamp; empty for the zero value so
// the listing tool does not show "0001-01-01T00:00:00Z" for unfired schedules.
func formatScheduleTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (s *Server) toolScheduleList(id interface{}, _ json.RawMessage) *Response {
	if !s.scheduleMgmtEnabled() {
		return toolError(id, "schedule management not configured")
	}
	schedules, err := s.schedDB.Schedules()
	if err != nil {
		return toolError(id, fmt.Sprintf("Failed to list schedules: %v", err))
	}
	if len(schedules) == 0 {
		return toolResult(id, "No scheduled tasks.")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d schedule(s):\n\n", len(schedules))
	for _, sch := range schedules {
		state := "enabled"
		if !sch.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(&sb, "- **%s** `%s` [%s]\n", sch.Name, sch.ID, state)
		fmt.Fprintf(&sb, "  - schedule: `%s`\n", sch.Schedule)
		fmt.Fprintf(&sb, "  - project: %s\n", sch.Project)
		if sch.Backend != "" {
			fmt.Fprintf(&sb, "  - backend: %s\n", sch.Backend)
		}
		if next := formatScheduleTime(sch.NextRunAt); next != "" {
			fmt.Fprintf(&sb, "  - next_run_at: %s\n", next)
		}
		if last := formatScheduleTime(sch.LastRunAt); last != "" {
			fmt.Fprintf(&sb, "  - last_run_at: %s\n", last)
		}
		if sch.LastError != "" {
			fmt.Fprintf(&sb, "  - last_error: %s\n", sch.LastError)
		}
	}
	return toolResult(id, sb.String())
}

func (s *Server) toolScheduleCreate(id interface{}, args json.RawMessage) *Response {
	if !s.scheduleMgmtEnabled() {
		return toolError(id, "schedule management not configured")
	}
	var p struct {
		Name      string `json:"name"`
		Project   string `json:"project"`
		Prompt    string `json:"prompt"`
		Schedule  string `json:"schedule"`
		RunOnceAt string `json:"run_once_at"`
		Backend   string `json:"backend"`
		Enabled   *bool  `json:"enabled"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	sched := &model.ScheduledTask{
		Name:     strings.TrimSpace(p.Name),
		Project:  strings.TrimSpace(p.Project),
		Prompt:   p.Prompt,
		Schedule: strings.TrimSpace(p.Schedule),
		Backend:  strings.TrimSpace(p.Backend),
		Enabled:  true, // default
	}
	if raw := strings.TrimSpace(p.RunOnceAt); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError(id, fmt.Sprintf("run_once_at must be RFC3339 (e.g. 2026-05-17T14:00:00Z): %v", err))
		}
		if !t.After(time.Now()) {
			return toolError(id, "run_once_at must be in the future")
		}
		sched.RunOnceAt = t
	}
	if p.Enabled != nil {
		sched.Enabled = *p.Enabled
	}
	if err := sched.Validate(); err != nil {
		return toolError(id, err.Error())
	}
	// Pre-populate NextRunAt so the UI shows it before the first tick lands.
	sched.NextRunAt = sched.NextFire(time.Now())
	if err := s.schedDB.AddSchedule(sched); err != nil {
		log.Printf("[mcp] schedule_create failed: %v", err)
		return toolError(id, fmt.Sprintf("Failed to create schedule: %v", err))
	}
	cadence := sched.Schedule
	if sched.IsOneShot() {
		cadence = "once @ " + sched.RunOnceAt.UTC().Format(time.RFC3339)
	}
	log.Printf("[mcp] schedule_create ok: id=%s name=%s cadence=%q", sched.ID, sched.Name, cadence)
	return toolResult(id, fmt.Sprintf("Schedule created.\n\n- **ID**: %s\n- **Name**: %s\n- **Cadence**: %s\n- **Project**: %s\n- **Enabled**: %v\n- **Next run**: %s",
		sched.ID, sched.Name, cadence, sched.Project, sched.Enabled, formatScheduleTime(sched.NextRunAt)))
}

func (s *Server) toolScheduleUpdate(id interface{}, args json.RawMessage) *Response {
	if !s.scheduleMgmtEnabled() {
		return toolError(id, "schedule management not configured")
	}
	var p struct {
		ID        string  `json:"id"`
		Name      *string `json:"name"`
		Project   *string `json:"project"`
		Prompt    *string `json:"prompt"`
		Schedule  *string `json:"schedule"`
		RunOnceAt *string `json:"run_once_at"`
		Backend   *string `json:"backend"`
		Enabled   *bool   `json:"enabled"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if strings.TrimSpace(p.ID) == "" {
		return toolError(id, "id is required")
	}
	sched, err := s.schedDB.GetSchedule(p.ID)
	if err != nil {
		return toolError(id, fmt.Sprintf("schedule not found: %s", p.ID))
	}
	// Reject ambiguous "both cadences in one call" up front. Per-field
	// auto-clear below would otherwise silently pick a winner by ordering.
	if p.Schedule != nil && strings.TrimSpace(*p.Schedule) != "" &&
		p.RunOnceAt != nil && strings.TrimSpace(*p.RunOnceAt) != "" {
		return toolError(id, "specify either schedule (cron) or run_once_at, not both")
	}
	cadenceChanged := false
	if p.Name != nil {
		sched.Name = strings.TrimSpace(*p.Name)
	}
	if p.Project != nil {
		sched.Project = strings.TrimSpace(*p.Project)
	}
	if p.Prompt != nil {
		sched.Prompt = *p.Prompt
	}
	if p.Schedule != nil {
		newExpr := strings.TrimSpace(*p.Schedule)
		if newExpr != sched.Schedule {
			cadenceChanged = true
		}
		sched.Schedule = newExpr
		// Setting a non-empty cron expression clears any one-shot anchor.
		// The both-set guard above ensures this clear is never hiding an
		// explicit user-supplied run_once_at.
		if newExpr != "" {
			sched.RunOnceAt = time.Time{}
		}
	}
	if p.RunOnceAt != nil {
		raw := strings.TrimSpace(*p.RunOnceAt)
		var newAt time.Time
		if raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return toolError(id, fmt.Sprintf("run_once_at must be RFC3339 (e.g. 2026-05-17T14:00:00Z): %v", err))
			}
			if !t.After(time.Now()) {
				return toolError(id, "run_once_at must be in the future")
			}
			newAt = t
		}
		if !sched.RunOnceAt.Equal(newAt) {
			cadenceChanged = true
		}
		sched.RunOnceAt = newAt
		// Setting a non-zero one-shot anchor clears any cron expression.
		if !newAt.IsZero() {
			sched.Schedule = ""
		}
	}
	if p.Backend != nil {
		sched.Backend = strings.TrimSpace(*p.Backend)
	}
	if p.Enabled != nil {
		sched.Enabled = *p.Enabled
	}
	if err := sched.Validate(); err != nil {
		return toolError(id, err.Error())
	}
	if cadenceChanged {
		// Anchor on LastRunAt when previously fired so an unchanged cadence
		// preserves alignment; otherwise anchor on now. Mirrors the API's
		// recompute path. NextFire returns RunOnceAt directly for one-shots.
		anchor := sched.LastRunAt
		if anchor.IsZero() {
			anchor = time.Now()
		}
		sched.NextRunAt = sched.NextFire(anchor)
	}
	sched.LastError = ""
	if err := s.schedDB.UpdateSchedule(sched); err != nil {
		log.Printf("[mcp] schedule_update failed: id=%s err=%v", sched.ID, err)
		return toolError(id, fmt.Sprintf("Failed to update schedule: %v", err))
	}
	cadence := sched.Schedule
	if sched.IsOneShot() {
		cadence = "once @ " + sched.RunOnceAt.UTC().Format(time.RFC3339)
	}
	log.Printf("[mcp] schedule_update ok: id=%s cadence=%q enabled=%v", sched.ID, cadence, sched.Enabled)
	return toolResult(id, fmt.Sprintf("Schedule updated.\n\n- **ID**: %s\n- **Name**: %s\n- **Cadence**: %s\n- **Enabled**: %v\n- **Next run**: %s",
		sched.ID, sched.Name, cadence, sched.Enabled, formatScheduleTime(sched.NextRunAt)))
}

func (s *Server) toolScheduleDelete(id interface{}, args json.RawMessage) *Response {
	if !s.scheduleMgmtEnabled() {
		return toolError(id, "schedule management not configured")
	}
	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	if strings.TrimSpace(p.ID) == "" {
		return toolError(id, "id is required")
	}
	if err := s.schedDB.DeleteSchedule(p.ID); err != nil {
		log.Printf("[mcp] schedule_delete failed: id=%s err=%v", p.ID, err)
		return toolError(id, fmt.Sprintf("Failed to delete schedule: %v", err))
	}
	log.Printf("[mcp] schedule_delete ok: id=%s", p.ID)
	return toolResult(id, fmt.Sprintf("Deleted schedule %s.", p.ID))
}

func (s *Server) toolScheduleRunNow(id interface{}, args json.RawMessage) *Response {
	if !s.scheduleMgmtEnabled() {
		return toolError(id, "schedule management not configured")
	}
	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	if strings.TrimSpace(p.ID) == "" {
		return toolError(id, "id is required")
	}
	task, err := s.schedRunner.RunNow(p.ID)
	if err != nil {
		log.Printf("[mcp] schedule_run_now failed: id=%s err=%v", p.ID, err)
		return toolError(id, fmt.Sprintf("Failed to run schedule: %v", err))
	}
	log.Printf("[mcp] schedule_run_now ok: id=%s task=%s", p.ID, task.ID)
	return toolResult(id, fmt.Sprintf("Schedule fired. Created task %s (%s).", task.ID, task.Name))
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
