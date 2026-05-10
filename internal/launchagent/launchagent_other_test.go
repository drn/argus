//go:build !darwin

package launchagent

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// On non-darwin every operation is a no-op stub. The contract is
// (1) Available() reports false, (2) every operation either returns
// ErrUnsupported or a sentinel zero value, (3) nothing panics.

func TestAvailable_FalseOnNonDarwin(t *testing.T) {
	testutil.False(t, Available())
}

func TestPlistPath_Unsupported(t *testing.T) {
	got, err := PlistPath()
	testutil.Equal(t, got, "")
	testutil.True(t, errors.Is(err, ErrUnsupported))
}

func TestCurrentStatus_HasReason(t *testing.T) {
	st := CurrentStatus()
	testutil.False(t, st.Installed)
	testutil.False(t, st.Loaded)
	testutil.Equal(t, st.PlistPath, "")
	if st.Reason == "" {
		t.Error("expected non-empty Reason explaining the platform restriction")
	}
}

func TestInstall_Unsupported(t *testing.T) {
	testutil.True(t, errors.Is(Install("/some/path"), ErrUnsupported))
}

func TestUninstall_Unsupported(t *testing.T) {
	testutil.True(t, errors.Is(Uninstall(), ErrUnsupported))
}

func TestEnsureDaemonSymlink_Passthrough(t *testing.T) {
	for _, in := range []string{"", "/usr/local/bin/argus", "relative/path"} {
		testutil.Equal(t, EnsureDaemonSymlink(in), in)
	}
}

func TestResolveDaemonExe_Unsupported(t *testing.T) {
	got, err := ResolveDaemonExe()
	testutil.Equal(t, got, "")
	testutil.True(t, errors.Is(err, ErrUnsupported))
}
