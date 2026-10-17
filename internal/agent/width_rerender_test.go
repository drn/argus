package agent

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type ordinaryKickRecorder struct {
	calls int
	err   error
}

func (r *ordinaryKickRecorder) KickRerender(*model.Task, config.Config, uint16, uint16) error {
	r.calls++
	return r.err
}

type boundedKickRecorder struct {
	ordinaryKickRecorder
	widthCalls int
}

func (r *boundedKickRecorder) KickWidthRerender(*model.Task, config.Config, uint16, uint16) error {
	r.widthCalls++
	return r.err
}

func TestKickWidthRerender_ProviderCapability(t *testing.T) {
	boom := errors.New("boom")
	t.Run("older provider preserves ordinary stop", func(t *testing.T) {
		r := &ordinaryKickRecorder{err: boom}
		testutil.ErrorIs(t, KickWidthRerender(r, &model.Task{}, config.Config{}, 30, 98), boom)
		testutil.Equal(t, r.calls, 1)
	})
	t.Run("bounded provider errors never fall back", func(t *testing.T) {
		r := &boundedKickRecorder{ordinaryKickRecorder: ordinaryKickRecorder{err: boom}}
		testutil.ErrorIs(t, KickWidthRerender(r, &model.Task{}, config.Config{}, 30, 98), boom)
		testutil.Equal(t, r.widthCalls, 1)
		testutil.Equal(t, r.calls, 0)
	})
}
