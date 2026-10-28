package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// ensureCodexSkillsFn prepares an Argus-only Codex home containing the
// embedded skills and links to the user's Codex state. A package var (rather
// than a direct skills.EnsureCodexSkills call) so tests can stub it —
// mirrors ensureBuiltinRoutingFn (routing_prompt.go).
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
func opencodeSessionConfigContent(model, skillsDir, instructionsPath string) (string, error) {
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

	mergeStringArray(config, "skills", skillsDir, &changed)
	mergeStringArray(config, "instructions", instructionsPath, &changed)

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
func SetEnsureCodexSkillsForTest(fn func(source string) (string, error)) func() {
	old := ensureCodexSkillsFn
	ensureCodexSkillsFn = fn
	return func() { ensureCodexSkillsFn = old }
}

// mergeStringArray adds value to config[key] (an array of strings) without
// dropping user entries. A user-owned value with an incompatible shape is not
// Argus's to replace: it is preserved and the warning is the only signal that
// the Argus entry was skipped.
func mergeStringArray(config map[string]any, key, value string, changed *bool) {
	if value == "" {
		return
	}
	existing, ok := config[key]
	if !ok || existing == nil {
		config[key] = []string{value}
		*changed = true
		return
	}
	arr, ok := existing.([]any)
	if !ok {
		uxlog.Log("[opencode] OPENCODE_CONFIG_CONTENT %s is %T, not an array; preserving it and skipping the Argus entry", key, existing)
		return
	}
	for _, item := range arr {
		if item == value {
			return
		}
	}
	config[key] = append(arr, value)
	*changed = true
}
