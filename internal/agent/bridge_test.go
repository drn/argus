package agent

import (
	"os/exec"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSessionBridgeGeneration(t *testing.T) {
	t.Parallel()
	testutil.Equal(t, SessionBridgeGeneration(nil), "")
	testutil.Equal(t, (&Session{}).BridgeGeneration(), "")
	s := &Session{Cmd: &exec.Cmd{Env: []string{"ARGUS_BRIDGE_GENERATION=parent", "ARGUS_BRIDGE_GENERATION=current"}}}
	testutil.Equal(t, SessionBridgeGeneration(s), "current")
}
