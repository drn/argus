package daemon

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// taskRecordingRunner records the task each session-core handler rebuilds
// from the wire before handing it to the runner.
type taskRecordingRunner struct {
	fakeSupClient
	started, kicked, recycled *model.Task
}

func (r *taskRecordingRunner) Start(t *model.Task, _ config.Config, _, _ uint16, _ bool) (agent.SessionHandle, error) {
	r.started = t
	return nil, errTestStartRefused
}

func (r *taskRecordingRunner) KickRerender(t *model.Task, _ config.Config, _, _ uint16) error {
	r.kicked = t
	return nil
}

func (r *taskRecordingRunner) Recycle(t *model.Task, _ config.Config, _, _ uint16) error {
	r.recycled = t
	return nil
}

var errTestStartRefused = errors.New("refused")

// TestSessionCore_RebuildCarriesAccount pins that every wire→task rebuild keeps
// the fields BuildCmd reads beyond the model-resolution set: without Account a
// supervisor spawn silently runs on the default Claude account.
func TestSessionCore_RebuildCarriesAccount(t *testing.T) {
	r := &taskRecordingRunner{}
	c := newSessionCore(r, config.DefaultConfig, make(chan struct{}))

	t.Run("start", func(t *testing.T) {
		var resp StartResp
		testutil.NoError(t, c.StartSession(&StartReq{TaskID: "a", Account: "work", SandboxOverride: "enabled"}, &resp))
		testutil.Equal(t, r.started.Account, "work")
		testutil.Equal(t, r.started.SandboxOverride, "enabled")
	})
	t.Run("kick", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.KickRerender(&KickReq{TaskID: "a", Account: "work", SandboxOverride: "disabled"}, &resp))
		testutil.Equal(t, r.kicked.Account, "work")
		testutil.Equal(t, r.kicked.SandboxOverride, "disabled")
	})
	t.Run("recycle", func(t *testing.T) {
		var resp StatusResp
		testutil.NoError(t, c.Recycle(&RecycleReq{TaskID: "a", Account: "work", SandboxOverride: "enabled"}, &resp))
		testutil.Equal(t, r.recycled.Account, "work")
		testutil.Equal(t, r.recycled.SandboxOverride, "enabled")
	})
}

func TestProtocolVersionCoversTaskAccount(t *testing.T) {
	if ProtocolVersion < 8 {
		t.Fatalf("task launch requests carry Account/SandboxOverride, which landed in v8; ProtocolVersion is %d", ProtocolVersion)
	}
}
