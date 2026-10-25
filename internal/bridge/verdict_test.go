package bridge

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestOverride(t *testing.T) {
	t.Parallel()
	states := map[string]State{"busy": {Busy: true}, "question": {Busy: true, NeedsInput: true}, "idle": {}}
	testutil.DeepEqual(t, Override([]string{"fallback", "busy"}, states, Quiescent), []string{"fallback", "idle", "question"})
	testutil.DeepEqual(t, Override([]string{"fallback", "busy"}, states, AwaitingInput), []string{"fallback", "question"})
	testutil.DeepEqual(t, Override(nil, states, Working), []string{"busy"})
}

func TestResolvePermission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, confidence string
		needs, scraper, want   bool
	}{
		{"approved running", KindPermission, ConfidencePendingOrRunning, true, false, false},
		{"visible permission", KindPermission, ConfidencePendingOrRunning, true, true, true},
		{"exact permission", KindPermission, ConfidenceExact, true, false, true},
		{"legacy permission", KindPermission, "", true, false, false},
		{"question", KindQuestion, ConfidenceExact, true, false, true},
		{"plan", KindPlan, ConfidenceExact, true, false, true},
		{"cleared", KindPermission, ConfidencePendingOrRunning, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := State{Busy: true, NeedsInput: tc.needs, NeedsInputKind: tc.kind, NeedsInputConfidence: tc.confidence}
			got := Resolve(st, tc.scraper)
			testutil.Equal(t, got.NeedsInput, tc.want)
			testutil.True(t, got.Busy)
			testutil.Equal(t, st.NeedsInput, tc.needs)
			testutil.Equal(t, Quiescent(got), tc.want)
			testutil.Equal(t, Working(got), !tc.want)
			want := []string{}
			if tc.want {
				want = []string{"t"}
			}
			testutil.DeepEqual(t, Override(nil, map[string]State{"t": got}, AwaitingInput), want)
		})
	}
}
