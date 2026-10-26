package mcplimits

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestClientTimeoutExceedsInboxCap(t *testing.T) {
	t.Parallel()
	testutil.Equal(t, HeraInboxTimeoutSeconds, 120)
	testutil.Equal(t, ClientTimeoutSeconds > HeraInboxTimeoutSeconds, true)
	testutil.Equal(t, ClientTimeoutMilliseconds, ClientTimeoutSeconds*1000)
}
