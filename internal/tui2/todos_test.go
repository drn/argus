package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestToDoListPanel_CursorNavigation(t *testing.T) {
	p := NewToDoListPanel()
	items := []ToDoItem{
		{Name: "first"},
		{Name: "second"},
		{Name: "third"},
	}
	p.SetItems(items)

	t.Run("initial cursor at 0", func(t *testing.T) {
		testutil.Equal(t, p.cursor, 0)
		testutil.Equal(t, p.SelectedItem().Name, "first")
	})

	t.Run("move down", func(t *testing.T) {
		p.MoveDown()
		testutil.Equal(t, p.cursor, 1)
		testutil.Equal(t, p.SelectedItem().Name, "second")
	})

	t.Run("move down again", func(t *testing.T) {
		p.MoveDown()
		testutil.Equal(t, p.cursor, 2)
		testutil.Equal(t, p.SelectedItem().Name, "third")
	})

	t.Run("move down at end is no-op", func(t *testing.T) {
		p.MoveDown()
		testutil.Equal(t, p.cursor, 2)
	})

	t.Run("move up", func(t *testing.T) {
		p.MoveUp()
		testutil.Equal(t, p.cursor, 1)
	})

	t.Run("move up to top", func(t *testing.T) {
		p.MoveUp()
		testutil.Equal(t, p.cursor, 0)
	})

	t.Run("move up at top is no-op", func(t *testing.T) {
		p.MoveUp()
		testutil.Equal(t, p.cursor, 0)
	})
}

func TestToDoListPanel_EmptyList(t *testing.T) {
	p := NewToDoListPanel()
	p.SetItems(nil)
	testutil.Nil(t, p.SelectedItem())
	p.MoveDown() // should not panic
	p.MoveUp()   // should not panic
}

func TestToDoListPanel_CursorClampOnShrink(t *testing.T) {
	p := NewToDoListPanel()
	p.SetItems([]ToDoItem{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	p.MoveDown()
	p.MoveDown()
	testutil.Equal(t, p.cursor, 2)

	// Shrink list — cursor should clamp
	p.SetItems([]ToDoItem{{Name: "a"}})
	testutil.Equal(t, p.cursor, 0)
}

func TestToDoListPanel_CursorChangeCallback(t *testing.T) {
	p := NewToDoListPanel()
	var lastItem *ToDoItem
	p.OnCursorChange = func(item *ToDoItem) {
		lastItem = item
	}
	p.SetItems([]ToDoItem{{Name: "x"}, {Name: "y"}})
	testutil.Equal(t, lastItem.Name, "x")

	p.MoveDown()
	testutil.Equal(t, lastItem.Name, "y")
}

func TestWrapTextLines(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  int // expected line count
	}{
		{"empty", "", 40, 1},
		{"single line", "hello world", 40, 1},
		{"wraps long line", "the quick brown fox jumps over the lazy dog", 20, 3},
		{"preserves newlines", "line one\nline two\nline three", 40, 3},
		{"zero width", "text", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := wrapTextLines(tt.text, tt.width)
			testutil.Equal(t, len(lines), tt.want)
		})
	}
}

func TestToDosView_SyncTasks(t *testing.T) {
	v := NewToDosView()
	v.items = []ToDoItem{
		{Name: "todo-a", Path: "/vault/a.md"},
		{Name: "todo-b", Path: "/vault/b.md"},
	}
	v.list.SetItems(v.items)

	taskMap := map[string]*model.Task{
		"/vault/a.md": {Name: "task-a", Status: model.StatusInProgress},
	}
	v.SyncTasks(taskMap)

	testutil.Equal(t, v.list.taskMap["/vault/a.md"].Status, model.StatusInProgress)
}

func TestToDosView_CompletedItems(t *testing.T) {
	v := NewToDosView()
	v.items = []ToDoItem{
		{Name: "a", Path: "/vault/a.md"},
		{Name: "b", Path: "/vault/b.md"},
		{Name: "c", Path: "/vault/c.md"},
	}

	t.Run("no task map returns empty", func(t *testing.T) {
		v.taskMap = nil
		testutil.Equal(t, len(v.CompletedItems()), 0)
	})

	t.Run("returns only completed", func(t *testing.T) {
		v.taskMap = map[string]*model.Task{
			"/vault/a.md": {Status: model.StatusComplete},
			"/vault/b.md": {Status: model.StatusInProgress},
		}
		completed := v.CompletedItems()
		testutil.Equal(t, len(completed), 1)
		testutil.Equal(t, completed[0].Name, "a")
	})

	t.Run("unlinked items not included", func(t *testing.T) {
		v.taskMap = map[string]*model.Task{}
		testutil.Equal(t, len(v.CompletedItems()), 0)
	})
}

func TestToDoListPanel_SetTaskMap(t *testing.T) {
	p := NewToDoListPanel()
	testutil.Nil(t, p.taskMap)

	taskMap := map[string]*model.Task{
		"/vault/x.md": {Name: "x", Status: model.StatusComplete},
	}
	p.SetTaskMap(taskMap)
	testutil.Equal(t, p.taskMap["/vault/x.md"].Status, model.StatusComplete)
}

func TestToDosView_StatusToggle(t *testing.T) {
	v := NewToDosView()
	v.items = []ToDoItem{
		{Name: "a", Path: "/vault/a.md"},
		{Name: "b", Path: "/vault/b.md"},
	}
	v.list.SetItems(v.items)

	taskA := &model.Task{Name: "task-a", Status: model.StatusPending}
	v.SyncTasks(map[string]*model.Task{
		"/vault/a.md": taskA,
	})

	var changed *model.Task
	v.OnStatusChange = func(task *model.Task) {
		changed = task
	}

	t.Run("s advances linked task status", func(t *testing.T) {
		changed = nil
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
		testutil.Equal(t, changed != nil, true)
		testutil.Equal(t, changed.Status, model.StatusInProgress)
	})

	t.Run("s advances again", func(t *testing.T) {
		changed = nil
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
		testutil.Equal(t, changed.Status, model.StatusInReview)
	})

	t.Run("S goes back", func(t *testing.T) {
		changed = nil
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone))
		testutil.Equal(t, changed.Status, model.StatusInProgress)
	})

	t.Run("s on unlinked todo is no-op", func(t *testing.T) {
		changed = nil
		// Move cursor to todo-b which has no linked task
		v.list.MoveDown()
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
		testutil.Nil(t, changed)
	})
}

func TestTabIndices(t *testing.T) {
	// Verify tab ordering is correct after adding TabToDos
	testutil.Equal(t, int(TabTasks), 0)
	testutil.Equal(t, int(TabToDos), 1)
	testutil.Equal(t, int(TabReviews), 2)
	testutil.Equal(t, int(TabSettings), 3)
	testutil.Equal(t, len(tabLabels), 4)
	testutil.Equal(t, tabLabels[TabToDos], "To Dos")
}
