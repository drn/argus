package client

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/testutil"
)

func TestCliUsage(t *testing.T) {
	fd := newFakeDaemon(t)
	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	fd.mu.Lock()
	fd.usage = map[string]accountusage.Usage{"default": {FiveHour: &accountusage.Reading{Pct: 100, ResetsAt: reset}}}
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	got, err := c.AccountUsage()
	testutil.NoError(t, err)
	testutil.Equal(t, got["default"].FiveHour.Pct, 100.0)
	testutil.True(t, got["default"].FiveHour.ResetsAt.Equal(reset))
	testutil.Equal(t, c.AccountUsageSource()()["default"].FiveHour.Pct, 100.0)
}

func TestCliUsageOld(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.usageMissing = true
	fd.mu.Unlock()
	c := fakeClient(t, fd)

	_, err := c.AccountUsage()
	testutil.Error(t, err)
	testutil.Nil(t, c.AccountUsageSource()())
}
