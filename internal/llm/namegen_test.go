package llm

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

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

// setupFailingClaude wires a fake `claude` that writes stderr and exits
// non-zero, so we can assert GenerateName folds the captured stderr into
// its error instead of dropping it as a bare "exit status 1".
func setupFailingClaude(t *testing.T, stderr string) {
	t.Helper()
	setupClaudeScript(t, "printf '"+stderr+"' >&2\nexit 1\n")
}

// setupFailingClaudeStdout wires a fake `claude` that writes its failure
// reason to STDOUT and exits non-zero — the real runtime-error shape of
// `claude -p` (budget exceeded / usage limit / overload), where stderr is
// empty. Asserts GenerateName folds the stdout reason in.
func setupFailingClaudeStdout(t *testing.T, stdout string) {
	t.Helper()
	setupClaudeScript(t, "printf '"+stdout+"'\nexit 1\n")
}

// setupClaudeScript wires a fake `claude` whose body is `body` (a /bin/sh
// fragment) onto PATH + nameGenCmd, and zeroes retryBackoff so the single
// retry doesn't slow the suite.
func setupClaudeScript(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script not portable on Windows")
	}
	tmp := t.TempDir()
	fake := tmp + "/claude"
	if err := writeExec(fake, "#!/bin/sh\n"+body); err != nil {
		t.Fatalf("writeExec: %v", err)
	}
	t.Setenv("PATH", tmp)

	prevBackoff := retryBackoff
	retryBackoff = 0
	prev := nameGenCmd
	t.Cleanup(func() { nameGenCmd = prev; retryBackoff = prevBackoff })
	nameGenCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, fake)
	}
}

func TestGenerateName_ExitErrorIncludesStderr(t *testing.T) {
	setupFailingClaude(t, "Credit balance is too low to run this request.")

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "claude -p failed")
	testutil.Contains(t, err.Error(), "Credit balance is too low")
}

func TestGenerateName_ExitErrorEmptyStderr(t *testing.T) {
	setupFailingClaude(t, "")

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	// No stdout/stderr to fold in — error stays the bare exit-status form.
	testutil.Contains(t, err.Error(), "claude -p failed: exit status 1")
}

// TestGenerateName_ExitErrorIncludesStdout is the regression guard for the
// real failure mode: `claude -p` writes runtime errors (budget exceeded,
// usage limit, overload) to STDOUT and exits non-zero with empty stderr. The
// reason must be folded into the error, not dropped as a bare "exit status 1".
func TestGenerateName_ExitErrorIncludesStdout(t *testing.T) {
	setupFailingClaudeStdout(t, "Error: Exceeded USD budget (0.05)")

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "claude -p failed")
	testutil.Contains(t, err.Error(), "Exceeded USD budget")
}

// TestGenerateName_RetriesOnceThenSucceeds asserts a transient non-zero exit
// is retried and a valid name on the second attempt is returned.
func TestGenerateName_RetriesOnceThenSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not portable on Windows")
	}
	marker := t.TempDir() + "/attempted"
	// First invocation: no marker yet → create it and exit 1. Second
	// invocation: marker present → emit a valid name.
	setupClaudeScript(t, "if [ -f '"+marker+"' ]; then printf 'retried-name\\n'; else : > '"+marker+"'; exit 1; fi\n")

	got, err := GenerateName(context.Background(), "build a feature")
	testutil.NoError(t, err)
	testutil.Equal(t, got, "retried-name")
}

// TestGenerateName_RetryExhausted asserts that when both attempts fail, the
// operation fails open with the diagnosable reason folded in.
func TestGenerateName_RetryExhausted(t *testing.T) {
	setupFailingClaudeStdout(t, "Error: Overloaded")

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "Overloaded")
}

// TestGenerateName_BudgetFlag pins the per-call budget cap value passed to the
// CLI so it can't silently drift back below the measured per-call cost.
func TestGenerateName_BudgetFlag(t *testing.T) {
	var capturedArgs []string
	setupFakeClaude(t, `ok-name\n`, &capturedArgs)

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.NoError(t, err)

	var budget string
	for i, a := range capturedArgs {
		if a == "--max-budget-usd" && i+1 < len(capturedArgs) {
			budget = capturedArgs[i+1]
		}
	}
	testutil.Equal(t, budget, "0.05")
}

// TestDefaultTimeout_FloorGuard pins a lower bound on the operation deadline.
// The cap must stay comfortably above the observed claude-CLI cold-start tail
// under load (>45s `signal: killed` failures were seen at 45s). It is a
// fire-and-forget background goroutine, so a generous cap has no UX cost —
// shrinking it back toward the observed tail reintroduces the kills, hence the
// floor. Asserts a floor (not an exact value) so future bumps don't break it.
func TestDefaultTimeout_FloorGuard(t *testing.T) {
	const floor = 120 * time.Second
	if DefaultTimeout < floor {
		t.Errorf("DefaultTimeout = %v, must be ≥ %v (cold-start tail guard)", DefaultTimeout, floor)
	}
}

// TestGenerateName_ScrubsControlCharsFromReason is the log-injection guard:
// `claude -p` stdout is untrusted (may echo prompt text) and is folded into an
// error that flows verbatim into uxlog/slog. A newline in the reason must not
// survive into the error string, else it forges a second physical log line.
func TestGenerateName_ScrubsControlCharsFromReason(t *testing.T) {
	// Embedded LF + a forged-looking log line + an ANSI escape.
	setupFailingClaudeStdout(t, `Error: Overloaded\n2026/06/24 [autoname] renamed task=victim\x1b[0m`)

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	msg := err.Error()
	testutil.Contains(t, msg, "Overloaded")
	if strings.ContainsAny(msg, "\n\r\x1b") {
		t.Errorf("error string still contains control chars (log-injection risk): %q", msg)
	}
}

// TestGenerateName_KeepsFirstReasonOnExhaustion asserts that when both attempts
// fail, the FIRST attempt's (richer) reason is surfaced, not the last — the
// retry can die bare as the deadline closes in, and the first reason is the
// diagnosable one.
func TestGenerateName_KeepsFirstReasonOnExhaustion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not portable on Windows")
	}
	marker := t.TempDir() + "/attempted"
	// Attempt 0: rich budget reason on stdout, exit 1. Attempt 1: bare exit 1
	// (no output) — mimics a deadline SIGKILL losing the reason.
	setupClaudeScript(t, "if [ -f '"+marker+"' ]; then exit 1; else : > '"+marker+"'; printf 'Error: Exceeded USD budget'; exit 1; fi\n")

	_, err := GenerateName(context.Background(), "build a feature")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "Exceeded USD budget")
}

func TestScrubReason(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Error: Overloaded", "Error: Overloaded"},
		{"trim", "  busy  ", "busy"},
		{"newline", "a\nb", "a b"},
		{"crlf", "a\r\nb", "a  b"}, // CR and LF each map to a space (no collapsing)
		{"tab", "a\tb", "a b"},
		{"ansi escape", "a\x1b[0mb", "a [0mb"}, // only ESC is a control rune; "[0m" is harmless literal text without it
		{"empty", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, scrubReason(tt.in), tt.want)
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under limit", "short", 10, "short"},
		{"at limit", "exactly10!", 10, "exactly10!"},
		{"over limit", "abcdefghij", 5, "abcde…"},
		{"multibyte boundary", "abcdé", 4, "abcd…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, truncate(tt.in, tt.max), tt.want)
		})
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
