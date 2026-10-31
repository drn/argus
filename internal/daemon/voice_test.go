package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hotkey"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/voice"
	"github.com/drn/argus/internal/voicetask"
	"github.com/drn/argus/internal/wispr"
)

type voiceFakeDriver struct {
	mu     sync.Mutex
	starts int
	stops  int
}

func (f *voiceFakeDriver) StartRecording(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return nil
}

func (f *voiceFakeDriver) StopRecording(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return nil
}

func (f *voiceFakeDriver) WaitTranscript(context.Context, time.Time, time.Duration) (wispr.Transcript, error) {
	return wispr.Transcript{}, wispr.ErrDismissed
}

func (f *voiceFakeDriver) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

type voiceFakeTap struct{ closed bool }

func (t *voiceFakeTap) Close() { t.closed = true }

type voiceSeams struct {
	driver   *voiceFakeDriver
	tap      *voiceFakeTap
	tapErr   error
	combo    hotkey.Combo
	notes    []string
	granted  bool
	request  bool
	requests int
}

func installVoiceSeams(d *Daemon) *voiceSeams {
	s := &voiceSeams{driver: &voiceFakeDriver{}, tap: &voiceFakeTap{}}
	d.voiceDriver = func(config.VoiceConfig) voice.Driver { return s.driver }
	d.voiceNotify = func(title, body string) { s.notes = append(s.notes, title+": "+body) }
	d.voiceInputGranted = func() bool { return s.granted }
	d.voiceRequestInput = func() bool { s.requests++; return s.request }
	d.voiceStartTap = func(c hotkey.Combo, _ func(hotkey.Event)) (voiceTap, error) {
		s.combo = c
		if s.tapErr != nil {
			return nil, s.tapErr
		}
		return s.tap, nil
	}
	d.voiceHotkeySupported = true
	return s
}

func TestStartVoice(t *testing.T) {
	tests := []struct {
		name        string
		voice       config.VoiceConfig
		unsupported bool
		granted     bool
		request     bool
		tapErr      error
		wantEnabled bool
		wantHotkey  string
		wantTap     bool
		wantNotes   int
		wantReqs    int
	}{
		{name: "disabled", voice: config.VoiceConfig{}, wantHotkey: "right_option"},
		{name: "unsupported", voice: config.VoiceConfig{Enabled: true, Hotkey: "ctrl"}, unsupported: true, wantEnabled: true, wantHotkey: "ctrl"},
		{name: "invalid hotkey", voice: config.VoiceConfig{Enabled: true, Hotkey: "banana"}, wantEnabled: true, wantHotkey: "banana", wantNotes: 1},
		{name: "granted", voice: config.VoiceConfig{Enabled: true, Hotkey: "right_option"}, granted: true, wantEnabled: true, wantTap: true},
		{name: "not granted request denied", voice: config.VoiceConfig{Enabled: true, Hotkey: "right_option"}, wantEnabled: true, wantTap: true, wantNotes: 1, wantReqs: 1},
		{name: "not granted request ok", voice: config.VoiceConfig{Enabled: true, Hotkey: "right_option"}, request: true, wantEnabled: true, wantTap: true, wantReqs: 1},
		{name: "tap fails", voice: config.VoiceConfig{Enabled: true, Hotkey: "right_option"}, granted: true, tapErr: errors.New("no tap"), wantEnabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			s := installVoiceSeams(d)
			s.granted, s.request, s.tapErr = tt.granted, tt.request, tt.tapErr
			d.voiceHotkeySupported = !tt.unsupported
			cfg := config.DefaultConfig()
			cfg.Voice = tt.voice
			cfg.Voice.TranscriptTimeoutSeconds = 12

			d.startVoice(cfg)
			t.Cleanup(d.stopVoice)

			svc := d.voiceService()
			testutil.NotNil(t, svc)
			testutil.Equal(t, svc.TranscriptTimeout, 12*time.Second)
			st := svc.Status()
			testutil.Equal(t, st.HotkeyEnabled, tt.wantEnabled)
			testutil.Equal(t, st.TapActive, tt.wantTap)
			testutil.Equal(t, st.InputMonitoringGranted, tt.granted)
			if tt.wantHotkey != "" {
				testutil.Equal(t, st.Hotkey, tt.wantHotkey)
			}
			if tt.wantTap {
				testutil.Equal(t, st.Hotkey, s.combo.String())
			}
			testutil.Equal(t, len(s.notes), tt.wantNotes)
			testutil.Equal(t, s.requests, tt.wantReqs)
		})
	}
}

func TestStartVoice_PermissionNotice(t *testing.T) {
	d, _ := testDaemon(t)
	s := installVoiceSeams(d)
	cfg := config.DefaultConfig()
	cfg.Voice.Enabled = true
	d.startVoice(cfg)
	t.Cleanup(d.stopVoice)
	testutil.Equal(t, len(s.notes), 1)
	testutil.Contains(t, s.notes[0], voicePermissionHint)
}

func TestStopVoice_ClosesTap(t *testing.T) {
	d, _ := testDaemon(t)
	s := installVoiceSeams(d)
	s.granted = true
	cfg := config.DefaultConfig()
	cfg.Voice.Enabled = true
	d.startVoice(cfg)

	d.stopVoice()
	testutil.True(t, s.tap.closed)
	d.mu.Lock()
	tap := d.voiceTap
	d.mu.Unlock()
	testutil.Nil(t, tap)
	d.stopVoice()
}

func TestHandleVoiceHotkey(t *testing.T) {
	d, _ := testDaemon(t)
	d.handleVoiceHotkey(hotkey.Began)

	s := installVoiceSeams(d)
	d.startVoice(config.DefaultConfig())
	t.Cleanup(d.stopVoice)

	d.handleVoiceHotkey(hotkey.Began)
	testutil.True(t, d.voiceService().Status().Recording)
	d.handleVoiceHotkey(hotkey.Cancelled)
	testutil.True(t, d.voiceService().Status().Recording)
	d.handleVoiceHotkey(hotkey.Ended)
	testutil.False(t, d.voiceService().Status().Recording)

	starts, stops := s.driver.counts()
	testutil.Equal(t, starts, 1)
	testutil.Equal(t, stops, 1)
}

func TestRPC_VoiceRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, sockPath := testDaemon(t)
	s := installVoiceSeams(d)

	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitForSocket(t, sockPath)
	c := dialRPC(t, sockPath)

	var st VoiceStatusResp
	testutil.NoError(t, c.Call("Daemon.VoiceStatus", &Empty{}, &st))
	testutil.True(t, st.Available)
	testutil.False(t, st.Recording)
	testutil.False(t, st.HotkeyEnabled)
	testutil.Equal(t, st.Hotkey, "right_option")

	var resp StatusResp
	testutil.NoError(t, c.Call("Daemon.VoiceStart", &Empty{}, &resp))
	testutil.True(t, resp.OK)
	testutil.NoError(t, c.Call("Daemon.VoiceStatus", &Empty{}, &st))
	testutil.True(t, st.Recording)
	testutil.False(t, st.StartedAt.IsZero())

	resp = StatusResp{}
	testutil.NoError(t, c.Call("Daemon.VoiceStop", &Empty{}, &resp))
	testutil.True(t, resp.OK)
	starts, stops := s.driver.counts()
	testutil.Equal(t, starts, 1)
	testutil.Equal(t, stops, 1)
}

type voiceErrDriver struct{ voiceFakeDriver }

func (*voiceErrDriver) StartRecording(context.Context) error { return errors.New("start boom") }
func (*voiceErrDriver) StopRecording(context.Context) error  { return errors.New("stop boom") }

func TestRPC_VoiceErrors(t *testing.T) {
	d, _ := testDaemon(t)
	svc := &RPCService{daemon: d}

	t.Run("unavailable", func(t *testing.T) {
		var start, stop StatusResp
		testutil.NoError(t, svc.VoiceStart(&Empty{}, &start))
		testutil.NoError(t, svc.VoiceStop(&Empty{}, &stop))
		testutil.Equal(t, start.Error, errVoiceUnavailable.Error())
		testutil.Equal(t, stop.Error, errVoiceUnavailable.Error())
		var st VoiceStatusResp
		testutil.NoError(t, svc.VoiceStatus(&Empty{}, &st))
		testutil.False(t, st.Available)
	})

	t.Run("driver errors", func(t *testing.T) {
		vs := voice.New(&voiceErrDriver{}, nil, nil)
		t.Cleanup(vs.Close)
		d.mu.Lock()
		d.voice = vs
		d.mu.Unlock()

		var start StatusResp
		testutil.NoError(t, svc.VoiceStart(&Empty{}, &start))
		testutil.Equal(t, start.Error, "start boom")

		vs.Driver = &voiceFakeDriver{}
		testutil.NoError(t, vs.Start())
		vs.Driver = &voiceErrDriver{}
		var stop StatusResp
		testutil.NoError(t, svc.VoiceStop(&Empty{}, &stop))
		testutil.Equal(t, stop.Error, "stop boom")
	})
}

func TestLaunchVoiceTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)

	_, err := d.launchVoiceTask(context.Background(), "fix the bug")
	testutil.ErrorIs(t, err, voicetask.ErrNoProject)

	testutil.NoError(t, d.db.SetProject("argus", config.Project{Path: t.TempDir()}))
	names, err := d.voiceProjects()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, names, []string{"argus"})

	var got struct{ name, prompt, project string }
	orig := newVoiceLauncher
	newVoiceLauncher = func(cfg config.Config, projects func() ([]string, error), _ voicetask.CreateFunc, stamp func(string) error, sanitize func(string) string) *voicetask.Launcher {
		return orig(cfg, projects, func(name, prompt, project, _ string) (*model.Task, error) {
			got.name, got.prompt, got.project = name, prompt, project
			return &model.Task{ID: "v1", Name: name}, nil
		}, func(string) error { return nil }, sanitize)
	}
	t.Cleanup(func() { newVoiceLauncher = orig })

	res, err := d.launchVoiceTask(context.Background(), "fix the bug in project argus")
	testutil.NoError(t, err)
	testutil.Equal(t, res.Project, "argus")
	testutil.Equal(t, got.project, "argus")
	testutil.Equal(t, got.prompt, res.Prompt)
}

func TestVoiceCreate_UnknownProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _ := testDaemon(t)
	_, err := d.voiceCreate("n", "p", "missing", "")
	testutil.Error(t, err)
}

func TestVoiceStamp(t *testing.T) {
	d, _ := testDaemon(t)
	testutil.NoError(t, d.db.Add(&model.Task{ID: "v1", Name: "voice"}))
	testutil.NoError(t, d.voiceStamp("v1"))
	entries, err := d.db.ListMeta("v1", db.UIMetaNamespace)
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Key] = e.Value
	}
	testutil.DeepEqual(t, got, map[string]string{db.UIMetaKeyNew: "true", db.UIMetaKeySource: "voice"})
}

func TestVoiceTaskName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"short", "short"},
		{"a\tb\nc\rd", "a b c d"},
		{strings.Repeat("é", 50), strings.Repeat("é", 40)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			testutil.Equal(t, voiceTaskName(tt.in), tt.want)
		})
	}
}

func TestStartHotkeyTap_InvalidCombo(t *testing.T) {
	tap, err := startHotkeyTap(hotkey.Combo{}, func(hotkey.Event) {})
	testutil.Error(t, err)
	testutil.Nil(t, tap)
}

func TestNewWisprDriver(t *testing.T) {
	drv := newWisprDriver(config.VoiceConfig{WisprDB: "/tmp/x.sqlite"})
	w, ok := drv.(*wispr.Driver)
	testutil.True(t, ok)
	testutil.Equal(t, w.DBPath, "/tmp/x.sqlite")
}
