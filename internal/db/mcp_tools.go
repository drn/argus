package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PluginMCPTool is a runtime-registered MCP tool from a plugin. Stored in the
// plugin_mcp_tools table; the MCP server consults it alongside built-in tools
// at tools/list and proxies tools/call invocations to CallbackURL.
//
// InputSchema is raw JSON so it round-trips into tools/list responses byte-for-
// byte — re-encoding via map[string]interface{} would lose key ordering that
// some MCP clients display verbatim.
type PluginMCPTool struct {
	Name         string
	Scope        string
	Description  string
	InputSchema  json.RawMessage
	CallbackURL  string
	AuthHeader   string
	RegisteredAt time.Time
	LastSeenAt   time.Time
}

// UpsertPluginMCPTool inserts or replaces a row by primary-key name. The MCP
// registry uses re-POST of the same body as a heartbeat — LastSeenAt MUST
// refresh on every call; RegisteredAt is whatever the caller provides
// (registry preserves the original value when heartbeating).
func (d *DB) UpsertPluginMCPTool(t *PluginMCPTool) error {
	if t == nil {
		return errors.New("nil plugin mcp tool")
	}
	if t.InputSchema == nil {
		t.InputSchema = json.RawMessage(`{}`)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(`
		INSERT INTO plugin_mcp_tools (name, scope, description, input_schema, callback_url, auth_header, registered_at, last_seen_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			scope = excluded.scope,
			description = excluded.description,
			input_schema = excluded.input_schema,
			callback_url = excluded.callback_url,
			auth_header = excluded.auth_header,
			last_seen_at = excluded.last_seen_at
	`,
		t.Name, t.Scope, t.Description, string(t.InputSchema),
		t.CallbackURL, t.AuthHeader,
		t.RegisteredAt.Unix(), t.LastSeenAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert plugin mcp tool: %w", err)
	}
	return nil
}

// GetPluginMCPTool returns a single tool by primary-key name. (nil, nil) when
// no row matches.
func (d *DB) GetPluginMCPTool(name string) (*PluginMCPTool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(`
		SELECT name, scope, description, input_schema, callback_url, auth_header, registered_at, last_seen_at
		FROM plugin_mcp_tools WHERE name = ?
	`, name)
	t, err := scanPluginMCPTool(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// PluginMCPTools returns every registered tool, ordered by name for stable
// tools/list output.
func (d *DB) PluginMCPTools() ([]*PluginMCPTool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(`
		SELECT name, scope, description, input_schema, callback_url, auth_header, registered_at, last_seen_at
		FROM plugin_mcp_tools ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list plugin mcp tools: %w", err)
	}
	defer rows.Close()
	var out []*PluginMCPTool
	for rows.Next() {
		t, scanErr := scanPluginMCPTool(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, t)
	}
	return out, nil
}

// DeletePluginMCPTool removes a single tool by name. Returns whether a row
// was actually removed so the caller can distinguish "deleted" from "never
// existed" for logging / 404 decisions.
func (d *DB) DeletePluginMCPTool(name string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_mcp_tools WHERE name = ?`, name)
	if err != nil {
		return false, fmt.Errorf("delete plugin mcp tool: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeletePluginMCPToolsByScope cascades a scope-wide removal — used when the
// plugin's token is revoked. Returns the number of rows dropped.
func (d *DB) DeletePluginMCPToolsByScope(scope string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM plugin_mcp_tools WHERE scope = ?`, scope)
	if err != nil {
		return 0, fmt.Errorf("delete plugin mcp tools by scope: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeletePluginMCPToolsIdle drops every row whose LastSeenAt is older than
// cutoff. Returns the removed rows so the sweep loop can log scope+name pairs
// for the operator. The two-step (select-then-delete) is done in a single
// transaction so a concurrent re-register can't race the sweeper into dropping
// a freshly heartbeat'd row.
func (d *DB) DeletePluginMCPToolsIdle(cutoff time.Time) ([]*PluginMCPTool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin idle sweep: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.Query(`
		SELECT name, scope, description, input_schema, callback_url, auth_header, registered_at, last_seen_at
		FROM plugin_mcp_tools WHERE last_seen_at < ? ORDER BY name
	`, cutoff.Unix())
	if err != nil {
		return nil, fmt.Errorf("query idle plugin mcp tools: %w", err)
	}
	var out []*PluginMCPTool
	for rows.Next() {
		t, scanErr := scanPluginMCPTool(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		out = append(out, t)
	}
	rows.Close()

	if len(out) > 0 {
		if _, err := tx.Exec(`DELETE FROM plugin_mcp_tools WHERE last_seen_at < ?`, cutoff.Unix()); err != nil {
			return nil, fmt.Errorf("delete idle plugin mcp tools: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit idle sweep: %w", err)
	}
	return out, nil
}

// scanRow is the subset of *sql.Row / *sql.Rows that scanPluginMCPTool needs.
// Lets the same scan logic serve QueryRow (single result) and Query (loop).
type scanRow interface {
	Scan(dest ...any) error
}

func scanPluginMCPTool(r scanRow) (*PluginMCPTool, error) {
	var (
		t        PluginMCPTool
		schema   string
		regAt    int64
		lastSeen int64
	)
	if err := r.Scan(
		&t.Name, &t.Scope, &t.Description, &schema,
		&t.CallbackURL, &t.AuthHeader, &regAt, &lastSeen,
	); err != nil {
		return nil, err
	}
	if schema == "" {
		schema = "{}"
	}
	t.InputSchema = json.RawMessage(schema)
	t.RegisteredAt = time.Unix(regAt, 0).UTC()
	t.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	return &t, nil
}
