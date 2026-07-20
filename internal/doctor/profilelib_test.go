package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDiagnoseProfileLibrary(t *testing.T) {
	tests := []struct {
		name       string
		validNames []string
		dirMissing bool
		listErr    error
		want       ProfileLibraryStatus
	}{
		{
			name:       "found: at least one valid profile",
			validNames: []string{"default"},
			want:       ProfileLibraryFound,
		},
		{
			name:       "found: several valid profiles",
			validNames: []string{"default", "lean"},
			want:       ProfileLibraryFound,
		},
		{
			name:       "none: directory missing",
			dirMissing: true,
			listErr:    errors.New("open ~/.argus/profiles: no such file or directory"),
			want:       ProfileLibraryNone,
		},
		{
			name: "none: directory exists but empty (no valid names)",
			want: ProfileLibraryNone,
		},
		{
			name:    "unknown: listing failed for a reason other than nonexistence",
			listErr: errors.New("open ~/.argus/profiles: permission denied"),
			want:    ProfileLibraryUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiagnoseProfileLibrary(tt.validNames, tt.dirMissing, tt.listErr)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestRenderProfileLibrary(t *testing.T) {
	tests := []struct {
		name         string
		status       ProfileLibraryStatus
		wantContains []string
	}{
		{
			name:         "found",
			status:       ProfileLibraryFound,
			wantContains: []string{"FOUND"},
		},
		{
			name:         "none found includes the remediation command",
			status:       ProfileLibraryNone,
			wantContains: []string{"NONE FOUND", "argus profiles install-defaults"},
		},
		{
			name:         "unknown names the library directory",
			status:       ProfileLibraryUnknown,
			wantContains: []string{"UNKNOWN", "~/.argus/profiles/"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderProfileLibrary(tt.status)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderProfileLibrary(%v) = %q, want substring %q", tt.status, got, want)
				}
			}
		})
	}
}
