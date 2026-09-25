package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/drn/argus/internal/routing"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// ensureCodexSkillsFn prepares an Argus-only Codex home containing the
// embedded skills and links to the user's Codex state. A package var (rather
// than a direct skills.EnsureCodexSkills call) so tests can stub it —
// mirrors ensureBuiltinRoutingFn (routing_prompt.go) and
// nonClaudeRoutingContentFn below.
//
// The real implementation is isTestBinary()-gated (see skills/builtin.go),
// so it always returns ("", nil) under `go test`, meaning BuildCmd's
// isCodex-gated materialization call can't be observed by calling the real
// function from a test. SetEnsureCodexSkillsForTest is the seam.
var ensureCodexSkillsFn = skills.EnsureCodexSkills

// ensureSessionSkillsFn supplies the Argus-owned skill directory to backends
// with a per-process discovery hook (Claude --add-dir, Pi --skill, OpenCode
// OPENCODE_CONFIG_CONTENT). It is replaceable in command-construction tests.
var ensureSessionSkillsFn = skills.EnsureBuiltinSkills

func opencodeSkillsConfigContent(skillsDir string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_CONTENT"))
	config := make(map[string]any)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return "", fmt.Errorf("parse OPENCODE_CONFIG_CONTENT: %w", err)
		}
		if config == nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT must be an object")
		}
	}
	if existing, ok := config["skills"]; ok {
		arr, ok := existing.([]any)
		if !ok {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT skills must be an array")
		}
		for _, item := range arr {
			if item == skillsDir {
				return raw, nil
			}
		}
		config["skills"] = append(arr, skillsDir)
	} else {
		config["skills"] = []string{skillsDir}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode OPENCODE_CONFIG_CONTENT: %w", err)
	}
	return string(encoded), nil
}

// SetEnsureCodexSkillsForTest overrides the codex-skills materialization
// function BuildCmd calls. Returns a restore func.
func SetEnsureCodexSkillsForTest(fn func() (string, error)) func() {
	old := ensureCodexSkillsFn
	ensureCodexSkillsFn = fn
	return func() { ensureCodexSkillsFn = old }
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
// backend: routing orientation only, for isCodex or isOpencode. Neither
// backend receives CLAUDE.md content — both have their own native
// instruction-file discovery (Codex: global ~/.codex/AGENTS.md plus
// repo-local AGENTS.md walking cwd-to-worktree-root; opencode: repo-local
// CLAUDE.md as an AGENTS.md fallback, global ~/.claude/CLAUDE.md as a
// documented compatibility fallback) — so prepending CLAUDE.md content here
// would be pure duplication. See
// openspec/changes/remove-codex-claude-md-injection.
// Returns "" when there is nothing to prepend (neither backend applies, or no
// source produced content). A source read failure is logged and that source
// is skipped — never blocks command construction, mirroring the existing
// Claude-side --add-dir/--append-system-prompt-file failure handling.
func nonClaudeContextPrefix(isCodex, isOpencode bool) string {
	if !isCodex && !isOpencode {
		return ""
	}

	var sections []string

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
