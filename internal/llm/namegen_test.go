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

func TestGenerateName_ValidStubbedOutput(t *testing.T) {
	// Stub the exec factory to return a command that prints a kebab-case
	// name without actually running claude. `echo` is portable enough across
	// dev shells that this test runs without skipping on macOS/Linux.
	if runtime.GOOS == "windows" {
		t.Skip("echo path differs on Windows")
	}
	// Ensure exec.LookPath("claude") succeeds — point PATH at a temp dir
	// containing a fake `claude` script so we exercise the success path.
	tmp := t.TempDir()
	fake := tmp + "/claude"
	if err := writeExec(fake, "#!/bin/sh\nprintf 'fix-auth-token\\n'\n"); err != nil {
		t.Fatalf("writeExec: %v", err)
	}
	t.Setenv("PATH", tmp)

	prev := nameGenCmd
	t.Cleanup(func() { nameGenCmd = prev })
	nameGenCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, fake)
	}

	got, err := GenerateName(context.Background(), "Refactor the auth token refresh flow")
	testutil.NoError(t, err)
	testutil.Equal(t, got, "fix-auth-token")
}

func TestGenerateName_InvalidModelOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo path differs on Windows")
	}
	tmp := t.TempDir()
	fake := tmp + "/claude"
	if err := writeExec(fake, "#!/bin/sh\nprintf 'Sorry, I cannot help with that.'\n"); err != nil {
		t.Fatalf("writeExec: %v", err)
	}
	t.Setenv("PATH", tmp)

	prev := nameGenCmd
	t.Cleanup(func() { nameGenCmd = prev })
	nameGenCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, fake)
	}

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
