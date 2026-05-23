package layout

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestParse_TasksDefaultShape(t *testing.T) {
	src := `{
		"name": "tasks-default",
		"title": "Tasks (default)",
		"root": {
			"type": "split",
			"direction": "horizontal",
			"sizes": [1, 3, 1],
			"children": [
				{"type": "task-list"},
				{
					"type": "split",
					"direction": "vertical",
					"sizes": [3, 7],
					"children": [
						{"type": "git"},
						{"type": "task-preview"}
					]
				},
				{"type": "task-detail"}
			]
		}
	}`
	got, err := Parse([]byte(src))
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "tasks-default")
	testutil.Equal(t, got.Title, "Tasks (default)")
	testutil.Equal(t, got.Root.Type, "split")
	testutil.Equal(t, got.Root.Direction, "horizontal")
	testutil.Equal(t, len(got.Root.Children), 3)
	testutil.Equal(t, got.Root.Children[0].Type, "task-list")
	testutil.Equal(t, got.Root.Children[1].Children[1].Type, "task-preview")
}

func TestParse_TerminalWithBindAndCycle(t *testing.T) {
	src := `{
		"name": "single-terminal",
		"title": "single",
		"root": {"type": "terminal", "bind": "task:abc123", "cycle": true}
	}`
	got, err := Parse([]byte(src))
	testutil.NoError(t, err)
	testutil.Equal(t, got.Root.Type, "terminal")
	testutil.Equal(t, got.Root.Bind, "task:abc123")
	testutil.Equal(t, got.Root.Cycle, true)
}

func TestParse_StreampaneWithSource(t *testing.T) {
	src := `{
		"name": "logs",
		"title": "Logs",
		"root": {"type": "streampane", "source": "file:~/.argus/ux.log"}
	}`
	got, err := Parse([]byte(src))
	testutil.NoError(t, err)
	testutil.Equal(t, got.Root.Type, "streampane")
	testutil.Equal(t, got.Root.Source, "file:~/.argus/ux.log")
}

func TestParse_WithHotkeys(t *testing.T) {
	src := `{
		"name": "hk",
		"title": "hk",
		"root": {"type": "terminal"},
		"hotkeys": {"tab": "cycle right", "ctrl-1": "focus first"}
	}`
	got, err := Parse([]byte(src))
	testutil.NoError(t, err)
	testutil.Equal(t, got.Hotkeys["tab"], "cycle right")
	testutil.Equal(t, got.Hotkeys["ctrl-1"], "focus first")
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte("{not json"))
	testutil.Error(t, err)
}

func TestParse_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		substr string
	}{
		{
			name:   "missing name",
			src:    `{"title": "x", "root": {"type": "terminal"}}`,
			substr: "name",
		},
		{
			name:   "missing root type",
			src:    `{"name": "x", "title": "x", "root": {}}`,
			substr: "type",
		},
		{
			name:   "unknown node type",
			src:    `{"name": "x", "title": "x", "root": {"type": "explosion"}}`,
			substr: "unknown node type",
		},
		{
			name:   "split missing direction",
			src:    `{"name": "x", "title": "x", "root": {"type": "split", "sizes": [1, 1], "children": [{"type": "terminal"}, {"type": "terminal"}]}}`,
			substr: "direction",
		},
		{
			name:   "split bad direction",
			src:    `{"name": "x", "title": "x", "root": {"type": "split", "direction": "diagonal", "sizes": [1, 1], "children": [{"type": "terminal"}, {"type": "terminal"}]}}`,
			substr: "direction",
		},
		{
			name:   "split sizes/children length mismatch",
			src:    `{"name": "x", "title": "x", "root": {"type": "split", "direction": "horizontal", "sizes": [1, 1, 1], "children": [{"type": "terminal"}, {"type": "terminal"}]}}`,
			substr: "sizes",
		},
		{
			name:   "split needs at least two children",
			src:    `{"name": "x", "title": "x", "root": {"type": "split", "direction": "horizontal", "sizes": [1], "children": [{"type": "terminal"}]}}`,
			substr: "children",
		},
		{
			name:   "non-positive size",
			src:    `{"name": "x", "title": "x", "root": {"type": "split", "direction": "horizontal", "sizes": [0, 1], "children": [{"type": "terminal"}, {"type": "terminal"}]}}`,
			substr: "size",
		},
		{
			name:   "leaf with children",
			src:    `{"name": "x", "title": "x", "root": {"type": "terminal", "children": [{"type": "git"}]}}`,
			substr: "leaf",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			testutil.Error(t, err)
			if !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.substr)
			}
		})
	}
}

func TestParse_NestedSplitValidates(t *testing.T) {
	// Outer is fine but inner has a bad child type.
	src := `{
		"name": "x", "title": "x",
		"root": {
			"type": "split", "direction": "horizontal", "sizes": [1, 1],
			"children": [
				{"type": "terminal"},
				{"type": "split", "direction": "vertical", "sizes": [1, 1], "children": [
					{"type": "terminal"},
					{"type": "boom"}
				]}
			]
		}
	}`
	_, err := Parse([]byte(src))
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "unknown node type")
}
