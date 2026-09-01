package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// repoRoot resolves the repository root from this package's directory. Go runs
// each test with its own package dir as the working directory, so internal/daemon
// is always exactly two levels down.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	testutil.NoError(t, err)
	// Sanity-anchor on a file that can only exist at the root, so a future
	// package move fails loudly here instead of silently digesting nothing.
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

// TestSupervisorSurfaceDigest is the mechanical drift guard (design D3): a change
// to any declared supervisor-resident path fails here until the author explicitly
// decides whether the supervisor's observable behavior changed.
//
// `go test ./...` is a CI step, so this is a real PR gate — the point is that
// touching supervisor-resident code cannot be silent.
func TestSupervisorSurfaceDigest(t *testing.T) {
	root := repoRoot(t)

	for _, tc := range []struct {
		name      string
		paths     []string
		recorded  string
		component string
		version   int
	}{
		{"spawn", SupervisorSpawnPaths, SpawnSurfaceDigest, "SupervisorSpawnSurface", SupervisorSpawnSurface},
		{"stream", SupervisorStreamPaths, StreamSurfaceDigest, "SupervisorStreamSurface", SupervisorStreamSurface},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SurfaceDigest(root, tc.paths)
			testutil.NoError(t, err)
			if got == tc.recorded {
				return
			}
			t.Fatalf(`the declared %s surface changed (%s is currently %d).

  recorded: %s
  computed: %s

A file in Supervisor%sPaths was modified. Decide, explicitly:

  * The supervisor's observable behavior CHANGED → bump %s in
    internal/daemon/surface.go (add a history line), AND record the new digest below.
  * It did NOT change (comment, rename, pure refactor) → record the new digest only.

Either way, set:

    %sSurfaceDigest = %q

When in doubt, bump STREAM. An unnecessary bump costs one restart prompt; a missed
one silently reports a genuinely-changed supervisor as coherent.`,
				tc.name, tc.component, tc.version,
				tc.recorded, got,
				capitalize(tc.name), tc.component,
				capitalize(tc.name), got)
		})
	}
}

// capitalize upper-cases the first byte of an ASCII identifier fragment, so the
// guard's failure message can name SpawnSurfaceDigest / StreamSurfaceDigest.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// TestSupervisorSurfacePathsWellFormed pins the manifests themselves: every
// declared path must exist, the two sets must be disjoint (a file is spawn-side
// or stream-side, never both — the tiering would be meaningless otherwise), and
// neither may be empty (an empty manifest would digest to a constant and silently
// guard nothing).
func TestSupervisorSurfacePathsWellFormed(t *testing.T) {
	root := repoRoot(t)

	if len(SupervisorSpawnPaths) == 0 {
		t.Error("SupervisorSpawnPaths is empty — the digest would guard nothing")
	}
	if len(SupervisorStreamPaths) == 0 {
		t.Error("SupervisorStreamPaths is empty — the digest would guard nothing")
	}

	seen := map[string]string{}
	for _, tc := range []struct {
		set   string
		paths []string
	}{
		{"spawn", SupervisorSpawnPaths},
		{"stream", SupervisorStreamPaths},
	} {
		for _, p := range tc.paths {
			if prev, dup := seen[p]; dup {
				t.Errorf("%s declared in both the %s and %s manifests", p, prev, tc.set)
				continue
			}
			seen[p] = tc.set
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				t.Errorf("declared %s-surface path %s does not exist: %v", tc.set, p, err)
			}
		}
	}
}

// TestSurfaceDigestSensitivity proves the digest actually reacts to content and
// to path identity — a digest that ignored either would pass CI forever.
func TestSurfaceDigestSensitivity(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	write("a.go", "package a")
	write("b.go", "package b")

	base, err := SurfaceDigest(dir, []string{"a.go", "b.go"})
	testutil.NoError(t, err)

	t.Run("order independent", func(t *testing.T) {
		got, err := SurfaceDigest(dir, []string{"b.go", "a.go"})
		testutil.NoError(t, err)
		testutil.Equal(t, got, base)
	})

	t.Run("content change alters the digest", func(t *testing.T) {
		write("a.go", "package a // touched")
		got, err := SurfaceDigest(dir, []string{"a.go", "b.go"})
		testutil.NoError(t, err)
		if got == base {
			t.Fatal("digest unchanged after editing a declared file")
		}
		write("a.go", "package a")
	})

	t.Run("moving content between declared files alters the digest", func(t *testing.T) {
		write("a.go", "package b")
		write("b.go", "package a")
		got, err := SurfaceDigest(dir, []string{"a.go", "b.go"})
		testutil.NoError(t, err)
		if got == base {
			t.Fatal("digest unchanged after swapping content between two declared files")
		}
	})

	t.Run("missing file is an error, never a silent pass", func(t *testing.T) {
		_, err := SurfaceDigest(dir, []string{"nope.go"})
		if err == nil {
			t.Fatal("expected an error for a declared path that does not exist")
		}
		testutil.Contains(t, err.Error(), "nope.go")
	})
}

func TestCurrentSupervisorSurface(t *testing.T) {
	cur := CurrentSupervisorSurface()
	testutil.Equal(t, cur.Spawn, SupervisorSpawnSurface)
	testutil.Equal(t, cur.Stream, SupervisorStreamSurface)
	if !cur.Known() {
		t.Fatal("this build's own surface version must always be Known()")
	}
	testutil.Contains(t, cur.String(), "spawn=")
	testutil.Contains(t, cur.String(), "stream=")
}

func TestSurfaceVersionKnown(t *testing.T) {
	tests := []struct {
		name string
		v    SurfaceVersion
		want bool
	}{
		{"zero value is a pre-v6 supervisor", SurfaceVersion{}, false},
		{"spawn only", SurfaceVersion{Spawn: 1}, true},
		{"stream only", SurfaceVersion{Stream: 1}, true},
		{"both", SurfaceVersion{Spawn: 2, Stream: 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, tt.v.Known(), tt.want)
		})
	}
	testutil.Equal(t, SurfaceVersion{}.String(), "unknown")
}

func TestCompareSupervisorSurface(t *testing.T) {
	cur := CurrentSupervisorSurface()

	tests := []struct {
		name     string
		reported SurfaceVersion
		want     SurfaceSkew
	}{
		{
			"identical surfaces are coherent whatever the binary hashes say",
			cur, SurfaceCoherent,
		},
		{
			"an unreported surface is unknown, never stale",
			SurfaceVersion{}, SurfaceUnknown,
		},
		{
			"spawn behind, stream matching ⇒ new sessions only",
			SurfaceVersion{Spawn: cur.Spawn - 1, Stream: cur.Stream}, SurfaceSpawnStale,
		},
		{
			"stream behind ⇒ live sessions affected",
			SurfaceVersion{Spawn: cur.Spawn, Stream: cur.Stream + 1}, SurfaceStreamStale,
		},
		{
			"both behind ⇒ stream outranks spawn",
			SurfaceVersion{Spawn: cur.Spawn + 7, Stream: cur.Stream + 7}, SurfaceStreamStale,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, CompareSupervisorSurface(tt.reported), tt.want)
		})
	}
}

func TestSurfaceSkewStaleAndText(t *testing.T) {
	tests := []struct {
		name  string
		skew  SurfaceSkew
		stale bool
		label string
	}{
		{"coherent", SurfaceCoherent, false, "coherent"},
		{"unknown is deliberately not stale", SurfaceUnknown, false, "unknown"},
		{"spawn", SurfaceSpawnStale, true, "spawn-stale"},
		{"stream", SurfaceStreamStale, true, "stream-stale"},
		{"legacy (pre-v6 with a differing hash)", SurfaceLegacyStale, true, "legacy-stale"},
		{"out of range", SurfaceSkew(99), false, "surfaceskew(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, tt.skew.Stale(), tt.stale)
			testutil.Equal(t, tt.skew.String(), tt.label)
			if tt.skew.Consequence() == "" {
				t.Error("every verdict must state its consequence")
			}
		})
	}
	// The two tiers must say materially different things — that separation is
	// the entire reason the surface version has two components.
	if SurfaceSpawnStale.Consequence() == SurfaceStreamStale.Consequence() {
		t.Fatal("spawn and stream consequences must differ")
	}
	testutil.Contains(t, SurfaceSpawnStale.Consequence(), "unaffected")
	testutil.Contains(t, SurfaceStreamStale.Consequence(), "live sessions are affected")
}

// TestSurfaceSkewHeadline pins the short form used where the full sentence will
// not fit (the skew modal's 72-column body): still distinct per tier, and short
// enough to render without truncation.
func TestSurfaceSkewHeadline(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []SurfaceSkew{SurfaceCoherent, SurfaceUnknown, SurfaceSpawnStale, SurfaceStreamStale, SurfaceLegacyStale} {
		h := s.Headline()
		if h == "" {
			t.Errorf("%s has no headline", s)
		}
		if len(h) > 68 {
			t.Errorf("%s headline is %d chars; the modal body is 68 columns: %q", s, len(h), h)
		}
		if seen[h] {
			t.Errorf("%s reuses another tier's headline %q — the tiers must read differently", s, h)
		}
		seen[h] = true
	}
	testutil.Contains(t, SurfaceSpawnStale.Headline(), "unaffected")
	testutil.Contains(t, SurfaceStreamStale.Headline(), "live sessions")
}

// TestSurfaceSkewAffectsLiveSessions pins which tiers justify interrupting
// agents — the single fact the two-component split exists to preserve.
func TestSurfaceSkewAffectsLiveSessions(t *testing.T) {
	tests := []struct {
		skew SurfaceSkew
		want bool
	}{
		{SurfaceCoherent, false},
		{SurfaceUnknown, false},
		{SurfaceSpawnStale, false}, // the whole point: spawn never warrants a bounce
		{SurfaceStreamStale, true},
		{SurfaceLegacyStale, true}, // tier unknowable ⇒ assume the stricter one
	}
	for _, tt := range tests {
		t.Run(tt.skew.String(), func(t *testing.T) {
			testutil.Equal(t, tt.skew.AffectsLiveSessions(), tt.want)
		})
	}
}
