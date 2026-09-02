package things3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/todo"
)

// capturedRun is a fake AppleScript runner: it records every script it was
// asked to run (for assertions on the generated AppleScript) and returns a
// fixed output/error, so tests never touch a real Things 3 install.
type capturedRun struct {
	scripts []string
	output  string
	err     error
}

func (c *capturedRun) run(_ context.Context, script string) (string, error) {
	c.scripts = append(c.scripts, script)
	return c.output, c.err
}

func newTestBackend(project, tag string, c *capturedRun) *Backend {
	return &Backend{project: project, tag: tag, run: c.run}
}

// row builds a fake AppleScript response row matching rowExpr's shape.
func row(id, title, notes, status string) string {
	return strings.Join([]string{id, title, notes, status}, fieldSep)
}

func TestNewForOS(t *testing.T) {
	t.Run("darwin", func(t *testing.T) {
		b, err := newForOS("darwin", config.TodoConfig{Things3: config.Things3Config{Project: "Argus", Tag: "argus"}})
		testutil.NoError(t, err)
		backend, ok := b.(*Backend)
		if !ok {
			t.Fatalf("expected *Backend, got %T", b)
		}
		testutil.Equal(t, backend.project, "Argus")
		testutil.Equal(t, backend.tag, "argus")
	})

	t.Run("non-darwin", func(t *testing.T) {
		_, err := newForOS("linux", config.TodoConfig{})
		if err == nil {
			t.Fatal("expected an error on a non-macOS host")
		}
		testutil.Contains(t, err.Error(), "linux")
	})
}

func TestEscapeAS(t *testing.T) {
	cases := map[string]string{
		`plain`:                 `plain`,
		`with "quotes"`:         `with \"quotes\"`,
		`back\slash`:            `back\\slash`,
		"has" + fieldSep + "x":  "has x",
		"has" + recordSep + "x": "has x",
	}
	for in, want := range cases {
		if got := escapeAS(in); got != want {
			t.Errorf("escapeAS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDestination(t *testing.T) {
	c := &capturedRun{}
	testutil.Equal(t, newTestBackend("", "", c).destination(), `list "Inbox"`)
	testutil.Equal(t, newTestBackend("Argus", "", c).destination(), `project "Argus"`)
}

func TestCreate(t *testing.T) {
	t.Run("success with project and tag", func(t *testing.T) {
		c := &capturedRun{output: row("abc-1", "Buy milk", "2%", "open")}
		b := newTestBackend("Argus", "argus", c)

		item, err := b.Create(context.Background(), todo.CreateInput{Title: "Buy milk", Notes: "2%"})
		testutil.NoError(t, err)
		testutil.Equal(t, item.ID, "abc-1")
		testutil.Equal(t, item.Title, "Buy milk")
		testutil.Equal(t, item.Notes, "2%")
		testutil.Equal(t, item.Done, false)

		testutil.Equal(t, len(c.scripts), 1)
		testutil.Contains(t, c.scripts[0], `name:"Buy milk"`)
		testutil.Contains(t, c.scripts[0], `notes:"2%"`)
		testutil.Contains(t, c.scripts[0], `tag names:"argus"`)
		testutil.Contains(t, c.scripts[0], `project "Argus"`)
	})

	t.Run("success with no project falls back to Inbox", func(t *testing.T) {
		c := &capturedRun{output: row("abc-2", "Task", "", "open")}
		b := newTestBackend("", "", c)

		_, err := b.Create(context.Background(), todo.CreateInput{Title: "Task"})
		testutil.NoError(t, err)
		testutil.Contains(t, c.scripts[0], `list "Inbox"`)
	})

	t.Run("empty title is rejected without shelling out", func(t *testing.T) {
		c := &capturedRun{}
		b := newTestBackend("", "", c)
		_, err := b.Create(context.Background(), todo.CreateInput{Title: "   "})
		if err == nil {
			t.Fatal("expected an error for an empty title")
		}
		testutil.Equal(t, len(c.scripts), 0)
	})

	t.Run("osascript error propagates", func(t *testing.T) {
		c := &capturedRun{err: errors.New("boom")}
		b := newTestBackend("", "", c)
		_, err := b.Create(context.Background(), todo.CreateInput{Title: "Task"})
		testutil.Contains(t, err.Error(), "boom")
	})

	t.Run("malformed response is an error", func(t *testing.T) {
		c := &capturedRun{output: "not-enough-fields"}
		b := newTestBackend("", "", c)
		_, err := b.Create(context.Background(), todo.CreateInput{Title: "Task"})
		if err == nil {
			t.Fatal("expected an error for a malformed response")
		}
	})
}

func TestList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		c := &capturedRun{output: ""}
		b := newTestBackend("", "", c)
		items, err := b.List(context.Background())
		testutil.NoError(t, err)
		if len(items) != 0 {
			t.Fatalf("expected no items, got %d", len(items))
		}
	})

	t.Run("multiple items", func(t *testing.T) {
		out := strings.Join([]string{
			row("a", "First", "", "open"),
			row("b", "Second", "notes here", "open"),
		}, recordSep)
		c := &capturedRun{output: out}
		b := newTestBackend("Argus", "", c)

		items, err := b.List(context.Background())
		testutil.NoError(t, err)
		testutil.Equal(t, len(items), 2)
		testutil.Equal(t, items[0].ID, "a")
		testutil.Equal(t, items[1].Notes, "notes here")
		testutil.Contains(t, c.scripts[0], "whose status is open")
	})

	t.Run("osascript error propagates", func(t *testing.T) {
		c := &capturedRun{err: errors.New("no such application")}
		b := newTestBackend("", "", c)
		_, err := b.List(context.Background())
		testutil.Contains(t, err.Error(), "no such application")
	})
}

func TestUpdate(t *testing.T) {
	t.Run("updates title and notes", func(t *testing.T) {
		c := &capturedRun{output: row("a", "New title", "New notes", "open")}
		b := newTestBackend("", "", c)

		title := "New title"
		notes := "New notes"
		item, err := b.Update(context.Background(), "a", todo.UpdateInput{Title: &title, Notes: &notes})
		testutil.NoError(t, err)
		testutil.Equal(t, item.Title, "New title")
		testutil.Contains(t, c.scripts[0], `set name of t to "New title"`)
		testutil.Contains(t, c.scripts[0], `set notes of t to "New notes"`)
	})

	t.Run("missing id is rejected without shelling out", func(t *testing.T) {
		c := &capturedRun{}
		b := newTestBackend("", "", c)
		title := "X"
		_, err := b.Update(context.Background(), "  ", todo.UpdateInput{Title: &title})
		if err == nil {
			t.Fatal("expected an error for a missing id")
		}
		testutil.Equal(t, len(c.scripts), 0)
	})

	t.Run("unknown id reports not found", func(t *testing.T) {
		c := &capturedRun{output: notFoundSentinel}
		b := newTestBackend("", "", c)
		title := "X"
		_, err := b.Update(context.Background(), "missing", todo.UpdateInput{Title: &title})
		if err == nil {
			t.Fatal("expected a not-found error")
		}
		testutil.Contains(t, err.Error(), "not found")
	})
}

func TestComplete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := &capturedRun{output: row("a", "Task", "", "completed")}
		b := newTestBackend("", "", c)
		item, err := b.Complete(context.Background(), "a")
		testutil.NoError(t, err)
		testutil.Equal(t, item.Done, true)
		testutil.Contains(t, c.scripts[0], "set status of t to completed")
	})

	t.Run("unknown id reports not found", func(t *testing.T) {
		c := &capturedRun{output: notFoundSentinel}
		b := newTestBackend("", "", c)
		_, err := b.Complete(context.Background(), "missing")
		testutil.Contains(t, err.Error(), "not found")
	})
}

func TestDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := &capturedRun{output: "OK"}
		b := newTestBackend("", "", c)
		err := b.Delete(context.Background(), "a")
		testutil.NoError(t, err)
		testutil.Contains(t, c.scripts[0], "delete t")
	})

	t.Run("missing id is rejected without shelling out", func(t *testing.T) {
		c := &capturedRun{}
		b := newTestBackend("", "", c)
		err := b.Delete(context.Background(), "")
		if err == nil {
			t.Fatal("expected an error for a missing id")
		}
		testutil.Equal(t, len(c.scripts), 0)
	})

	t.Run("unknown id reports not found", func(t *testing.T) {
		c := &capturedRun{output: notFoundSentinel}
		b := newTestBackend("", "", c)
		err := b.Delete(context.Background(), "missing")
		testutil.Contains(t, err.Error(), "not found")
	})

	t.Run("osascript error propagates", func(t *testing.T) {
		c := &capturedRun{err: errors.New("permission denied")}
		b := newTestBackend("", "", c)
		err := b.Delete(context.Background(), "a")
		testutil.Contains(t, err.Error(), "permission denied")
	})
}

func TestRegisteredWithTodoPackage(t *testing.T) {
	names := todo.Registered()
	found := false
	for _, n := range names {
		if n == "things3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected \"things3\" to be registered, got %v", names)
	}
}

func TestParseItem_MalformedRow(t *testing.T) {
	_, err := parseItem("only-one-field")
	if err == nil {
		t.Fatal("expected an error for a malformed row")
	}
}

func TestRunOsascript_UsesStderrOnFailure(t *testing.T) {
	// osascript itself isn't invoked in unit tests (see capturedRun above);
	// this only exercises the pure error-formatting path with a command that
	// is guaranteed to fail identically on any host running these tests.
	_, err := runOsascript(context.Background(), `this is not valid AppleScript ((`)
	if err == nil {
		t.Fatal("expected osascript to fail on invalid input")
	}
	testutil.Contains(t, err.Error(), "osascript:")
}

var _ = fmt.Sprintf // keep fmt imported if row()/tests above are trimmed later
