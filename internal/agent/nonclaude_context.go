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

// stripJSONCTrailingCommas removes commas that directly precede a closing
// brace or bracket, which OpenCode's own config parser accepts (it parses the
// inline config as JSONC with trailing commas allowed) but encoding/json
// rejects. Without this, an operator's valid OpenCode config such as
// `{"model":"p/m",}` would look malformed to Argus and be left unmerged.
//
// Only trailing commas are handled, and only outside string literals, so a
// comma inside a quoted value is never touched. Anything Argus still cannot
// parse (comments, a broken document) keeps the existing fail-open behavior.
func stripJSONCTrailingCommas(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			// Look ahead past whitespace for a closing brace/bracket.
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\n' || raw[j] == '\r') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue // drop the comma; the value it trailed is intact
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// opencodeSessionConfigContent merges Argus's per-child OpenCode overrides into
// the inherited inline configuration. OpenCode v2's full TUI rejects the
// top-level --model flag, so a resolved model is delivered through the same
// child-only configuration channel as the skills path instead.
//
// A valid inherited object is preserved key-for-key, with only the requested
// model and skills entries changed. An inherited --model flag in the backend
// command is handled by BuildCmd before this helper is called. A malformed
// inherited value is never replaced: the caller logs the error and continues
// without Argus's override, matching the fail-open skills behavior.
func opencodeSessionConfigContent(model, skillsDir string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_CONTENT"))
	config := make(map[string]any)
	if raw != "" {
		// OpenCode parses this document as JSONC (trailing commas allowed), so
		// accept the same superset before handing it to encoding/json.
		if err := json.Unmarshal([]byte(stripJSONCTrailingCommas(raw)), &config); err != nil {
			return "", fmt.Errorf("parse OPENCODE_CONFIG_CONTENT: %w", err)
		}
		if config == nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT must be an object")
		}
	}

	changed := false
	if model != "" {
		existingModel, exists := config["model"]
		modelString, isString := existingModel.(string)
		if !exists || !isString || modelString != model {
			config["model"] = model
			changed = true
		}
	}

	if skillsDir != "" {
		existing, ok := config["skills"]
		if !ok || existing == nil {
			config["skills"] = []string{skillsDir}
			changed = true
		} else {
			arr, ok := existing.([]any)
			if !ok {
				// A user-owned config with an incompatible skills shape is not
				// Argus's to replace. Preserve it and still deliver the model;
				// the warning is the only signal that skills were skipped.
				uxlog.Log("[opencode] OPENCODE_CONFIG_CONTENT skills is %T, not an array; preserving it and skipping Argus skills", existing)
			} else {
				found := false
				for _, item := range arr {
					if item == skillsDir {
						found = true
						break
					}
				}
				if !found {
					config["skills"] = append(arr, skillsDir)
					changed = true
				}
			}
		}
	}

	if !changed {
		return raw, nil
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
