package claudeaccount

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func stub(t *testing.T, fn func(ctx context.Context, dir string) ([]byte, error)) {
	t.Helper()
	old := runAuthStatus
	runAuthStatus = fn
	ResetCache()
	t.Cleanup(func() { runAuthStatus = old; ResetCache() })
}

func TestAuthStatus(t *testing.T) {
	t.Run("logged in", func(t *testing.T) {
		var gotDir string
		stub(t, func(_ context.Context, dir string) ([]byte, error) {
			gotDir = dir
			return []byte(`{"loggedIn":true,"email":"a@b.c","orgName":"Org","subscriptionType":"team"}`), nil
		})
		st, err := AuthStatus(context.Background(), "/x")
		testutil.NoError(t, err)
		testutil.Equal(t, gotDir, "/x")
		testutil.DeepEqual(t, st, Status{LoggedIn: true, Email: "a@b.c", Org: "Org", Plan: "team"})
	})
	t.Run("logged out with nonzero exit", func(t *testing.T) {
		stub(t, func(context.Context, string) ([]byte, error) {
			return []byte(`{"loggedIn":false}`), errors.New("exit 1")
		})
		st, err := AuthStatus(context.Background(), "/x")
		testutil.NoError(t, err)
		testutil.False(t, st.LoggedIn)
	})
	t.Run("exec error", func(t *testing.T) {
		stub(t, func(context.Context, string) ([]byte, error) { return nil, errors.New("boom") })
		_, err := AuthStatus(context.Background(), "/x")
		testutil.Error(t, err)
	})
	t.Run("bad json", func(t *testing.T) {
		stub(t, func(context.Context, string) ([]byte, error) { return []byte("nope"), nil })
		_, err := AuthStatus(context.Background(), "/x")
		testutil.Error(t, err)
	})
}

func TestCachedAuthStatus(t *testing.T) {
	calls := 0
	stub(t, func(context.Context, string) ([]byte, error) {
		calls++
		return []byte(`{"loggedIn":true,"email":"a@b.c"}`), nil
	})
	now := time.Now()
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	_, err := CachedAuthStatus(context.Background(), "/x")
	testutil.NoError(t, err)
	_, err = CachedAuthStatus(context.Background(), "/x")
	testutil.NoError(t, err)
	testutil.Equal(t, calls, 1)

	now = now.Add(CacheTTL + time.Second)
	_, _ = CachedAuthStatus(context.Background(), "/x")
	testutil.Equal(t, calls, 2)

	_, _ = CachedAuthStatus(context.Background(), "/y")
	testutil.Equal(t, calls, 3)
}

func TestCachedAuthStatus_ErrorNotCached(t *testing.T) {
	calls := 0
	stub(t, func(context.Context, string) ([]byte, error) { calls++; return nil, errors.New("x") })
	_, err := CachedAuthStatus(context.Background(), "/x")
	testutil.Error(t, err)
	_, _ = CachedAuthStatus(context.Background(), "/x")
	testutil.Equal(t, calls, 2)
}

func TestProbeEnv(t *testing.T) {
	environ := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/ambient", "ANTHROPIC_API_KEY=k", "CLAUDE_CODE_OAUTH_TOKEN=t"}
	t.Run("default dir unsets CLAUDE_CONFIG_DIR and keeps auth env", func(t *testing.T) {
		testutil.DeepEqual(t, probeEnv(environ, "/home/u/.claude", "/home/u/.claude"),
			[]string{"PATH=/bin", "ANTHROPIC_API_KEY=k", "CLAUDE_CODE_OAUTH_TOKEN=t"})
	})
	t.Run("explicit dir sets it and strips auth overrides", func(t *testing.T) {
		testutil.DeepEqual(t, probeEnv(environ, "/home/u/.claude-personal", "/home/u/.claude"),
			[]string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/home/u/.claude-personal"})
	})
}
