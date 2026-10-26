package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestInjectMCPTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entry   string
		enabled bool
	}{
		{"new", ``, true},
		{"missing disabled", `"argus":{"type":"remote","url":"http://localhost:7742/mcp","enabled":false}`, false},
		{"stale enabled", `"argus":{"type":"remote","url":"http://localhost:7742/mcp","enabled":true,"timeout":60000}`, true},
		{"current disabled", `"argus":{"type":"remote","url":"http://localhost:7742/mcp","enabled":false,"timeout":150000}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.json")
			testutil.NoError(t, os.WriteFile(path, []byte(`{"other":42,"mcp":{`+tc.entry+`}}`), 0o600))
			testutil.NoError(t, injectOpencodeJSON(path, 7742, ""))
			entry := readMCP(t, path)
			testutil.Equal(t, entry["timeout"], any(float64(150000)))
			testutil.Equal(t, entry["enabled"], any(tc.enabled))
			raw, err := os.ReadFile(path)
			testutil.NoError(t, err)
			var config map[string]any
			testutil.NoError(t, json.Unmarshal(raw, &config))
			testutil.Equal(t, config["other"], any(float64(42)))
			// A backdated mtime detects an unnecessary rewrite without sleeping.
			old := time.Unix(1000000000, 0)
			testutil.NoError(t, os.Chtimes(path, old, old))
			testutil.NoError(t, injectOpencodeJSON(path, 7742, ""))
			info, err := os.Stat(path)
			testutil.NoError(t, err)
			testutil.Equal(t, info.ModTime().Unix(), old.Unix())
		})
	}
}
