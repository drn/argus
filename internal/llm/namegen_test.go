package llm

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSanitizeAndValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain kebab", "fix-auth-token", "fix-auth-token", false},
		{"trailing newline", "fix-auth-token\n", "fix-auth-token", false},
		{"trailing period", "fix-auth-token.", "fix-auth-token", false},
		{"wrapped quotes", `"fix-auth-token"`, "fix-auth-token", false},
		{"single quotes", `'fix-auth-token'`, "fix-auth-token", false},
		{"backticks", "`fix-auth-token`", "fix-auth-token", false},
		{"code fence", "```fix-auth-token```", "fix-auth-token", false},
		{"uppercase", "Fix-Auth-Token", "fix-auth-token", false},
		{"alphanumeric", "v2-migration", "v2-migration", false},

		{"empty", "", "", true},
		{"whitespace only", "   \n", "", true},
		{"too long", strings.Repeat("a", 31), "", true},
		{"underscore", "fix_auth_token", "", true},
		{"space", "fix auth token", "", true},
		{"leading hyphen", "-fix-auth", "", true},
		{"trailing hyphen", "fix-auth-", "", true},
		{"double hyphen", "fix--auth", "", true},
		{"slash", "fix/auth", "", true},
		{"path traversal attempt", "../../etc/passwd", "", true},
		{"sentence reply", "Sure, here is the name: fix-auth", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAndValidate(tt.in)
			if tt.wantErr {
				testutil.Equal(t, got, "")
			} else {
				testutil.Equal(t, got, tt.want)
			}
		})
	}
}

func TestGenerateName_EmptyPrompt(t *testing.T) {
	_, err := GenerateName(context.Background(), "   \n  ")
	testutil.ErrorIs(t, err, ErrEmptyPrompt)
}

func TestGenerateName_NoClaude(t *testing.T) {
	// Force PATH to a directory with no `claude` binary.
	t.Setenv("PATH", t.TempDir())
	_, err := GenerateName(context.Background(), "build a feature")
	testutil.ErrorIs(t, err, ErrUnavailable)
}

// setupFakeClaude wires a fake `claude` binary onto PATH and swaps
// nameGenCmd to run it. captureArgs, if non-nil, is populated with the
// args nameGenCmd received on each call. Returns early via t.Skip on
// Windows (the shell stub isn't portable there).
func setupFakeClaude(t *testing.T, stdout string, captureArgs *[]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script not portable on Windows")
	}
	tmp := t.TempDir()
	fake := tmp + "/claude"
	if err := writeExec(fake, "#!/bin/sh\nprintf '"+stdout+"'\n"); err != nil {
		t.Fatalf("writeExec: %v", err)
	}
	t.Setenv("PATH", tmp)

	prev := nameGenCmd
	t.Cleanup(func() { nameGenCmd = prev })
	nameGenCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if captureArgs != nil {
			*captureArgs = args
		}
		return exec.CommandContext(ctx, fake)
	}
}

func TestGenerateName_ValidStubbedOutput(t *testing.T) {
	setupFakeClaude(t, `fix-auth-token\n`, nil)

	got, err := GenerateName(context.Background(), "Refactor the auth token refresh flow")
	testutil.NoError(t, err)
	testutil.Equal(t, got, "fix-auth-token")
}

// TestGenerateName_PromptFraming asserts the user prompt is passed as a
// "Task description:" framed argument and the system prompt instructs the
// model not to answer it. Without both pieces, Haiku reads question-shaped
// prompts as questions for itself and replies in prose.
func TestGenerateName_PromptFraming(t *testing.T) {
	var capturedArgs []string
	setupFakeClaude(t, `ok-name\n`, &capturedArgs)

	_, err := GenerateName(context.Background(), "looks like X isn't working?")
	testutil.NoError(t, err)

	var sysPrompt, promptArg string
	for i, a := range capturedArgs {
		if a == "--system-prompt" && i+1 < len(capturedArgs) {
			sysPrompt = capturedArgs[i+1]
		}
		if a == "--" && i+1 < len(capturedArgs) {
			promptArg = capturedArgs[i+1]
		}
	}
	if !strings.Contains(sysPrompt, "TASK DESCRIPTION") {
		t.Errorf("system prompt missing TASK DESCRIPTION framing: %q", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "do not answer") {
		t.Errorf("system prompt missing do-not-answer directive: %q", sysPrompt)
	}
	if !strings.HasPrefix(promptArg, "Task description: ") {
		t.Errorf("prompt arg missing framing prefix: %q", promptArg)
	}
}

func TestGenerateName_InvalidModelOutput(t *testing.T) {
	setupFakeClaude(t, "Sorry, I cannot help with that.", nil)

	_, err := GenerateName(context.Background(), "build a feature")
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("want validation error, got %v", err)
	}
}

func writeExec(path, body string) error {
	if err := writeFile(path, body); err != nil {
		return err
	}
	return chmod(path, 0o755)
}
