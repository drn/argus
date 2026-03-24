package tui2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func TestConfirmDeleteToDoModal_Confirm(t *testing.T) {
	item := ToDoItem{Name: "my-todo", Path: "/vault/my-todo.md"}
	m := NewConfirmDeleteToDoModal(item)
	testutil.Equal(t, m.Item().Name, "my-todo")
	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), false)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Canceled(), false)
}

func TestConfirmDeleteToDoModal_Cancel(t *testing.T) {
	item := ToDoItem{Name: "my-todo", Path: "/vault/my-todo.md"}
	m := NewConfirmDeleteToDoModal(item)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestToDoDelete_RemovesFile(t *testing.T) {
	dir := t.TempDir()

	// Create a test todo file.
	name := "delete-me.md"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("# Delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify file exists.
	_, err := os.Stat(path)
	testutil.NoError(t, err)

	// Remove the file (simulating single-todo delete).
	err = os.Remove(path)
	testutil.NoError(t, err)

	// Verify it's gone.
	_, err = os.Stat(path)
	testutil.Equal(t, os.IsNotExist(err), true)
}

func TestToDoDelete_OtherFilesUntouched(t *testing.T) {
	dir := t.TempDir()

	// Create two files.
	for _, name := range []string{"keep.md", "delete.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Delete only one.
	err := os.Remove(filepath.Join(dir, "delete.md"))
	testutil.NoError(t, err)

	// Verify the other remains.
	entries, err := os.ReadDir(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Name(), "keep.md")
}
