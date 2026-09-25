package skills

import (
	"bytes"
	"crypto/sha256"
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

// ArgusCodexHome is the Codex state root used only by Argus-launched sessions.
// The caller sets CODEX_HOME to this path for the child process. Ordinary Codex
// sessions keep their own home and never discover Argus's embedded skills.
func ArgusCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	codexHome, err := UserCodexHome()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".local", "share", "argus", "codex-home")
	if codexHome != filepath.Join(home, ".codex") {
		// A distinct source home needs a distinct overlay: existing links must
		// never make a later custom CODEX_HOME see another home's content.
		digest := sha256.Sum256([]byte(codexHome))
		return filepath.Join(root, fmt.Sprintf("custom-%x", digest[:8])), nil
	}
	return root, nil
}

// UserCodexHome resolves the normal Codex state root before Argus overrides
// CODEX_HOME for a child session.
func UserCodexHome() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home dir: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	root, err := filepath.Abs(codexHome)
	if err != nil {
		return "", fmt.Errorf("resolve Codex home: %w", err)
	}
	return root, nil
}

// EnsureCodexSkills builds a Codex home under ~/.local/share/argus for Argus sessions.
// Existing Codex state and user skills are linked in from the normal Codex
// home, while Argus's embedded skills exist only in this Argus-owned home.
// It is inert under go test; tests call ensureCodexSkills directly.
func EnsureCodexSkills() (string, error) {
	if isTestBinary() {
		return "", nil
	}
	return ensureCodexSkills()
}

// ensureCodexSkills is the testable core of EnsureCodexSkills.
func ensureCodexSkills() (string, error) {
	codexHome, err := UserCodexHome()
	if err != nil {
		return "", err
	}
	argusHome, err := ArgusCodexHome()
	if err != nil {
		return "", err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	managedRoot := filepath.Join(userHome, ".local", "share", "argus", "codex-home")
	if codexHome == managedRoot || strings.HasPrefix(codexHome, managedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("CODEX_HOME already points at Argus's managed Codex home")
	}
	if err := os.MkdirAll(argusHome, 0700); err != nil {
		return "", fmt.Errorf("create Argus Codex home: %w", err)
	}
	// #nosec G302 -- this is a directory, so the owner needs execute access.
	if err := os.Chmod(argusHome, 0700); err != nil {
		return "", fmt.Errorf("protect Argus Codex home: %w", err)
	}
	// Link existing state and configuration so authentication, preferences,
	// plugins, and session history carry over. The skills subtree is handled
	// separately so Argus skills never enter the user's normal Codex catalog.
	if err := linkCodexEntries(codexHome, argusHome, "skills"); err != nil {
		return "", err
	}
	skillsDir := filepath.Join(argusHome, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return "", fmt.Errorf("create Argus Codex skills dir: %w", err)
	}
	if err := linkCodexSkills(filepath.Join(codexHome, "skills"), skillsDir); err != nil {
		return "", err
	}
	if _, err := materializeBuiltinSkillsInto(skillsDir, false); err != nil {
		return "", err
	}
	return argusHome, nil
}

// linkCodexEntries mirrors existing root entries without writing to the
// user's Codex home. The source wins if Codex replaced an overlay symlink;
// the overlay copy is preserved before restoring the link.
func linkCodexEntries(sourceDir, targetDir, skip string) error {
	entries, err := os.ReadDir(sourceDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Codex home: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == skip {
			continue
		}
		source := filepath.Join(sourceDir, entry.Name())
		target := filepath.Join(targetDir, entry.Name())
		if err := linkCodexSource(source, target, targetDir); err != nil {
			return fmt.Errorf("link Codex state %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func linkCodexSkills(sourceDir, targetDir string) error {
	if err := removeDanglingCodexSkillLinks(sourceDir, targetDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(sourceDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Codex skills: %w", err)
	}
	for _, entry := range entries {
		source := filepath.Join(sourceDir, entry.Name())
		// Previously installed Argus skills in the user's global Codex home
		// must not be linked back into the isolated home as foreign skills.
		if _, err := os.Stat(filepath.Join(source, ownerMarkerFile)); err == nil {
			continue
		}
		target := filepath.Join(targetDir, entry.Name())
		if entry.Name() == reservedCodexSystemDir {
			if _, err := os.Lstat(target); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("inspect Codex system skills: %w", err)
			}
		}
		if err := linkCodexSource(source, target, filepath.Dir(targetDir)); err != nil {
			return fmt.Errorf("link Codex skill %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func linkCodexSource(source, target, overlayHome string) error {
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if linked, err := os.Readlink(target); err == nil && linked == source {
				return nil
			}
		}
		// Keep Argus-session writes recoverable while making the user's normal
		// Codex home authoritative on the next launch.
		backupDir, err := os.MkdirTemp(overlayHome, ".argus-preserved-")
		if err != nil {
			return fmt.Errorf("preserve overlay entry: %w", err)
		}
		if err := os.Rename(target, filepath.Join(backupDir, filepath.Base(target))); err != nil {
			return fmt.Errorf("preserve overlay entry: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect overlay entry: %w", err)
	}
	return os.Symlink(source, target)
}

func removeDanglingCodexSkillLinks(sourceDir, targetDir string) error {
	entries, err := os.ReadDir(targetDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read overlay skills: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == reservedCodexSystemDir {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(targetDir, entry.Name())
		linked, err := os.Readlink(path)
		if err != nil || linked != filepath.Join(sourceDir, entry.Name()) {
			continue
		}
		if _, err := os.Stat(linked); os.IsNotExist(err) {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove missing Codex skill link %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
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
	return parseFrontmatterLines(strings.Split(string(data), "\n"), field)
}

// parseFrontmatterLines is readEmbeddedFrontmatterField's line-parsing core,
// split out so it's directly testable against synthetic input — the
// embedded-FS path has no swappable backing store to inject test fixtures
// into (builtinFS is a fixed //go:embed of the real skill sources), so this
// is what lets the slice-index adapter passed to readBlockScalar be exercised
// by the same edge-case tests as readFrontmatterField's scanner-based path,
// without touching builtinFS at all.
func parseFrontmatterLines(lines []string, field string) string {
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
			if folded, ok := blockScalarStyle(val); ok {
				idx := i + 1
				return readBlockScalar(func() (string, bool) {
					if idx < len(lines) {
						line := lines[idx]
						idx++
						return line, true
					}
					return "", false
				}, folded)
			}
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
