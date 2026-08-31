package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/routing"
	"github.com/drn/argus/internal/uxlog"
)

// readGlobalClaudeMDFn reads the user's global ~/.claude/CLAUDE.md content,
// for prepending into a Codex backend's prompt (see
// openspec/changes/add-nonclaude-context-parity). A package var (rather than
// a direct call) so tests can stub it — mirrors ensureBuiltinRoutingFn.
var readGlobalClaudeMDFn = readGlobalClaudeMD

// SetReadGlobalClaudeMDForTest overrides the global CLAUDE.md reader
// nonClaudeContextPrefix calls. Returns a restore func.
func SetReadGlobalClaudeMDForTest(fn func() (string, error)) func() {
	old := readGlobalClaudeMDFn
	readGlobalClaudeMDFn = fn
	return func() { readGlobalClaudeMDFn = old }
}

// readGlobalClaudeMD returns the content of ~/.claude/CLAUDE.md, or ("", nil)
// if it does not exist. isTestBinary()-gated to return ("", nil) under
// `go test`: without this, the dozens of TestBuildCmd_* cases asserting exact
// command strings for Codex would pick up whatever real ~/.claude/CLAUDE.md
// happens to exist on the machine running the tests. Mirrors the rationale
// documented on ensureBuiltinRoutingFn.
func readGlobalClaudeMD() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	return readGlobalClaudeMDReal()
}

// readGlobalClaudeMDReal is the untested-for-isTestBinary core of
// readGlobalClaudeMD, split out so tests can exercise the real HOME-resolving
// logic directly (readGlobalClaudeMD always short-circuits under `go test`).
func readGlobalClaudeMDReal() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return readClaudeMDFile(filepath.Join(home, ".claude", "CLAUDE.md"))
}

// readRepoClaudeMD returns the content of CLAUDE.md at the given worktree
// root, or ("", nil) if it does not exist. Not isTestBinary-gated: callers
// pass an isolated per-test worktree (t.TempDir()), so this is naturally
// hermetic without a seam — unlike the global reader above and routing
// content below, which read from outside any test's isolated directory.
func readRepoClaudeMD(worktree string) (string, error) {
	return readClaudeMDFile(filepath.Join(worktree, "CLAUDE.md"))
}

// readClaudeMDFile reads path, returning ("", nil) if it does not exist.
func readClaudeMDFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// nonClaudeRoutingContentFn returns argus's builtin hera/routing orientation
// content for prepending into a non-Claude backend's prompt — the same
// content Claude receives via --append-system-prompt-file. A package var so
// tests can stub it — mirrors ensureBuiltinRoutingFn.
var nonClaudeRoutingContentFn = nonClaudeRoutingContent

// SetNonClaudeRoutingContentForTest overrides the routing-content reader
// nonClaudeContextPrefix calls. Returns a restore func.
func SetNonClaudeRoutingContentForTest(fn func() (string, error)) func() {
	old := nonClaudeRoutingContentFn
	nonClaudeRoutingContentFn = fn
	return func() { nonClaudeRoutingContentFn = old }
}

// nonClaudeRoutingContent returns the raw builtin routing prose.
// isTestBinary()-gated to return ("", nil) under `go test`: routing.BuiltinContent
// itself carries no such gate (it's the raw content reader
// routing.EnsureBuiltinRouting's materialize step calls, safe for that
// caller since EnsureBuiltinRouting gates the whole operation), so without
// gating here the dozens of exact-command-string BuildCmd tests for
// Codex/opencode would break on real embedded routing prose.
func nonClaudeRoutingContent() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	return nonClaudeRoutingContentReal()
}

// nonClaudeRoutingContentReal is the untested-for-isTestBinary core of
// nonClaudeRoutingContent, split out so tests can exercise the real embedded-
// content read directly (nonClaudeRoutingContent always short-circuits under
// `go test`).
func nonClaudeRoutingContentReal() (string, error) {
	content, err := routing.BuiltinContent()
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// nonClaudeContextPrefix builds the prompt-prefix block for a non-Claude
// backend, per openspec/changes/add-nonclaude-context-parity: Codex gets
// global + repo CLAUDE.md content plus routing orientation; opencode gets
// routing orientation only — opencode's own native instruction-file discovery
// already reads repo/global CLAUDE.md itself (as an AGENTS.md fallback), so
// duplicating that content here would double the token cost for no benefit.
// Returns "" when there is nothing to prepend (neither backend applies, or no
// source produced content). A source read failure is logged and that source
// is skipped — never blocks command construction, mirroring the existing
// Claude-side --add-dir/--append-system-prompt-file failure handling.
func nonClaudeContextPrefix(isCodex, isOpencode bool, worktree string) string {
	if !isCodex && !isOpencode {
		return ""
	}

	var sections []string

	if isCodex {
		if content, err := readGlobalClaudeMDFn(); err != nil {
			uxlog.Log("[context-prefix] read global CLAUDE.md failed (continuing without it): %v", err)
		} else if content != "" {
			sections = append(sections, "# Global CLAUDE.md (~/.claude/CLAUDE.md)\n\n"+content)
		}
		if content, err := readRepoClaudeMD(worktree); err != nil {
			uxlog.Log("[context-prefix] read repo CLAUDE.md failed (continuing without it): %v", err)
		} else if content != "" {
			sections = append(sections, "# Repository CLAUDE.md\n\n"+content)
		}
	}

	if content, err := nonClaudeRoutingContentFn(); err != nil {
		uxlog.Log("[context-prefix] read routing content failed (continuing without it): %v", err)
	} else if content != "" {
		sections = append(sections, content)
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n---\n\n") + "\n\n---\n\n"
}
