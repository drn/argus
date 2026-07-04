package profiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// writeProfile writes a profile TOML file named <name>.toml into dir, creating
// dir if needed. It returns dir for chaining.
func writeProfile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return dir
}

func TestLoad_FromLibrary(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "lean", `
[archetype.code_slice]
model = "sonnet"
`)
	l := &Loader{LibraryDir: lib}

	p, err := l.Load("lean")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Name, "lean")
	testutil.Equal(t, p.Source, SourceLibrary)
	testutil.Equal(t, p.Archetype["code_slice"].Model, "sonnet")
}

func TestLoad_InRepoPrecedence(t *testing.T) {
	repo := writeProfile(t, t.TempDir(), "lean", `
[archetype.code_slice]
model = "opus"
`)
	lib := writeProfile(t, t.TempDir(), "lean", `
[archetype.code_slice]
model = "sonnet"
`)
	l := &Loader{RepoDir: repo, LibraryDir: lib}

	p, err := l.Load("lean")
	testutil.NoError(t, err)
	// in-repo file wins...
	testutil.Equal(t, p.Archetype["code_slice"].Model, "opus")
	// ...and the source is reported as in-repo.
	testutil.Equal(t, p.Source, SourceInRepo)
}

func TestLoad_SourceReportedLibrary(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "default", `
[archetype.docs]
model = "haiku"
`)
	l := &Loader{RepoDir: t.TempDir(), LibraryDir: lib} // empty repo dir
	p, err := l.Load("default")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Source, SourceLibrary)
}

func TestLoad_NotFound(t *testing.T) {
	l := &Loader{LibraryDir: t.TempDir()}
	_, err := l.Load("missing")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "missing")
}

func TestLoad_PerArchetypeFieldsParse(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[archetype.brainstorm]
model  = "opus"
effort = "high"
window = "1m"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	a := p.Archetype["brainstorm"]
	testutil.Equal(t, a.Model, "opus")
	testutil.Equal(t, a.Effort, "high")
	testutil.Equal(t, a.Window, "1m")
}

func TestLoad_MenuParses(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	a := p.Archetype["code_slice"]
	testutil.Equal(t, a.Model, "")
	testutil.Equal(t, a.Effort, "")
	testutil.Equal(t, len(a.Menu), 2)
	testutil.Equal(t, a.Menu[0].Model, "sonnet")
	testutil.Equal(t, a.Menu[0].Effort, "high")
	testutil.Equal(t, a.Menu[1].Model, "opus")
	testutil.Equal(t, a.Menu[1].Effort, "low")
}

func TestLoad_ScalarArchetypeUnchanged(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[archetype.docs]
model  = "haiku"
effort = "low"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	a := p.Archetype["docs"]
	testutil.Equal(t, a.Model, "haiku")
	testutil.Equal(t, a.Effort, "low")
	testutil.Equal(t, len(a.Menu), 0)
}

func TestLoad_RigorParse(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[rigor]
review_passes       = 2
gating              = true
security_spot_check = true
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Rigor.ReviewPasses, 2)
	testutil.True(t, p.Rigor.Gating)
	testutil.True(t, p.Rigor.SecuritySpotCheck)
}

func TestLoad_ExtendsOverlay_ChildOverridesOnlySetFields(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "default", `
[archetype.code_slice]
model = "sonnet"

[archetype.ci_loop]
model = "haiku"

[rigor]
review_passes = 1
gating        = false
`)
	writeProfile(t, lib, "lean", `
extends = "default"

[archetype.code_slice]
model = "opus"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("lean")
	testutil.NoError(t, err)
	// override applied
	testutil.Equal(t, p.Archetype["code_slice"].Model, "opus")
	// inherited unchanged
	testutil.Equal(t, p.Archetype["ci_loop"].Model, "haiku")
	testutil.Equal(t, p.Rigor.ReviewPasses, 1)
	// leaf name + source preserved through resolution
	testutil.Equal(t, p.Name, "lean")
}

func TestLoad_ExtendsOverlay_Recursive(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "base", `
[archetype.code_slice]
model = "sonnet"

[archetype.ci_loop]
model = "haiku"
`)
	writeProfile(t, lib, "mid", `
extends = "base"

[archetype.code_slice]
model = "opus"
`)
	writeProfile(t, lib, "leaf", `
extends = "mid"

[archetype.bug_fix]
model = "haiku"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("leaf")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Archetype["code_slice"].Model, "opus") // from mid
	testutil.Equal(t, p.Archetype["ci_loop"].Model, "haiku")   // from base
	testutil.Equal(t, p.Archetype["bug_fix"].Model, "haiku")   // from leaf
}

func TestLoad_PartialArchetypeOverlay(t *testing.T) {
	// Child overrides only one field of an archetype the parent fully specifies;
	// the parent's other fields survive.
	lib := t.TempDir()
	writeProfile(t, lib, "default", `
[archetype.brainstorm]
model  = "opus"
effort = "high"
window = "1m"
`)
	writeProfile(t, lib, "child", `
extends = "default"

[archetype.brainstorm]
effort = "low"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("child")
	testutil.NoError(t, err)
	a := p.Archetype["brainstorm"]
	testutil.Equal(t, a.Model, "opus") // inherited
	testutil.Equal(t, a.Effort, "low") // overridden
	testutil.Equal(t, a.Window, "1m")  // inherited
}

func TestLoad_ExtendsOverlay_ChildOverridesMenu(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "default", `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
`)
	writeProfile(t, lib, "child", `
extends = "default"

[archetype.code_slice]
menu = [
  { model = "haiku",  effort = "medium" },
  { model = "sonnet", effort = "low" },
]
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("child")
	testutil.NoError(t, err)
	a := p.Archetype["code_slice"]
	testutil.Equal(t, len(a.Menu), 2)
	testutil.Equal(t, a.Menu[0].Model, "haiku")
	testutil.Equal(t, a.Menu[0].Effort, "medium")
	testutil.Equal(t, a.Menu[1].Model, "sonnet")
	testutil.Equal(t, a.Menu[1].Effort, "low")
}

func TestLoad_ExtendsOverlay_ChildSwitchesMenuToScalar(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "default", `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
`)
	writeProfile(t, lib, "child", `
extends = "default"

[archetype.code_slice]
model = "haiku"
effort = "medium"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("child")
	testutil.NoError(t, err)
	a := p.Archetype["code_slice"]
	testutil.Equal(t, a.Model, "haiku")
	testutil.Equal(t, a.Effort, "medium")
	testutil.Equal(t, len(a.Menu), 0)
}

func TestLoad_ExtendsOverlay_ChildSwitchesScalarToMenu(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "default", `
[archetype.code_slice]
model = "sonnet"
effort = "high"
`)
	writeProfile(t, lib, "child", `
extends = "default"

[archetype.code_slice]
menu = [
  { model = "haiku",  effort = "medium" },
  { model = "sonnet", effort = "low" },
]
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("child")
	testutil.NoError(t, err)
	a := p.Archetype["code_slice"]
	testutil.Equal(t, a.Model, "")
	testutil.Equal(t, a.Effort, "")
	testutil.Equal(t, len(a.Menu), 2)
	testutil.Equal(t, a.Menu[0].Model, "haiku")
	testutil.Equal(t, a.Menu[1].Model, "sonnet")
}

func TestLoad_ExtendsCycle(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "a", `extends = "b"`)
	writeProfile(t, lib, "b", `extends = "a"`)
	l := &Loader{LibraryDir: lib}
	_, err := l.Load("a")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "cycle")
}

func TestLoad_SelfCycle(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "self", `extends = "self"`)
	l := &Loader{LibraryDir: lib}
	_, err := l.Load("self")
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "cycle")
}

func TestLoad_PanelRetained(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[panel]
reviewers = ["opus", "gpt-5"]
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	testutil.True(t, p.PanelPresent)
	// retained verbatim, available to consumers
	revs, ok := p.Panel["reviewers"].([]any)
	testutil.True(t, ok)
	testutil.Equal(t, len(revs), 2)
}

func TestLoad_PanelAbsent(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "p", `
[archetype.docs]
model = "haiku"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.Load("p")
	testutil.NoError(t, err)
	testutil.False(t, p.PanelPresent)
}

func TestResolveProject_EmptyTargetsDefault(t *testing.T) {
	lib := writeProfile(t, t.TempDir(), "default", `
[archetype.docs]
model = "haiku"
`)
	l := &Loader{LibraryDir: lib}
	p, err := l.ResolveProject("")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Name, "default")
}

func TestResolveProject_NamedBinding(t *testing.T) {
	lib := t.TempDir()
	writeProfile(t, lib, "default", `[archetype.docs]
model = "haiku"`)
	writeProfile(t, lib, "lean", `extends = "default"`)
	l := &Loader{LibraryDir: lib}
	p, err := l.ResolveProject("lean")
	testutil.NoError(t, err)
	testutil.Equal(t, p.Name, "lean")
	testutil.Equal(t, p.Archetype["docs"].Model, "haiku")
}

// TestDiscover_UnionDedupAndSort covers Loader.Discover: it returns the sorted,
// de-duplicated union of profile names across RepoDir and LibraryDir, stripping
// the ".toml" suffix and skipping non-toml files and missing dirs.
func TestDiscover_UnionDedupAndSort(t *testing.T) {
	repo := t.TempDir()
	lib := t.TempDir()
	writeProfile(t, repo, "lean", "")
	writeProfile(t, repo, "shared", "") // also in lib → de-duped
	writeProfile(t, lib, "shared", "")
	writeProfile(t, lib, "customer_grade", "")
	// A non-toml file is ignored.
	if err := os.WriteFile(filepath.Join(lib, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &Loader{RepoDir: repo, LibraryDir: lib}
	got := l.Discover()
	testutil.DeepEqual(t, got, []string{"customer_grade", "lean", "shared"})
}

// TestDiscover_MissingDirsSkipped: a Loader pointing at non-existent dirs returns
// an empty list, not an error.
func TestDiscover_MissingDirsSkipped(t *testing.T) {
	l := &Loader{RepoDir: filepath.Join(t.TempDir(), "nope"), LibraryDir: ""}
	testutil.Equal(t, len(l.Discover()), 0)
}
