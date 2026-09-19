package db

// hideHeraManagedConfigKey is the config-table key under which the Tasks
// view's hide-hera-managed toggle (`H`) is persisted as "true"/"false".
const hideHeraManagedConfigKey = "ui.hide_hera_managed"

// lastTabConfigKey is the config-table key under which the shell's last
// active top-level tab is persisted as "tasks"/"hera"/"settings" — a plain
// string, not the widget.Tab int, so this package doesn't need to import
// internal/tui/widget; the string<->widget.Tab mapping lives in internal/tui.
const lastTabConfigKey = "ui.last_tab"

// LoadHideHeraManaged returns the persisted hide-hera-managed toggle state, or
// false (no error) when none has been saved (fresh install / --remote mode).
func (d *DB) LoadHideHeraManaged() (bool, error) {
	v, err := d.GetConfigValue(hideHeraManagedConfigKey)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SaveHideHeraManaged persists the Tasks view's hide-hera-managed toggle
// state so relaunching argus restores the same visibility the user last set.
func (d *DB) SaveHideHeraManaged(hidden bool) error {
	v := "false"
	if hidden {
		v = "true"
	}
	return d.SetConfigValue(hideHeraManagedConfigKey, v)
}

// LoadLastTab returns the persisted last active top-level tab
// ("tasks"/"hera"/"settings"), or "" (no error) when none has been saved.
func (d *DB) LoadLastTab() (string, error) {
	return d.GetConfigValue(lastTabConfigKey)
}

// SaveLastTab persists the shell's last active top-level tab so relaunching
// argus can restore it instead of always defaulting to Tasks.
func (d *DB) SaveLastTab(tab string) error {
	return d.SetConfigValue(lastTabConfigKey, tab)
}
