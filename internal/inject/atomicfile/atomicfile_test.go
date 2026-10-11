package atomicfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestWrite(t *testing.T) {
	t.Run("writes and leaves no temp", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "f")
		testutil.NoError(t, Write(p, []byte("hi"), "x"))
		got, err := os.ReadFile(p)
		testutil.NoError(t, err)
		testutil.Equal(t, string(got), "hi")
		entries, _ := os.ReadDir(dir)
		testutil.Equal(t, len(entries), 1)
	})
	t.Run("symlink written through to target", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "real.json")
		testutil.NoError(t, os.WriteFile(target, []byte("old"), 0o644))
		link := filepath.Join(t.TempDir(), "link.json")
		testutil.NoError(t, os.Symlink(target, link))
		testutil.NoError(t, Write(link, []byte("new"), "x"))
		info, err := os.Lstat(link)
		testutil.NoError(t, err)
		testutil.Equal(t, info.Mode()&os.ModeSymlink != 0, true)
		got, _ := os.ReadFile(target)
		testutil.Equal(t, string(got), "new")
	})
	t.Run("missing dir", func(t *testing.T) {
		err := Write(filepath.Join(t.TempDir(), "nope", "f"), nil, "lbl")
		testutil.Contains(t, err.Error(), "lbl: create temp")
	})
	t.Run("rename onto directory fails and cleans up", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "d")
		testutil.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0o755))
		err := Write(target, []byte("x"), "lbl")
		testutil.Contains(t, err.Error(), "lbl: rename")
		entries, _ := os.ReadDir(dir)
		testutil.Equal(t, len(entries), 1)
	})
}

func TestWriteJSON(t *testing.T) {
	t.Run("indented with newline", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "f.json")
		testutil.NoError(t, WriteJSON(p, map[string]any{"a": 1}, "x"))
		got, _ := os.ReadFile(p)
		testutil.Equal(t, string(got), "{\n  \"a\": 1\n}\n")
	})
	t.Run("marshal error", func(t *testing.T) {
		err := WriteJSON(filepath.Join(t.TempDir(), "f"), map[string]any{"c": make(chan int)}, "lbl")
		testutil.Contains(t, err.Error(), "lbl: marshal")
	})
}

func TestReadJSONObject(t *testing.T) {
	dir := t.TempDir()
	t.Run("missing is empty", func(t *testing.T) {
		m, err := ReadJSONObject(filepath.Join(dir, "none"), "x")
		testutil.NoError(t, err)
		testutil.Equal(t, len(m), 0)
	})
	t.Run("null is empty", func(t *testing.T) {
		p := filepath.Join(dir, "null.json")
		testutil.NoError(t, os.WriteFile(p, []byte("null"), 0o644))
		m, err := ReadJSONObject(p, "x")
		testutil.NoError(t, err)
		testutil.Equal(t, len(m), 0)
	})
	t.Run("invalid", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		testutil.NoError(t, os.WriteFile(p, []byte("{nope"), 0o644))
		_, err := ReadJSONObject(p, "lbl")
		testutil.Contains(t, err.Error(), "lbl: cannot parse")
	})
	t.Run("unreadable", func(t *testing.T) {
		_, err := ReadJSONObject(dir, "lbl")
		testutil.Contains(t, err.Error(), "lbl: read")
	})
	t.Run("object", func(t *testing.T) {
		p := filepath.Join(dir, "ok.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"k":"v"}`), 0o644))
		m, err := ReadJSONObject(p, "x")
		testutil.NoError(t, err)
		testutil.Equal(t, m["k"], any("v"))
	})
}
