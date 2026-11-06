package db

import "strconv"

// LoadTasksExpandAll restores the local Tasks project-expansion preference.
func (d *DB) LoadTasksExpandAll() (bool, error) {
	value, err := d.GetConfigValue("ui.tasks_expand_all")
	return value == "true", err
}

func (d *DB) SaveTasksExpandAll(enabled bool) error {
	return d.SetConfigValue("ui.tasks_expand_all", strconv.FormatBool(enabled))
}
