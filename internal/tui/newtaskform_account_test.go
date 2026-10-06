package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/apistore"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

var _ remoteAccountLister = (*apistore.Store)(nil)

func accountTestConfig() config.Config {
	return config.Config{
		Accounts: map[string]config.Account{
			"work": {ClaudeConfigDir: "/tmp/work"},
			"cx":   {CodexHome: "/tmp/cx"},
			"both": {ClaudeConfigDir: "/tmp/both", CodexHome: "/tmp/both-cx"},
		},
		ProjectAccounts: map[string]string{"q": "work", "c": "cx"},
	}
}

// accountForm builds a form with a claude backend "b" and a codex backend "x".
func accountForm(t *testing.T, backend string) *NewTaskForm {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := accountTestConfig()
	f := NewNewTaskForm(
		map[string]config.Project{"p": {Path: "/tmp/p"}, "q": {Path: "/tmp/q"}, "c": {Path: "/tmp/c"}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}, "x": {Command: "codex"}, "o": {Command: "opencode"}}, backend,
	)
	f.SetAccounts(AccountOptionsFromConfig(cfg), func(project string) string {
		return cfg.ResolveAccount("", project)
	})
	return f
}

func tabKey(f *NewTaskForm) {
	f.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(tview.Primitive) {})
}

func key(f *NewTaskForm, k tcell.Key) {
	f.InputHandler()(tcell.NewEventKey(k, 0, 0), func(tview.Primitive) {})
}

func selectBackend(f *NewTaskForm, name string) {
	f.focused = ntFieldBackend
	for i := 0; i < len(f.backendNames) && f.backendNames[f.backendIdx] != name; i++ {
		key(f, tcell.KeyRight)
	}
}

func TestNewTaskForm_Account(t *testing.T) {
	t.Run("hidden with no accounts and tab order unchanged", func(t *testing.T) {
		f := optNameForm(t)
		testutil.False(t, f.hasAccountField())
		f.focused = ntFieldSandbox
		tabKey(f)
		testutil.Equal(t, f.focused, ntFieldPrompt)
		testutil.Equal(t, f.Task().Account, "")
	})

	t.Run("visible in tab order after sandbox", func(t *testing.T) {
		f := accountForm(t, "b")
		f.focused = ntFieldSandbox
		tabKey(f)
		testutil.Equal(t, f.focused, ntFieldAccount)
		tabKey(f)
		testutil.Equal(t, f.focused, ntFieldPrompt)
	})

	t.Run("explicit default pick submits default", func(t *testing.T) {
		f := accountForm(t, "b")
		testutil.Equal(t, f.Task().Account, "default")
	})

	t.Run("project default preselect still submits the explicit name", func(t *testing.T) {
		f := accountForm(t, "b")
		f.projInput = []rune("q")
		f.onProjectChanged()
		testutil.Equal(t, f.Task().Account, "work")
		f.projInput = []rune("p")
		f.onProjectChanged()
		testutil.Equal(t, f.Task().Account, "default")
	})

	t.Run("cycling selects account", func(t *testing.T) {
		f := accountForm(t, "b")
		f.focused = ntFieldAccount
		key(f, tcell.KeyRight)
		testutil.Equal(t, f.Task().Account, "both")
		key(f, tcell.KeyRight)
		testutil.Equal(t, f.Task().Account, "work")
	})

	t.Run("options filtered by backend tool", func(t *testing.T) {
		f := accountForm(t, "b")
		testutil.DeepEqual(t, f.accountNames, []string{"default", "both", "work"})
		selectBackend(f, "x")
		testutil.DeepEqual(t, f.accountNames, []string{"default", "both", "cx"})
	})

	t.Run("backend change keeps a selection that still applies", func(t *testing.T) {
		f := accountForm(t, "b")
		f.focused = ntFieldAccount
		key(f, tcell.KeyRight)
		testutil.Equal(t, f.Account(), "both")
		selectBackend(f, "x")
		testutil.Equal(t, f.Account(), "both")
	})

	t.Run("backend change drops a selection that no longer applies", func(t *testing.T) {
		f := accountForm(t, "b")
		f.projInput = []rune("c")
		f.onProjectChanged()
		testutil.Equal(t, f.Account(), "default")
		f.focused = ntFieldAccount
		key(f, tcell.KeyLeft)
		testutil.Equal(t, f.Account(), "work")
		selectBackend(f, "x")
		testutil.Equal(t, f.Account(), "cx")
	})

	t.Run("hidden when only default applies", func(t *testing.T) {
		f := accountForm(t, "o")
		testutil.False(t, f.hasAccountField())
		testutil.Equal(t, f.Task().Account, "")
		f.focused = ntFieldSandbox
		tabKey(f)
		testutil.Equal(t, f.focused, ntFieldPrompt)
	})

	t.Run("label appended to display name", func(t *testing.T) {
		f := accountForm(t, "b")
		f.SetAccountLabel("work", "me@x.com")
		testutil.Equal(t, f.accountDisplayNames()[2], "work (me@x.com)")
	})

	t.Run("configured label is the fallback display label", func(t *testing.T) {
		f := accountForm(t, "b")
		f.SetAccounts([]AccountOption{{Name: "work", Label: "Work", Claude: true}}, nil)
		testutil.Equal(t, f.accountDisplayNames()[1], "work (Work)")
	})
}

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, "skills", name)
	testutil.NoError(t, os.MkdirAll(p, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o644))
}

func skillNames(f *NewTaskForm) []string {
	var out []string
	for _, s := range f.skills {
		if strings.HasPrefix(s.Name, "argus-") || strings.HasPrefix(s.Name, "hera") {
			continue
		}
		out = append(out, s.Name)
	}
	return out
}

func TestNewTaskForm_AccountSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(home, ".claude"), "home-skill")
	workDir := filepath.Join(home, "work-claude")
	writeSkill(t, workDir, "work-skill")

	cfg := config.Config{
		Accounts:        map[string]config.Account{"work": {ClaudeConfigDir: workDir}},
		ProjectAccounts: map[string]string{"q": "work"},
	}
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}, "q": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}, "o": {Command: "opencode"}}, "b",
	)
	f.SetAccounts(AccountOptionsFromConfig(cfg), func(project string) string { return cfg.ResolveAccount("", project) })

	t.Run("default account reads ~/.claude", func(t *testing.T) {
		testutil.DeepEqual(t, skillNames(f), []string{"home-skill"})
	})
	t.Run("selected account reads its config dir", func(t *testing.T) {
		f.focused = ntFieldAccount
		key(f, tcell.KeyRight)
		testutil.Equal(t, f.Account(), "work")
		testutil.DeepEqual(t, skillNames(f), []string{"work-skill"})
	})
	t.Run("project default account reads its config dir", func(t *testing.T) {
		f.projInput = []rune("q")
		f.onProjectChanged()
		testutil.DeepEqual(t, skillNames(f), []string{"work-skill"})
	})
	t.Run("hidden field still follows resolved default", func(t *testing.T) {
		selectBackend(f, "o")
		testutil.False(t, f.hasAccountField())
		testutil.DeepEqual(t, skillNames(f), []string{"home-skill"})
	})
}

func TestAccountOptionsFromRemote(t *testing.T) {
	opts, labels := accountOptionsFromRemote([]apiclient.AccountJSON{
		{Name: "default", Supports: apiclient.AccountSupportsJSON{Claude: true, Codex: true}},
		{Name: "work", Label: "Work", ClaudeConfigDir: "/remote/w", Supports: apiclient.AccountSupportsJSON{Claude: true}, LoggedIn: true, Email: "w@x.com"},
		{Name: "off", Supports: apiclient.AccountSupportsJSON{Claude: true}},
		{Name: "cx", Supports: apiclient.AccountSupportsJSON{Codex: true}},
	})
	testutil.DeepEqual(t, opts, []AccountOption{
		{Name: "work", Label: "Work", Claude: true},
		{Name: "off", Claude: true},
		{Name: "cx", Codex: true},
	})
	testutil.DeepEqual(t, labels, map[string]string{"work": "w@x.com", "off": "signed out"})
}

// accountStore is a remote-shaped store listing accounts; its local config
// has no accounts, so any options must come from Accounts().
type accountStore struct {
	stubStore
	list []apiclient.AccountJSON
}

func (s accountStore) Accounts(context.Context) ([]apiclient.AccountJSON, error) { return s.list, nil }

func TestSetupNewTaskAccounts_RemoteUsesStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tApp, _, _ := simApp(t)
	tApp.SetRoot(tview.NewBox(), true)
	stop := runApp(t, tApp)
	defer stop()

	st := accountStore{list: []apiclient.AccountJSON{
		{Name: "default"},
		{Name: "work", Supports: apiclient.AccountSupportsJSON{Claude: true}, LoggedIn: true, Email: "w@x.com"},
	}}
	a := &App{db: st, tapp: tApp}
	f := NewNewTaskForm(map[string]config.Project{"p": {}}, "p", map[string]config.Backend{"b": {Command: "claude"}}, "b")
	readUI(t, tApp, func() {
		a.newTaskForm = f
		a.setupNewTaskAccounts(f, config.Config{})
	})

	var names []string
	var label string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		readUI(t, tApp, func() { names, label = f.accountNames, f.accountLabels["work"] })
		if len(names) > 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	testutil.DeepEqual(t, names, []string{"default", "work"})
	testutil.Equal(t, label, "w@x.com")
}

func TestSmoke_NewTaskFormAccountField(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	testutil.NoError(t, d.SetProject("test", config.Project{Path: t.TempDir()}))
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, 'n', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		app.newTaskForm.SetAccounts([]AccountOption{{Name: "work", Claude: true, Codex: true}}, func(string) string { return "default" })
		app.newTaskForm.focused = ntFieldAccount
	})
	app.tapp.QueueUpdateDraw(func() {})
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRight, 0, 0)
	syncUI(t, app.tapp)

	var got string
	readUI(t, app.tapp, func() { got = app.newTaskForm.Account() })
	testutil.Equal(t, got, "work")
	sim.Show()
	testutil.True(t, anyContains(screenRows(sim), "Account:"))
}
