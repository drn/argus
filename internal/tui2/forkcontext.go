package tui2

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
)

// forkAnsiRe matches ANSI escape sequences (CSI, OSC, DEC private mode).
var forkAnsiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b\[\?[0-9;]*[a-zA-Z]|\x1b[()][A-Z0-9]`)

// Patterns for sanitizeForkOutput noise filtering.
var (
	// spinnerRe matches lines that are only spinner characters (with optional whitespace).
	spinnerRe = regexp.MustCompile(`^[✳✶✻✽✢·\s]+$`)
	// thinkingRe matches lines containing only "(thinking)" with optional spinner prefix.
	thinkingRe = regexp.MustCompile(`^[✳✶✻✽✢·\s]*(ping…)?\(thinking\)\s*$`)
	// warpClaudRe matches Warping.../Clauding... status lines with optional spinner prefix.
	warpClaudRe = regexp.MustCompile(`^[✳✶✻✽✢·\s]*(Warping|Clauding)….*$`)
	// statusBarRe matches the permission/status bar chrome.
	statusBarRe = regexp.MustCompile(`^⏵`)
	// separatorRe matches lines of only box-drawing chars.
	separatorRe = regexp.MustCompile(`^─+\s*$`)
	// promptRe matches bare shell prompts.
	promptRe = regexp.MustCompile(`^❯\s*$`)
	// partialRenderRe matches short lines of partial spinner text renders (up to ~4 chars).
	partialRenderRe = regexp.MustCompile(`^[✳✶✻✽✢·]?[A-Za-z…]{0,4}(\(thinking\))?\s*$`)
	// timingRe matches timing/token hints like "(3s)(...)" or "(30s · ↑342 tokens)".
	timingRe = regexp.MustCompile(`^[✳✶✻✽✢·]?…?\s*\(\d+s.*\)\s*$`)
	// cwdResetRe matches "Shell cwd was reset" tool results.
	cwdResetRe = regexp.MustCompile(`^⎿\s+Shell cwd was reset`)
	// runningRe matches "Running…" tool status (with optional leading whitespace from \r normalization).
	runningRe = regexp.MustCompile(`^\s*⎿\s+Running…\s*$`)
	// noOutputRe matches "(No output)" markers.
	noOutputRe = regexp.MustCompile(`\(No output\)`)
	// bakedRe matches "Baked for Ns" status lines.
	bakedRe = regexp.MustCompile(`Baked for \d+s`)
	// expandHintRe matches "… +N lines (ctrl+o to expand)" hints.
	expandHintRe = regexp.MustCompile(`…\s*\+\d+ lines \(ctrl\+o to expand\)`)
	// loneDigitRe matches lines that are just a single digit (partial render artifacts).
	loneDigitRe = regexp.MustCompile(`^\d\s*$`)
	// emptyAssistantRe matches lone ⏺ markers with no content.
	emptyAssistantRe = regexp.MustCompile(`^⏺\s*$`)
	// keybindHintRe matches keyboard shortcut hints like "(ctrl+b ctrl+b...)".
	keybindHintRe = regexp.MustCompile(`^\(ctrl\+[a-z].*\)\s*$`)

	// Inline noise patterns for long concatenated terminal lines.
	// These appear mid-line when VT cells from different screen areas get concatenated.
	inlineRunningRe   = regexp.MustCompile(`⎿\s+Running…\s*`)
	inlineCwdResetRe  = regexp.MustCompile(`⎿\s+Shell cwd was reset[^⏺⎿]*`)
	inlineWarpClaudRe = regexp.MustCompile(`[✳✶✻✽✢·]\s*(Warping|Clauding)…[^⏺⎿]*`)
	inlineSeparatorRe = regexp.MustCompile(`─{5,}[^⏺⎿]*`)
	inlinePromptRe    = regexp.MustCompile(`❯[^⏺⎿]*`)
	inlineStatusBarRe = regexp.MustCompile(`⏵[^⏺⎿]*`)
	inlineNoOutputRe  = regexp.MustCompile(`⏺\(No output\)[^⏺⎿]*`)
	inlineBakedRe     = regexp.MustCompile(`[✳✶✻✽✢·]?Baked for \d+s[^⏺⎿]*`)
	inlineExpandRe    = regexp.MustCompile(`…\s*\+\d+ lines \(ctrl\+o to expand\)[^⏺⎿]*`)
)

// forkContext holds extracted context from a source task.
type forkContext struct {
	SourceName   string
	SourcePrompt string
	SourceStatus string
	SourceBranch string
	RecentOutput string // ANSI-stripped
	GitDiff      string
}

// maxOutputBytes is the maximum bytes to read from the session log tail.
const maxOutputBytes = 32 * 1024

// maxDiffBytes caps the git diff size to avoid bloating the context.
const maxDiffBytes = 64 * 1024

// extractForkContext reads context from the source task's session log and worktree.
func extractForkContext(task *model.Task) *forkContext {
	ctx := &forkContext{
		SourceName:   task.Name,
		SourcePrompt: task.Prompt,
		SourceStatus: task.Status.String(),
		SourceBranch: task.Branch,
	}

	// Read recent agent output from session log.
	ctx.RecentOutput = readSessionLogTail(task.ID)

	// Read git diff from the source worktree.
	if task.Worktree != "" {
		ctx.GitDiff = readGitDiff(task.Worktree)
	}

	return ctx
}

// readSessionLogTail reads the last maxOutputBytes from a task's session log,
// strips ANSI escape sequences, and returns clean text.
func readSessionLogTail(taskID string) string {
	logPath := agent.SessionLogPath(taskID)
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ""
	}

	size := info.Size()
	offset := int64(0)
	if size > maxOutputBytes {
		offset = size - maxOutputBytes
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	return sanitizeForkOutput(stripANSI(string(data)))
}

// readGitDiff runs git diff HEAD in the worktree and returns the output.
// Validates the worktree path is under a known worktree root before executing.
func readGitDiff(worktree string) string {
	if !isWorktreeSubdir(worktree) {
		return ""
	}
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if len(out) > maxDiffBytes {
		return string(out[:maxDiffBytes]) + "\n\n... (diff truncated)"
	}
	return string(out)
}

// sanitizeForkOutput removes terminal rendering noise from ANSI-stripped PTY output.
// Preserves assistant messages (⏺), tool results (⎿), tool calls (Bash(...)), and
// actual content lines. Removes spinners, thinking indicators, status bars, separators,
// partial character renders, and collapses consecutive blank lines.
func sanitizeForkOutput(s string) string {
	if s == "" {
		return ""
	}

	// Normalize line endings: PTY output uses \r for cursor-return-to-column-0.
	// Treat \r\n as \n, then remaining \r as \n to split overlapping content.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// Replace non-breaking spaces (U+00A0) with regular spaces.
	s = strings.ReplaceAll(s, "\u00a0", " ")

	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := false

	for _, line := range lines {
		// For long lines, strip inline noise first (VT cell concatenation artifacts).
		if len(line) > 120 {
			line = cleanLongLine(line)
		}
		trimmed := strings.TrimRight(line, " \t")

		// Always remove these noise patterns.
		if isNoiseLine(trimmed) {
			continue
		}

		// Collapse consecutive blank lines to at most one.
		if trimmed == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}

		prevBlank = false
		out = append(out, line)
	}

	// Trim trailing blank lines.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// isNoiseLine returns true if the line is terminal rendering noise that should be removed.
func isNoiseLine(line string) bool {
	// Empty lines are handled by the caller (blank line collapsing).
	if line == "" {
		return false
	}

	// Spinner-only lines.
	if spinnerRe.MatchString(line) {
		return true
	}
	// Thinking indicators.
	if thinkingRe.MatchString(line) {
		return true
	}
	// Warping.../Clauding... status.
	if warpClaudRe.MatchString(line) {
		return true
	}
	// Status bar chrome (⏵⏵ bypass permissions...).
	if statusBarRe.MatchString(line) {
		return true
	}
	// Separator lines (────...).
	if separatorRe.MatchString(line) {
		return true
	}
	// Bare shell prompts (❯).
	if promptRe.MatchString(line) {
		return true
	}
	// Partial character renders (1-3 chars, often from frame-by-frame Warping/Clauding).
	if partialRenderRe.MatchString(line) {
		return true
	}
	// Timing/token hints.
	if timingRe.MatchString(line) {
		return true
	}
	// Shell cwd reset messages.
	if cwdResetRe.MatchString(line) {
		return true
	}
	// "Running…" tool status.
	if runningRe.MatchString(line) {
		return true
	}
	// "(No output)" markers.
	if noOutputRe.MatchString(line) {
		return true
	}
	// "Baked for Ns" status.
	if bakedRe.MatchString(line) {
		return true
	}
	// "… +N lines (ctrl+o to expand)" hints.
	if expandHintRe.MatchString(line) {
		return true
	}
	// Lone digits (partial render artifacts).
	if loneDigitRe.MatchString(line) {
		return true
	}
	// Empty assistant markers (⏺ with no content).
	if emptyAssistantRe.MatchString(line) {
		return true
	}
	// Keyboard shortcut hints.
	if keybindHintRe.MatchString(line) {
		return true
	}

	return false
}

// cleanLongLine strips inline noise from long concatenated terminal lines.
// VT cell rendering concatenates the main content area, status indicators,
// separators, and prompt area onto a single line. This extracts the useful content.
func cleanLongLine(line string) string {
	s := line
	s = inlineRunningRe.ReplaceAllString(s, "")
	s = inlineCwdResetRe.ReplaceAllString(s, "")
	s = inlineWarpClaudRe.ReplaceAllString(s, "")
	s = inlineExpandRe.ReplaceAllString(s, "")
	s = inlineNoOutputRe.ReplaceAllString(s, "")
	s = inlineBakedRe.ReplaceAllString(s, "")
	s = inlineSeparatorRe.ReplaceAllString(s, "")
	s = inlinePromptRe.ReplaceAllString(s, "")
	s = inlineStatusBarRe.ReplaceAllString(s, "")
	return strings.TrimRight(s, " \t")
}

// stripANSI removes ANSI escape sequences from text.
func stripANSI(s string) string {
	return forkAnsiRe.ReplaceAllString(s, "")
}

// writeForkContextFiles writes .context/ files into the destination worktree.
func writeForkContextFiles(destWorktree string, ctx *forkContext) error {
	dir := destWorktree + "/.context"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .context dir: %w", err)
	}

	// fork-source.md — metadata and original prompt.
	var sb strings.Builder
	sb.WriteString("# Fork Source\n\n")
	fmt.Fprintf(&sb, "- **Source task:** %s\n", ctx.SourceName)
	fmt.Fprintf(&sb, "- **Status:** %s\n", ctx.SourceStatus)
	if ctx.SourceBranch != "" {
		fmt.Fprintf(&sb, "- **Branch:** %s\n", ctx.SourceBranch)
	}
	sb.WriteString("\n## Original Prompt\n\n")
	sb.WriteString(ctx.SourcePrompt)
	sb.WriteString("\n")
	if err := os.WriteFile(dir+"/fork-source.md", []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write fork-source.md: %w", err)
	}

	// fork-output.md — recent agent output (skip if empty).
	if ctx.RecentOutput != "" {
		content := "# Agent Output (last ~32KB)\n\n```\n" + ctx.RecentOutput + "\n```\n"
		if err := os.WriteFile(dir+"/fork-output.md", []byte(content), 0o644); err != nil {
			return fmt.Errorf("write fork-output.md: %w", err)
		}
	}

	// fork-diff.patch — git diff (skip if empty).
	if ctx.GitDiff != "" {
		if err := os.WriteFile(dir+"/fork-diff.patch", []byte(ctx.GitDiff), 0o644); err != nil {
			return fmt.Errorf("write fork-diff.patch: %w", err)
		}
	}

	return nil
}

// buildForkPrompt creates the prompt for the forked task, referencing .context/ files.
// Only references files that were actually written based on the extracted context.
func buildForkPrompt(source *model.Task, ctx *forkContext) string {
	var sb strings.Builder
	sb.WriteString("Continue the work from a previous attempt. Context from the previous agent session is available in `.context/` files in the working directory:\n\n")
	sb.WriteString("- `.context/fork-source.md` — source task metadata and original prompt\n")
	if ctx.RecentOutput != "" {
		sb.WriteString("- `.context/fork-output.md` — recent agent output\n")
	}
	if ctx.GitDiff != "" {
		sb.WriteString("- `.context/fork-diff.patch` — code changes made so far\n")
	}
	sb.WriteString("\nRead these files first to understand what was done and what remains.\n\n")
	sb.WriteString("Original prompt:\n")
	sb.WriteString(source.Prompt)
	return sb.String()
}
