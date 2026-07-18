package config

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestProject_ProfileParsesFromTOML pins the config-management delta: a project
// entry declaring `profile = "..."` exposes that profile name on the loaded
// project.
func TestProject_ProfileParsesFromTOML(t *testing.T) {
	path := writeFile(t, `
[projects.acme]
path = "/tmp/acme"
profile = "customer_grade"
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	testutil.Equal(t, got.Projects["acme"].Profile, "customer_grade")
	testutil.Equal(t, got.Projects["acme"].Path, "/tmp/acme")
}

// TestProject_ResolveProfileName covers the empty→default binding rule: an
// explicit profile is returned verbatim, an absent one resolves to "default".
func TestProject_ResolveProfileName(t *testing.T) {
	t.Run("explicit profile returned verbatim", func(t *testing.T) {
		p := Project{Profile: "lean"}
		testutil.Equal(t, p.ResolveProfileName(), "lean")
	})

	t.Run("absent profile resolves to default", func(t *testing.T) {
		p := Project{}
		testutil.Equal(t, p.ResolveProfileName(), "default")
	})
}

// TestProject_AbsentProfileParsesEmpty pins that a project entry omitting
// `profile` loads with an empty binding (which ResolveProfileName then treats as
// the default profile).
func TestProject_AbsentProfileParsesEmpty(t *testing.T) {
	path := writeFile(t, `
[projects.acme]
path = "/tmp/acme"
`)
	l := NewFileLoader(path)

	got := l.Apply(DefaultConfig())
	testutil.NoError(t, l.Err())

	testutil.Equal(t, got.Projects["acme"].Profile, "")
	testutil.Equal(t, got.Projects["acme"].ResolveProfileName(), "default")
}
