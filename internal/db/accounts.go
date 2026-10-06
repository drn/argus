package db

import (
	"encoding/json"
	"fmt"

	"github.com/drn/argus/internal/config"
)

// Config kv keys for the Settings-UI-edited account selection state.
const (
	ConfigKeyDefaultAccount  = "accounts.default"
	ConfigKeyProjectAccounts = "accounts.projects"
)

// Accounts returns the Settings-UI-edited accounts keyed by name. db.Config
// loads them before the config.toml overlay, which overrides per name.
func (d *DB) Accounts() (map[string]config.Account, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT name, label, claude_config_dir, codex_home FROM accounts`)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]config.Account)
	for rows.Next() {
		var name string
		var a config.Account
		if err := rows.Scan(&name, &a.Label, &a.ClaudeConfigDir, &a.CodexHome); err != nil {
			return nil, fmt.Errorf("scan accounts: %w", err)
		}
		out[name] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return out, nil
}

// SetAccount inserts or replaces one account. Callers validate first
// (config.Config.ValidateAccount); the store accepts whatever it is given.
func (d *DB) SetAccount(name string, a config.Account) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT OR REPLACE INTO accounts (name, label, claude_config_dir, codex_home) VALUES (?, ?, ?, ?)`,
		name, a.Label, a.ClaudeConfigDir, a.CodexHome)
	return err
}

// DeleteAccount removes one account. Tasks already bound to it are untouched
// and fail loudly on their next spawn (add-agent-accounts).
func (d *DB) DeleteAccount(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM accounts WHERE name = ?`, name)
	return err
}

// SetDefaultAccount stores the global default account name ("" clears it).
func (d *DB) SetDefaultAccount(name string) error {
	return d.SetConfigValue(ConfigKeyDefaultAccount, name)
}

// SetProjectAccount stores one project's default account; an empty account
// removes the entry so the project falls back to the global default.
func (d *DB) SetProjectAccount(project, account string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// One lock across the read-modify-write so concurrent writers can't lose an update.
	m := map[string]string{}
	var raw string
	if err := d.conn.QueryRow(`SELECT value FROM config WHERE key = ?`, ConfigKeyProjectAccounts).Scan(&raw); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
		if m == nil {
			m = map[string]string{}
		}
	}
	if account == "" {
		delete(m, project)
	} else {
		m[project] = account
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = d.conn.Exec(`INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)`, ConfigKeyProjectAccounts, string(b))
	return err
}

// AccountsFromConfigToml reports which account settings config.toml defines
// (and therefore overrides the DB for). Always empty for an in-memory/test DB.
func (d *DB) AccountsFromConfigToml() config.AccountSources {
	return d.cfgLoader.AccountSources()
}
