package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type sandboxSignalStartSink struct {
	started chan model.Event
}

func (s *sandboxSignalStartSink) Emit(ev model.Event) {
	if ev.Type == model.EventTypeSessionStarted && ev.TaskID == "signal-lifecycle" {
		s.started <- ev
	}
}

func TestRunner_RecycleSandboxSignal(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "fresh"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("ARGUS_SANDBOXED", "1")
			sink := &sandboxSignalStartSink{started: make(chan model.Event, 4)}
			prev := events.SetSink(sink)
			t.Cleanup(func() { events.SetSink(prev) })
			waitStarted := func() model.Event {
				t.Helper()
				select {
				case ev := <-sink.started:
					return ev
				case <-time.After(5 * time.Second):
					t.Fatal("session did not start")
					return model.Event{}
				}
			}
			cfg := config.Config{
				Defaults: config.Defaults{Backend: "custom"},
				Backends: map[string]config.Backend{"custom": {Command: "sh -c 'exec sleep 60'"}},
			}
			r := NewRunner(nil)
			t.Cleanup(r.shutdownCancel)
			task := &model.Task{ID: "signal-lifecycle", Worktree: t.TempDir(), SessionID: "old-session", Sandboxed: true}
			_, err := r.Start(task, cfg, 24, 80, resume)
			testutil.NoError(t, err)
			waitStarted()
			first := r.Get(task.ID).(*Session)
			t.Cleanup(func() { testutil.NoError(t, first.Stop()) })
			_, found := envLast(first.Cmd.Env, "ARGUS_SANDBOXED")
			testutil.False(t, found)

			// Exercise the actual queued restart, using the cleared ID and seed
			// prompt that callers supply for recycle. The parent's old signal and
			// the task's old persisted sandbox flag must not survive this launch.
			recycled := *task
			recycled.SessionID = ""
			recycled.Prompt = "recycle seed"
			testutil.NoError(t, r.Recycle(&recycled, cfg, 24, 80))
			ev := waitStarted()
			var payload struct{ Resume bool }
			testutil.NoError(t, json.Unmarshal(ev.Payload, &payload))
			testutil.False(t, payload.Resume)
			second := r.Get(task.ID).(*Session)
			testutil.True(t, first != second)
			t.Cleanup(func() { testutil.NoError(t, second.Stop()) })
			_, found = envLast(second.Cmd.Env, "ARGUS_SANDBOXED")
			testutil.False(t, found)
		})
	}
}
