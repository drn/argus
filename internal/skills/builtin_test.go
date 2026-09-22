package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBuiltinItems_IncludesAllExpectedSkills(t *testing.T) {
	items := BuiltinItems()
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	testutil.DeepEqual(t, names, []string{
		"archive",
		"argus-complete",
		"argus-schedule",
		"hera",
		"hera-plan",
		"hera-review",
		"hera-review-test-adversary",
		"hera-spawn-review",
		"resolve-archetype-model",
		"task-recycle",
	})
}

func TestBuiltinItems_ReviewSkillsHaveDescriptions(t *testing.T) {
	items := BuiltinItems()
	byName := make(map[string]string, len(items))
	for _, it := range items {
		byName[it.Name] = it.Description
	}

	reviewDesc, ok := byName["hera-review"]
	if !ok || reviewDesc == "" {
		t.Fatalf("expected non-empty description for hera-review, got %q (present: %v)", reviewDesc, ok)
	}
	adversaryDesc, ok := byName["hera-review-test-adversary"]
	if !ok || adversaryDesc == "" {
		t.Fatalf("expected non-empty description for hera-review-test-adversary, got %q (present: %v)", adversaryDesc, ok)
	}
	spawnReviewDesc, ok := byName["hera-spawn-review"]
	if !ok || spawnReviewDesc == "" {
		t.Fatalf("expected non-empty description for hera-spawn-review, got %q (present: %v)", spawnReviewDesc, ok)
	}
	resolveModelDesc, ok := byName["resolve-archetype-model"]
	if !ok || resolveModelDesc == "" {
		t.Fatalf("expected non-empty description for resolve-archetype-model, got %q (present: %v)", resolveModelDesc, ok)
	}
}

func TestMaterializeBuiltinSkillsInto_WritesEmbeddedSet(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	got, err := materializeBuiltinSkillsInto(skillsDir, true)
	testutil.NoError(t, err)
	testutil.Equal(t, got, skillsDir)

	entries, err := os.ReadDir(skillsDir)
	testutil.NoError(t, err)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	testutil.DeepEqual(t, names, []string{
		"archive",
		"argus-complete",
		"argus-schedule",
		"hera",
		"hera-plan",
		"hera-review",
		"hera-review-test-adversary",
		"hera-spawn-review",
		"resolve-archetype-model",
		"task-recycle",
	})

	if _, err := os.Stat(filepath.Join(skillsDir, "hera", skillManifestFile)); err != nil {
		t.Fatalf("expected hera/SKILL.md to exist: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_WritesOwnershipMarker(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if _, err := materializeBuiltinSkillsInto(skillsDir, true); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "hera", ownerMarkerFile)); err != nil {
		t.Fatalf("expected ownership marker under hera/, got: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_ExclusiveOwner_RemovesStaleDirectories(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	staleDir := filepath.Join(skillsDir, "not-a-real-skill")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir, true); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("expected stale directory to be removed, stat err: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_IdempotentNoRewrite(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if _, err := materializeBuiltinSkillsInto(skillsDir, true); err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	manifest := filepath.Join(skillsDir, "hera", skillManifestFile)
	before, err := os.Stat(manifest)
	testutil.NoError(t, err)

	if _, err := materializeBuiltinSkillsInto(skillsDir, true); err != nil {
		t.Fatalf("second materialize: %v", err)
	}
	after, err := os.Stat(manifest)
	testutil.NoError(t, err)

	testutil.Equal(t, after.ModTime(), before.ModTime())
}

// --- Shared (non-exclusive) directory: the $CODEX_HOME/skills case ---

func TestMaterializeBuiltinSkillsInto_SharedDir_PreservesReservedSystemDir(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	systemDir := filepath.Join(skillsDir, reservedCodexSystemDir)
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	marker := filepath.Join(systemDir, "codex-bundled-skill-marker.txt")
	if err := os.WriteFile(marker, []byte("codex's own content"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir, false); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected .system/ content to survive materialization, got: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_SharedDir_PreservesForeignUserSkill(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	foreignDir := filepath.Join(skillsDir, "my-own-custom-skill")
	if err := os.MkdirAll(foreignDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	marker := filepath.Join(foreignDir, "SKILL.md")
	if err := os.WriteFile(marker, []byte("a user's own skill, not argus's"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir, false); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected unrelated pre-existing user skill to survive materialization untouched, got: %v", err)
	}
}

// TestMaterializeBuiltinSkillsInto_SharedDir_NameCollisionLeavesForeignContentUntouched
// covers the narrower variant of the same bug class the two tests above
// don't reach: a directory that happens to share a NAME with one of argus's
// own builtin skills (e.g. a user's own Codex skill named "hera"), rather
// than an unrelated name. Without the write-side ownership check, argus
// would silently overwrite the user's content and stamp its own marker onto
// it — making it eligible for deletion on a later run once "hera" dropped
// out of the embedded set.
func TestMaterializeBuiltinSkillsInto_SharedDir_NameCollisionLeavesForeignContentUntouched(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	collidingDir := filepath.Join(skillsDir, "hera") // "hera" is a real embedded builtin skill name
	if err := os.MkdirAll(collidingDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	userFile := filepath.Join(collidingDir, skillManifestFile)
	userContent := []byte("this is a user's own unrelated skill, not argus's hera skill")
	if err := os.WriteFile(userFile, userContent, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir, false); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	got, err := os.ReadFile(userFile)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), string(userContent))

	if _, err := os.Stat(filepath.Join(collidingDir, ownerMarkerFile)); !os.IsNotExist(err) {
		t.Fatalf("expected no ownership marker to be written onto foreign colliding directory, stat err: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_SharedDir_RemovesOwnStaleSkill(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	// Simulate a directory argus itself materialized on a previous run for a
	// skill that is no longer part of the embedded set: it carries the
	// ownership marker but its name isn't among today's builtins.
	orphan := filepath.Join(skillsDir, "orphaned-argus-skill")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, ownerMarkerFile), []byte(ownerMarkerContent), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir, false); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected argus-owned orphaned skill dir to be removed, stat err: %v", err)
	}
}

func TestEnsureCodexSkills_InertUnderTest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	root, err := EnsureCodexSkills()
	testutil.NoError(t, err)
	testutil.Equal(t, root, "")

	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("expected no ~/.codex directory created under test, stat err: %v", err)
	}
}

func TestEnsureCodexSkills_RespectsCodexHomeEnvVar(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	got, err := ensureCodexSkills()
	testutil.NoError(t, err)
	testutil.Equal(t, got, filepath.Join(codexHome, "skills"))

	if _, err := os.Stat(filepath.Join(codexHome, "skills", "hera", skillManifestFile)); err != nil {
		t.Fatalf("expected hera/SKILL.md under CODEX_HOME/skills: %v", err)
	}
}

func TestEnsureCodexSkills_PreservesSystemAndForeignDirs(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	systemMarker := filepath.Join(codexHome, "skills", reservedCodexSystemDir, "bundled.txt")
	if err := os.MkdirAll(filepath.Dir(systemMarker), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(systemMarker, []byte("codex bundled"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	foreignMarker := filepath.Join(codexHome, "skills", "someones-custom-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(foreignMarker), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(foreignMarker, []byte("not argus's"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// "hera" is a real embedded builtin skill name — this pre-existing,
	// unmarked directory must be left untouched, not claimed/overwritten,
	// even though its name collides with one argus wants to materialize.
	collidingFile := filepath.Join(codexHome, "skills", "hera", skillManifestFile)
	if err := os.MkdirAll(filepath.Dir(collidingFile), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	collidingContent := []byte("a user's own skill that happens to be named hera")
	if err := os.WriteFile(collidingFile, collidingContent, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := ensureCodexSkills(); err != nil {
		t.Fatalf("ensureCodexSkills: %v", err)
	}

	if _, err := os.Stat(systemMarker); err != nil {
		t.Fatalf("expected .system/ to survive: %v", err)
	}
	if _, err := os.Stat(foreignMarker); err != nil {
		t.Fatalf("expected foreign user skill to survive: %v", err)
	}
	got, err := os.ReadFile(collidingFile)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), string(collidingContent))
}

func TestEnsureCodexSkills_DefaultsToDotCodexUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	got, err := ensureCodexSkills()
	testutil.NoError(t, err)
	testutil.Equal(t, got, filepath.Join(home, ".codex", "skills"))
}

func TestBuiltinSkillsDir(t *testing.T) {
	got := BuiltinSkillsDir("/x/.argus/skills")
	testutil.Equal(t, got, filepath.Join("/x/.argus/skills", ".claude", "skills"))
}
