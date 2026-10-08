package sessiontag

import (
	"os"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestNew_OwnedByCurrentProcessAndUnique(t *testing.T) {
	a, b := New(), New()
	testutil.NotEqual(t, a, b)
	pid, ok := OwnerPID(a)
	testutil.True(t, ok)
	testutil.Equal(t, pid, os.Getpid())
}

func TestOwnerPID(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		pid  int
		ok   bool
	}{
		{"valid", "123-abcd", 123, true},
		{"no dash", "123abcd", 0, false},
		{"non numeric", "x-abcd", 0, false},
		{"zero", "0-abcd", 0, false},
		{"negative", "-5-abcd", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, ok := OwnerPID(tt.tag)
			testutil.Equal(t, pid, tt.pid)
			testutil.Equal(t, ok, tt.ok)
		})
	}
}

func TestStripEnv(t *testing.T) {
	got := StripEnv([]string{"A=1", EnvKey + "=9-x", "ARGUS_SESSION_TAGGED=keep", "B=2"})
	testutil.DeepEqual(t, got, []string{"A=1", "ARGUS_SESSION_TAGGED=keep", "B=2"})
}

func TestWithTag(t *testing.T) {
	t.Run("replaces existing", func(t *testing.T) {
		got := WithTag([]string{"A=1", EnvKey + "=old"}, "new")
		testutil.DeepEqual(t, got, []string{"A=1", EnvKey + "=new"})
	})
	t.Run("nil env inherits os.Environ", func(t *testing.T) {
		t.Setenv("SESSIONTAG_PROBE", "yes")
		got := WithTag(nil, "t")
		joined := strings.Join(got, "\n")
		testutil.Contains(t, joined, "SESSIONTAG_PROBE=yes")
		testutil.Equal(t, got[len(got)-1], EnvKey+"=t")
	})
}
