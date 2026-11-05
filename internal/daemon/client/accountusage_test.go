package client

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/backendtier"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
)

func TestCliUsage(t *testing.T) {
	fd := newFakeDaemon(t)
	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	fd.mu.Lock()
	fd.usage = map[string]usagebudget.Reading{"default": {Percentage: 100, FiveHour: &usagebudget.Window{Percentage: 100, ResetAt: reset}}}
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	got, err := c.AccountUsage()
	testutil.NoError(t, err)
	testutil.Equal(t, got["default"].FiveHour.Percentage, 100.0)
	testutil.True(t, got["default"].FiveHour.ResetAt.Equal(reset))
	testutil.Equal(t, c.AccountUsageSource()(config.Config{})["default"].Percentage, 100.0)
}

func TestCliUsageOld(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.usageMissing = true
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	_, err := c.AccountUsage()
	testutil.Error(t, err)
	testutil.Nil(t, c.AccountUsageSource()(config.Config{}))
}

func TestCliRoutingUsage(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.routing = backendtier.Usage{
		Claude: map[string]usagebudget.Reading{"personal": {Percentage: 70}},
		Codex:  &usagebudget.Reading{Percentage: 12},
	}
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	got, err := c.RoutingUsage()
	testutil.NoError(t, err)
	testutil.Equal(t, got.Claude["personal"].Percentage, 70.0)
	testutil.Equal(t, got.Codex.Percentage, 12.0)

	fd.mu.Lock()
	fd.usageMissing = true
	fd.mu.Unlock()
	_, err = c.RoutingUsage()
	testutil.Error(t, err)
}
