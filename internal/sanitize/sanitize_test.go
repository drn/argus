package sanitize

import (
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"bold", "\x1b[1mhello\x1b[0m", "hello"},
		{"color", "\x1b[31mred\x1b[0m text", "red text"},
		{"256 color", "\x1b[38;5;42mgreen\x1b[0m", "green"},
		{"cursor hide", "\x1b[?25l", ""},
		{"cursor show", "\x1b[?25h", ""},
		{"sync update", "\x1b[?2026h\x1b[?2026l", ""},
		{"osc title", "\x1b]0;title\x07rest", "rest"},
		{"osc with ST", "\x1b]0;title\x1b\\rest", "rest"},
		{"charset", "\x1b(B\x1b)0", ""},
		{"keypad mode", "\x1b=\x1b>", ""},
		{"mixed", "\x1b[1;32m=> \x1b[0mDone", "=> Done"},
		{"DEC private mixed", "\x1b[?25l\x1b[?2026htext\x1b[?2026l\x1b[?25h", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripANSI(tt.in)
			if got != tt.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanPTYOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{
			"spinner lines removed",
			"✳\n✶\n✻\n✽\n✢\n·\n",
			"",
		},
		{
			"thinking lines removed",
			"(thinking)\n✶ping…(thinking)\n✳(thinking)\n",
			"",
		},
		{
			"warping and clauding removed",
			"✻Warping…\nClauding…\n",
			"",
		},
		{
			"status bar chrome removed",
			"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt\n",
			"",
		},
		{
			"separator lines removed",
			"──────────────────────────────────────────────────────────────────────\n",
			"",
		},
		{
			"consecutive blank lines collapsed",
			"hello\n\n\n\n\nworld\n",
			"hello\n\nworld\n",
		},
		{
			"preserves assistant messages",
			"⏺Scan pipeline is running\n",
			"⏺Scan pipeline is running\n",
		},
		{
			"preserves tool calls",
			"Bash(cd /tmp && ls)\n⏺Bash(echo hello)\n",
			"Bash(cd /tmp && ls)\n⏺Bash(echo hello)\n",
		},
		{
			"running marker removed",
			"⎿  Running…\n",
			"",
		},
		{
			"carriage returns normalized",
			"⏺Bash(echo hello)\r  ⎿  Running…\r✳ Warping…\n",
			"⏺Bash(echo hello)\n",
		},
		{
			"ANSI sequences stripped",
			"\x1b[?25l\x1b[1;32mhello\x1b[0m\x1b[?25h\n",
			"hello\n",
		},
		{
			"DEC private mode stripped from content",
			"\x1b[?2026hthinking output\x1b[?2026l\n",
			"thinking output\n",
		},
		{
			"lone digits removed",
			"4\n5\n1\n",
			"",
		},
		{
			"empty assistant markers removed",
			"⏺\n⏺  \n",
			"",
		},
		{
			"keybind hints removed",
			"(ctrl+b ctrl+b (twice) to run in background)\n",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanPTYOutput(tt.in)
			if got != tt.want {
				t.Errorf("CleanPTYOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_cleanLongLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"strips inline Running marker",
			"⏺Bash(cd /tmp && ls 2>&1)  ⎿  Running…                              ✳ Warping… (thinking)                              ──────────────────────────────────",
			"⏺Bash(cd /tmp && ls 2>&1)",
		},
		{
			"strips status bar and prompt",
			"⏺All done.  ❯                              ⏵⏵ bypass permissions on",
			"⏺All done.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanLongLine(tt.in)
			if got != tt.want {
				t.Errorf("cleanLongLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanPTYOutput_RealWorld(t *testing.T) {
	noisy := strings.Join([]string{
		"\x1b[?25l\x1b[?2026h⏺Bash(cd /tmp&&ls 2>&1)  ⎿  Running…                              ✳ Warping… (thinking)                              ──────────────────────────────────────────────────",
		"❯  ",
		"⏵⏵ bypass permissions on (shift+tab to cycle)",
		"✶ping…(thinking)",
		"",
		"",
		"⏺The scan completed successfully.\x1b[0m",
		"",
		"✳",
		"Clauding…",
		"",
	}, "\n")

	got := CleanPTYOutput(noisy)

	if !strings.Contains(got, "⏺Bash(cd /tmp&&ls 2>&1)") {
		t.Error("missing expected Bash tool call")
	}
	if !strings.Contains(got, "⏺The scan completed successfully.") {
		t.Error("missing expected assistant message")
	}
	if strings.Contains(got, "Warping") {
		t.Errorf("noise not stripped: %s", got)
	}
	if strings.Contains(got, "Clauding") {
		t.Errorf("noise not stripped: %s", got)
	}
	if strings.Contains(got, "bypass") {
		t.Errorf("status bar not stripped: %s", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("ANSI not stripped: %s", got)
	}
}
