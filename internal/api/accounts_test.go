package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudeaccount"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func accountsServer(t *testing.T, toml string) (*Server, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644))
	creator := func(name, prompt, project, backend, taskModel, sandboxOverride, account string, _ bool) (*model.Task, error) {
		task := &model.Task{Name: name, Prompt: prompt, Project: project, Backend: backend, Account: account, Status: model.StatusInProgress}
		return task, d.Add(task)
	}
	return New(d, agent.NewRunner(nil), "test-token", creator, nil), d
}

const twoAccountsTOML = `
default_account = "work"

[accounts.work]
claude_config_dir = "/tmp/argus-test-work"
label = "Work"

[accounts.personal]
claude_config_dir = "/tmp/argus-test-personal"
codex_home = "/tmp/argus-test-personal-codex"

[accounts.codexonly]
codex_home = "/tmp/argus-test-codexonly"
`

func stubAuth(t *testing.T, fn func(ctx context.Context, dir string) (claudeaccount.Status, error)) {
	t.Helper()
	old := claudeAuthStatus
	claudeAuthStatus = fn
	t.Cleanup(func() { claudeAuthStatus = old })
}

func TestHandleCreateTask_Account(t *testing.T) {
	srv, d := accountsServer(t, twoAccountsTOML)
	mux := srv.routes()

	t.Run("persists named account", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"a","prompt":"p","project":"proj","account":"personal"}`))
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, err := d.Get(resp["id"].(string))
		testutil.NoError(t, err)
		testutil.Equal(t, got.Account, "personal")
	})

	t.Run("unknown account is 400 and creates nothing", func(t *testing.T) {
		before, _ := d.Tasks()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"b","prompt":"p","project":"proj","account":"nope"}`))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		testutil.Contains(t, w.Body.String(), "nope")
		after, _ := d.Tasks()
		testutil.Equal(t, len(after), len(before))
	})

	t.Run("account lacking the explicit backend's tool is 400", func(t *testing.T) {
		testutil.NoError(t, d.SetBackend("cx", config.Backend{Command: "codex"}))
		before, _ := d.Tasks()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"cx","prompt":"p","project":"proj","backend":"cx","account":"work"}`))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		testutil.Contains(t, w.Body.String(), "does not support backend")
		after, _ := d.Tasks()
		testutil.Equal(t, len(after), len(before))
	})

	t.Run("explicit default account is kept, not blanked", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"d","prompt":"p","project":"proj","account":"default"}`))
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, _ := d.Get(resp["id"].(string))
		testutil.Equal(t, got.Account, "default")
	})

	t.Run("omitted account passes empty to creator", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"c","prompt":"p","project":"proj"}`))
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, _ := d.Get(resp["id"].(string))
		testutil.Equal(t, got.Account, "")
	})
}

// A creator failing with agent.ErrAccount (implicit-backend mismatch that only
// CreateAndStart can see) must surface as 400, not 500.
func TestHandleCreateTask_ErrAccountIs400(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	creator := func(_, _, _, _, _, _, _ string, _ bool) (*model.Task, error) {
		return nil, fmt.Errorf("%w: account %q does not support backend %q", agent.ErrAccount, "work", "x")
	}
	mux := New(d, agent.NewRunner(nil), "test-token", creator, nil).routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"a","prompt":"p","project":"proj"}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)

	other := func(_, _, _, _, _, _, _ string, _ bool) (*model.Task, error) { return nil, errors.New("boom") }
	mux = New(d, agent.NewRunner(nil), "test-token", other, nil).routes()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks", `{"name":"a","prompt":"p","project":"proj"}`))
	testutil.Equal(t, w.Code, http.StatusInternalServerError)
}

func TestHandleForkTask_InheritsAccount(t *testing.T) {
	srv, d := accountsServer(t, twoAccountsTOML)
	src := &model.Task{Name: "src-acct", Status: model.StatusComplete, Project: "proj1", Backend: "claude", Account: "personal", Prompt: "p"}
	testutil.NoError(t, d.Add(src))

	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+src.ID+"/fork", `{}`))
	testutil.Equal(t, w.Code, http.StatusCreated)
	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	got, err := d.Get(resp["id"].(string))
	testutil.NoError(t, err)
	testutil.Equal(t, got.Account, "personal")
}

func TestHandleListAccounts(t *testing.T) {
	t.Run("default first, is_default follows config, sign-in merged", func(t *testing.T) {
		stubAuth(t, func(_ context.Context, dir string) (claudeaccount.Status, error) {
			if dir == "/tmp/argus-test-codexonly" {
				t.Errorf("sign-in lookup ran for a codex-only account")
			}
			if dir == "/tmp/argus-test-work" {
				return claudeaccount.Status{LoggedIn: true, Email: "w@x.com", Org: "Org", Plan: "max"}, nil
			}
			return claudeaccount.Status{}, errors.New("boom")
		})
		srv, _ := accountsServer(t, twoAccountsTOML)
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/accounts", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		var got []accountJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		testutil.Equal(t, len(got), 4)
		testutil.Equal(t, got[0].Name, "default")
		testutil.False(t, got[0].IsDefault)
		testutil.False(t, got[0].LoggedIn)
		testutil.Equal(t, got[0].Supports, accountSupportsJSON{Claude: true, Codex: true})
		testutil.Equal(t, got[1].Name, "codexonly")
		testutil.Equal(t, got[1].ClaudeConfigDir, "")
		testutil.Equal(t, got[1].CodexHome, "/tmp/argus-test-codexonly")
		testutil.Equal(t, got[1].Supports, accountSupportsJSON{Codex: true})
		testutil.False(t, got[1].LoggedIn)
		testutil.Equal(t, got[2].Name, "personal")
		testutil.False(t, got[2].LoggedIn)
		testutil.Equal(t, got[2].CodexHome, "/tmp/argus-test-personal-codex")
		testutil.Equal(t, got[2].Supports, accountSupportsJSON{Claude: true, Codex: true})
		testutil.Equal(t, got[3].Name, "work")
		testutil.True(t, got[3].IsDefault)
		testutil.True(t, got[3].LoggedIn)
		testutil.Equal(t, got[3].Label, "Work")
		testutil.Equal(t, got[3].Email, "w@x.com")
		testutil.Equal(t, got[3].ClaudeConfigDir, "/tmp/argus-test-work")
		testutil.Equal(t, got[3].Supports, accountSupportsJSON{Claude: true})
	})

	t.Run("no accounts configured yields default only", func(t *testing.T) {
		stubAuth(t, func(context.Context, string) (claudeaccount.Status, error) { return claudeaccount.Status{}, nil })
		srv, _ := accountsServer(t, "")
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/accounts", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		var got []accountJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].Name, "default")
		testutil.True(t, got[0].IsDefault)
	})
}

func TestHandleListAccounts_ProjectDefault(t *testing.T) {
	stubAuth(t, func(context.Context, string) (claudeaccount.Status, error) { return claudeaccount.Status{}, nil })
	srv, _ := accountsServer(t, twoAccountsTOML+"\n[project_accounts]\nside = \"personal\"\n")
	isDefault := func(path string) []string {
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", path, ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		var got []accountJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		var out []string
		for _, a := range got {
			if a.IsDefault {
				out = append(out, a.Name)
			}
		}
		return out
	}
	for _, tc := range []struct{ path, want string }{
		{"/api/accounts", "work"},
		{"/api/accounts?project=side", "personal"},
		{"/api/accounts?project=other", "work"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			testutil.DeepEqual(t, isDefault(tc.path), []string{tc.want})
		})
	}
}

func TestHandleListSkills_AccountConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkSkill := func(dir, name string) {
		p := filepath.Join(dir, "skills", name)
		testutil.NoError(t, os.MkdirAll(p, 0o755))
		testutil.NoError(t, os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644))
	}
	workDir := filepath.Join(home, "work")
	mkSkill(filepath.Join(home, ".claude"), "home-skill")
	mkSkill(workDir, "work-skill")
	srv, d := accountsServer(t, `
[accounts.work]
claude_config_dir = "`+workDir+`"

[accounts.cx]
codex_home = "/tmp/argus-test-cx"

[project_accounts]
wp = "work"
`)
	tk := &model.Task{Name: "t", Project: "other", Account: "work", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(tk))

	names := func(path string) (int, []string) {
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", path, ""))
		var body struct {
			Skills []skillJSON `json:"skills"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		var out []string
		for _, s := range body.Skills {
			out = append(out, s.Name)
		}
		return w.Code, out
	}
	for _, tc := range []struct {
		name, path string
		want       []string
	}{
		{"no account uses ~/.claude", "/api/skills", []string{"home-skill"}},
		{"explicit account", "/api/skills?account=work", []string{"work-skill"}},
		{"explicit default overrides project default", "/api/skills?project=wp&account=default", []string{"home-skill"}},
		{"project default account", "/api/skills?project=wp", []string{"work-skill"}},
		{"task account", "/api/skills?task=" + tk.ID, []string{"work-skill"}},
		{"account without claude dir falls back", "/api/skills?account=cx", []string{"home-skill"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, got := names(tc.path)
			testutil.Equal(t, code, http.StatusOK)
			testutil.DeepEqual(t, got, tc.want)
		})
	}
	t.Run("unknown account is 400", func(t *testing.T) {
		code, _ := names("/api/skills?account=nope")
		testutil.Equal(t, code, http.StatusBadRequest)
	})
	t.Run("unknown task is 400", func(t *testing.T) {
		code, _ := names("/api/skills?task=nope")
		testutil.Equal(t, code, http.StatusBadRequest)
	})
}

func TestParseMultipartTaskForm_ReadsAccount(t *testing.T) {
	body := "--b\r\nContent-Disposition: form-data; name=\"account\"\r\n\r\nwork\r\n--b--\r\n"
	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	_, _, _, _, _, _, acct, _, err := parseMultipartTaskForm(req)
	testutil.NoError(t, err)
	testutil.Equal(t, acct, "work")
}
