// Package voice runs daemon-owned hold-to-talk task capture: it drives Wispr
// Flow's recording, waits for the finished transcript, and launches an Argus
// task from it.
package voice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
	"github.com/drn/argus/internal/voicetask"
	"github.com/drn/argus/internal/wispr"
)

const (
	DefaultMaxDuration       = 5 * time.Minute
	DefaultMinHold           = 300 * time.Millisecond
	DefaultTranscriptTimeout = 30 * time.Second

	// sinceSlack widens the transcript lookup window so a Wispr history row
	// stamped slightly before our recorded start (clock skew, URL latency) still matches.
	sinceSlack = 2 * time.Second
)

const (
	TitleLaunched = "Argus: task launched"
	TitleFailed   = "Argus: voice task failed"
)

// Result values reported in Status.LastResult.
const (
	ResultLaunched  = "launched"
	ResultFailed    = "failed"
	ResultDiscarded = "discarded"
)

// Driver is the dictation backend (wispr.Driver in production).
type Driver interface {
	StartRecording(ctx context.Context) error
	StopRecording(ctx context.Context) error
	WaitTranscript(ctx context.Context, since time.Time, timeout time.Duration) (wispr.Transcript, error)
}

// LaunchFunc turns a transcript into a started task.
type LaunchFunc func(ctx context.Context, transcript string) (voicetask.Launched, error)

// Status is a point-in-time snapshot of the service.
type Status struct {
	Recording              bool
	Processing             int
	StartedAt              time.Time
	HotkeyEnabled          bool
	Hotkey                 string
	TapActive              bool
	InputMonitoringGranted bool
	LastResult             string
	LastDetail             string
	LastTaskID             string
	LastAt                 time.Time
}

// Service coordinates one recording at a time; transcript processing runs in
// the background so a new recording may start while an earlier one is still
// being transcribed.
type Service struct {
	Driver            Driver
	Launch            LaunchFunc
	Notify            func(title, body string)
	Now               func() time.Time
	TranscriptTimeout time.Duration
	MaxDuration       time.Duration
	MinHold           time.Duration
	// InputMonitoring reports the live Input Monitoring grant for Status.
	InputMonitoring func() bool

	mu        sync.Mutex
	recording bool
	startedAt time.Time
	gen       uint64
	timer     *time.Timer
	pending   int

	hotkeyEnabled bool
	hotkey        string
	tapActive     bool

	lastResult string
	lastDetail string
	lastTaskID string
	lastAt     time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a Service with default timings.
func New(driver Driver, launch LaunchFunc, notify func(title, body string)) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		Driver:            driver,
		Launch:            launch,
		Notify:            notify,
		Now:               time.Now,
		TranscriptTimeout: DefaultTranscriptTimeout,
		MaxDuration:       DefaultMaxDuration,
		MinHold:           DefaultMinHold,
		ctx:               ctx,
		cancel:            cancel,
	}
}

// SetHotkey records the global hotkey state reported by Status.
func (s *Service) SetHotkey(enabled bool, hotkey string, tapActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hotkeyEnabled, s.hotkey, s.tapActive = enabled, hotkey, tapActive
}

// Start begins a recording. It is a no-op while one is already in progress.
func (s *Service) Start() error {
	s.mu.Lock()
	if s.recording {
		s.mu.Unlock()
		uxlog.Log("[voice] start ignored: already recording")
		return nil
	}
	s.recording = true
	s.startedAt = s.now()
	s.gen++
	gen := s.gen
	s.mu.Unlock()

	if err := s.Driver.StartRecording(s.baseCtx()); err != nil {
		s.mu.Lock()
		if s.gen == gen {
			s.recording = false
		}
		s.mu.Unlock()
		uxlog.Log("[voice] start failed: %v", err)
		s.finish(ResultFailed, "couldn't start Wispr Flow recording", "")
		s.notify(TitleFailed, "Couldn't start Wispr Flow recording")
		return err
	}

	s.mu.Lock()
	if s.gen == gen && s.recording {
		s.timer = time.AfterFunc(s.maxDuration(), func() { s.autoStop(gen) })
	}
	s.mu.Unlock()
	uxlog.Log("[voice] recording started")
	return nil
}

func (s *Service) autoStop(gen uint64) {
	s.mu.Lock()
	match := s.recording && s.gen == gen
	s.mu.Unlock()
	if !match {
		return
	}
	uxlog.Log("[voice] max duration reached; auto-stopping")
	_ = s.Stop()
}

// Stop ends the current recording and processes its transcript in the
// background. It is a no-op when idle.
func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.recording {
		s.mu.Unlock()
		uxlog.Log("[voice] stop ignored: not recording")
		return nil
	}
	s.recording = false
	startedAt := s.startedAt
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()

	held := s.now().Sub(startedAt)
	if err := s.Driver.StopRecording(s.baseCtx()); err != nil {
		uxlog.Log("[voice] stop failed: %v", err)
		s.finish(ResultFailed, "couldn't stop Wispr Flow recording", "")
		s.notify(TitleFailed, "Couldn't stop Wispr Flow recording")
		return err
	}
	if held < s.minHold() {
		uxlog.Log("[voice] discarded short hold held=%s", held.Round(time.Millisecond))
		s.finish(ResultDiscarded, "hold too short", "")
		return nil
	}
	uxlog.Log("[voice] recording stopped held=%s; waiting for transcript", held.Round(time.Millisecond))

	s.mu.Lock()
	s.pending++
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.pending--
			s.mu.Unlock()
		}()
		s.process(startedAt.Add(-sinceSlack))
	}()
	return nil
}

func (s *Service) process(since time.Time) {
	tr, err := s.Driver.WaitTranscript(s.baseCtx(), since, s.transcriptTimeout())
	if err != nil {
		reason := transcriptFailure(err)
		uxlog.Log("[voice] transcript failed: %v", err)
		s.finish(ResultFailed, reason, "")
		s.notify(TitleFailed, reason)
		return
	}
	uxlog.Log("[voice] transcript received id=%s len=%d", tr.ID, len(tr.Text))

	res, err := s.Launch(s.baseCtx(), tr.Text)
	if err != nil {
		reason := launchFailure(err)
		uxlog.Log("[voice] launch failed: %v", err)
		s.finish(ResultFailed, reason, "")
		s.notify(TitleFailed, reason)
		return
	}
	body := launchedBody(res)
	taskID := ""
	if res.Task != nil {
		taskID = res.Task.ID
	}
	uxlog.Log("[voice] task launched id=%s project=%s source=%s", taskID, res.Project, res.Source)
	s.finish(ResultLaunched, body, taskID)
	s.notify(TitleLaunched, body)
}

func transcriptFailure(err error) string {
	switch {
	case errors.Is(err, wispr.ErrTimeout):
		return "No transcript from Wispr Flow (timed out)"
	case errors.Is(err, wispr.ErrDismissed):
		return "Wispr Flow dismissed the dictation"
	case errors.Is(err, wispr.ErrNoDB):
		return "Wispr Flow history database not found"
	case errors.Is(err, context.Canceled):
		return "Voice capture shut down"
	}
	return fmt.Sprintf("Couldn't read the Wispr Flow transcript: %v", err)
}

func launchFailure(err error) string {
	switch {
	case errors.Is(err, voicetask.ErrNoProject):
		return `No project: say "in project <name>" or set defaults.voice_project`
	case errors.Is(err, voicetask.ErrEmptyTranscript):
		return "The transcript had no task text"
	}
	return fmt.Sprintf("Couldn't create the task: %v", err)
}

func launchedBody(res voicetask.Launched) string {
	name := ""
	if res.Task != nil {
		name = res.Task.Name
	}
	if name == "" {
		name = res.Prompt
	}
	body := name + " · " + res.Project
	switch res.Source {
	case "jev":
		body += " (Jev)"
	case "default":
		body += " (default)"
	}
	if res.SpokenProject != "" && res.Source != "spoken" {
		body += " — unknown project “" + res.SpokenProject + "”"
	}
	return body
}

func (s *Service) finish(result, detail, taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResult, s.lastDetail, s.lastTaskID, s.lastAt = result, detail, taskID, s.now()
}

// Status returns a snapshot of the service state.
func (s *Service) Status() Status {
	granted := false
	if s.InputMonitoring != nil {
		granted = s.InputMonitoring()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Recording:              s.recording,
		Processing:             s.pending,
		HotkeyEnabled:          s.hotkeyEnabled,
		Hotkey:                 s.hotkey,
		TapActive:              s.tapActive,
		InputMonitoringGranted: granted,
		LastResult:             s.lastResult,
		LastDetail:             s.lastDetail,
		LastTaskID:             s.lastTaskID,
		LastAt:                 s.lastAt,
	}
	if s.recording {
		st.StartedAt = s.startedAt
	}
	return st
}

// Wait blocks until every in-flight transcript has been processed.
func (s *Service) Wait() { s.wg.Wait() }

// Close cancels in-flight transcript waits and the auto-stop timer.
func (s *Service) Close() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	uxlog.Log("[voice] service closed")
}

func (s *Service) notify(title, body string) {
	if s.Notify != nil {
		s.Notify(title, body)
	}
}

func (s *Service) baseCtx() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) maxDuration() time.Duration {
	if s.MaxDuration > 0 {
		return s.MaxDuration
	}
	return DefaultMaxDuration
}

func (s *Service) minHold() time.Duration {
	if s.MinHold > 0 {
		return s.MinHold
	}
	return DefaultMinHold
}

func (s *Service) transcriptTimeout() time.Duration {
	if s.TranscriptTimeout > 0 {
		return s.TranscriptTimeout
	}
	return DefaultTranscriptTimeout
}
