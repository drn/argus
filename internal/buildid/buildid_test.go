package buildid

import (
	"runtime/debug"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// fromSettings is the pure extraction that all identity reporting depends on:
// it must pull vcs.revision into Revision and map vcs.modified=="true" to a
// dirty flag, leaving both zero-valued when the settings are absent.
func TestFromSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     VCS
	}{
		{
			name: "revision and dirty",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
				{Key: "vcs.modified", Value: "true"},
			},
			want: VCS{Revision: "a1b2c3d4e5f6", Modified: true},
		},
		{
			name: "revision and clean",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
				{Key: "vcs.modified", Value: "false"},
			},
			want: VCS{Revision: "a1b2c3d4e5f6", Modified: false},
		},
		{
			name:     "no vcs info",
			settings: []debug.BuildSetting{{Key: "GOOS", Value: "darwin"}},
			want:     VCS{},
		},
		{
			name:     "nil settings",
			settings: nil,
			want:     VCS{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, fromSettings(tt.settings), tt.want)
		})
	}
}

// Present reports whether the binary carries any VCS revision — the display
// layer uses it to choose the SHA vs. content-hash fallback.
func TestVCSPresent(t *testing.T) {
	testutil.Equal(t, VCS{Revision: "abc"}.Present(), true)
	testutil.Equal(t, VCS{}.Present(), false)
	testutil.Equal(t, VCS{Modified: true}.Present(), false)
}

// Current reads through the injectable readBuildInfo seam so the whole
// ReadBuildInfo→VCS path is exercised without depending on how the test binary
// itself was built.
func TestCurrent(t *testing.T) {
	orig := readBuildInfo
	t.Cleanup(func() { readBuildInfo = orig })

	t.Run("build info present", func(t *testing.T) {
		readBuildInfo = func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "deadbeef"},
				{Key: "vcs.modified", Value: "true"},
			}}, true
		}
		testutil.Equal(t, Current(), VCS{Revision: "deadbeef", Modified: true})
	})

	t.Run("build info absent", func(t *testing.T) {
		readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
		testutil.Equal(t, Current(), VCS{})
	})
}
