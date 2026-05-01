package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/push"
	"github.com/drn/argus/internal/uxlog"
)

const tokenBytes = 32 // 256 bits

// GenerateToken creates a cryptographically random hex-encoded API token.
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LoadOrCreateToken reads the token from path, or generates and writes one.
// Uses atomic write (temp file + rename) to avoid partial reads.
func LoadOrCreateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) > 0 {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", err
	}

	// Atomic write: temp file in same dir, then rename.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".api-token-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	return token, nil
}

// requireMaster writes a 403 if the request was authenticated with a device
// token rather than the master token. Returns true if the caller should stop
// processing. Used by destructive/configuration-mutating endpoints.
func requireMaster(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Argus-Auth") != "master" {
		http.Error(w, `{"error":"master token required"}`, http.StatusForbidden)
		return true
	}
	return false
}

// hashToken returns the SHA-256 hex digest of a plaintext token, used for
// constant-time lookup against the api_tokens table.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// MintToken generates a new device token, persists its hash, and returns the
// plaintext (only available at this moment).
func MintToken(d *db.DB, label string) (string, int64, error) {
	plain, err := GenerateToken()
	if err != nil {
		return "", 0, err
	}
	hash := hashToken(plain)
	last4 := ""
	if len(plain) >= 4 {
		last4 = plain[len(plain)-4:]
	}
	id, err := d.AddAPIToken(label, hash, last4)
	if err != nil {
		return "", 0, err
	}
	return plain, id, nil
}

// authMiddleware returns an http.Handler that validates the Authorization header.
// Requests to any of the skipPaths are served without auth. A skipPath ending
// in "/" matches by prefix; otherwise exact match.
//
// The middleware accepts both:
//  - the master token (loaded from ~/.argus/api-token, passed in here)
//  - any non-revoked per-device token in the api_tokens table (if `database`
//    is non-nil)
//
// The master token is treated as admin and is required to mint device tokens.
func authMiddleware(token string, database *db.DB, pm *push.Manager, next http.Handler, skipPaths ...string) http.Handler {
	exact := make(map[string]bool, len(skipPaths))
	var prefixes []string
	for _, p := range skipPaths {
		if strings.HasSuffix(p, "/") && p != "/" {
			prefixes = append(prefixes, p)
		} else {
			exact[p] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exact[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		for _, p := range prefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		// Accept either Authorization: Bearer <token> or ?token=<token>.
		// Query param is required for EventSource (which cannot set headers).
		var provided string
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			provided = strings.TrimPrefix(auth, "Bearer ")
		} else if t := r.URL.Query().Get("token"); t != "" {
			provided = t
		} else {
			http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
			// Master token; mark request and proceed.
			r.Header.Set("X-Argus-Auth", "master")
			recordVAPIDOrigin(pm, r)
			next.ServeHTTP(w, r)
			return
		}
		// Try device tokens.
		if database != nil {
			if t, _ := database.FindAPITokenByHash(hashToken(provided)); t != nil {
				r.Header.Set("X-Argus-Auth", "device")
				r.Header.Set("X-Argus-Token-Id", strconv.FormatInt(t.ID, 10))
				recordVAPIDOrigin(pm, r)
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
	})
}

// recordVAPIDOrigin extracts an https origin from the request and pushes it
// into the push manager as the VAPID JWT `sub`. Apple WebPush rejects any
// other format (mailto:argus@localhost → 403). Called on every authenticated
// request so the subject tracks whichever https URL the PWA is being served
// from (typically the user's tailscale-funnel host). Skips silently when the
// derived origin isn't https — http LAN access shouldn't poison the value.
func recordVAPIDOrigin(pm *push.Manager, r *http.Request) {
	if pm == nil {
		return
	}
	origin := vapidOriginFromRequest(r)
	if origin == "" {
		return
	}
	if err := pm.SetSubject(origin); err != nil {
		uxlog.Log("[push] vapid subject persist failed: %v", err)
	}
}

// vapidOriginFromRequest derives "https://<host>" from the request. Prefers
// the Origin header (set by browsers on cross-origin fetches and by the SPA
// on subscribe POSTs) but falls back to scheme+Host so EventSource and
// same-origin GETs still contribute. Returns "" if the result isn't an
// https URL.
func vapidOriginFromRequest(r *http.Request) string {
	if o := strings.TrimSpace(r.Header.Get("Origin")); strings.HasPrefix(o, "https://") {
		return trimURLPath(o)
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	if scheme != "https" || r.Host == "" {
		return ""
	}
	return scheme + "://" + r.Host
}

// trimURLPath returns the scheme+host portion of a URL, dropping any path,
// query, or fragment. The Origin header normally has no path, but RFC 6454
// permits one — strip defensively so the VAPID `sub` claim is consistent.
func trimURLPath(u string) string {
	// Skip past "https://".
	const prefix = "https://"
	if !strings.HasPrefix(u, prefix) {
		return u
	}
	rest := u[len(prefix):]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return prefix + rest
}
