package tui

import (
	"sort"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

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

	var child = got[0]
	if got[1].ID == "c" {
		child = got[1]
	}
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
