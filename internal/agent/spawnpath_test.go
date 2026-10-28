package agent

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestEnsureLocalBinOnPath(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		home string
		want []string
	}{
		{"missing dir appended", []string{"A=1", "PATH=/usr/bin:/bin"}, "/h", []string{"A=1", "PATH=/usr/bin:/bin:/h/.local/bin"}},
		{"already present untouched", []string{"PATH=/h/.local/bin:/usr/bin"}, "/h", []string{"PATH=/h/.local/bin:/usr/bin"}},
		{"present late untouched", []string{"PATH=/usr/bin:/h/.local/bin"}, "/h", []string{"PATH=/usr/bin:/h/.local/bin"}},
		{"empty PATH", []string{"PATH="}, "/h", []string{"PATH=/h/.local/bin"}},
		{"no PATH entry", []string{"A=1"}, "/h", []string{"A=1", "PATH=/h/.local/bin:" + defaultSpawnPath}},
		{"empty home no-op", []string{"PATH=/usr/bin"}, "", []string{"PATH=/usr/bin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]string(nil), tt.env...)
			testutil.DeepEqual(t, ensureLocalBinOnPath(in, tt.home), tt.want)
			testutil.DeepEqual(t, in, tt.env)
		})
	}
}
