package routing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBuiltinContent_ContainsBothSnippets(t *testing.T) {
	content, err := BuiltinContent()
	testutil.NoError(t, err)
	s := string(content)
	testutil.Contains(t, s, "## Hera multi-agent coordination (argus sandboxes)")
	testutil.Contains(t, s, "## Argus task self-management (argus sandboxes)")
}

// TestBuiltinContent_MatchesRepoSnippets is a drift guard: it reads
// claude/snippets/*.md directly off disk (not through go:embed) and asserts
// byte-identity against the embedded copies. Without this, an edit to the
// canonical snippet that isn't mirrored into internal/routing/builtin/ would
// silently drift — exactly what happened, unguarded, between
// internal/skills/builtin/hera/SKILL.md and .claude/skills/hera/SKILL.md.
func TestBuiltinContent_MatchesRepoSnippets(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	cases := []struct {
		snippet string
		builtin string
	}{
		{filepath.Join(repoRoot, "claude", "snippets", "hera.md"), filepath.Join("builtin", "hera.md")},
		{filepath.Join(repoRoot, "claude", "snippets", "argus-tasks.md"), filepath.Join("builtin", "argus-tasks.md")},
	}
	for _, tc := range cases {
		t.Run(tc.snippet, func(t *testing.T) {
			want, err := os.ReadFile(tc.snippet)
			testutil.NoError(t, err)
			got, err := os.ReadFile(tc.builtin)
			testutil.NoError(t, err)
			testutil.DeepEqual(t, got, want)
		})
	}
}

func TestBuiltinContent_DeterministicOrder(t *testing.T) {
	c1, err := BuiltinContent()
	testutil.NoError(t, err)
	c2, err := BuiltinContent()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, c1, c2)

	// argus-tasks.md sorts before hera.md — pin the concatenation order.
	tasksIdx := indexOf(string(c1), "## Argus task self-management")
	heraIdx := indexOf(string(c1), "## Hera multi-agent coordination")
	if tasksIdx == -1 || heraIdx == -1 {
		t.Fatalf("expected both sections present")
	}
	if tasksIdx > heraIdx {
		t.Errorf("expected argus-tasks.md content before hera.md content (name-sorted), got tasks@%d hera@%d", tasksIdx, heraIdx)
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestMaterialize_WritesConcatenatedContent(t *testing.T) {
	dir := t.TempDir()
	path, err := materialize(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, path, filepath.Join(dir, systemPromptFilename))

	got, err := os.ReadFile(path)
	testutil.NoError(t, err)
	want, err := BuiltinContent()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, want)
}

func TestMaterialize_IdempotentNoRewrite(t *testing.T) {
	dir := t.TempDir()
	path, err := materialize(dir)
	testutil.NoError(t, err)

	info1, err := os.Stat(path)
	testutil.NoError(t, err)

	// Re-materialize; unchanged content must not trigger a rewrite (checked
	// via ModTime rather than assuming atomicWriteIfDifferent internals).
	path2, err := materialize(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, path2, path)

	info2, err := os.Stat(path)
	testutil.NoError(t, err)
	testutil.Equal(t, info1.ModTime(), info2.ModTime())
}

func TestMaterialize_CreatesWorkspaceDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "routing")
	path, err := materialize(dir)
	testutil.NoError(t, err)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected materialized file to exist: %v", err)
	}
}

func TestEnsureBuiltinRouting_InertUnderGoTest(t *testing.T) {
	path, err := EnsureBuiltinRouting()
	testutil.NoError(t, err)
	testutil.Equal(t, path, "")
}
