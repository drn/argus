package main

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

type fakeVoiceClient struct {
	calls  []string
	err    error
	status daemon.VoiceStatusResp
	closed bool
}

func (f *fakeVoiceClient) VoiceStart() error { f.calls = append(f.calls, "start"); return f.err }
func (f *fakeVoiceClient) VoiceStop() error  { f.calls = append(f.calls, "stop"); return f.err }
func (f *fakeVoiceClient) VoiceStatus() (daemon.VoiceStatusResp, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.err
}
func (f *fakeVoiceClient) Close() error { f.closed = true; return nil }

func stubDialVoice(t *testing.T, c voiceClient, err error) {
	t.Helper()
	orig := dialVoice
	dialVoice = func() (voiceClient, error) { return c, err }
	t.Cleanup(func() { dialVoice = orig })
}

var voiceNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func TestRunVoice_Args(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, voiceUsage},
		{"too many", []string{"start", "x"}, voiceUsage},
		{"unknown", []string{"bogus"}, "unknown voice subcommand: bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubDialVoice(t, nil, errors.New("must not dial"))
			var out, errOut bytes.Buffer
			testutil.Equal(t, runVoice(tt.args, &out, &errOut, voiceNow), 1)
			testutil.Contains(t, errOut.String(), tt.want)
			testutil.Equal(t, out.String(), "")
		})
	}
}

func TestRunVoice_DialError(t *testing.T) {
	stubDialVoice(t, nil, errors.New("no socket"))
	var out, errOut bytes.Buffer
	testutil.Equal(t, runVoice([]string{"status"}, &out, &errOut, voiceNow), 1)
	testutil.Contains(t, errOut.String(), "daemon not reachable (no socket)")
}

func TestRunVoice_Subcommands(t *testing.T) {
	tests := []struct {
		sub     string
		err     error
		code    int
		wantOut string
		wantErr string
	}{
		{"start", nil, 0, "Recording with Wispr Flow", ""},
		{"stop", nil, 0, "launches in the background", ""},
		{"status", nil, 0, "Voice capture is not running", ""},
		{"start", errors.New("voice off"), 1, "", "argus voice start: voice off"},
		{"stop", errors.New("voice off"), 1, "", "argus voice stop: voice off"},
		{"status", errors.New("voice off"), 1, "", "argus voice status: voice off"},
	}
	for _, tt := range tests {
		t.Run(tt.sub+"/err="+strconv.FormatBool(tt.err != nil), func(t *testing.T) {
			fc := &fakeVoiceClient{err: tt.err}
			stubDialVoice(t, fc, nil)
			var out, errOut bytes.Buffer

			testutil.Equal(t, runVoice([]string{tt.sub}, &out, &errOut, voiceNow), tt.code)
			testutil.DeepEqual(t, fc.calls, []string{tt.sub})
			testutil.True(t, fc.closed)
			if tt.wantOut != "" {
				testutil.Contains(t, out.String(), tt.wantOut)
			}
			if tt.wantErr != "" {
				testutil.Contains(t, errOut.String(), tt.wantErr)
			}
		})
	}
}

func TestPrintVoiceStatus(t *testing.T) {
	tests := []struct {
		name    string
		st      daemon.VoiceStatusResp
		want    []string
		notWant []string
	}{
		{
			name: "disabled, never used",
			st:   daemon.VoiceStatusResp{Available: true, Hotkey: "right_option"},
			want: []string{
				"right_option (disabled; set [voice] enabled = true",
				"Input Monitoring:  not granted; open System Settings > Privacy & Security > Input Monitoring",
				"Recording:         no",
				"Last result:       none yet",
			},
			notWant: []string{"Processing"},
		},
		{
			name: "active and recording",
			st: daemon.VoiceStatusResp{
				Available: true, HotkeyEnabled: true, TapActive: true, Hotkey: "right_option",
				InputMonitoringGranted: true, Recording: true, StartedAt: voiceNow.Add(-3 * time.Second),
				Processing: 2,
				LastResult: "launched", LastDetail: "fix bug · argus", LastTaskID: "t1", LastAt: voiceNow.Add(-time.Minute),
			},
			want: []string{
				"Hotkey:            right_option (active)",
				"Input Monitoring:  granted",
				"Recording:         yes, for 3s",
				"Processing:        2 transcript(s)",
				"Last result:       launched: fix bug · argus [task t1] (1m0s ago)",
			},
		},
		{
			name: "enabled but tap failed",
			st:   daemon.VoiceStatusResp{Available: true, HotkeyEnabled: true, Hotkey: "ctrl", LastResult: "discarded"},
			want: []string{"ctrl (enabled but not listening", "Last result:       discarded\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printVoiceStatus(&buf, tt.st, voiceNow)
			for _, w := range tt.want {
				testutil.Contains(t, buf.String(), w)
			}
			for _, nw := range tt.notWant {
				testutil.False(t, strings.Contains(buf.String(), nw))
			}
		})
	}
}
