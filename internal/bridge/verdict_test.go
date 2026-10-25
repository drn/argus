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
