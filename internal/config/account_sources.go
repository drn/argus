package config

// Kept out of accounts.go on purpose: that file is listed in the supervisor
// spawn-surface manifest (daemon.SupervisorSpawnPaths), and this type is
// Settings-UI-only, so editing it must not churn the spawn digest.

// AccountSources reports which account settings config.toml defines, so the
// Settings view can show them read-only (they override the DB).
type AccountSources struct {
	Accounts map[string]bool // account names defined as [accounts.<name>]
	Default  bool            // default_account is set in the file
	Projects map[string]bool // project names in [project_accounts]
}
