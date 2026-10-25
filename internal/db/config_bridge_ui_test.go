package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestBridgeUISettingsOverlay(t *testing.T) {
	for _, key := range []string{"band", "toasts"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			d, err := Open(filepath.Join(dir, "data.sql"))
			testutil.NoError(t, err)
			t.Cleanup(func() { _ = d.Close() })
			value := func() bool {
				if key == "band" {
					return d.Config().Bridge.Band
				}
				return d.Config().Bridge.Toasts
			}
			testutil.True(t, value())
			testutil.NoError(t, d.SetConfigValue("bridge."+key, "false"))
			testutil.False(t, value())
			testutil.NoError(t, d.SetConfigValue("bridge."+key, "true"))
			testutil.True(t, value())
			testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte("[bridge]\n"+key+" = false\n"), 0600))
			testutil.False(t, value())
		})
	}
}
