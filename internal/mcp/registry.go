package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
)

// DefaultIdleWindow is the silence-after which an unrenewed plugin tool is
// dropped by the sweeper. The plan calls for 10 minutes. Plugins keep
// registrations alive either by re-POSTing the same body to /api/mcp/tools
// (idempotent upsert refreshes LastSeenAt) or by being invoked — Invoke
// touches LastSeenAt on every successful HTTP round trip.
const DefaultIdleWindow = 10 * time.Minute

// DefaultInvokeTimeout caps a single plugin callback round trip. var (not
// const) so tests can shrink it; production stays generous because plugins
// may do non-trivial work (LLM calls, disk I/O) before responding.
var DefaultInvokeTimeout = 30 * time.Second

// MaxInputSchemaBytes caps the JSON-encoded input_schema column. SQLite TEXT
// is unbounded; a misbehaving plugin shouldn't be able to fill the table.
const MaxInputSchemaBytes = 64 * 1024

// MaxToolDescriptionBytes caps the description column.
const MaxToolDescriptionBytes = 4 * 1024

// MaxCallbackURLBytes caps the callback URL. Far above any realistic length.
const MaxCallbackURLBytes = 2048

// MaxAuthHeaderBytes caps the auth header value.
const MaxAuthHeaderBytes = 4096

// MaxToolNameBytes caps the registered tool name.
const MaxToolNameBytes = 128

// MaxToolsPerScope caps how many tools a single plugin can register. The plan
// gives plugins "any number" of layouts but proxy resources scale with tool
// count, so cap aggressively. 100 is two orders of magnitude above any
// realistic plugin shape (the worked-example "ludwig" plugin in the plan
// registers ~7 tools).
const MaxToolsPerScope = 100

// maxProxyResponseBytes caps the plugin's response body. A 4 MiB cap matches
// the MCP HTTP request body limit in server.go.
const maxProxyResponseBytes = 4 * 1024 * 1024

// RegistryStore is the persistence contract used by Registry. *db.DB satisfies
// it implicitly — Registry stays test-driven without coupling to SQLite.
type RegistryStore interface {
	UpsertPluginMCPTool(*db.PluginMCPTool) error
	DeletePluginMCPTool(name string) (bool, error)
	DeletePluginMCPToolsByScope(scope string) (int, error)
	PluginMCPTools() ([]*db.PluginMCPTool, error)
	GetPluginMCPTool(name string) (*db.PluginMCPTool, error)
	DeletePluginMCPToolsIdle(cutoff time.Time) ([]*db.PluginMCPTool, error)
}

// ToolRegistration is the inbound shape POST /api/mcp/tools and the
// daemon-side wiring pass into Registry.Register. The Registry validates,
// namespace-checks, and persists.
type ToolRegistration struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	CallbackURL string
	AuthHeader  string
}

// CallerContext is the optional caller identity threaded through to the
// plugin's callback_url. The MCP protocol surface today has no per-call task
// or session identifier, so the daemon always sends empty strings in
// production. The contract is stable — plugins can ignore the field, future
// work can populate it without breaking existing callbacks.
type CallerContext struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

// Registry persists plugin-registered MCP tools and proxies invocations.
// Safe for concurrent use — every method delegates to the underlying store
// (whose methods are mu-guarded inside *db.DB) and the http.Client is
// goroutine-safe by design.
type Registry struct {
	store  RegistryStore
	client *http.Client
	now    func() time.Time
}

// NewRegistry builds a Registry over the given store with a default
// http.Client (timeout = DefaultInvokeTimeout). Tests can swap r.now or set
// r.client directly via small helpers in the test files of this package.
func NewRegistry(store RegistryStore) *Registry {
	return &Registry{
		store:  store,
		client: &http.Client{Timeout: DefaultInvokeTimeout},
		now:    time.Now,
	}
}

// Register validates a plugin tool registration and upserts it. The tool's
// Name MUST start with `<scope>_` so namespace boundaries are enforced at
// registration time per the plan. A second Register call with an existing
// (Name) row refreshes LastSeenAt — the heartbeat for the idle sweeper.
//
// Errors are kept user-readable so the API layer can pass them through to
// the plugin caller without translation.
func (r *Registry) Register(scope string, in ToolRegistration) error {
	if scope == "" {
		return errors.New("scope required")
	}
	if err := validateScopedName(scope, in.Name); err != nil {
		return err
	}
	if !strings.HasPrefix(in.CallbackURL, "http://") && !strings.HasPrefix(in.CallbackURL, "https://") {
		return errors.New("callback_url must be http:// or https://")
	}
	if len(in.CallbackURL) > MaxCallbackURLBytes {
		return fmt.Errorf("callback_url exceeds %d bytes", MaxCallbackURLBytes)
	}
	if len(in.AuthHeader) > MaxAuthHeaderBytes {
		return fmt.Errorf("auth_header exceeds %d bytes", MaxAuthHeaderBytes)
	}
	if len(in.Description) > MaxToolDescriptionBytes {
		return fmt.Errorf("description exceeds %d bytes", MaxToolDescriptionBytes)
	}
	if len(in.InputSchema) > MaxInputSchemaBytes {
		return fmt.Errorf("input_schema exceeds %d bytes", MaxInputSchemaBytes)
	}
	schema := in.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{}`)
	} else if !json.Valid(schema) {
		return errors.New("input_schema must be valid JSON")
	}

	all, err := r.store.PluginMCPTools()
	if err != nil {
		return err
	}
	var existing *db.PluginMCPTool
	var perScope int
	for _, t := range all {
		if t.Name == in.Name {
			existing = t
		}
		if t.Scope == scope {
			perScope++
		}
		if t.Name == in.Name && t.Scope != scope {
			return fmt.Errorf("tool name %q already registered by scope %q", in.Name, t.Scope)
		}
	}
	if existing == nil && perScope >= MaxToolsPerScope {
		return fmt.Errorf("scope %q exceeds %d-tool limit", scope, MaxToolsPerScope)
	}

	now := r.now()
	row := &db.PluginMCPTool{
		Name:        in.Name,
		Scope:       scope,
		Description: in.Description,
		InputSchema: schema,
		CallbackURL: in.CallbackURL,
		AuthHeader:  in.AuthHeader,
		LastSeenAt:  now,
	}
	if existing != nil {
		// Heartbeat path — preserve the original RegisteredAt so the operator
		// can see how long a plugin has been around at a glance.
		row.RegisteredAt = existing.RegisteredAt
	} else {
		row.RegisteredAt = now
	}
	return r.store.UpsertPluginMCPTool(row)
}

// Unregister removes a single tool. scope must match the row's scope (the
// owning plugin) unless scope is empty — empty scope is treated as the
// master credential and may remove any tool. Idempotent: a missing name
// returns nil so a repeat DELETE doesn't 404 confusingly.
func (r *Registry) Unregister(scope, name string) error {
	existing, err := r.store.GetPluginMCPTool(name)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if scope != "" && existing.Scope != scope {
		return errors.New("tool not owned by this scope")
	}
	_, err = r.store.DeletePluginMCPTool(name)
	return err
}

// UnregisterScope drops every tool owned by scope. Called by the API token
// revoke cascade so a revoked plugin token leaves no callable surface behind.
func (r *Registry) UnregisterScope(scope string) (int, error) {
	return r.store.DeletePluginMCPToolsByScope(scope)
}

// List returns every registered tool, ordered by name (matches the store's
// ORDER BY) so tools/list is stable across calls.
func (r *Registry) List() ([]*db.PluginMCPTool, error) {
	return r.store.PluginMCPTools()
}

// Get returns a single tool by name, or (nil, nil) when absent.
func (r *Registry) Get(name string) (*db.PluginMCPTool, error) {
	return r.store.GetPluginMCPTool(name)
}

// SweepIdle drops every tool whose LastSeenAt is older than idleWindow before
// "now". Returns the dropped rows so the caller can log a single line per
// dropped tool (scope + name).
func (r *Registry) SweepIdle(idleWindow time.Duration) ([]*db.PluginMCPTool, error) {
	cutoff := r.now().Add(-idleWindow)
	return r.store.DeletePluginMCPToolsIdle(cutoff)
}

// Invoke posts the input JSON to the plugin's callback_url and decodes the
// plugin's MCP-native tool response. Touches LastSeenAt on every successful
// round trip — every invocation counts as a heartbeat, so a busy plugin
// never gets swept.
func (r *Registry) Invoke(ctx context.Context, name string, input json.RawMessage, caller CallerContext) (*ToolCallResult, error) {
	t, err := r.store.GetPluginMCPTool(name)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("plugin tool not registered: %s", name)
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(invokeRequest{Tool: name, Input: input, Context: caller})
	if err != nil {
		return nil, fmt.Errorf("marshal invoke: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.AuthHeader != "" {
		req.Header.Set("Authorization", t.AuthHeader)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin invoke: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("plugin returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out ToolCallResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProxyResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode plugin response: %w", err)
	}
	// Heartbeat — successful call refreshes LastSeenAt. Best-effort: a
	// transient store error here would only mean the sweeper might evict
	// the tool a bit sooner than expected, which is harmless.
	t.LastSeenAt = r.now()
	_ = r.store.UpsertPluginMCPTool(t)
	return &out, nil
}

// invokeRequest is the JSON body argus POSTs to the plugin's callback_url.
// Field order matches the plan's contract reference exactly.
type invokeRequest struct {
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	Context CallerContext   `json:"context"`
}

// validateScopedName enforces the plan's namespace rule and a defensive
// character allowlist: ASCII alphanumerics plus '_' and '-' only, so a
// registered tool name can never contain whitespace, slashes, or
// shell-special characters that some MCP clients might render unsafely.
func validateScopedName(scope, name string) error {
	if name == "" {
		return errors.New("name required")
	}
	prefix := scope + "_"
	if !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("name %q must start with %q (scope prefix)", name, prefix)
	}
	if name == prefix {
		return errors.New("name must include identifier after scope_ prefix")
	}
	if len(name) > MaxToolNameBytes {
		return fmt.Errorf("name exceeds %d bytes", MaxToolNameBytes)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("name %q contains invalid character %q", name, r)
		}
	}
	return nil
}
