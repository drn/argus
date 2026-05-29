package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// settingsResponse is the JSON shape returned by GET /api/settings.
// It mirrors the slice of TUI settings that are meaningful to manage from
// the web (sandbox, KB, defaults, API). TUI-rendering settings (spinner,
// theme, keybindings) are intentionally omitted.
type settingsResponse struct {
	Sandbox  sandboxJSON  `json:"sandbox"`
	KB       kbJSON       `json:"kb"`
	API      apiSettings  `json:"api"`
	Defaults defaultsJSON `json:"defaults"`
}

type sandboxJSON struct {
	Enabled          bool     `json:"enabled"`
	Available        bool     `json:"available"`
	DenyRead         []string `json:"deny_read"`
	ExtraWrite       []string `json:"extra_write"`
	AllowAppleEvents []string `json:"allow_apple_events"`
}

type kbJSON struct {
	Enabled        bool   `json:"enabled"`
	MetisVaultPath string `json:"metis_vault_path"`
}

type apiSettings struct {
	Enabled  bool `json:"enabled"`
	HTTPPort int  `json:"http_port"`
}

type defaultsJSON struct {
	Backend      string `json:"backend"`
	ShareProject string `json:"share_project"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.db.Config()
	writeJSON(w, http.StatusOK, settingsResponse{
		Sandbox: sandboxJSON{
			Enabled:          cfg.Sandbox.Enabled,
			Available:        isSandboxAvailable(),
			DenyRead:         stringsOrEmpty(cfg.Sandbox.DenyRead),
			ExtraWrite:       stringsOrEmpty(cfg.Sandbox.ExtraWrite),
			AllowAppleEvents: stringsOrEmpty(cfg.Sandbox.AllowAppleEvents),
		},
		KB: kbJSON{
			Enabled:        cfg.KB.Enabled,
			MetisVaultPath: cfg.KB.MetisVaultPath,
		},
		API: apiSettings{
			Enabled:  cfg.API.Enabled,
			HTTPPort: cfg.API.HTTPPort,
		},
		Defaults: defaultsJSON{
			Backend:      cfg.Defaults.Backend,
			ShareProject: cfg.Defaults.ShareProject,
		},
	})
}

// updateSettingsReq is the partial-update body for PUT /api/settings.
// Every section is a pointer so callers can update one section at a time
// without needing to round-trip the rest.
type updateSettingsReq struct {
	Sandbox  *sandboxUpdate  `json:"sandbox,omitempty"`
	KB       *kbUpdate       `json:"kb,omitempty"`
	API      *apiUpdate      `json:"api,omitempty"`
	Defaults *defaultsUpdate `json:"defaults,omitempty"`
}

// Each *Update mirrors the corresponding response section but with pointer
// fields so absent keys mean "don't change". Slice fields use a sentinel:
// nil = leave alone, empty slice ([]) = clear.
type sandboxUpdate struct {
	Enabled          *bool     `json:"enabled,omitempty"`
	DenyRead         *[]string `json:"deny_read,omitempty"`
	ExtraWrite       *[]string `json:"extra_write,omitempty"`
	AllowAppleEvents *[]string `json:"allow_apple_events,omitempty"`
}

type kbUpdate struct {
	Enabled        *bool   `json:"enabled,omitempty"`
	MetisVaultPath *string `json:"metis_vault_path,omitempty"`
}

type apiUpdate struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type defaultsUpdate struct {
	Backend      *string `json:"backend,omitempty"`
	ShareProject *string `json:"share_project,omitempty"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req updateSettingsReq
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	updates := buildSettingsUpdates(req)
	for k, v := range updates {
		if err := s.db.SetConfigValue(k, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		uxlog.Log("[api] settings %s = %q", k, v)
	}
	s.handleGetSettings(w, r)
}

// buildSettingsUpdates flattens the partial update into config (key, value)
// pairs. Extracted for unit testing — covers the bool/int formatting and
// CSV joining without needing a server fixture.
func buildSettingsUpdates(req updateSettingsReq) map[string]string {
	out := make(map[string]string)
	if s := req.Sandbox; s != nil {
		if s.Enabled != nil {
			out["sandbox.enabled"] = boolStr(*s.Enabled)
		}
		if s.DenyRead != nil {
			out["sandbox.deny_read"] = strings.Join(*s.DenyRead, ",")
		}
		if s.ExtraWrite != nil {
			out["sandbox.extra_write"] = strings.Join(*s.ExtraWrite, ",")
		}
		if s.AllowAppleEvents != nil {
			// Drop bundle IDs that fail syntactic validation. The SBPL
			// profile generator also skips invalid entries, but rejecting
			// at the API boundary gives clearer feedback (the SPA can
			// inspect the GET response and see that an entry didn't
			// survive) and keeps the persisted CSV free of garbage.
			cleaned := make([]string, 0, len(*s.AllowAppleEvents))
			for _, b := range *s.AllowAppleEvents {
				b = strings.TrimSpace(b)
				if agent.IsValidBundleID(b) {
					cleaned = append(cleaned, b)
				}
			}
			out["sandbox.allow_apple_events"] = strings.Join(cleaned, ",")
		}
	}
	if k := req.KB; k != nil {
		if k.Enabled != nil {
			out["kb.enabled"] = boolStr(*k.Enabled)
		}
		if k.MetisVaultPath != nil {
			out["kb.metis_vault_path"] = *k.MetisVaultPath
		}
	}
	if a := req.API; a != nil {
		if a.Enabled != nil {
			out["api.enabled"] = boolStr(*a.Enabled)
		}
	}
	if d := req.Defaults; d != nil {
		if d.Backend != nil {
			out["defaults.backend"] = *d.Backend
		}
		if d.ShareProject != nil {
			out["defaults.share_project"] = *d.ShareProject
		}
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func stringsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- Logs ---

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	// logPath whitelists "ux" and "daemon" — anything else returns an error,
	// so the path passed to readTail is one of two compile-time-known values
	// rooted at db.DataDir(). gosec's taint analysis can't see that.
	path, err := logPath(r.PathValue("name"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Default tail = 64KB, max 1MB.
	tail := 64 * 1024
	if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil && n > 0 {
		tail = min(n, 1<<20)
	}
	data, err := readTail(path, tail)
	if err != nil {
		// Missing log file is normal (e.g., daemon hasn't logged yet).
		// Surface as 200 with empty body rather than 404 so the SPA can
		// render "(empty)" without special-casing.
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// X-Content-Type-Options: nosniff is set globally in corsMiddleware, so
	// browsers won't reinterpret log bytes as HTML/JS. Content is server
	// logs we wrote ourselves, not user input.
	w.Write(data) //nolint:errcheck,gosec // G705: text/plain log tail, nosniff applied
}

func logPath(name string) (string, error) {
	dataDir := db.DataDir()
	switch name {
	case "ux":
		return uxlog.Path(dataDir), nil
	case "daemon":
		return filepath.Join(dataDir, "daemon.log"), nil
	}
	return "", &logNameError{name: name}
}

type logNameError struct{ name string }

func (e *logNameError) Error() string { return "unknown log: " + e.name }

func readTail(path string, n int) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is whitelisted by logPath()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := max(info.Size()-int64(n), 0)
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.Read(buf); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}

// isSandboxAvailable indirects through a function variable so settings tests
// can stub the result without launching sandbox-exec.
var isSandboxAvailable = agent.IsSandboxAvailable
