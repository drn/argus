package tui

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// Editable account fields, also the suffix of an srAccountField row key.
const (
	acctFieldLabel  = "label"
	acctFieldClaude = "claude_config_dir"
	acctFieldCodex  = "codex_home"
)

var accountNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// accountEdit is the inline-edit state of the Accounts category. name is empty
// (and field is empty) while a brand-new account's name is being typed.
type accountEdit struct {
	active bool
	name   string
	field  string
	buf    string
}

// loadAccounts snapshots the merged account config (DB rows overridden per key
// by config.toml) plus which entries config.toml defines, for display and
// editability. Called from Refresh right after Config().
func (sv *SettingsView) loadAccounts(cfg config.Config) {
	sv.accounts = cfg.Accounts
	sv.defaultAccount = cfg.DefaultAccount
	sv.projectAccounts = cfg.ProjectAccounts
	sv.accountSources = config.AccountSources{}
	if d, ok := sv.database.(*db.DB); ok {
		sv.accountSources = d.AccountsFromConfigToml()
	}
	sv.accountProjectNames = sv.accountProjectNames[:0]
	for name := range cfg.Projects {
		sv.accountProjectNames = append(sv.accountProjectNames, name)
	}
	sort.Strings(sv.accountProjectNames)
	if _, ok := sv.accounts[sv.pendingAccountDelete]; !ok {
		sv.pendingAccountDelete = ""
	}
	if sv.acctEdit.active && sv.acctEdit.name != "" {
		// Cancel an edit whose account vanished, or that config.toml now defines
		// (the file would silently shadow whatever the edit writes).
		if _, ok := sv.accounts[sv.acctEdit.name]; !ok || sv.accountSources.Accounts[sv.acctEdit.name] {
			sv.acctEdit = accountEdit{}
		}
	}
}

// accountsEditable reports whether accounts can be written from this view: a
// local *db.DB only (--remote mode has no REST write surface for accounts).
func (sv *SettingsView) accountsEditable() bool {
	return !sv.remote && sv.accountDB() != nil
}

func (sv *SettingsView) accountDB() *db.DB {
	d, _ := sv.database.(*db.DB)
	return d
}

// accountNameOptions is the cycle order for the default / project rows:
// "default" first, then the valid configured accounts sorted by name.
func (sv *SettingsView) accountNameOptions() []string {
	return config.Config{Accounts: sv.accounts}.AccountNames()
}

func (sv *SettingsView) accountExists(name string) bool {
	_, ok := sv.accounts[name]
	return ok
}

func accountKey(name, field string) string { return name + "|" + field }

// splitAccountKey splits an srAccountField key. It cuts at the LAST "|" so a
// config.toml account name containing "|" stays intact (field names never do);
// callers must not use it on srAccount rows, whose key is the bare name.
func splitAccountKey(key string) (name, field string) {
	i := strings.LastIndex(key, "|")
	if i < 0 {
		return key, ""
	}
	return key[:i], key[i+1:]
}

// rowAccountName returns the account a row belongs to ("" for non-account rows).
func rowAccountName(row *settingsRow) string {
	switch row.kind {
	case srAccount:
		return row.key
	case srAccountField:
		name, _ := splitAccountKey(row.key)
		return name
	}
	return ""
}

func accountFieldValue(a config.Account, field string) string {
	switch field {
	case acctFieldLabel:
		return a.Label
	case acctFieldClaude:
		return a.ClaudeConfigDir
	case acctFieldCodex:
		return a.CodexHome
	}
	return ""
}

func accountFieldTitle(field string) string {
	switch field {
	case acctFieldLabel:
		return "Label"
	case acctFieldClaude:
		return "Claude config dir"
	case acctFieldCodex:
		return "Codex home"
	}
	return field
}

func withAccountField(a config.Account, field, val string) config.Account {
	switch field {
	case acctFieldLabel:
		a.Label = val
	case acctFieldClaude:
		a.ClaudeConfigDir = val
	case acctFieldCodex:
		a.CodexHome = val
	}
	return a
}

// accountReadOnlyReason explains why account name can't be edited, or "".
func (sv *SettingsView) accountReadOnlyReason(name string) string {
	switch {
	case sv.remote:
		return "read-only in --remote mode"
	case !sv.accountsEditable():
		return "read-only"
	case sv.accountSources.Accounts[name]:
		return "defined in config.toml — read-only"
	}
	return ""
}

func displayAccountName(name string) string {
	if name == "" {
		return config.DefaultAccountName
	}
	return name
}

// accountRows builds the Accounts category rows: default account, each
// account's header + field rows, then one row per project.
func (sv *SettingsView) accountRows() []settingsRow {
	var rows []settingsRow
	marker := ""
	if sv.accountSources.Default {
		marker = " (config.toml)"
	}
	if sv.defaultAccount != "" && sv.defaultAccount != config.DefaultAccountName && !sv.accountExists(sv.defaultAccount) {
		marker += " (missing)"
	}
	rows = append(rows, settingsRow{kind: srAccountDefault, key: "_default",
		label: "Default account: " + displayAccountName(sv.defaultAccount) + marker})

	names := make([]string, 0, len(sv.accounts))
	for n := range sv.accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := sv.accounts[n]
		m := ""
		if sv.accountSources.Accounts[n] {
			m = " (config.toml)"
		}
		rows = append(rows, settingsRow{kind: srAccount, key: n, label: n + m})
		for _, f := range []string{acctFieldLabel, acctFieldClaude, acctFieldCodex} {
			val := accountFieldValue(a, f)
			switch {
			case sv.acctEdit.active && sv.acctEdit.name == n && sv.acctEdit.field == f:
				val = sv.acctEdit.buf + "▎"
			case val == "":
				val = "(not set)"
			}
			rows = append(rows, settingsRow{kind: srAccountField, key: accountKey(n, f),
				label: "    " + accountFieldTitle(f) + ": " + val})
		}
	}
	if sv.acctEdit.active && sv.acctEdit.name == "" {
		rows = append(rows, settingsRow{kind: srAccountField, key: accountKey("", "name"),
			label: "New account name: " + sv.acctEdit.buf + "▎"})
	}

	for _, p := range sv.accountProjectNames {
		val := "(global default)"
		if a := sv.projectAccounts[p]; a != "" {
			val = a
		}
		m := ""
		if sv.accountSources.Projects[p] {
			m = " (config.toml)"
		}
		if a := sv.projectAccounts[p]; a != "" && a != config.DefaultAccountName && !sv.accountExists(a) {
			m += " (missing)"
		}
		rows = append(rows, settingsRow{kind: srProjectAccount, key: p, label: "Project " + p + " → " + val + m})
	}
	return rows
}

// selectAccountRow moves the cursor onto the row with the given kind and key.
func (sv *SettingsView) selectAccountRow(kind settingsRowKind, key string) {
	for i, r := range sv.rows {
		if r.kind == kind && r.key == key {
			sv.cursor = i
			return
		}
	}
}

// validateAccountEdit checks an account the way config.toml entries are
// checked (Config.ValidateAccount) before it is stored.
func (sv *SettingsView) validateAccountEdit(name string, a config.Account) error {
	accounts := maps.Clone(sv.accounts)
	if accounts == nil {
		accounts = map[string]config.Account{}
	}
	accounts[name] = a
	if err := (config.Config{Accounts: accounts}).ValidateAccount(name); err != nil {
		return err
	}
	for other, o := range sv.accounts {
		if other == name {
			continue
		}
		if sameAccountDir(a.ClaudeConfigDir, o.ClaudeConfigDir) || sameAccountDir(a.CodexHome, o.CodexHome) {
			return fmt.Errorf("directory already used by account %q (accounts must not share a login)", other)
		}
	}
	return nil
}

func sameAccountDir(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && b != "" && filepath.Clean(config.ExpandHome(a)) == filepath.Clean(config.ExpandHome(b))
}

// accountReferences counts how many projects (and whether the global default)
// still point at account name.
func (sv *SettingsView) accountReferences(name string) (projects []string, isDefault bool) {
	for p, a := range sv.projectAccounts {
		if a == name {
			projects = append(projects, p)
		}
	}
	sort.Strings(projects)
	return projects, sv.defaultAccount == name
}

func (sv *SettingsView) rejectAccount(format string, args ...any) bool {
	sv.accountErr = fmt.Sprintf(format, args...)
	uxlog.Log("[settings] accounts: %s", sv.accountErr)
	sv.rebuildRows()
	return true
}

// handleNewAccount starts the inline name prompt for a new account.
func (sv *SettingsView) handleNewAccount() bool {
	sv.accountErr = ""
	sv.pendingAccountDelete = ""
	if !sv.accountsEditable() {
		return sv.rejectAccount("Accounts are read-only in --remote mode")
	}
	sv.acctEdit = accountEdit{active: true}
	sv.rebuildRows()
	sv.selectAccountRow(srAccountField, accountKey("", "name"))
	return true
}

// handleEditAccountField starts inline editing of the selected field row.
func (sv *SettingsView) handleEditAccountField() bool {
	if sv.currentRowKind() != srAccountField {
		return false
	}
	name, field := splitAccountKey(sv.SelectedRow().key)
	if name == "" {
		return false
	}
	sv.accountErr = ""
	sv.pendingAccountDelete = ""
	if reason := sv.accountReadOnlyReason(name); reason != "" {
		return sv.rejectAccount("%s: %s", name, reason)
	}
	sv.acctEdit = accountEdit{active: true, name: name, field: field, buf: accountFieldValue(sv.accounts[name], field)}
	sv.rebuildRows()
	return true
}

// commitAccountEdit applies the finished inline edit. A rejected edit leaves
// the stored value unchanged and shows the reason.
func (sv *SettingsView) commitAccountEdit() bool {
	e := sv.acctEdit
	sv.acctEdit = accountEdit{}
	val := strings.TrimSpace(e.buf)
	d := sv.accountDB()
	if d == nil {
		sv.rebuildRows()
		return true
	}
	if e.name == "" {
		return sv.commitNewAccount(d, val)
	}
	updated := withAccountField(sv.accounts[e.name], e.field, val)
	if err := sv.validateAccountEdit(e.name, updated); err != nil {
		return sv.rejectAccount("Rejected %s for %s: %v", accountFieldTitle(e.field), e.name, err)
	}
	if err := d.SetAccount(e.name, updated); err != nil {
		return sv.rejectAccount("Failed to save %s: %v", e.name, err)
	}
	uxlog.Log("[settings] accounts: %s %s updated", e.name, e.field)
	sv.setLocalAccount(e.name, updated)
	return true
}

func (sv *SettingsView) commitNewAccount(d *db.DB, name string) bool {
	switch {
	case name == "":
		sv.rebuildRows() // blank add cancels
		return true
	case !accountNameRE.MatchString(name):
		return sv.rejectAccount("Rejected %q: use letters, digits, - and _ only", name)
	case strings.EqualFold(name, config.DefaultAccountName):
		return sv.rejectAccount("Rejected %q: reserved for the tool defaults", name)
	}
	for existing := range sv.accounts {
		if strings.EqualFold(existing, name) {
			return sv.rejectAccount("Rejected %q: account %q already exists", name, existing)
		}
	}
	a := config.Account{ClaudeConfigDir: "~/.claude-" + name}
	if err := sv.validateAccountEdit(name, a); err != nil {
		return sv.rejectAccount("Rejected %q: %v", name, err)
	}
	if err := d.SetAccount(name, a); err != nil {
		return sv.rejectAccount("Failed to save %s: %v", name, err)
	}
	uxlog.Log("[settings] accounts: added %s (claude_config_dir=%s)", name, a.ClaudeConfigDir)
	sv.setLocalAccount(name, a)
	sv.selectAccountRow(srAccount, name)
	return true
}

func (sv *SettingsView) setLocalAccount(name string, a config.Account) {
	next := maps.Clone(sv.accounts)
	if next == nil {
		next = map[string]config.Account{}
	}
	next[name] = a
	sv.accounts = next
	sv.rebuildRows()
}

// accountTaskCount counts tasks bound to account name, for the delete prompt.
func (sv *SettingsView) accountTaskCount(name string) int {
	tasks, err := sv.database.Tasks()
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.Account == name {
			n++
		}
	}
	return n
}

// handleDeleteAccountRow handles `d` on the Accounts rows: account/field rows
// delete the account (two presses — the first shows how many tasks use it),
// the default/project rows reset their selection.
func (sv *SettingsView) handleDeleteAccountRow() bool {
	row := sv.SelectedRow()
	if row == nil {
		return false
	}
	sv.accountErr = ""
	switch row.kind {
	case srAccountDefault:
		return sv.setDefaultAccount("")
	case srProjectAccount:
		return sv.setProjectAccount(row.key, "")
	case srAccount, srAccountField:
		name := rowAccountName(row)
		if name == "" {
			return false
		}
		if reason := sv.accountReadOnlyReason(name); reason != "" {
			return sv.rejectAccount("%s: %s", name, reason)
		}
		if sv.pendingAccountDelete != name {
			sv.pendingAccountDelete = name
			sv.rebuildRows()
			return true
		}
		sv.pendingAccountDelete = ""
		d := sv.accountDB()
		if d == nil {
			return false
		}
		if err := d.DeleteAccount(name); err != nil {
			return sv.rejectAccount("Failed to delete %s: %v", name, err)
		}
		uxlog.Log("[settings] accounts: deleted %s", name)
		next := maps.Clone(sv.accounts)
		delete(next, name)
		sv.accounts = next
		sv.clearAccountReferences(d, name)
		sv.rebuildRows()
		if sv.cursor >= len(sv.rows) {
			sv.cursor = len(sv.rows) - 1
		}
		return true
	}
	return false
}

// clearAccountReferences resets the default / project selections that pointed
// at a just-deleted account, so tasks do not silently fall through to another
// login. Entries defined in config.toml are left alone (they render "(missing)").
func (sv *SettingsView) clearAccountReferences(d *db.DB, name string) {
	projects, isDefault := sv.accountReferences(name)
	if isDefault && !sv.accountSources.Default {
		if err := d.SetDefaultAccount(""); err != nil {
			uxlog.Log("[settings] accounts: failed to clear default account %q: %v", name, err)
		} else {
			sv.defaultAccount = ""
		}
	}
	for _, p := range projects {
		if sv.accountSources.Projects[p] {
			continue
		}
		if err := d.SetProjectAccount(p, ""); err != nil {
			uxlog.Log("[settings] accounts: failed to clear project %s account %q: %v", p, name, err)
			continue
		}
		next := maps.Clone(sv.projectAccounts)
		delete(next, p)
		sv.projectAccounts = next
	}
	sv.rebuildRows()
}

// cycleAccountRow advances the default-account or a project's account through
// the valid options (Right/Enter; Left returns to the rail, so it wraps).
func (sv *SettingsView) cycleAccountRow() bool {
	row := sv.SelectedRow()
	if row == nil {
		return false
	}
	sv.accountErr = ""
	sv.pendingAccountDelete = ""
	switch row.kind {
	case srAccountDefault:
		if sv.accountSources.Default {
			return sv.rejectAccount("default_account is defined in config.toml — read-only")
		}
		return sv.setDefaultAccount(nextAccountOption(sv.accountNameOptions(), displayAccountName(sv.defaultAccount), false))
	case srProjectAccount:
		if sv.accountSources.Projects[row.key] {
			return sv.rejectAccount("%s is defined in config.toml — read-only", row.key)
		}
		return sv.setProjectAccount(row.key, nextAccountOption(sv.accountNameOptions(), sv.projectAccounts[row.key], true))
	}
	return false
}

// nextAccountOption returns the option after cur, wrapping. With withUnset a
// leading "" (no selection) is part of the cycle.
func nextAccountOption(options []string, cur string, withUnset bool) string {
	all := options
	if withUnset {
		all = append([]string{""}, options...)
	}
	for i, o := range all {
		if o == cur {
			return all[(i+1)%len(all)]
		}
	}
	return all[0]
}

func (sv *SettingsView) setDefaultAccount(name string) bool {
	if sv.accountSources.Default {
		return sv.rejectAccount("default_account is defined in config.toml — read-only")
	}
	d := sv.accountDB()
	if d == nil {
		return sv.rejectAccount("Accounts are read-only in --remote mode")
	}
	if name == config.DefaultAccountName {
		name = ""
	}
	if err := d.SetDefaultAccount(name); err != nil {
		return sv.rejectAccount("Failed to save default account: %v", err)
	}
	uxlog.Log("[settings] accounts: default account set to %q", name)
	sv.defaultAccount = name
	sv.rebuildRows()
	return true
}

func (sv *SettingsView) setProjectAccount(project, account string) bool {
	if sv.accountSources.Projects[project] {
		return sv.rejectAccount("%s is defined in config.toml — read-only", project)
	}
	d := sv.accountDB()
	if d == nil {
		return sv.rejectAccount("Accounts are read-only in --remote mode")
	}
	if err := d.SetProjectAccount(project, account); err != nil {
		return sv.rejectAccount("Failed to save %s account: %v", project, err)
	}
	uxlog.Log("[settings] accounts: project %s account set to %q", project, account)
	next := maps.Clone(sv.projectAccounts)
	if next == nil {
		next = map[string]string{}
	}
	if account == "" {
		delete(next, project)
	} else {
		next[project] = account
	}
	sv.projectAccounts = next
	sv.rebuildRows()
	return true
}

// handleAccountEditKey handles keystrokes while an account field (or a new
// account's name) is being edited inline. Enter saves, Escape cancels.
func (sv *SettingsView) handleAccountEditKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEnter:
		return sv.commitAccountEdit()
	case tcell.KeyEscape:
		sv.acctEdit = accountEdit{}
		sv.rebuildRows()
		return true
	case tcell.KeyDown, tcell.KeyUp, tcell.KeyLeft, tcell.KeyRight:
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(sv.acctEdit.buf) > 0 {
			_, size := utf8.DecodeLastRuneInString(sv.acctEdit.buf)
			sv.acctEdit.buf = sv.acctEdit.buf[:len(sv.acctEdit.buf)-size]
			sv.rebuildRows()
		}
		return true
	case tcell.KeyRune:
		sv.acctEdit.buf += string(ev.Rune())
		sv.rebuildRows()
		return true
	}
	return false
}

// pasteAccountText appends pasted text (single line) to the active account edit.
func (sv *SettingsView) pasteAccountText(text string) {
	sv.acctEdit.buf += strings.Join(strings.Fields(text), " ")
	sv.rebuildRows()
}

// renderAccountsDetail draws the detail block under the Accounts rows.
func (sv *SettingsView) renderAccountsDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	r := 0
	line := func(text string, style tcell.Style) {
		if r < h-1 {
			widget.DrawText(screen, x, y+r, w, text, style)
		}
		r++
	}

	name := rowAccountName(row)
	switch row.kind {
	case srAccountDefault:
		line("Default account", theme.StyleTitle)
		line("Used for new tasks that pick no account and whose project has no", theme.StyleDimmed)
		line("project default. \"default\" is each tool's own login (~/.claude, ~/.codex).", theme.StyleDimmed)
		if sv.accountSources.Default {
			line("(config.toml-defined — read-only)", theme.StyleDimmed)
		}
	case srProjectAccount:
		line("Project default: "+row.key, theme.StyleTitle)
		line("New tasks in this project use this account unless one is picked.", theme.StyleDimmed)
		line("Pin company projects to your work account so company code never", theme.StyleDimmed)
		line("runs under a personal login.", theme.StyleDimmed)
		if sv.accountSources.Projects[row.key] {
			line("(config.toml-defined — read-only)", theme.StyleDimmed)
		}
	default:
		if name == "" {
			line("New account", theme.StyleTitle)
			line("Letters, digits, - and _. Seeded with ~/.claude-<name>.", theme.StyleDimmed)
			break
		}
		a := sv.accounts[name]
		line("Account "+name, theme.StyleTitle)
		if reason := sv.accountReadOnlyReason(name); reason != "" {
			line("("+reason+")", theme.StyleDimmed)
		}
		r++
		line("  Claude config dir: "+orNotSet(a.ClaudeConfigDir), theme.StyleDimmed)
		line("  Codex home:        "+orNotSet(a.CodexHome), theme.StyleDimmed)
		r++
		if a.ClaudeConfigDir != "" {
			line("First login (Claude): start a task on this account, then run /login.", theme.StyleDimmed)
		}
		if a.CodexHome != "" {
			line("First login (Codex): CODEX_HOME="+a.CodexHome+" codex login", theme.StyleDimmed)
		}
		if sv.pendingAccountDelete == name {
			projects, isDefault := sv.accountReferences(name)
			warn := tcell.StyleDefault.Foreground(theme.ColorError)
			line("Delete "+name+"? Press d again to confirm.", warn)
			line(fmt.Sprintf("  %d task(s) use it and will fail to resume", sv.accountTaskCount(name)), warn)
			if isDefault {
				line("  it is the default account (will reset)", warn)
			}
			if len(projects) > 0 {
				line(fmt.Sprintf("  %d project default(s) will reset", len(projects)), warn)
			}
		}
	}
	if sv.accountErr != "" {
		r++
		line(sv.accountErr, tcell.StyleDefault.Foreground(theme.ColorError))
	}

	hint := "[n] add  [e] edit field  [d] delete/reset  [→/enter] cycle  [◀] rail"
	if sv.acctEdit.active {
		hint = "[enter] save  [esc] cancel"
	}
	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, hint, theme.StyleDimmed)
	}
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
