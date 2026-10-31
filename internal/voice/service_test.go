package voice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/voicetask"
	"github.com/drn/argus/internal/wispr"
)

type fakeDriver struct {
	mu       sync.Mutex
	starts   int
	stops    int
	waits    []time.Time
	timeouts []time.Duration
	startErr error
	stopErr  error
	// gate, when set, blocks WaitTranscript until a value arrives or ctx ends.
	gate chan struct{}
	tr   wispr.Transcript
	err  error
}

func (f *fakeDriver) StartRecording(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return f.startErr
}

func (f *fakeDriver) StopRecording(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return f.stopErr
}

func (f *fakeDriver) WaitTranscript(ctx context.Context, since time.Time, timeout time.Duration) (wispr.Transcript, error) {
	f.mu.Lock()
	f.waits = append(f.waits, since)
	f.timeouts = append(f.timeouts, timeout)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return wispr.Transcript{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tr, f.err
}

func (f *fakeDriver) counts() (starts, stops, waits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops, len(f.waits)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type note struct{ Title, Body string }

type recorder struct {
	mu    sync.Mutex
	notes []note
	texts []string
}

func (r *recorder) notify(title, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, note{title, body})
}

func (r *recorder) snapshot() []note {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]note(nil), r.notes...)
}

type harness struct {
	svc    *Service
	driver *fakeDriver
	clock  *fakeClock
	rec    *recorder
}

func newHarness(t *testing.T, launch LaunchFunc) *harness {
	t.Helper()
	h := &harness{
		driver: &fakeDriver{tr: wispr.Transcript{ID: "tr1", Text: "fix the login bug in project argus"}},
		clock:  &fakeClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)},
		rec:    &recorder{},
	}
	if launch == nil {
		launch = func(_ context.Context, text string) (voicetask.Launched, error) {
			h.rec.mu.Lock()
			h.rec.texts = append(h.rec.texts, text)
			h.rec.mu.Unlock()
			return voicetask.Launched{
				Task:    &model.Task{ID: "task-1", Name: "fix the login bug"},
				Project: "argus",
				Prompt:  "fix the login bug",
				Source:  "spoken",
			}, nil
		}
	}
	h.svc = New(h.driver, launch, h.rec.notify)
	h.svc.Now = h.clock.Now
	t.Cleanup(h.svc.Close)
	return h
}

func (h *harness) hold(t *testing.T, d time.Duration) {
	t.Helper()
	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(d)
	testutil.NoError(t, h.svc.Stop())
	h.svc.Wait()
}

func TestService_HappyPath(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.TranscriptTimeout = 7 * time.Second
	start := h.clock.Now()

	h.hold(t, time.Second)

	starts, stops, waits := h.driver.counts()
	testutil.Equal(t, starts, 1)
	testutil.Equal(t, stops, 1)
	testutil.Equal(t, waits, 1)
	testutil.Equal(t, h.driver.waits[0], start.Add(-2*time.Second))
	testutil.Equal(t, h.driver.timeouts[0], 7*time.Second)
	testutil.DeepEqual(t, h.rec.texts, []string{"fix the login bug in project argus"})
	testutil.DeepEqual(t, h.rec.snapshot(), []note{{TitleLaunched, "fix the login bug · argus"}})

	st := h.svc.Status()
	testutil.False(t, st.Recording)
	testutil.Equal(t, st.Processing, 0)
	testutil.Equal(t, st.LastResult, ResultLaunched)
	testutil.Equal(t, st.LastTaskID, "task-1")
	testutil.Equal(t, st.LastDetail, "fix the login bug · argus")
	testutil.Equal(t, st.LastAt, h.clock.Now())
}

func TestService_RecordingStatus(t *testing.T) {
	h := newHarness(t, nil)
	start := h.clock.Now()
	testutil.NoError(t, h.svc.Start())
	st := h.svc.Status()
	testutil.True(t, st.Recording)
	testutil.Equal(t, st.StartedAt, start)
}

func TestService_ShortHoldDiscarded(t *testing.T) {
	h := newHarness(t, nil)
	h.hold(t, 100*time.Millisecond)

	_, stops, waits := h.driver.counts()
	testutil.Equal(t, stops, 1)
	testutil.Equal(t, waits, 0)
	testutil.Equal(t, len(h.rec.snapshot()), 0)
	testutil.Equal(t, h.svc.Status().LastResult, ResultDiscarded)
}

func TestService_DoubleStartIsNoop(t *testing.T) {
	h := newHarness(t, nil)
	testutil.NoError(t, h.svc.Start())
	testutil.NoError(t, h.svc.Start())
	starts, _, _ := h.driver.counts()
	testutil.Equal(t, starts, 1)
}

func TestService_StopWhenIdleIsNoop(t *testing.T) {
	h := newHarness(t, nil)
	testutil.NoError(t, h.svc.Stop())
	_, stops, _ := h.driver.counts()
	testutil.Equal(t, stops, 0)
	testutil.Equal(t, h.svc.Status().LastResult, "")
}

func TestService_StartFailure(t *testing.T) {
	h := newHarness(t, nil)
	boom := errors.New("open failed")
	h.driver.startErr = boom

	testutil.ErrorIs(t, h.svc.Start(), boom)
	testutil.False(t, h.svc.Status().Recording)
	testutil.Equal(t, h.svc.Status().LastResult, ResultFailed)
	testutil.DeepEqual(t, h.rec.snapshot(), []note{{TitleFailed, "Couldn't start Wispr Flow recording"}})

	h.driver.startErr = nil
	testutil.NoError(t, h.svc.Start())
	testutil.True(t, h.svc.Status().Recording)
}

func TestService_StopFailure(t *testing.T) {
	h := newHarness(t, nil)
	boom := errors.New("open failed")
	h.driver.stopErr = boom
	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(time.Second)

	testutil.ErrorIs(t, h.svc.Stop(), boom)
	h.svc.Wait()
	_, _, waits := h.driver.counts()
	testutil.Equal(t, waits, 0)
	testutil.False(t, h.svc.Status().Recording)
	testutil.DeepEqual(t, h.rec.snapshot(), []note{{TitleFailed, "Couldn't stop Wispr Flow recording"}})
}

func TestService_Failures(t *testing.T) {
	tests := []struct {
		name      string
		waitErr   error
		launchErr error
		want      string
	}{
		{"timeout", wispr.ErrTimeout, nil, "No transcript from Wispr Flow (timed out)"},
		{"dismissed", wispr.ErrDismissed, nil, "Wispr Flow dismissed the dictation"},
		{"no db", wispr.ErrNoDB, nil, "Wispr Flow history database not found"},
		{"other wait error", errors.New("disk"), nil, "Couldn't read the Wispr Flow transcript: disk"},
		{"no project", nil, voicetask.ErrNoProject, `No project: say "in project <name>" or set defaults.voice_project`},
		{"empty", nil, voicetask.ErrEmptyTranscript, "The transcript had no task text"},
		{"create", nil, errors.New("worktree exists"), "Couldn't create the task: worktree exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := func(context.Context, string) (voicetask.Launched, error) {
				return voicetask.Launched{}, tt.launchErr
			}
			h := newHarness(t, launch)
			h.driver.err = tt.waitErr

			h.hold(t, time.Second)

			testutil.DeepEqual(t, h.rec.snapshot(), []note{{TitleFailed, tt.want}})
			st := h.svc.Status()
			testutil.Equal(t, st.LastResult, ResultFailed)
			testutil.Equal(t, st.LastDetail, tt.want)
			testutil.Equal(t, st.LastTaskID, "")
		})
	}
}

func TestService_MaxDurationAutoStop(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.MaxDuration = 10 * time.Millisecond
	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(time.Minute)

	deadline := time.Now().Add(5 * time.Second)
	for h.svc.Status().Recording && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	testutil.False(t, h.svc.Status().Recording)
	h.svc.Wait()
	_, stops, waits := h.driver.counts()
	testutil.Equal(t, stops, 1)
	testutil.Equal(t, waits, 1)
	testutil.Equal(t, h.svc.Status().LastResult, ResultLaunched)
}

func TestService_AutoStopIgnoresStaleGeneration(t *testing.T) {
	h := newHarness(t, nil)
	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(time.Second)
	testutil.NoError(t, h.svc.Stop())
	testutil.NoError(t, h.svc.Start())

	h.svc.autoStop(1)
	testutil.True(t, h.svc.Status().Recording)
}

func TestService_ConcurrentProcessing(t *testing.T) {
	h := newHarness(t, nil)
	h.driver.gate = make(chan struct{})

	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(time.Second)
	testutil.NoError(t, h.svc.Stop())
	testutil.Equal(t, h.svc.Status().Processing, 1)

	testutil.NoError(t, h.svc.Start())
	testutil.True(t, h.svc.Status().Recording)
	h.clock.Advance(time.Second)
	testutil.NoError(t, h.svc.Stop())
	testutil.Equal(t, h.svc.Status().Processing, 2)

	close(h.driver.gate)
	h.svc.Wait()
	testutil.Equal(t, h.svc.Status().Processing, 0)
	testutil.Equal(t, len(h.rec.snapshot()), 2)
}

func TestService_CloseCancelsPendingWait(t *testing.T) {
	h := newHarness(t, nil)
	h.driver.gate = make(chan struct{})
	h.svc.Notify = nil
	testutil.NoError(t, h.svc.Start())
	h.clock.Advance(time.Second)
	testutil.NoError(t, h.svc.Stop())

	h.svc.Close()
	st := h.svc.Status()
	testutil.Equal(t, st.LastResult, ResultFailed)
	testutil.Equal(t, st.LastDetail, "Voice capture shut down")
}

func TestService_HotkeyAndPermissionStatus(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.InputMonitoring = func() bool { return true }
	h.svc.SetHotkey(true, "right_option", true)

	st := h.svc.Status()
	testutil.True(t, st.HotkeyEnabled)
	testutil.Equal(t, st.Hotkey, "right_option")
	testutil.True(t, st.TapActive)
	testutil.True(t, st.InputMonitoringGranted)
}

func TestService_ZeroValueDefaults(t *testing.T) {
	s := &Service{}
	testutil.Equal(t, s.maxDuration(), DefaultMaxDuration)
	testutil.Equal(t, s.minHold(), DefaultMinHold)
	testutil.Equal(t, s.transcriptTimeout(), DefaultTranscriptTimeout)
	testutil.Equal(t, s.baseCtx(), context.Background())
	testutil.False(t, s.now().IsZero())
	s.notify("t", "b")
	s.Close()
}

func TestLaunchedBody(t *testing.T) {
	task := &model.Task{Name: "fix bug"}
	tests := []struct {
		name string
		res  voicetask.Launched
		want string
	}{
		{"spoken", voicetask.Launched{Task: task, Project: "argus", Source: "spoken"}, "fix bug · argus"},
		{"jev", voicetask.Launched{Task: task, Project: "argus", Source: "jev"}, "fix bug · argus (Jev)"},
		{"default", voicetask.Launched{Task: task, Project: "argus", Source: "default"}, "fix bug · argus (default)"},
		{"unknown spoken", voicetask.Launched{Task: task, Project: "argus", Source: "default", SpokenProject: "zeus"}, "fix bug · argus (default) — unknown project “zeus”"},
		{"spoken ignores earlier miss", voicetask.Launched{Task: task, Project: "argus", Source: "spoken", SpokenProject: "zeus"}, "fix bug · argus"},
		{"nil task falls back to prompt", voicetask.Launched{Project: "argus", Prompt: "do it", Source: "spoken"}, "do it · argus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, launchedBody(tt.res), tt.want)
		})
	}
}
