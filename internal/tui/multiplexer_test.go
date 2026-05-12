package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDetectMultiplexer(t *testing.T) {
	tests := []struct {
		name         string
		envForceSync string
		envTMUX      string
		envSTY       string
		envTERM      string
		want         bool
	}{
		{name: "no signals", want: false},
		{name: "TMUX set", envTMUX: "/tmp/tmux-501/default,12345,0", want: true},
		{name: "STY set (GNU screen)", envSTY: "12345.pts-0.host", want: true},
		{name: "TERM=tmux-256color", envTERM: "tmux-256color", want: true},
		{name: "TERM=screen-256color", envTERM: "screen-256color", want: true},
		{name: "TERM=tmux", envTERM: "tmux", want: true},
		{name: "TERM=screen", envTERM: "screen", want: true},
		{name: "TERM=xterm-256color (bare iTerm2)", envTERM: "xterm-256color", want: false},
		{name: "TERM=alacritty", envTERM: "alacritty", want: false},
		{name: "TERM=ghostty", envTERM: "ghostty", want: false},
		{name: "override ON with no other signals", envForceSync: "1", want: true},
		{name: "override ON case-insensitive TRUE", envForceSync: "TRUE", want: true},
		{name: "override ON yes", envForceSync: "yes", want: true},
		{name: "override OFF defeats TMUX", envForceSync: "0", envTMUX: "/tmp/tmux", want: false},
		{name: "override OFF defeats TERM=tmux-256color", envForceSync: "false", envTERM: "tmux-256color", want: false},
		{name: "override OFF case-insensitive NO", envForceSync: "NO", envTMUX: "/tmp/tmux", want: false},
		{name: "override garbage falls through to detection (no signals)", envForceSync: "maybe", want: false},
		{name: "override garbage falls through to detection (TMUX wins)", envForceSync: "later", envTMUX: "/tmp/tmux", want: true},
		{name: "override empty string falls through to detection", envForceSync: "", envTMUX: "/tmp/tmux", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ARGUS_FORCE_SYNC", tt.envForceSync)
			t.Setenv("TMUX", tt.envTMUX)
			t.Setenv("STY", tt.envSTY)
			t.Setenv("TERM", tt.envTERM)
			testutil.Equal(t, detectMultiplexer(), tt.want)
		})
	}
}
