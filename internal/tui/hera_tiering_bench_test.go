package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/hera"
)

// tierBenchRoles is the role count the benchmark annotates per "build": ~Aaron's
// real roster (active + archived orchestrators) after PR 3's index fix.
const tierBenchRoles = 200

func tierBenchApp(b *testing.B) (*App, []hera.RoleView) {
	b.Helper()
	b.Setenv("HOME", b.TempDir())
	d, err := db.OpenInMemory()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	_ = d.SetConfigValue("defaults.backend", "claude")
	_ = d.SetBackend("claude", config.Backend{Command: "claude"})
	for i := 0; i < 8; i++ {
		_ = d.SetProject(fmt.Sprintf("p%d", i), config.Project{Path: b.TempDir(), Profile: "lean"})
	}
	dir := b.TempDir()
	_ = dir
	app := New(d, agent.NewRunner(nil), false)
	pdir := filepath.Join(db.DataDir(), "profiles")
	_ = os.MkdirAll(pdir, 0o755)
	_ = os.WriteFile(filepath.Join(pdir, "lean.toml"), []byte("[archetype.code_slice]\nmodels = { claude = \"sonnet\" }\neffort = \"high\"\n"), 0o644)
	roles := make([]hera.RoleView, tierBenchRoles)
	for i := range roles {
		roles[i] = hera.RoleView{
			ArgusProject: fmt.Sprintf("p%d", i%8),
			Archetype:    "code_slice",
			WorktreePath: b.TempDir(),
			Kind:         db.HeraKindWorker,
		}
	}
	return app, roles
}

func BenchmarkResolveHeraTier_PerBuild(b *testing.B) {
	app, roles := tierBenchApp(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resolve, _ := app.heraTierResolverForBuild()
		for j := range roles {
			resolve(&roles[j])
		}
	}
}

// BenchmarkConfig_RealCopy times db.Config() against a COPY of the live DB
// (ARGUS_BENCH_DB=<path to a copy>); skipped when unset so CI never touches it.
func BenchmarkConfig_RealCopy(b *testing.B) {
	src := os.Getenv("ARGUS_BENCH_DB")
	if src == "" {
		b.Skip("ARGUS_BENCH_DB not set")
	}
	d, err := db.Open(src)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Config()
	}
}

// BenchmarkResolveHeraTier_PerRoleIndependent is the pre-change cost model: a fresh
// resolver (config snapshot + profile loads) for every role, which is exactly what
// resolveHeraTier did per role before hera-tier-resolve-once.
func BenchmarkResolveHeraTier_PerRoleIndependent(b *testing.B) {
	app, roles := tierBenchApp(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range roles {
			app.resolveHeraTier(&roles[j])
		}
	}
}
