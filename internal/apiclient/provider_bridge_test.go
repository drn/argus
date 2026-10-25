package apiclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestProviderBridgeStates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"live", `{"t":{"task_id":"t","busy":true,"generation":"g"}}`, 200, false},
		{"empty", `{}`, 200, false}, {"old daemon", `{}`, 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				testutil.Equal(t, r.URL.Path, "/api/bridge/states")
				testutil.Equal(t, r.Header.Get("Authorization"), "Bearer token")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			states, err := NewProvider(New(srv.URL, "token")).BridgeStates()
			testutil.Equal(t, err != nil, tc.wantErr)
			if tc.name == "live" {
				testutil.True(t, states["t"].Busy)
				testutil.Equal(t, states["t"].Generation, "g")
			}
		})
	}
}
