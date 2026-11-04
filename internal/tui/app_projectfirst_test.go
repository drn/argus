package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

func TestSmoke_ProjectFirst(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, entry := range []string{"task key", "coordinator key", "task palette", "coordinator palette"} {
			name := "off/" + entry
			if enabled {
				name = "on/" + entry
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				d := testDB(t)
				if enabled {
					testutil.NoError(t, d.SetConfigValue("ui.project_first", "true"))
				}
				testutil.NoError(t, d.SetConfigValue(db.ConfigKeyDefaultBackend, "codex"))
				testutil.NoError(t, d.SetAccount("work", config.Account{CodexHome: t.TempDir()}))
				testutil.NoError(t, d.SetProjectAccount("beta", "work"))
				for p, cfg := range map[string]config.Project{
					"alpha": {Branch: "main"}, "beta": {Branch: "develop", Profile: "customer_grade"},
					"gamma": {}, "unused": {},
				} {
					testutil.NoError(t, d.SetProject(p, cfg))
				}
				for i, p := range []string{"alpha", "beta", "gamma"} {
					testutil.NoError(t, d.Add(&model.Task{ID: p, Name: p, Project: p, Status: model.StatusPending, CreatedAt: time.Unix(int64(i+1), 0)}))
				}
				app := New(d, agent.NewRunner(nil), false)
				sim, stop := wireApp(t, app)
				defer stop()
				// A FIFO key barrier flushes complete pastes and key bursts without sleeps.
				processed := make(chan struct{}, 1)
				app.tapp.QueueUpdateDraw(func() {
					app.tapp.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
						if e.Key() == tcell.KeyF12 {
							processed <- struct{}{}
							return nil
						}
						return app.handleGlobalKey(e)
					})
				})
				flush := func() {
					t.Helper()
					app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyF12, 0, 0))
					select {
					case <-processed:
					case <-time.After(uiTimeout):
						t.Fatal("events did not drain")
					}
				}
				press := func(k tcell.Key) { app.tapp.QueueEvent(tcell.NewEventKey(k, 0, 0)); flush() }
				typeText := func(text string, paste bool) {
					if paste {
						app.tapp.QueueEvent(tcell.NewEventPaste(true))
					}
					for _, r := range text {
						app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
					}
					if paste {
						app.tapp.QueueEvent(tcell.NewEventPaste(false))
					}
					flush()
				}
				coordinator := strings.HasPrefix(entry, "coordinator")
				open := func() {
					app.tapp.QueueUpdateDraw(func() {
						if coordinator {
							app.switchTab(widget.TabHera)
						}
						if strings.HasSuffix(entry, "palette") {
							if coordinator {
								app.heraRailActionRegistry()[keymap.ActHeraNewCoord]()
							} else {
								app.taskListActionRegistry()[keymap.ActTaskNew]()
							}
						}
					})
					if strings.HasSuffix(entry, "key") {
						typeText("n", false)
					}
					flush()
				}
				open()
				var form *NewTaskForm
				readUI(t, app.tapp, func() {
					form = app.newTaskForm
					testutil.Equal(t, form != nil, true)
					testutil.Equal(t, form.projectStep, enabled)
					if enabled {
						testutil.Equal(t, string(form.projInput), "")
						testutil.DeepEqual(t, form.projACMatches, []string{"alpha", "gamma", "beta", "unused"})
						testutil.Equal(t, form.projACMatches[form.projACIdx], "alpha")
						sim.Show()
						cells, w, h := sim.GetContents()
						var b strings.Builder
						for y := range h {
							for x := range w {
								if rs := cells[y*w+x].Runes; len(rs) > 0 {
									b.WriteRune(rs[0])
								}
							}
							b.WriteByte('\n')
						}
						testutil.Contains(t, b.String(), "Choose project")
						testutil.Equal(t, strings.Contains(b.String(), "Backend:"), false)
					} else {
						testutil.Equal(t, form.focused, ntFieldPrompt)
						testutil.Equal(t, form.SelectedProject(), "alpha")
					}
				})
				if enabled {
					// No backspacing: typing replaces the default highlight.
					typeText("beta", false)
					press(tcell.KeyEnter)
					readUI(t, app.tapp, func() {
						testutil.Equal(t, form.projectStep, false)
						testutil.Equal(t, form.focused, ntFieldPrompt)
						testutil.Equal(t, form.SelectedProject(), "beta")
						testutil.Equal(t, form.resolvedBranch(), "develop")
						testutil.Equal(t, form.boundProfile(), "customer_grade")
						testutil.Equal(t, form.Account(), "work")
						testutil.Equal(t, form.Task().Backend, "codex")
						testutil.Equal(t, form.SandboxOverride(), "")
					})
				}
				typeText("keep this prompt", true)
				press(tcell.KeyTab)
				typeText("keep-name", true)
				if enabled {
					press(tcell.KeyEscape)
					readUI(t, app.tapp, func() {
						testutil.Equal(t, form.projectStep, true)
						testutil.Equal(t, form.projACMatches[form.projACIdx], "beta")
					})
					typeText("gamma", true)
					press(tcell.KeyEnter)
					readUI(t, app.tapp, func() {
						testutil.Equal(t, form.SelectedProject(), "gamma")
						testutil.Equal(t, form.resolvedBranch(), "")
						testutil.Equal(t, form.boundProfile(), "default")
						testutil.Equal(t, form.Account(), "default")
						testutil.Equal(t, string(form.prompt), "keep this prompt")
						testutil.Equal(t, form.EnteredName(), "keep-name")
						testutil.Equal(t, form.focused, ntFieldPrompt)
					})
					press(tcell.KeyEscape)
				} else {
					// Existing prompt → optional name → project traversal is unchanged.
					press(tcell.KeyTab)
					readUI(t, app.tapp, func() { testutil.Equal(t, form.focused, ntFieldProject) })
				}
				press(tcell.KeyEscape)
				readUI(t, app.tapp, func() {
					testutil.Nil(t, app.newTaskForm)
					testutil.Nil(t, app.newTaskOnDone)
					testutil.Equal(t, app.mode, modeTaskList)
					if coordinator {
						testutil.Equal(t, app.tapp.GetFocus() == app.heraPage, true)
					} else {
						testutil.Equal(t, app.tapp.GetFocus() == app.tasklist, true)
					}
				})
				open()
				readUI(t, app.tapp, func() {
					f := app.newTaskForm
					if enabled {
						testutil.Equal(t, f.projACMatches[f.projACIdx], "alpha")
					}
					testutil.Equal(t, string(f.prompt), "")
				})
				press(tcell.KeyCtrlQ)
				tasks, err := d.Tasks()
				testutil.NoError(t, err)
				testutil.Equal(t, len(tasks), 3)
			})
		}
	}
}

func TestProjectFirst_WorkerBypassAndBranchResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{}))
	testutil.NoError(t, d.SetProject("q", config.Project{}))
	testutil.NoError(t, d.SetConfigValue("ui.project_first", "true"))
	app := New(d, agent.NewRunner(nil), false)
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app.heraSpawnWorker(heraCoordSel(orch, "tc"))
	f := app.newTaskForm
	testutil.Equal(t, f.projectStep, false)
	testutil.Equal(t, f.focused, ntFieldPrompt)
	testutil.Equal(t, f.SelectedProject(), "p")
	f.branchPath = "/p"
	app.applyNewTaskBranches(f, "/q", []string{"wrong project"})
	testutil.Equal(t, len(f.branchACAll), 0)
	app.applyNewTaskBranches(f, "/p", []string{"main"})
	testutil.DeepEqual(t, f.branchACAll, []string{"main"})
	app.closeNewTaskForm()
	app.heraNewCoordinator(hera.Selection{})
	app.applyNewTaskBranches(f, "/p", []string{"old form"})
	testutil.Equal(t, len(app.newTaskForm.branchACAll), 0)
}

func TestProjectFirst_RemoteBypass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetProject("alpha", config.Project{}))
	testutil.NoError(t, d.SetProject("beta", config.Project{}))
	testutil.NoError(t, d.SetConfigValue("ui.project_first", "true"))
	// Same non-local Store shape as apistore, with no HTTP/daemon dependencies.
	app := New(&nonRemoteStore{d}, agent.NewRunner(nil), false)
	app.onNewTask()
	testutil.Equal(t, app.newTaskForm.projectStep, false)
	testutil.Equal(t, app.newTaskForm.focused, ntFieldPrompt)
}

func TestProjectFirst_CoordinatorContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{}))
	// The coordinator's project must beat the alphabetical fallback.
	testutil.NoError(t, d.SetProject("alpha", config.Project{}))
	testutil.NoError(t, d.SetConfigValue("ui.project_first", "true"))
	app := New(d, agent.NewRunner(nil), false)
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app.heraNewCoordinator(heraCoordSel(orch, "tc"))
	f := app.newTaskForm
	testutil.Equal(t, f.projectStep, true)
	testutil.Equal(t, f.projACMatches[f.projACIdx], "p")
	testutil.Equal(t, f.projACMatches[0], "p")
	testutil.Equal(t, f.hideArchetype, true)
}

func TestProjectFirst_PreferenceAffectsNextOpening(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetProject("alpha", config.Project{}))
	testutil.NoError(t, d.SetProject("beta", config.Project{}))
	app := New(d, agent.NewRunner(nil), false)
	app.onNewTask()
	testutil.Equal(t, app.newTaskForm.projectStep, false)
	app.closeNewTaskForm()
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enable", false: "disable"}[enabled], func(t *testing.T) {
			selectRowInCategory(t, app.settings, catAppearance, srProjectFirst, "")
			app.settings.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
			app.onNewTask()
			testutil.Equal(t, app.newTaskForm.projectStep, enabled)
			app.closeNewTaskForm()
		})
	}
}
