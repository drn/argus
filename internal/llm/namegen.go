// Package llm provides LLM-backed helpers for non-interactive utilities.
//
// GenerateName shells out to the user's local `claude` CLI with Haiku
// pinned and every context source disabled (no tools, no MCPs, no
// settings, no slash commands, no session persistence). Per-call cost
// is ~150 input + ~10 output tokens (≈ $0.0002 at Haiku pricing).
// All failures fail-open: callers keep their fallback name.
package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DefaultTimeout caps each name-gen call. Haiku typically responds in
// 1–2s; the budget is generous to absorb CLI startup overhead.
const DefaultTimeout = 8 * time.Second

// nameSystemPrompt fully overrides the default Claude Code system prompt
// (passed via --system-prompt, not --append-system-prompt) so we don't
// pay for the default preamble or for CLAUDE.md auto-discovery.
const nameSystemPrompt = "You generate concise kebab-case task names. " +
	"Reply with ONLY the name (2-4 words, lowercase letters/digits, " +
	"hyphen-separated, no punctuation, no quotes, max 30 chars). " +
	"Capture the core action/intent — avoid filler words like 'task', " +
	"'help', 'please', or 'fix the'."

// validNamePattern matches kebab-case names: 1+ alphanumeric segments
// joined by single hyphens, no leading/trailing hyphen.
var validNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ErrUnavailable indicates the `claude` CLI is missing from PATH. Callers
// should treat this as a clean skip, not a failure worth surfacing.
var ErrUnavailable = errors.New("claude CLI unavailable")

// nameGenCmd is the exec factory used by GenerateName. Tests swap this to
// inject a fake binary or capture invocations without shelling out.
var nameGenCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...) //nolint:gosec // name is a fixed literal ("claude"); args are flag-controlled flags + the user prompt as a single arg, not passed through a shell.
}

// GenerateName asks Haiku to summarize prompt as a kebab-case task name.
// Returns ErrUnavailable if `claude` is not installed; other errors mean
// the call ran but produced unusable output. Empty/whitespace prompts
// return ErrUnavailable to spare a wasted API call.
func GenerateName(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", ErrUnavailable
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return "", ErrUnavailable
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	args := []string{
		"-p",
		"--model", "haiku",
		"--tools", "",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--setting-sources", "",
		"--no-session-persistence",
		"--system-prompt", nameSystemPrompt,
		"--output-format", "text",
		"--max-budget-usd", "0.01",
		prompt,
	}

	cmd := nameGenCmd(ctx, "claude", args...)
	// Run from a neutral cwd so claude can't auto-discover CLAUDE.md or
	// project-local config in the worktree even though --setting-sources ""
	// already disables settings loading. Belt-and-suspenders.
	cmd.Dir = os.TempDir()

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p failed: %w", err)
	}

	name := sanitizeAndValidate(string(out))
	if name == "" {
		return "", fmt.Errorf("invalid name from model: %q", strings.TrimSpace(string(out)))
	}
	return name, nil
}

// sanitizeAndValidate trims whitespace, strips chatty wrappers (quotes,
// code fences), lowercases, and verifies kebab-case + length. Returns
// the empty string when the candidate is unusable.
func sanitizeAndValidate(raw string) string {
	s := strings.TrimSpace(raw)
	// Strip code fences / backticks / quotes a chatty model might add.
	for _, c := range []string{"```", "`", `"`, "'"} {
		s = strings.Trim(s, c)
	}
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.TrimRight(s, ".!?,;:")
	s = strings.TrimSpace(s)

	if len(s) == 0 || len(s) > 30 {
		return ""
	}
	if !validNamePattern.MatchString(s) {
		return ""
	}
	return s
}
