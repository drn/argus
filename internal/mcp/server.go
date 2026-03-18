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
)

// KBQuerier is the interface the MCP server needs from the database.
type KBQuerier interface {
	KBSearch(query string, limit int) ([]kb.SearchResult, error)
	KBGet(path string) (*kb.Document, error)
	KBList(prefix string, limit int) ([]kb.Document, error)
	KBUpsert(doc *kb.Document) error
	KBDocumentCount() int
}

// Server is the MCP HTTP server.
type Server struct {
	db      KBQuerier
	port    int
	httpSrv *http.Server
}

// New creates a new MCP server.
func New(db KBQuerier, port int) *Server {
	return &Server{db: db, port: port}
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

func (s *Server) handleToolsList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: toolDefs},
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
