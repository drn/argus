package agent

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/routing"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// ensureCodexSkillsFn materializes argus's builtin skills into Codex's own
// installed-skills directory ($CODEX_HOME/skills). A package var (rather
// than a direct skills.EnsureCodexSkills call) so tests can stub it —
// mirrors ensureBuiltinRoutingFn (routing_prompt.go) and
// readGlobalClaudeMDFn/nonClaudeRoutingContentFn above.
//
// The real implementation is isTestBinary()-gated (see skills/builtin.go),
// so it always returns ("", nil) under `go test`, meaning BuildCmd's
// isCodex-gated materialization call can't be observed by calling the real
// function from a test. SetEnsureCodexSkillsForTest is the seam.
var ensureCodexSkillsFn = skills.EnsureCodexSkills

// SetEnsureCodexSkillsForTest overrides the codex-skills materialization
// function BuildCmd calls. Returns a restore func.
func SetEnsureCodexSkillsForTest(fn func() (string, error)) func() {
	old := ensureCodexSkillsFn
	ensureCodexSkillsFn = fn
	return func() { ensureCodexSkillsFn = old }
}

// maxClaudeMDBytes bounds how much of a CLAUDE.md file (global or repo) is
// read into a non-Claude backend's prompt prefix. Without a cap, an
// arbitrarily large repo CLAUDE.md — plausible for an untrusted or
// third-party repo a task's worktree happens to hold — would be read in full
// and prepended to every Codex-backend prompt on every spawn, amplifying
// both memory use and per-call token cost with no bound. 256 KiB comfortably
// exceeds any reasonable hand-written CLAUDE.md.
const maxClaudeMDBytes = 256 * 1024

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

// readClaudeMDFile reads path, returning ("", nil) if it does not exist. A
// file larger than maxClaudeMDBytes is skipped entirely — returned as ("",
// nil), same as "absent" — rather than truncated: a partial CLAUDE.md could
// silently change its meaning (e.g. cut off mid-instruction) in a way that's
// worse than omitting it outright. A skip is logged so it's not silently
// invisible.
func readClaudeMDFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxClaudeMDBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxClaudeMDBytes {
		uxlog.Log("[context-prefix] %s exceeds %d byte cap, skipping", path, maxClaudeMDBytes)
		return "", nil
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
		} else {
			uxlog.Log("[context-prefix] no global CLAUDE.md found, section omitted")
		}
		if content, err := readRepoClaudeMD(worktree); err != nil {
			uxlog.Log("[context-prefix] read repo CLAUDE.md failed (continuing without it): %v", err)
		} else if content != "" {
			sections = append(sections, "# Repository CLAUDE.md\n\n"+content)
		} else {
			uxlog.Log("[context-prefix] no repo CLAUDE.md found at %s, section omitted", filepath.Join(worktree, "CLAUDE.md"))
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
	prefix := strings.Join(sections, "\n\n---\n\n") + "\n\n---\n\n"
	uxlog.Log("[context-prefix] assembled %d section(s), %d bytes", len(sections), len(prefix))
	return prefix
}
