package tui2

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestLaunchToDoModal_ProjectSelection(t *testing.T) {
	projects := map[string]config.Project{
		"alpha": {Path: "/a"},
		"beta":  {Path: "/b"},
		"gamma": {Path: "/c"},
	}
	item := ToDoItem{Name: "test-todo", Content: "do stuff"}

	t.Run("default project selection", func(t *testing.T) {
		m := NewLaunchToDoModal(item, projects, "beta")
		testutil.Equal(t, m.SelectedProject(), "beta")
	})

	t.Run("unknown default falls back to first", func(t *testing.T) {
		m := NewLaunchToDoModal(item, projects, "unknown")
		testutil.Equal(t, m.SelectedProject(), "alpha") // sorted first
	})

	t.Run("empty projects", func(t *testing.T) {
		m := NewLaunchToDoModal(item, map[string]config.Project{}, "")
		testutil.Equal(t, m.SelectedProject(), "")
	})
}

func TestLaunchToDoModal_Item(t *testing.T) {
	item := ToDoItem{Name: "my-note", Content: "note content", Path: "/vault/my-note.md"}
	m := NewLaunchToDoModal(item, map[string]config.Project{"p": {}}, "p")
	testutil.Equal(t, m.Item().Name, "my-note")
	testutil.Equal(t, m.Item().Content, "note content")
}

func TestLaunchToDoModal_SetError(t *testing.T) {
	item := ToDoItem{Name: "x"}
	m := NewLaunchToDoModal(item, map[string]config.Project{"p": {}}, "p")
	m.done = true
	m.SetError("something broke")
	testutil.Equal(t, m.errMsg, "something broke")
	testutil.Equal(t, m.Done(), false) // done reset on error
}

func TestLaunchToDoModal_Prompt(t *testing.T) {
	item := ToDoItem{Name: "test"}
	m := NewLaunchToDoModal(item, map[string]config.Project{"p": {}}, "p")

	t.Run("empty by default", func(t *testing.T) {
		testutil.Equal(t, m.Prompt(), "")
	})

	t.Run("returns trimmed prompt", func(t *testing.T) {
		m.prompt = []rune("  fix the bug  ")
		testutil.Equal(t, m.Prompt(), "fix the bug")
	})
}

func TestLaunchToDoModal_FocusStartsOnPrompt(t *testing.T) {
	item := ToDoItem{Name: "test"}
	m := NewLaunchToDoModal(item, map[string]config.Project{"p": {}}, "p")
	testutil.Equal(t, m.focused, ltFieldPrompt)
}

func TestBuildToDoPrompt(t *testing.T) {
	t.Run("no user prompt uses note directly", func(t *testing.T) {
		got := buildToDoPrompt("", "note content")
		testutil.Equal(t, got, "note content")
	})

	t.Run("no note content uses user prompt directly", func(t *testing.T) {
		got := buildToDoPrompt("fix the bug", "")
		testutil.Equal(t, got, "fix the bug")
	})

	t.Run("both combines with context tags", func(t *testing.T) {
		got := buildToDoPrompt("fix the bug", "details here")
		want := "fix the bug\n\n<context>\ndetails here\n</context>"
		testutil.Equal(t, got, want)
	})

	t.Run("both empty returns empty", func(t *testing.T) {
		got := buildToDoPrompt("", "")
		testutil.Equal(t, got, "")
	})
}
