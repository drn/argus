package layout

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	l := Layout{Name: "x", Title: "X", Root: Node{Type: "terminal"}}
	testutil.NoError(t, r.Register(l))

	got, ok := r.Get("x")
	testutil.True(t, ok)
	testutil.Equal(t, got.Name, "x")
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nope")
	testutil.False(t, ok)
}

func TestRegistry_DuplicateNameReplaces(t *testing.T) {
	r := NewRegistry()
	testutil.NoError(t, r.Register(Layout{Name: "x", Title: "first", Root: Node{Type: "terminal"}}))
	testutil.NoError(t, r.Register(Layout{Name: "x", Title: "second", Root: Node{Type: "terminal"}}))

	got, ok := r.Get("x")
	testutil.True(t, ok)
	testutil.Equal(t, got.Title, "second")
}

func TestRegistry_RegisterEmptyNameFails(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Layout{Title: "no name", Root: Node{Type: "terminal"}})
	testutil.Error(t, err)
}

func TestRegistry_RegisterValidatesRoot(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Layout{Name: "x", Title: "x", Root: Node{Type: "boom"}})
	testutil.Error(t, err)
}

func TestRegistry_ListReturnsSortedByName(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zebra", "apple", "mango"} {
		testutil.NoError(t, r.Register(Layout{Name: n, Title: n, Root: Node{Type: "terminal"}}))
	}
	got := r.List()
	testutil.Equal(t, len(got), 3)
	testutil.Equal(t, got[0].Name, "apple")
	testutil.Equal(t, got[1].Name, "mango")
	testutil.Equal(t, got[2].Name, "zebra")
}

func TestRegistry_LoadDirParsesJSONFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), `{"name":"alpha","title":"A","root":{"type":"terminal"}}`)
	writeFile(t, filepath.Join(dir, "b.json"), `{"name":"beta","title":"B","root":{"type":"terminal"}}`)
	writeFile(t, filepath.Join(dir, "readme.md"), `not a layout`)

	r := NewRegistry()
	res := r.LoadDir(dir)
	testutil.Equal(t, res.Loaded, 2)
	testutil.Equal(t, len(res.Errors), 0)

	_, ok := r.Get("alpha")
	testutil.True(t, ok)
	_, ok = r.Get("beta")
	testutil.True(t, ok)
}

func TestRegistry_LoadDirCollectsErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.json"), `{"name":"good","title":"G","root":{"type":"terminal"}}`)
	writeFile(t, filepath.Join(dir, "bad.json"), `{not json`)
	writeFile(t, filepath.Join(dir, "invalid.json"), `{"name":"x","title":"x","root":{"type":"boom"}}`)

	r := NewRegistry()
	res := r.LoadDir(dir)
	testutil.Equal(t, res.Loaded, 1)
	testutil.Equal(t, len(res.Errors), 2)
	_, ok := r.Get("good")
	testutil.True(t, ok)
}

func TestRegistry_LoadDirMissingIsNoOp(t *testing.T) {
	r := NewRegistry()
	res := r.LoadDir(filepath.Join(t.TempDir(), "does-not-exist"))
	testutil.Equal(t, res.Loaded, 0)
	testutil.Equal(t, len(res.Errors), 0)
}

func TestRegistry_LoadDirEmptyPathIsNoOp(t *testing.T) {
	r := NewRegistry()
	res := r.LoadDir("")
	testutil.Equal(t, res.Loaded, 0)
	testutil.Equal(t, len(res.Errors), 0)
}

func TestRegistry_LoadDirIgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "inner.json"), `{"name":"inner","title":"I","root":{"type":"terminal"}}`)
	writeFile(t, filepath.Join(dir, "top.json"), `{"name":"top","title":"T","root":{"type":"terminal"}}`)

	r := NewRegistry()
	res := r.LoadDir(dir)
	testutil.Equal(t, res.Loaded, 1)
	_, ok := r.Get("inner")
	testutil.False(t, ok)
}

func TestRegistry_DefaultLayoutPresent(t *testing.T) {
	r := WithDefaults(NewRegistry())
	got, ok := r.Get(DefaultLayoutName)
	testutil.True(t, ok)
	testutil.Equal(t, got.Name, DefaultLayoutName)
	testutil.True(t, got.Root.IsSplit())
}

func TestNode_IsSplit(t *testing.T) {
	testutil.True(t, Node{Type: NodeSplit}.IsSplit())
	testutil.False(t, Node{Type: NodeTerminal}.IsSplit())
}

func TestRegistry_LoadDirSurfacesUnreadableDir(t *testing.T) {
	// Point at a path that exists but isn't a directory.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file.txt")
	writeFile(t, notADir, "")
	r := NewRegistry()
	res := r.LoadDir(notADir)
	testutil.Equal(t, res.Loaded, 0)
	if len(res.Errors) == 0 {
		t.Fatalf("expected an error reading non-directory")
	}
}

func TestRegistry_LoadDirSurfacesUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read 0o000 files")
	}
	dir := t.TempDir()
	// Write a valid-looking name but make it unreadable.
	path := filepath.Join(dir, "denied.json")
	writeFile(t, path, `{"name":"x","title":"x","root":{"type":"terminal"}}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	r := NewRegistry()
	res := r.LoadDir(dir)
	if len(res.Errors) == 0 {
		t.Fatalf("expected a read error")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
