package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mockArtifactStore records UpsertArtifact calls and assigns a stable id.
type mockArtifactStore struct {
	saved []*model.Artifact
	err   error
	stale []string // filenames PruneFolderArtifacts reports as removed
	kept  []string // keep list from the last prune call
}

func (m *mockArtifactStore) PruneFolderArtifacts(taskID, folder string, keep []string) ([]string, error) {
	m.kept = keep
	return m.stale, nil
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

func TestArtifactRegister_AudioUnderMediaCap(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	// Just over the OLD 25 MiB default cap, but nowhere near the new 1 GiB
	// media cap — proves the audio/video tier actually took effect (this same
	// size fails for a .txt file in TestArtifactRegister_SizeCap).
	big := strings.Repeat("a", model.MaxArtifactBytes+1024)
	src := writeTempFile(t, "clip.mp3", big)
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, !cr.IsError)
	testutil.Equal(t, store.saved[0].Type, model.ArtifactAudio)
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

func makeFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "gallery")
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestArtifactRegister_Folder(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	root := makeFolder(t, map[string]string{
		"01.png":          "png-bytes",
		"clips/a.mp4":     "mp4-bytes",
		"notes.md":        "# hi",
		".hidden/x.png":   "no",
		".DS_Store":       "no",
		"clips/.skip.txt": "no",
	})
	// A symlink out of the tree must be skipped, never followed.
	outside := writeTempFile(t, "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	cr := callArtifactRegister(t, s, map[string]any{"path": root, "id": "abc123"})
	testutil.True(t, !cr.IsError)
	got := map[string]*model.Artifact{}
	for _, a := range store.saved {
		got[a.Filename] = a
	}
	testutil.Equal(t, len(got), 3)
	for _, tc := range []struct {
		filename, name string
		typ            model.ArtifactType
	}{
		{"gallery/01.png", "01.png", model.ArtifactImage},
		{"gallery/clips/a.mp4", "clips/a.mp4", model.ArtifactVideo},
		{"gallery/notes.md", "notes.md", model.ArtifactMarkdown},
	} {
		a := got[tc.filename]
		testutil.True(t, a != nil)
		testutil.Equal(t, a.Folder, "gallery")
		testutil.Equal(t, a.Name, tc.name)
		testutil.Equal(t, a.Type, tc.typ)
		b, err := os.ReadFile(filepath.Join(agent.ArtifactsDir("abc123"), filepath.FromSlash(tc.filename)))
		testutil.NoError(t, err)
		testutil.True(t, len(b) > 0)
	}
	testutil.Equal(t, len(store.kept), 3)
	testutil.Contains(t, cr.Content[0].Text, "3 files")
}

func TestArtifactRegister_FolderSymlinkedRoot(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	root := makeFolder(t, map[string]string{"a.png": "x"})
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	cr := callArtifactRegister(t, s, map[string]any{"path": link, "id": "abc123"})
	testutil.True(t, !cr.IsError)
	testutil.Equal(t, len(store.saved), 1)
	testutil.Equal(t, store.saved[0].Folder, "alias")
}

func TestArtifactRegister_FolderReregisterPrunesStale(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	store.stale = []string{"gallery/old.png"}
	stalePath := filepath.Join(agent.ArtifactsDir("abc123"), "gallery", "old.png")
	if err := os.MkdirAll(filepath.Dir(stalePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stalePath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := makeFolder(t, map[string]string{"new.png": "x"})
	cr := callArtifactRegister(t, s, map[string]any{"path": root, "id": "abc123"})
	testutil.True(t, !cr.IsError)
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale file should be removed, stat err=%v", err)
	}
}

func TestArtifactRegister_FolderErrors(t *testing.T) {
	t.Run("empty folder", func(t *testing.T) {
		s, _ := testServerWithArtifacts(t)
		cr := callArtifactRegister(t, s, map[string]any{"path": makeFolder(t, map[string]string{".only-hidden": "x"}), "id": "abc123"})
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "no registrable files")
	})
	t.Run("too many files", func(t *testing.T) {
		s, store := testServerWithArtifacts(t)
		files := map[string]string{}
		for i := 0; i <= model.MaxFolderFiles; i++ {
			files[fmt.Sprintf("d%d/f%d.txt", i%5, i)] = "x"
		}
		cr := callArtifactRegister(t, s, map[string]any{"path": makeFolder(t, files), "id": "abc123"})
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "more than")
		testutil.Equal(t, len(store.saved), 0)
	})
	t.Run("hidden folder name", func(t *testing.T) {
		s, _ := testServerWithArtifacts(t)
		root := filepath.Join(t.TempDir(), ".secret")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		cr := callArtifactRegister(t, s, map[string]any{"path": root, "id": "abc123"})
		testutil.True(t, cr.IsError)
	})
	t.Run("manifest failure for every file", func(t *testing.T) {
		s, store := testServerWithArtifacts(t)
		store.err = errTest
		cr := callArtifactRegister(t, s, map[string]any{"path": makeFolder(t, map[string]string{"a.png": "x"}), "id": "abc123"})
		testutil.True(t, cr.IsError)
		if _, err := os.Stat(filepath.Join(agent.ArtifactsDir("abc123"), "gallery", "a.png")); !os.IsNotExist(err) {
			t.Fatalf("copied bytes should be rolled back, stat err=%v", err)
		}
	})
}

var errTest = errors.New("boom")

func TestArtifactRegister_ReservedStandaloneName(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	src := writeTempFile(t, ".thumbs", "x")
	cr := callArtifactRegister(t, s, map[string]any{"path": src, "id": "abc123"})
	testutil.True(t, cr.IsError)
	testutil.Equal(t, len(store.saved), 0)
}

func TestCopyArtifact_RejectsNonRegular(t *testing.T) {
	s, _ := testServerWithArtifacts(t)
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	_, err := s.copyArtifact("abc123", fifo, "g/pipe.txt", model.ArtifactText)
	testutil.True(t, err != nil)
}

func TestArtifactRegister_FolderReportsReplaced(t *testing.T) {
	s, store := testServerWithArtifacts(t)
	store.stale = []string{"gallery/old.png"}
	cr := callArtifactRegister(t, s, map[string]any{"path": makeFolder(t, map[string]string{"new.png": "x"}), "id": "abc123"})
	testutil.True(t, !cr.IsError)
	testutil.Contains(t, cr.Content[0].Text, "Replaced 1 previously registered")
}
