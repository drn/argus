package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestConfig_ProjectFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sql")
	d, err := Open(path)
	testutil.NoError(t, err)
	testutil.Equal(t, d.Config().UI.ProjectFirst, false)
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			testutil.NoError(t, d.SetConfigValue("ui.project_first", value))
			testutil.NoError(t, d.Close())
			d, err = Open(path)
			testutil.NoError(t, err)
			testutil.Equal(t, d.Config().UI.ProjectFirst, value == "true")
		})
	}
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetConfigValue("ui.project_first", "true"))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte("[ui]\nproject_first = false\n"), 0o600))
	testutil.Equal(t, d.Config().UI.ProjectFirst, false)
}
