package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/apistore"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestBridgeUISettingsRemoteRoundTrip(t *testing.T) {
	for _, key := range []string{"bridge.band", "bridge.toasts"} {
		t.Run(key, func(t *testing.T) {
			s, _, _ := bridgeAuthorityServer(t)
			server := httptest.NewServer(s.routes())
			t.Cleanup(server.Close)
			store := apistore.New(apiclient.New(server.URL, "test-token"))
			_, err := store.RefreshConfig(context.Background())
			testutil.NoError(t, err)
			testutil.True(t, store.Config().Bridge.Band)
			testutil.True(t, store.Config().Bridge.Toasts)
			for _, value := range []string{"false", "true"} {
				testutil.NoError(t, store.SetConfigValue(key, value))
				want := s.db.Config().Bridge
				testutil.Equal(t, store.Config().Bridge, want)
				if key == "bridge.band" {
					testutil.Equal(t, want.Band, value == "true")
					testutil.True(t, want.Toasts)
				} else {
					testutil.Equal(t, want.Toasts, value == "true")
					testutil.True(t, want.Band)
				}
				testutil.True(t, want.Enabled)
				testutil.True(t, want.Delivery)
			}
		})
	}
}

func TestBridgeUISettingsRemoteEffectiveOverlay(t *testing.T) {
	s, _, _ := bridgeAuthorityServer(t)
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	s.db = d
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte("[bridge]\nband = false\ntoasts = false\n"), 0600))
	server := httptest.NewServer(s.routes())
	t.Cleanup(server.Close)
	store := apistore.New(apiclient.New(server.URL, "test-token"))
	for _, key := range []string{"bridge.band", "bridge.toasts"} {
		testutil.NoError(t, store.SetConfigValue(key, "true"))
		testutil.False(t, store.Config().Bridge.Band)
		testutil.False(t, store.Config().Bridge.Toasts)
	}
}
