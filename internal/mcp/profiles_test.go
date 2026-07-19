package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// testProfileServer builds a Server over a temp-HOME in-memory DB with task
// management and the profile resolver wired. Mirrors testHeraServer's
// temp-HOME discipline (CLAUDE.md: tests never touch real ~/.argus).
func testProfileServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	s := New(d, 0, "")
	s.SetTaskManager(
		func(input TaskCreateInput) (*model.Task, error) {
			return nil, fmt.Errorf("task creation not supported in this test")
		},
		d,
		&mockStopper{},
	)
	s.SetProfileResolver(d)
	return s, d
}

// writeLibraryProfile writes <HOME>/.argus/profiles/<name>.toml. The caller
// MUST have already t.Setenv("HOME", ...) so db.DataDir() resolves under the
// temp HOME.
func writeLibraryProfile(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(db.DataDir(), "profiles")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, name+".toml"), []byte(content), 0o644))
}

// addProfileTestTask inserts a task with the given project/worktree/profile
// override into the DB and returns it.
func addProfileTestTask(t *testing.T, d *db.DB, project, worktree, profile string) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:     "test-task",
		Status:   model.StatusInProgress,
		Project:  project,
		Worktree: worktree,
		Profile:  profile,
	}
	testutil.NoError(t, d.Add(task))
	return task
}

// profileResolveResult mirrors the tool's JSON output shape for assertions.
type profileResolveResult struct {
	Resolved  bool                       `json:"resolved"`
	Name      string                     `json:"name"`
	Source    string                     `json:"source"`
	Archetype map[string]json.RawMessage `json:"archetype"`
	Rigor     map[string]any             `json:"rigor"`
	Panel     map[string]any             `json:"panel"`
	Errors    []string                   `json:"errors"`
}

func callProfileResolve(t *testing.T, s *Server, args string) (profileResolveResult, ToolCallResult) {
	t.Helper()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "profile_resolve",
		Arguments: json.RawMessage(args),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	var out profileResolveResult
	if !cr.IsError {
		testutil.NoError(t, json.Unmarshal([]byte(cr.Content[0].Text), &out))
	}
	return out, cr
}

func TestProfileResolve_ByCwd(t *testing.T) {
	s, d := testProfileServer(t)
	worktree := t.TempDir()
	testutil.NoError(t, d.SetProject("myproj", config.Project{Path: worktree, Profile: "customer_grade"}))
	addProfileTestTask(t, d, "myproj", worktree, "")
	writeLibraryProfile(t, "customer_grade", `
[archetype.review]
model  = "opus"
effort = "high"

[panel]
finders = ["opus", "fable"]
fix_verification = true
`)

	cwd := filepath.Join(worktree, "sub", "dir")
	out, cr := callProfileResolve(t, s, fmt.Sprintf(`{"cwd": %q}`, cwd))
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, true)
	testutil.Equal(t, out.Name, "customer_grade")
	testutil.Equal(t, len(out.Archetype), 1)
	testutil.NotNil(t, out.Panel)
	finders, _ := out.Panel["finders"].([]any)
	testutil.Equal(t, len(finders), 2)
}

func TestProfileResolve_PerSpawnOverridePrecedence(t *testing.T) {
	s, d := testProfileServer(t)
	worktree := t.TempDir()
	testutil.NoError(t, d.SetProject("myproj", config.Project{Path: worktree, Profile: "customer_grade"}))
	addProfileTestTask(t, d, "myproj", worktree, "lean")
	writeLibraryProfile(t, "customer_grade", `[archetype.review]
model = "opus"
`)
	writeLibraryProfile(t, "lean", `[archetype.review]
model = "sonnet"
`)

	out, cr := callProfileResolve(t, s, fmt.Sprintf(`{"cwd": %q}`, worktree))
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, true)
	testutil.Equal(t, out.Name, "lean")
}

func TestProfileResolve_ExplicitProfileNameBypassesCwd(t *testing.T) {
	s, _ := testProfileServer(t)
	writeLibraryProfile(t, "lean", `[archetype.review]
model = "sonnet"
`)

	out, cr := callProfileResolve(t, s, `{"profile": "lean"}`)
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, true)
	testutil.Equal(t, out.Name, "lean")
}

func TestProfileResolve_ExplicitProfileOverridesCwdBoundProject(t *testing.T) {
	s, d := testProfileServer(t)
	worktree := t.TempDir()
	testutil.NoError(t, d.SetProject("myproj", config.Project{Path: worktree, Profile: "customer_grade"}))
	addProfileTestTask(t, d, "myproj", worktree, "")
	writeLibraryProfile(t, "customer_grade", `[archetype.review]
model = "opus"
`)
	writeLibraryProfile(t, "lean", `[archetype.review]
model = "sonnet"
`)

	out, cr := callProfileResolve(t, s, fmt.Sprintf(`{"cwd": %q, "profile": "lean"}`, worktree))
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, true)
	testutil.Equal(t, out.Name, "lean")
}

func TestProfileResolve_MissingProfileFailsOpen(t *testing.T) {
	s, _ := testProfileServer(t)

	out, cr := callProfileResolve(t, s, `{"profile": "does-not-exist"}`)
	testutil.Equal(t, cr.IsError, false) // fail-open: not a hard tool error
	testutil.Equal(t, out.Resolved, false)
	testutil.Equal(t, len(out.Errors) > 0, true)
}

func TestProfileResolve_InvalidPanelFailsOpen(t *testing.T) {
	s, _ := testProfileServer(t)
	writeLibraryProfile(t, "broken", `[panel]
finders = []
`)

	out, cr := callProfileResolve(t, s, `{"profile": "broken"}`)
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, false)
	testutil.Equal(t, len(out.Errors) > 0, true)
}

func TestProfileResolve_ArchetypePassthroughVerbatim(t *testing.T) {
	s, _ := testProfileServer(t)
	writeLibraryProfile(t, "full", `
[archetype.review]
model  = "opus"
effort = "high"
window = "1m"

[archetype.docs]
model = "haiku"

[rigor]
review_passes = 2
gating = true
security_spot_check = true
`)

	out, cr := callProfileResolve(t, s, `{"profile": "full"}`)
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, true)
	testutil.Equal(t, len(out.Archetype), 2)
	var review struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Window string `json:"window"`
	}
	testutil.NoError(t, json.Unmarshal(out.Archetype["review"], &review))
	testutil.Equal(t, review.Model, "opus")
	testutil.Equal(t, review.Effort, "high")
	testutil.Equal(t, review.Window, "1m")

	// Raw-string assertions (not just a case-insensitive struct unmarshal,
	// which would mask a marshal-side tag regression): the wire format must
	// actually use the lowercase/snake_case keys the base spec documents,
	// since a non-Go consumer (an LLM agent, jq, a JS/Python script) reading
	// the raw JSON needs the exact case, not just something Go's
	// case-insensitive json.Unmarshal happens to tolerate.
	raw := cr.Content[0].Text
	testutil.Contains(t, raw, `"model":"opus"`)
	testutil.Contains(t, raw, `"effort":"high"`)
	testutil.Contains(t, raw, `"window":"1m"`)
	testutil.Contains(t, raw, `"review_passes":2`)
	testutil.Contains(t, raw, `"gating":true`)
	testutil.Contains(t, raw, `"security_spot_check":true`)
	for _, badKey := range []string{`"Model"`, `"Effort"`, `"Window"`, `"ReviewPasses"`, `"Gating"`, `"SecuritySpotCheck"`} {
		if strings.Contains(raw, badKey) {
			t.Fatalf("raw JSON contains PascalCase key %s (wire format must be lowercase/snake_case): %s", badKey, raw)
		}
	}
}

func TestProfileResolve_RejectsPathTraversalInExplicitProfile(t *testing.T) {
	s, _ := testProfileServer(t)

	for _, name := range []string{"../../../etc/passwd", "sub/dir", `sub\dir`, "..", "a/../b"} {
		out, cr := callProfileResolve(t, s, fmt.Sprintf(`{"profile": %q}`, name))
		testutil.Equal(t, cr.IsError, false) // fail-open: not a hard tool error
		testutil.Equal(t, out.Resolved, false)
		testutil.Equal(t, len(out.Errors) > 0, true)
	}
}

func TestProfileResolve_RejectsPathTraversalInPerSpawnOverride(t *testing.T) {
	s, d := testProfileServer(t)
	worktree := t.TempDir()
	testutil.NoError(t, d.SetProject("myproj", config.Project{Path: worktree, Profile: "customer_grade"}))
	addProfileTestTask(t, d, "myproj", worktree, "../../../etc/passwd")
	writeLibraryProfile(t, "customer_grade", `[archetype.review]
model = "opus"
`)

	out, cr := callProfileResolve(t, s, fmt.Sprintf(`{"cwd": %q}`, worktree))
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, out.Resolved, false)
	testutil.Equal(t, len(out.Errors) > 0, true)
}

func TestProfileResolve_RequiresCwdOrProfile(t *testing.T) {
	s, _ := testProfileServer(t)
	_, cr := callProfileResolve(t, s, `{}`)
	testutil.Equal(t, cr.IsError, true)
}

func TestProfileResolve_NotConfigured(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "profile_resolve",
		Arguments: json.RawMessage(`{"profile": "default"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
}

func TestToolsList_ProfileResolveOnlyWhenConfigured(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))
	for _, tool := range list.Tools {
		if tool.Name == "profile_resolve" {
			t.Fatalf("profile_resolve listed without SetProfileResolver configured")
		}
	}

	s2, _ := testProfileServer(t)
	resp2 := doRequest(t, s2, "tools/list", nil)
	testutil.NoError(t, respErr(resp2))
	result2, _ := json.Marshal(resp2.Result)
	var list2 ToolsListResult
	testutil.NoError(t, json.Unmarshal(result2, &list2))
	found := false
	for _, tool := range list2.Tools {
		if tool.Name == "profile_resolve" {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}
