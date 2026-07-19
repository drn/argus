package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDiagnoseStopHook(t *testing.T) {
	tests := []struct {
		name     string
		commands []string
		readErr  error
		want     StopHookStatus
	}{
		{
			name:     "registered: bare command",
			commands: []string{"argus coord-hook"},
			want:     StopHookRegistered,
		},
		{
			name:     "registered: full-path invocation",
			commands: []string{"/Users/aaron/.local/bin/argus coord-hook"},
			want:     StopHookRegistered,
		},
		{
			name:     "registered: one of several hooks",
			commands: []string{"some-other-hook --flag", "argus coord-hook"},
			want:     StopHookRegistered,
		},
		{
			name:     "not registered: unrelated hooks only",
			commands: []string{"some-other-hook --flag"},
			want:     StopHookNotRegistered,
		},
		{
			name:     "not registered: no hooks at all",
			commands: nil,
			want:     StopHookNotRegistered,
		},
		{
			name:     "unknown: settings file unreadable takes priority over commands",
			commands: []string{"argus coord-hook"},
			readErr:  errors.New("open ~/.claude/settings.json: no such file or directory"),
			want:     StopHookUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiagnoseStopHook(tt.commands, tt.readErr)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestRenderStopHook(t *testing.T) {
	tests := []struct {
		name           string
		status         StopHookStatus
		wantContains   []string
		wantNotContain string
	}{
		{
			name:         "registered",
			status:       StopHookRegistered,
			wantContains: []string{"REGISTERED"},
		},
		{
			name:         "not registered includes the snippet",
			status:       StopHookNotRegistered,
			wantContains: []string{"NOT REGISTERED", "argus coord-hook", "settings.json"},
		},
		{
			name:         "unknown names the settings file",
			status:       StopHookUnknown,
			wantContains: []string{"UNKNOWN", "settings.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderStopHook(tt.status)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderStopHook(%v) = %q, want substring %q", tt.status, got, want)
				}
			}
		})
	}
}
