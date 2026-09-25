package skills

import (
	"os"
	"path/filepath"
	"strings"
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
		"argus-archive",
		"argus-complete",
		"argus-recycle",
		"argus-resolve-model",
		"argus-schedule",
		"hera",
		"hera-plan",
		"hera-review",
		"hera-review-test-adversary",
		"hera-spawn-review",
	})
}

// TestBuiltinItems_ReviewSkillsHaveDescriptions asserts real description
// *content*, not just non-emptiness — the literal YAML block-scalar header
// text (">-") is itself a non-empty string, so a bare `!= ""` check here
// would pass even when the frontmatter parser mishandles a
// "description: >-" folded block scalar (as it did for every skill in this
// test before that parsing was fixed: hera-review, hera-review-test-adversary,
// hera-spawn-review, and argus-resolve-model all use this format).
func TestBuiltinItems_ReviewSkillsHaveDescriptions(t *testing.T) {
	items := BuiltinItems()
	byName := make(map[string]string, len(items))
	for _, it := range items {
		byName[it.Name] = it.Description
	}

	for _, name := range []string{"hera-review", "hera-review-test-adversary", "hera-spawn-review", "argus-resolve-model"} {
		desc, ok := byName[name]
		if !ok || desc == "" {
			t.Fatalf("expected non-empty description for %s, got %q (present: %v)", name, desc, ok)
		}
		if strings.HasPrefix(desc, ">") || strings.HasPrefix(desc, "|") {
			t.Fatalf("description for %s looks like an unparsed YAML block-scalar header, not real content: %q", name, desc)
		}
	}

	testutil.Contains(t, byName["hera-review"], "review CONTRACT")
	testutil.Contains(t, byName["hera-review-test-adversary"], "false confidence")
	testutil.Contains(t, byName["hera-spawn-review"], "review panel")
	testutil.Contains(t, byName["argus-resolve-model"], "per-archetype model")
}

// TestBuiltinItems_NoUnparsedBlockScalarHeaders is a general invariant over
// every current (and future) builtin skill: none of their descriptions may
// be a bare YAML block-scalar header. This is what actually would have
// caught the original bug — hera and hera-plan use "description: >-" too but
// aren't covered by name in TestBuiltinItems_ReviewSkillsHaveDescriptions
// above, and a skill added later with the same format shouldn't need its own
// enumerated test case to be protected.
func TestBuiltinItems_NoUnparsedBlockScalarHeaders(t *testing.T) {
	for _, it := range BuiltinItems() {
		if it.Description == ">" || it.Description == ">-" || it.Description == ">+" ||
			it.Description == "|" || it.Description == "|-" || it.Description == "|+" {
			t.Errorf("skill %q has an unparsed YAML block-scalar header as its description: %q", it.Name, it.Description)
		}
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
		"argus-archive",
		"argus-complete",
		"argus-recycle",
		"argus-resolve-model",
		"argus-schedule",
		"hera",
		"hera-plan",
		"hera-review",
		"hera-review-test-adversary",
		"hera-spawn-review",
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	got, err := ensureCodexSkills()
	testutil.NoError(t, err)
	expected, err := ArgusCodexHome()
	testutil.NoError(t, err)
	testutil.Equal(t, got, expected)
	info, err := os.Stat(got)
	testutil.NoError(t, err)
	testutil.Equal(t, info.Mode().Perm(), os.FileMode(0700))

	if _, err := os.Stat(filepath.Join(got, "skills", "hera", skillManifestFile)); err != nil {
		t.Fatalf("expected hera/SKILL.md under Argus CODEX_HOME/skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "skills", "hera", skillManifestFile)); !os.IsNotExist(err) {
		t.Fatalf("normal Codex home must not receive Argus skills: %v", err)
	}
}

func TestEnsureCodexSkills_PreservesSystemAndForeignDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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

	argusHome, err := ensureCodexSkills()
	testutil.NoError(t, err)

	if _, err := os.Stat(systemMarker); err != nil {
		t.Fatalf("expected .system/ to survive: %v", err)
	}
	if _, err := os.Stat(foreignMarker); err != nil {
		t.Fatalf("expected foreign user skill to survive: %v", err)
	}
	got, err := os.ReadFile(collidingFile)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), string(collidingContent))
	for _, path := range []string{systemMarker, foreignMarker, collidingFile} {
		linked := filepath.Join(argusHome, "skills", filepath.Base(filepath.Dir(path)), filepath.Base(path))
		if _, err := os.Stat(linked); err != nil {
			t.Fatalf("normal Codex skill should be available in Argus home at %s: %v", linked, err)
		}
	}
	if _, err := os.Stat(filepath.Join(argusHome, "skills", "argus-complete", skillManifestFile)); err != nil {
		t.Fatalf("Argus skill missing from isolated home: %v", err)
	}
}

func TestEnsureCodexSkills_SeparatesCustomHomes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	firstHome := t.TempDir()
	secondHome := t.TempDir()
	for _, home := range []string{firstHome, secondHome} {
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(home), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", firstHome)
	firstOverlay, err := ensureCodexSkills()
	testutil.NoError(t, err)
	t.Setenv("CODEX_HOME", secondHome)
	secondOverlay, err := ensureCodexSkills()
	testutil.NoError(t, err)
	if firstOverlay == secondOverlay {
		t.Fatal("distinct CODEX_HOME values shared one overlay")
	}
	for _, pair := range [][2]string{{firstOverlay, firstHome}, {secondOverlay, secondHome}} {
		linked, err := os.Readlink(filepath.Join(pair[0], "config.toml"))
		testutil.NoError(t, err)
		testutil.Equal(t, linked, filepath.Join(pair[1], "config.toml"))
	}
}

func TestEnsureCodexSkills_DefaultsToDotCodexUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	got, err := ensureCodexSkills()
	testutil.NoError(t, err)
	testutil.Equal(t, got, filepath.Join(home, ".local", "share", "argus", "codex-home"))
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("normal Codex home must not be created: %v", err)
	}
}

func TestEnsureCodexSkills_LinksExistingConfigAndSkipsPreviouslyGlobalArgusSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "skills", "hera"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = 'test'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "skills", "hera", ownerMarkerFile), []byte(ownerMarkerContent), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ensureCodexSkills()
	testutil.NoError(t, err)
	if link, err := os.Readlink(filepath.Join(got, "config.toml")); err != nil || link != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("config not linked to normal Codex home: %q, %v", link, err)
	}
	if info, err := os.Lstat(filepath.Join(got, "skills", "hera")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Argus skill should be materialized, not linked to old global install: %v, %v", info, err)
	}
}

func TestBuiltinSkillsDir(t *testing.T) {
	got := BuiltinSkillsDir("/x/.argus/skills")
	testutil.Equal(t, got, filepath.Join("/x/.argus/skills", ".claude", "skills"))
}

// The following pin parseFrontmatterLines's slice-index adapter to
// readBlockScalar against the same edge cases skills_test.go covers for
// readFrontmatterField's scanner-based adapter — the two call sites share one
// parsing core (readBlockScalar), but the adapters wrapping it are separate,
// hand-written closures and are worth verifying independently.

func TestParseFrontmatterLines_FoldedBlockScalar(t *testing.T) {
	got := parseFrontmatterLines([]string{
		"---",
		"name: example",
		"description: >-",
		"  This is a long description that wraps across",
		"  several lines but should read as one sentence.",
		"allowed-tools: mcp__example",
		"---",
	}, "description")
	testutil.Equal(t, got, "This is a long description that wraps across several lines but should read as one sentence.")
}

func TestParseFrontmatterLines_LiteralBlockScalar(t *testing.T) {
	got := parseFrontmatterLines([]string{
		"---",
		"description: |",
		"  line one",
		"  line two",
		"---",
	}, "description")
	testutil.Equal(t, got, "line one\nline two")
}

func TestParseFrontmatterLines_BlockScalarStopsAtDedent(t *testing.T) {
	lines := []string{
		"---",
		"description: >-",
		"  folded text here",
		"allowed-tools: mcp__example",
		"---",
	}
	testutil.Equal(t, parseFrontmatterLines(lines, "description"), "folded text here")
	testutil.Equal(t, parseFrontmatterLines(lines, "allowed-tools"), "mcp__example")
}

func TestParseFrontmatterLines_BlockScalarLeadingBlankLineStopsAtDedent(t *testing.T) {
	lines := []string{
		"---",
		"description: >-",
		"",
		"allowed-tools: mcp__example",
		"---",
	}
	got := parseFrontmatterLines(lines, "description")
	testutil.NotEqual(t, got, "allowed-tools: mcp__example")
	testutil.Equal(t, got, "")
	testutil.Equal(t, parseFrontmatterLines(lines, "allowed-tools"), "mcp__example")
}

func TestParseFrontmatterLines_NonBlockScalarArrowNotMisdetected(t *testing.T) {
	got := parseFrontmatterLines([]string{
		"---",
		"description: \">50% faster\"",
		"---",
	}, "description")
	testutil.Equal(t, got, ">50% faster")
}

func TestParseFrontmatterLines_BlockScalarCappedAtMaxLines(t *testing.T) {
	lines := []string{"---", "description: >-"}
	for i := 0; i < blockScalarMaxLines+500; i++ {
		lines = append(lines, "  line")
	}
	// Deliberately no closing "---".
	got := parseFrontmatterLines(lines, "description")
	testutil.True(t, len(got) > 0)
	testutil.Equal(t, strings.Count(got, "line"), blockScalarMaxLines)
}
