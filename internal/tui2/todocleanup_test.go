package tui2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func TestConfirmCleanupToDosModal_Confirm(t *testing.T) {
	m := NewConfirmCleanupToDosModal(3)
	testutil.Equal(t, m.Count(), 3)
	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), false)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), true)
	testutil.Equal(t, m.Canceled(), false)
}

func TestConfirmCleanupToDosModal_Cancel(t *testing.T) {
	m := NewConfirmCleanupToDosModal(5)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestConfirmCleanupToDosModal_CtrlQ(t *testing.T) {
	m := NewConfirmCleanupToDosModal(5)

	handler := m.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})

	testutil.Equal(t, m.Confirmed(), false)
	testutil.Equal(t, m.Canceled(), true)
}

func TestToDoCleanup_RemovesFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some test files
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Remove only a.md and c.md (simulating cleanup of completed items)
	for _, name := range []string{"a.md", "c.md"} {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			t.Fatalf("failed to remove %s: %v", name, err)
		}
	}

	// Verify: a.md and c.md gone, b.md still exists
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Name(), "b.md")
}
