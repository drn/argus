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

	got, err := materializeBuiltinSkillsInto(skillsDir)
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
	})

	if _, err := os.Stat(filepath.Join(skillsDir, "hera", skillManifestFile)); err != nil {
		t.Fatalf("expected hera/SKILL.md to exist: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_RemovesStaleDirectories(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	staleDir := filepath.Join(skillsDir, "not-a-real-skill")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := materializeBuiltinSkillsInto(skillsDir); err != nil {
		t.Fatalf("materializeBuiltinSkillsInto: %v", err)
	}

	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("expected stale directory to be removed, stat err: %v", err)
	}
}

func TestMaterializeBuiltinSkillsInto_IdempotentNoRewrite(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if _, err := materializeBuiltinSkillsInto(skillsDir); err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	manifest := filepath.Join(skillsDir, "hera", skillManifestFile)
	before, err := os.Stat(manifest)
	testutil.NoError(t, err)

	if _, err := materializeBuiltinSkillsInto(skillsDir); err != nil {
		t.Fatalf("second materialize: %v", err)
	}
	after, err := os.Stat(manifest)
	testutil.NoError(t, err)

	testutil.Equal(t, after.ModTime(), before.ModTime())
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
