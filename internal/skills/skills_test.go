package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestFilterSkills(t *testing.T) {
	items := []SkillItem{
		{Name: "commit", Description: "Create a commit"},
		{Name: "review", Description: "Review PR"},
		{Name: "test", Description: "Run tests"},
		{Name: "cortex:review", Description: "Plugin review"},
	}

	tests := []struct {
		name   string
		filter string
		want   []string
	}{
		{"empty returns all", "", []string{"commit", "review", "test", "cortex:review"}},
		{"substring co", "co", []string{"commit", "cortex:review"}},
		{"substring cortex:", "cortex:", []string{"cortex:review"}},
		{"substring re — matches user and plugin review", "re", []string{"review", "cortex:review"}},
		{"substring rev mid-name", "rev", []string{"review", "cortex:review"}},
		{"no match", "xyz", nil},
		{"case insensitive", "CO", []string{"commit", "cortex:review"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterSkills(items, tc.filter)
			var names []string
			for _, s := range got {
				names = append(names, s.Name)
			}
			testutil.DeepEqual(t, names, tc.want)
		})
	}
}

func TestLoadSkills_DiscoversUserSkillsAndPluginItems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// User skill: ~/.claude/skills/commit/SKILL.md
	userSkill := filepath.Join(home, ".claude", "skills", "commit")
	testutil.NoError(t, os.MkdirAll(userSkill, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(userSkill, "SKILL.md"),
		[]byte("---\nname: commit\ndescription: \"User commit skill\"\n---\n"), 0o644))

	// Plugin install tree: ~/.claude/plugins/cache/mp/cortex/1.0/{commands,skills}
	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "mp", "cortex", "1.0")
	cmdDir := filepath.Join(pluginRoot, "commands")
	testutil.NoError(t, os.MkdirAll(cmdDir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(cmdDir, "review.md"),
		[]byte("---\ndescription: Plugin review command\n---\n"), 0o644))
	testutil.NoError(t, os.WriteFile(filepath.Join(cmdDir, "notes.txt"),
		[]byte("not a command"), 0o644)) // non-.md file must be ignored

	// Nested plugin skill: skills/nested/font-licensing/SKILL.md — use frontmatter name
	nestedSkill := filepath.Join(pluginRoot, "skills", "merchant", "font-licensing")
	testutil.NoError(t, os.MkdirAll(nestedSkill, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(nestedSkill, "SKILL.md"),
		[]byte("---\nname: font-licensing\ndescription: Font licensing skill\n---\n"), 0o644))

	// installed_plugins.json pointing at that install path
	manifestDir := filepath.Join(home, ".claude", "plugins")
	testutil.NoError(t, os.MkdirAll(manifestDir, 0o755))
	manifest := map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"cortex@mp": []map[string]any{{"installPath": pluginRoot}},
		},
	}
	data, err := json.Marshal(manifest)
	testutil.NoError(t, err)
	testutil.NoError(t, os.WriteFile(filepath.Join(manifestDir, "installed_plugins.json"), data, 0o644))

	items := LoadSkills(nil)
	names := make([]string, len(items))
	for i, s := range items {
		names[i] = s.Name
	}
	testutil.DeepEqual(t, names, []string{
		"commit",
		"cortex:font-licensing",
		"cortex:review",
	})

	// Description propagates from frontmatter for plugin items.
	byName := map[string]string{}
	for _, s := range items {
		byName[s.Name] = s.Description
	}
	testutil.Equal(t, byName["cortex:review"], "Plugin review command")
	testutil.Equal(t, byName["cortex:font-licensing"], "Font licensing skill")
	testutil.Equal(t, byName["commit"], "User commit skill")
}

func TestLoadSkills_NoPluginManifestStillReturnsUserSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userSkill := filepath.Join(home, ".claude", "skills", "polish")
	testutil.NoError(t, os.MkdirAll(userSkill, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(userSkill, "SKILL.md"),
		[]byte("---\ndescription: Polish\n---\n"), 0o644))

	items := LoadSkills(nil)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "polish")
}

func TestLoadSkills_ExtraDirOverridesUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// User skill named "review" with description A.
	userSkill := filepath.Join(home, ".claude", "skills", "review")
	testutil.NoError(t, os.MkdirAll(userSkill, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(userSkill, "SKILL.md"),
		[]byte("---\ndescription: user review\n---\n"), 0o644))

	// Project skill named "review" with description B — should win.
	projectRoot := t.TempDir()
	projSkill := filepath.Join(projectRoot, "review")
	testutil.NoError(t, os.MkdirAll(projSkill, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(projSkill, "SKILL.md"),
		[]byte("---\ndescription: project review\n---\n"), 0o644))

	items := LoadSkills([]string{projectRoot})
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "review")
	testutil.Equal(t, items[0].Description, "project review")
}

func TestLoadSkills_FollowsSymlinkedSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Real skills directory lives outside the plugin install path — Claude Code
	// plugins typically ship skills/ as a symlink into the marketplace checkout.
	realSkills := filepath.Join(t.TempDir(), "real-skills")
	nested := filepath.Join(realSkills, "nucleus")
	testutil.NoError(t, os.MkdirAll(nested, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(nested, "SKILL.md"),
		[]byte("---\nname: nucleus\ndescription: Nucleus skill\n---\n"), 0o644))

	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "mp", "cortex", "1.0")
	testutil.NoError(t, os.MkdirAll(pluginRoot, 0o755))
	testutil.NoError(t, os.Symlink(realSkills, filepath.Join(pluginRoot, "skills")))

	manifestDir := filepath.Join(home, ".claude", "plugins")
	testutil.NoError(t, os.MkdirAll(manifestDir, 0o755))
	manifest := map[string]any{
		"plugins": map[string]any{
			"cortex@mp": []map[string]any{{"installPath": pluginRoot}},
		},
	}
	data, err := json.Marshal(manifest)
	testutil.NoError(t, err)
	testutil.NoError(t, os.WriteFile(filepath.Join(manifestDir, "installed_plugins.json"), data, 0o644))

	items := LoadSkills(nil)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "cortex:nucleus")
}

func TestLoadSkills_RejectsMaliciousPluginNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "mp", "evil", "1.0")
	cmdDir := filepath.Join(pluginRoot, "commands")
	testutil.NoError(t, os.MkdirAll(cmdDir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(cmdDir, "do.md"),
		[]byte("---\ndescription: x\n---\n"), 0o644))

	manifestDir := filepath.Join(home, ".claude", "plugins")
	testutil.NoError(t, os.MkdirAll(manifestDir, 0o755))
	// Plugin name embeds ANSI escape + newline — must be filtered out.
	manifest := map[string]any{
		"plugins": map[string]any{
			"evil\x1b[31m\n@mp": []map[string]any{{"installPath": pluginRoot}},
		},
	}
	data, err := json.Marshal(manifest)
	testutil.NoError(t, err)
	testutil.NoError(t, os.WriteFile(filepath.Join(manifestDir, "installed_plugins.json"), data, 0o644))

	items := LoadSkills(nil)
	testutil.Equal(t, len(items), 0)
}

func TestLoadSkills_SkipsDanglingSkillsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "mp", "cortex", "1.0")
	testutil.NoError(t, os.MkdirAll(pluginRoot, 0o755))
	// Point skills/ at a path that does not exist — EvalSymlinks returns an error.
	testutil.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(pluginRoot, "skills")))

	// But commands/ still works, so we expect only command discovery.
	cmdDir := filepath.Join(pluginRoot, "commands")
	testutil.NoError(t, os.MkdirAll(cmdDir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(cmdDir, "review.md"),
		[]byte("---\ndescription: cmd\n---\n"), 0o644))

	manifestDir := filepath.Join(home, ".claude", "plugins")
	testutil.NoError(t, os.MkdirAll(manifestDir, 0o755))
	manifest := map[string]any{
		"plugins": map[string]any{
			"cortex@mp": []map[string]any{{"installPath": pluginRoot}},
		},
	}
	data, err := json.Marshal(manifest)
	testutil.NoError(t, err)
	testutil.NoError(t, os.WriteFile(filepath.Join(manifestDir, "installed_plugins.json"), data, 0o644))

	items := LoadSkills(nil)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "cortex:review")
}

func TestReadFrontmatterField_OverLongLineReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	// A description field whose value exceeds the 1 MB cap must produce an
	// empty string rather than a truncated/garbled value.
	huge := strings.Repeat("x", 2*1024*1024)
	testutil.NoError(t, os.WriteFile(path,
		[]byte("---\ndescription: "+huge+"\n---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.Equal(t, got, "")
}

// TestReadFrontmatterField_FoldedBlockScalar pins support for YAML folded
// block scalars ("description: >-"), which a hand-rolled single-line scanner
// would otherwise return as the literal header text ">-" instead of the
// intended multi-line value — the shape several builtin skills (hera,
// hera-plan, hera-review, ...) use for their descriptions.
func TestReadFrontmatterField_FoldedBlockScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	testutil.NoError(t, os.WriteFile(path, []byte(
		"---\n"+
			"name: example\n"+
			"description: >-\n"+
			"  This is a long description that wraps across\n"+
			"  several indented lines in the source file but\n"+
			"  should read as one flowing sentence.\n"+
			"allowed-tools: mcp__example\n"+
			"---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.Equal(t, got,
		"This is a long description that wraps across several indented lines in the source file but should read as one flowing sentence.")
}

// TestReadFrontmatterField_LiteralBlockScalar pins support for YAML literal
// block scalars ("|"), which preserve line breaks rather than folding them
// into a single line.
func TestReadFrontmatterField_LiteralBlockScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	testutil.NoError(t, os.WriteFile(path, []byte(
		"---\n"+
			"description: |\n"+
			"  line one\n"+
			"  line two\n"+
			"---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.Equal(t, got, "line one\nline two")
}

// TestReadFrontmatterField_BlockScalarStopsAtDedent confirms the block
// scalar's continuation lines stop at the first line dedented below the
// block's own indentation, and a normal top-level field following it (e.g.
// closing frontmatter or another key) is not swallowed into the value.
func TestReadFrontmatterField_BlockScalarStopsAtDedent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	testutil.NoError(t, os.WriteFile(path, []byte(
		"---\n"+
			"description: >-\n"+
			"  folded text here\n"+
			"allowed-tools: mcp__example\n"+
			"---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.Equal(t, got, "folded text here")

	// The dedented "allowed-tools" line must not have been swallowed into
	// the description, and must itself still be readable as its own field.
	got2 := readFrontmatterField(path, "allowed-tools")
	testutil.Equal(t, got2, "mcp__example")
}

// TestReadFrontmatterField_BlockScalarLeadingBlankLineStopsAtDedent confirms
// a block scalar that opens with a blank line before any indented content
// still stops at the next top-level field, rather than swallowing it. A
// leading blank line means indentation hasn't been established yet when the
// dedented sibling field arrives; the dedent check must catch a column-0
// line unconditionally, not only once indentation is known.
func TestReadFrontmatterField_BlockScalarLeadingBlankLineStopsAtDedent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	testutil.NoError(t, os.WriteFile(path, []byte(
		"---\n"+
			"description: >-\n"+
			"\n"+
			"allowed-tools: mcp__example\n"+
			"---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.NotEqual(t, got, "allowed-tools: mcp__example")
	testutil.Equal(t, got, "")

	got2 := readFrontmatterField(path, "allowed-tools")
	testutil.Equal(t, got2, "mcp__example")
}

// TestReadFrontmatterField_NonBlockScalarArrowNotMisdetected confirms a
// value that merely starts with ">" but isn't a bare block-scalar header
// (e.g. quoted text containing a literal arrow) is read as an ordinary
// inline value, not misinterpreted as introducing a block scalar.
func TestReadFrontmatterField_NonBlockScalarArrowNotMisdetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	testutil.NoError(t, os.WriteFile(path, []byte(
		"---\n"+
			"description: \">50% faster\"\n"+
			"---\n"), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.Equal(t, got, ">50% faster")
}

// TestReadFrontmatterField_BlockScalarCappedAtMaxLines confirms a malformed
// block scalar with no closing "---" and no dedent (so nothing would ever
// stop the continuation loop on its own) terminates at blockScalarMaxLines
// rather than scanning to EOF unbounded.
func TestReadFrontmatterField_BlockScalarCappedAtMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	var b strings.Builder
	b.WriteString("---\ndescription: >-\n")
	for i := 0; i < blockScalarMaxLines+500; i++ {
		b.WriteString("  line\n")
	}
	// Deliberately no closing "---" — the whole file is the frontmatter block.
	testutil.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.True(t, len(got) > 0)
	testutil.Equal(t, strings.Count(got, "line"), blockScalarMaxLines)
}

// TestReadFrontmatterField_BlockScalarCappedAtMaxBytes confirms the total
// byte-size cap trips independently of the line-count cap: a handful of very
// long lines (well under blockScalarMaxLines) whose combined size exceeds
// blockScalarMaxBytes must still stop consumption, not be waved through just
// because there are few of them.
func TestReadFrontmatterField_BlockScalarCappedAtMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	// Each line stays safely under frontmatterMaxLine (the per-line scanner
	// buffer cap) so the outer scan itself never truncates; the total across
	// all lines is what should trip blockScalarMaxBytes.
	longLine := strings.Repeat("x", 100*1024) // 100 KB
	const lineCount = 20                      // 20 * 100 KB = ~2 MB > blockScalarMaxBytes (1 MB)

	var b strings.Builder
	b.WriteString("---\ndescription: >-\n")
	for i := 0; i < lineCount; i++ {
		b.WriteString("  " + longLine + "\n")
	}
	b.WriteString("---\n")
	testutil.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))

	got := readFrontmatterField(path, "description")
	testutil.True(t, len(got) > 0)
	testutil.True(t, len(got) < lineCount*len(longLine))
}
