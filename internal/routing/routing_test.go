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

func TestBuiltinContent_ContainsCodeReviewSection(t *testing.T) {
	content, err := BuiltinContent()
	testutil.NoError(t, err)
	s := string(content)
	testutil.Contains(t, s, "## Code review methodology (argus sandboxes)")
	testutil.Contains(t, s, "hera-review")
	testutil.Contains(t, s, "hera-review-test-adversary")
}

func TestBuiltinContent_ContainsPanelReviewSection(t *testing.T) {
	content, err := BuiltinContent()
	testutil.NoError(t, err)
	s := string(content)
	testutil.Contains(t, s, "## Panel review orchestration (argus sandboxes)")
	testutil.Contains(t, s, "hera-spawn-review")
}

func TestBuiltinContent_ContainsArchetypeModelSection(t *testing.T) {
	content, err := BuiltinContent()
	testutil.NoError(t, err)
	s := string(content)
	testutil.Contains(t, s, "## Archetype→model resolution for native sub-agent dispatch (argus sandboxes)")
	testutil.Contains(t, s, "resolve-archetype-model")
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
