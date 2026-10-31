package daemon

import (
	"testing"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/testutil"
)

func TestAccountUsageRPC(t *testing.T) {
	t.Run("no API server reports nothing", func(t *testing.T) {
		d, _ := testDaemon(t)
		var resp map[string]accountusage.Usage
		testutil.NoError(t, rpcFor(d).AccountUsage(&Empty{}, &resp))
		testutil.NotNil(t, resp)
		testutil.Equal(t, len(resp), 0)
	})
	t.Run("relays the API server ledger", func(t *testing.T) {
		d, _ := testDaemon(t)
		d.apiServer = api.New(d.db, d.runner, "tok", nil, nil)
		var resp map[string]accountusage.Usage
		testutil.NoError(t, rpcFor(d).AccountUsage(&Empty{}, &resp))
		testutil.NotNil(t, resp)
		testutil.Equal(t, len(resp), 0)
	})
}
