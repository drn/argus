package tui

import (
	"sort"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/dagview"
)

func findNodeByID(nodes []dagview.Node, id string) (dagview.Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return dagview.Node{}, false
}

// TestDAGNodesFromTasks_FiltersOrphansAndArchived covers the filter
// contract of dagNodesFromTasks: drop archived, drop pure orphans (no
// parents AND not referenced as a parent), keep every node that
// participates in the linked graph including stale-parent leaves.
func TestDAGNodesFromTasks_FiltersOrphansAndArchived(t *testing.T) {
	type want struct {
		ids []string
	}
	cases := []struct {
		name  string
		tasks []*model.Task
		want  want
	}{
		{
			name: "drops pure orphans, keeps linked pair",
			tasks: []*model.Task{
				{ID: "parent", Name: "parent", Status: model.StatusPending},
				{ID: "child", Name: "child", Status: model.StatusPending, DependsOn: []string{"parent"}},
				{ID: "orphan", Name: "orphan", Status: model.StatusPending},
			},
			want: want{ids: []string{"child", "parent"}},
		},
		{
			name: "drops archived",
			tasks: []*model.Task{
				{ID: "parent", Name: "parent", Status: model.StatusPending},
				{ID: "child", Name: "child", Status: model.StatusPending, DependsOn: []string{"parent"}},
				{ID: "old", Name: "old", Status: model.StatusComplete, Archived: true},
			},
			want: want{ids: []string{"child", "parent"}},
		},
		{
			name: "archived parent unlinks its child (child becomes orphan, dropped)",
			tasks: []*model.Task{
				{ID: "parent", Name: "parent", Status: model.StatusComplete, Archived: true},
				{ID: "child", Name: "child", Status: model.StatusPending, DependsOn: []string{"parent"}},
			},
			want: want{ids: nil},
		},
		{
			name: "stale DependsOn id keeps the node if it's referenced elsewhere",
			tasks: []*model.Task{
				{ID: "a", Name: "a", Status: model.StatusPending, DependsOn: []string{"ghost"}},
				{ID: "b", Name: "b", Status: model.StatusPending, DependsOn: []string{"a"}},
			},
			want: want{ids: []string{"a", "b"}},
		},
		{
			// A live parent satisfies hasParent on the first hit; the
			// trailing stale id is ignored. Both nodes are kept.
			name: "live parent + stale parent id mixed",
			tasks: []*model.Task{
				{ID: "live", Name: "live", Status: model.StatusPending},
				{ID: "child", Name: "child", Status: model.StatusPending, DependsOn: []string{"live", "ghost"}},
			},
			want: want{ids: []string{"child", "live"}},
		},
		{
			// Only an archived child points to the parent. Archived rows
			// are stripped from `live` before the `referenced` map is
			// built, so the parent is not referenced by anyone alive and
			// drops out as a pure orphan.
			name: "parent referenced only by archived child is dropped",
			tasks: []*model.Task{
				{ID: "parent", Name: "parent", Status: model.StatusPending},
				{ID: "ac", Name: "ac", Status: model.StatusComplete, Archived: true, DependsOn: []string{"parent"}},
			},
			want: want{ids: nil},
		},
		{
			name:  "empty input",
			tasks: nil,
			want:  want{ids: nil},
		},
		{
			name: "chain of three preserved",
			tasks: []*model.Task{
				{ID: "a", Name: "a", Status: model.StatusPending},
				{ID: "b", Name: "b", Status: model.StatusPending, DependsOn: []string{"a"}},
				{ID: "c", Name: "c", Status: model.StatusPending, DependsOn: []string{"b"}},
			},
			want: want{ids: []string{"a", "b", "c"}},
		},
		{
			// Filter is intentionally cycle-agnostic — orch.Link
			// prevents cycles at link time, so by the time the filter
			// runs the graph is already acyclic. A defective self-loop
			// input passes through; the layout's cycle guard handles
			// the rendering side.
			name: "self-loop passes through (cycle-agnostic filter)",
			tasks: []*model.Task{
				{ID: "a", Name: "a", Status: model.StatusPending, DependsOn: []string{"a"}},
			},
			want: want{ids: []string{"a"}},
		},
		{
			name: "mutual dependency passes through (cycle-agnostic filter)",
			tasks: []*model.Task{
				{ID: "a", Name: "a", Status: model.StatusPending, DependsOn: []string{"b"}},
				{ID: "b", Name: "b", Status: model.StatusPending, DependsOn: []string{"a"}},
			},
			want: want{ids: []string{"a", "b"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dagNodesFromTasks(tc.tasks)
			var ids []string
			for _, n := range got {
				ids = append(ids, n.ID)
			}
			sort.Strings(ids)
			testutil.DeepEqual(t, ids, tc.want.ids)
		})
	}
}

// TestDAGNodesFromTasks_PassthroughFields checks the per-node fields
// survive the filter — name, status, archived flag (always false post-
// filter), result, and a defensive copy of DependsOn.
func TestDAGNodesFromTasks_PassthroughFields(t *testing.T) {
	tasks := []*model.Task{
		{ID: "p", Name: "p", Status: model.StatusInProgress},
		{
			ID:        "c",
			Name:      "child",
			Status:    model.StatusInReview,
			Result:    `{"failed":true}`,
			DependsOn: []string{"p"},
		},
	}
	got := dagNodesFromTasks(tasks)
	testutil.Equal(t, len(got), 2)

	child, ok := findNodeByID(got, "c")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, child.Name, "child")
	testutil.Equal(t, child.Status, model.StatusInReview.String())
	testutil.Equal(t, child.Archived, false)
	testutil.Equal(t, child.Result, `{"failed":true}`)
	testutil.DeepEqual(t, child.DependsOn, []string{"p"})

	// DependsOn must be a defensive copy — mutating the source after the
	// projection must not leak into the widget's snapshot.
	tasks[1].DependsOn[0] = "mutated"
	testutil.DeepEqual(t, child.DependsOn, []string{"p"})
}

// TestSummarizeHalt covers the notice-string assembly used by the `h`
// keybinding handler. The function is a pure projection of HaltReport, so
// every branch is exercised by tiny in-memory inputs.
func TestSummarizeHalt(t *testing.T) {
	cases := []struct {
		name string
		in   orch.HaltReport
		want string
	}{
		{"empty report", orch.HaltReport{}, "no downstream tasks"},
		{"only stopped, singular", orch.HaltReport{Stopped: []string{"a"}}, "1 stopped"},
		{"only stopped, plural", orch.HaltReport{Stopped: []string{"a", "b"}}, "2 stopped"},
		{"only archived, singular", orch.HaltReport{Archived: []string{"x"}}, "1 archived"},
		{"only archived, plural", orch.HaltReport{Archived: []string{"x", "y", "z"}}, "3 archived"},
		{
			"both populated",
			orch.HaltReport{Stopped: []string{"a"}, Archived: []string{"x", "y"}},
			"1 stopped, 2 archived",
		},
		{
			// NotFound entries are tracked but intentionally excluded from
			// the user-visible summary — they're sessions that exited
			// between the snapshot and the stop call, and counting them
			// would inflate the notice.
			"NotFound is ignored in the summary",
			orch.HaltReport{NotFound: []string{"ghost"}},
			"no downstream tasks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, summarizeHalt(tc.in), tc.want)
		})
	}
}
