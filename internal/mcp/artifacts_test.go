package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mockArtifactStore records UpsertArtifact calls and assigns a stable id.
type mockArtifactStore struct {
	saved []*model.Artifact
	err   error
}

func (m *mockArtifactStore) UpsertArtifact(a *model.Artifact) (*model.Artifact, error) {
	if m.err != nil {
		return nil, m.err
	}
	if a.ID == "" {
		a.ID = "art-id"
	}
	m.saved = append(m.saved, a)
	return a, nil
}

// testServerWithArtifacts wires task management + an artifact store, and
// redirects HOME so agent.ArtifactsDir writes under a temp dir.
func testServerWithArtifacts(t *testing.T) (*Server, *mockArtifactStore) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s, _, _ := testServerWithTasks()
	store := &mockArtifactStore{}
	s.SetArtifactManager(store)
	return s, store
}

// callArtifactRegister invokes the tool and returns the result.
func callArtifactRegister(t *testing.T, s *Server, args map[string]any) ToolCallResult {
	t.Helper()
	raw, _ := json.Marshal(args)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "artifact_register",
		Arguments: raw,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var cr ToolCallResult
	json.Unmarshal(b, &cr) //nolint:errcheck
	return cr
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestArtifactRegister_CopiesAndRecords(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	src := writeTempFile(t, "coaching.html", "<html><body>hi</body></html>")

	cr := callArtifactRegister(t, s, map[string]any{
		"path":  src,
		"title": "Coaching report",
		"id":    "abc123",
	})
	testutil.True(t, !cr.IsError)
	testutil.Equal(t, len(store.saved), 1)

	rec := store.saved[0]
	testutil.Equal(t, rec.TaskID, "abc123")
	testutil.Equal(t, rec.Filename, "coaching.html")
	testutil.Equal(t, rec.Name, "Coaching report")
	testutil.Equal(t, rec.Type, model.ArtifactHTML) // inferred from extension
	testutil.Equal(t, rec.Size, int64(len("<html><body>hi</body></html>")))

	// Bytes were copied into the durable dir.
	dest := filepath.Join(agent.ArtifactsDir("abc123"), "coaching.html")
	got, err := os.ReadFile(dest)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), "<html><body>hi</body></html>")
}

func TestArtifactRegister_InfersTypeAndSanitizesName(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	// Source path has directories; the stored filename is just the basename.
	src := writeTempFile(t, "notes.md", "# Title")

	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, !cr.IsError)
	testutil.Equal(t, store.saved[0].Filename, "notes.md")
	testutil.Equal(t, store.saved[0].Type, model.ArtifactMarkdown)
	testutil.Equal(t, store.saved[0].Name, "notes.md") // defaults to basename
}

func TestArtifactRegister_ExplicitTypeValidated(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	src := writeTempFile(t, "data.bin", "x")
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123", "type": "bogus"})
	testutil.True(t, cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "invalid type")
}

func TestArtifactRegister_ResolvesByCwd(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	src := writeTempFile(t, "r.html", "<p>x</p>")
	// cwd lives under the fix-login worktree (/tmp/worktrees/myapp/fix-login).
	cr := callArtifactRegister(t, s, map[string]any{
		"path": src,
		"cwd":  "/tmp/worktrees/myapp/fix-login/sub",
	})
	testutil.True(t, !cr.IsError)
	testutil.Equal(t, store.saved[0].TaskID, "abc123") // the fix-login task
}

func TestArtifactRegister_MissingPath(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	cr := callArtifactRegister(t, s, map[string]any{"id": "abc123"})
	testutil.True(t, cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "path is required")
}

func TestArtifactRegister_UnknownTask(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	src := writeTempFile(t, "r.html", "x")
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "does-not-exist"})
	testutil.True(t, cr.IsError)
}

func TestArtifactRegister_NonexistentSource(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	cr := callArtifactRegister(t, s, map[string]any{"path": "/no/such/file.html", "id": "abc123"})
	testutil.True(t, cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "Failed to register")
}

func TestArtifactRegister_SizeCap(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	// One byte over the cap.
	big := strings.Repeat("a", model.MaxArtifactBytes+1)
	src := writeTempFile(t, "big.txt", big)
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "cap")
}

func TestArtifactRegister_NotConfigured(t *testing.T) {
	// Task management on, but no artifact store → tool absent / errors.
	t.Setenv("HOME", t.TempDir())
	s, _, _ := testServerWithTasks()
	src := writeTempFile(t, "r.html", "x")
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "not configured")
}

func TestArtifactRegister_ManifestFailureRollsBackCopy(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	store.err = errAlways
	src := writeTempFile(t, "r.html", "x")
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, cr.IsError)
	// The copied file must have been removed since the manifest write failed.
	_, statErr := os.Stat(filepath.Join(agent.ArtifactsDir("abc123"), "r.html"))
	testutil.True(t, os.IsNotExist(statErr))
}

func TestArtifactToolListed_OnlyWhenEnabled(t *testing.T) {
	// Enabled: tool appears.
	s, _ := testServerWithArtifacts(t)
	resp := doRequest(t, s, "tools/list", nil)
	b, _ := json.Marshal(resp.Result)
	testutil.Contains(t, string(b), "artifact_register")

	// Disabled (KB-only server): tool absent.
	bare := testServer()
	resp2 := doRequest(t, bare, "tools/list", nil)
	b2, _ := json.Marshal(resp2.Result)
	testutil.True(t, !strings.Contains(string(b2), "artifact_register"))
}

// errAlways is a sentinel store error used to exercise the rollback path.
var errAlways = errArtifactTest("boom")

type errArtifactTest string

func (e errArtifactTest) Error() string { return string(e) }
