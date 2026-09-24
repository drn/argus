package skills

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drn/argus/internal/uxlog"
)

// builtinFS embeds argus's own skill sources — the single source of truth for
// skills that only make sense inside argus (they drive mcp__argus__* tools,
// read ARGUS_TASK_ID/~/.argus, or encode the hera coordination model). No
// runtime path, network fetch, or external repository is consulted.
//
//go:embed builtin
var builtinFS embed.FS

// builtinRoot is the embedded root directory name, stripped when walking so
// callers see skill directories at the top level (e.g. "hera", not
// "builtin/hera").
const builtinRoot = "builtin"

// managedSkillsWorkspace is the name of the directory materialized skills live
// under, inside ~/.argus. It is passed to Claude Code's --add-dir flag; Claude
// Code then loads <managedSkillsWorkspace>/.claude/skills/ automatically (a
// documented exception to --add-dir otherwise granting file access only).
const managedSkillsWorkspace = "skills"

// ownerMarkerFile is written inside every skill directory this package
// materializes, so a later run can positively identify "argus created this"
// rather than inferring ownership from mere absence-of-recognition. Required
// for any skillsDir that isn't exclusively owned by argus (e.g. Codex's own
// $CODEX_HOME/skills, shared with Codex's bundled skills and any
// user-installed ones) — stale-removal there must never delete a directory
// argus didn't itself create.
const ownerMarkerFile = ".argus-managed"

// ownerMarkerContent is the fixed content written to ownerMarkerFile. Its
// exact bytes don't matter — only presence is checked — but a human-readable
// note beats an empty file if someone stumbles on it.
const ownerMarkerContent = "Materialized by argus. Safe to delete; argus will recreate it on the next launch.\n"

// reservedCodexSystemDir is Codex's own bundled-skills subtree under
// $CODEX_HOME/skills/. Never removed, unconditionally, regardless of the
// ownership marker check below — belt-and-braces on top of the marker gate
// since accidentally deleting Codex's own content would be unusually costly
// for the user to recover from.
const reservedCodexSystemDir = ".system"

// BuiltinItems returns one SkillItem per embedded builtin skill, sorted by
// name. Reads only the embedded FS — no filesystem or network access.
func BuiltinItems() []SkillItem {
	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return nil
	}
	var items []SkillItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifest := filepath.Join(builtinRoot, e.Name(), skillManifestFile)
		desc := readEmbeddedFrontmatterField(manifest, "description")
		items = append(items, SkillItem{Name: e.Name(), Description: desc})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// EnsureBuiltinSkills materializes embedded builtin skills to the managed
// workspace (~/.argus/skills) idempotently. Returns the workspace root path
// on success (pointing to ~/.argus/skills where .claude/skills/ lives), or
// an error on failure. Errors are non-fatal for launch (callers log but continue).
// On success, returns the workspace root (e.g., ~/.argus/skills) suitable for
// --add-dir flag.
func EnsureBuiltinSkills() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	workspaceRoot := filepath.Join(home, ".argus", managedSkillsWorkspace)
	if _, err := materializeBuiltinSkillsInto(BuiltinSkillsDir(workspaceRoot), true); err != nil {
		return "", err
	}
	return workspaceRoot, nil
}

// BuiltinSkillsDir returns the directory containing materialized skill bodies
// (<name>/SKILL.md) given the workspace root EnsureBuiltinSkills returns.
// Distinct from the workspace root itself: Claude's --add-dir points at the
// root (so Claude Code's own .claude/skills/ auto-discovery kicks in), while
// a caller that scans a directory of <name>/SKILL.md subdirectories directly
// (opencode's `skills` config array) needs this deeper path.
func BuiltinSkillsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".claude", "skills")
}

// EnsureCodexSkills materializes the same embedded builtin skill bodies to
// Codex's own first-party installed-skills directory ($CODEX_HOME/skills,
// defaulting to ~/.codex/skills when CODEX_HOME is unset), outside the
// .system/ subtree Codex reserves for its own bundled skills. This is
// deliberately narrower than the generic cross-tool `.agents/skills`
// convention (visible to any tool implementing that convention, not just
// Codex) — see openspec/changes/add-nonclaude-context-parity/design.md
// Decision 2. Because $CODEX_HOME/skills is shared with Codex's own content
// and any skills the user installed themselves, stale-directory removal
// there is gated on a positive per-directory ownership marker (never on mere
// absence-of-recognition) and .system/ is skipped unconditionally regardless
// — see materializeBuiltinSkillsInto. Idempotent and inert (no filesystem
// writes, no error) when running inside a Go test binary, mirroring
// EnsureBuiltinSkills.
func EnsureCodexSkills() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	return ensureCodexSkills()
}

// ensureCodexSkills is the untested-for-isTestBinary core of EnsureCodexSkills,
// split out so tests can exercise the real materialization logic directly
// (EnsureCodexSkills always short-circuits under `go test`).
func ensureCodexSkills() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home dir: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	// exclusiveOwner=false: $CODEX_HOME/skills is shared with Codex's own
	// bundled .system/ content and any skills the user installed themselves —
	// unlike ~/.argus/skills/.claude/skills, nothing else ever writes there.
	return materializeBuiltinSkillsInto(filepath.Join(codexHome, "skills"), false)
}

// materializeBuiltinSkillsInto writes every embedded builtin skill's files
// into skillsDir/<name>/, idempotently (rewriting a file only when its
// content differs from what's already on disk).
//
// When exclusiveOwner is true, skillsDir is a directory argus alone ever
// writes to (e.g. ~/.argus/skills/.claude/skills) — any directory there that
// no longer corresponds to an embedded skill is stale and removed outright.
//
// When exclusiveOwner is false, skillsDir is shared with another tool (e.g.
// Codex's own $CODEX_HOME/skills). A directory is removed only if it carries
// ownerMarkerFile (proof this function created it on a previous run) AND no
// longer corresponds to an embedded skill — never merely because its name is
// unrecognized. This is what protects Codex's own bundled skills and any
// user-installed ones from being swept away. reservedCodexSystemDir is also
// unconditionally skipped, exclusiveOwner or not, as a second layer of
// protection for that one specific, unusually costly-to-lose directory. The
// same ownership check also gates the WRITE side, not just removal: if a
// builtin skill's name collides with a pre-existing, unmarked directory
// (e.g. a user's own Codex skill happens to be named "hera"), that directory
// is left untouched rather than claimed and overwritten — see
// isForeignPreexistingDir.
//
// Returns skillsDir on success.
func materializeBuiltinSkillsInto(skillsDir string, exclusiveOwner bool) (string, error) {
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}

	// Materialize each builtin skill.
	builtins := BuiltinItems()
	present := make(map[string]bool, len(builtins))
	written := 0
	for _, item := range builtins {
		skillDir := filepath.Join(builtinRoot, item.Name)
		entries, err := fs.ReadDir(builtinFS, skillDir)
		if err != nil {
			uxlog.Log("[skills] embedded skill %q unreadable, skipping: %v", item.Name, err)
			continue
		}
		targetDir := filepath.Join(skillsDir, item.Name)
		if !exclusiveOwner && isForeignPreexistingDir(targetDir) {
			uxlog.Log("[skills] %q under %q already exists and is not argus-owned (name collision) — leaving it untouched, not claiming it", item.Name, skillsDir)
			continue
		}
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			uxlog.Log("[skills] create dir for skill %q under %q failed, skipping: %v", item.Name, skillsDir, err)
			continue
		}
		present[item.Name] = true
		ok := true
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			src := filepath.Join(skillDir, e.Name())
			data, err := fs.ReadFile(builtinFS, src)
			if err != nil {
				uxlog.Log("[skills] read embedded file %q failed, skipping: %v", src, err)
				ok = false
				continue
			}
			dst := filepath.Join(targetDir, e.Name())
			if err := atomicWriteIfDifferent(dst, data); err != nil {
				uxlog.Log("[skills] write %q failed: %v", dst, err)
				ok = false
			}
		}
		if err := atomicWriteIfDifferent(filepath.Join(targetDir, ownerMarkerFile), []byte(ownerMarkerContent)); err != nil {
			uxlog.Log("[skills] write ownership marker for %q failed: %v", item.Name, err)
			ok = false
		}
		if ok {
			written++
		}
	}

	// Remove stale skills.
	existingDirs, _ := os.ReadDir(skillsDir)
	removed := 0
	for _, d := range existingDirs {
		if !d.IsDir() || present[d.Name()] {
			continue
		}
		if d.Name() == reservedCodexSystemDir {
			continue
		}
		if !exclusiveOwner {
			if _, err := os.Stat(filepath.Join(skillsDir, d.Name(), ownerMarkerFile)); err != nil {
				uxlog.Log("[skills] leaving unrecognized directory %q under %q untouched (no argus ownership marker)", d.Name(), skillsDir)
				continue
			}
		}
		if err := os.RemoveAll(filepath.Join(skillsDir, d.Name())); err != nil {
			uxlog.Log("[skills] remove stale skill dir %q under %q failed: %v", d.Name(), skillsDir, err)
			continue
		}
		removed++
	}
	uxlog.Log("[skills] materialized %d/%d builtin skills into %q (removed %d stale)", written, len(builtins), skillsDir, removed)
	return skillsDir, nil
}

// isForeignPreexistingDir reports whether targetDir already exists on disk
// but was not created by a prior run of this package's materialization
// (i.e. it exists and lacks ownerMarkerFile). Only meaningful for the shared
// (non-exclusiveOwner) case: a directory name argus wants to materialize can
// collide with a pre-existing, unrelated directory of the same name — e.g. a
// user's own Codex skill happening to be named "hera" — and without this
// check the write path below would silently overwrite its content and then
// stamp it with argus's ownership marker, making it eligible for deletion on
// a later run once that name drops out of the embedded set. A name that
// doesn't exist yet, or that already carries the marker from a prior argus
// run, is free to claim (returns false).
func isForeignPreexistingDir(targetDir string) bool {
	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(targetDir, ownerMarkerFile))
	return err != nil
}

// atomicWriteIfDifferent writes data to path only if the current file content
// differs. Uses a temp file + rename pattern to avoid partial writes.
func atomicWriteIfDifferent(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readEmbeddedFrontmatterField extracts a YAML field value from the frontmatter
// of an embedded file. Returns "" if the file doesn't exist or the field is missing.
func readEmbeddedFrontmatterField(path, field string) string {
	data, err := fs.ReadFile(builtinFS, path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "---") {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "---" {
			break
		}
		prefix := field + ":"
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	return ""
}

// isTestBinary returns true if the current process is a test executable.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}
