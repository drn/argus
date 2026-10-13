package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/drn/argus/internal/hostsleep"
	"github.com/drn/argus/internal/sysmetrics"
	"github.com/drn/argus/internal/testutil"
)

func TestHandleSystemMetrics(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	// Seed the cached snapshot directly (the collector is not started in unit
	// tests) so we can assert the snapshot fields round-trip through the handler.
	srv.metrics.SetForTest(sysmetrics.Snapshot{
		CPUPercent: 12.5, CPUAvail: true,
		MemTotal: 16 << 30, MemUsed: 8 << 30, MemPercent: 50, MemAvail: true,
		DiskTotal: 100 << 30, DiskUsed: 40 << 30, DiskFree: 60 << 30, DiskPercent: 40, DiskPath: "/home/.argus", DiskAvail: true,
		LoadAvail: false, // unavailable metric must still serialize
	})

	req := authedReq("GET", "/api/system-metrics", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)

	var resp systemMetricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	testutil.Equal(t, resp.CPUPercent, 12.5)
	testutil.True(t, resp.CPUAvail)
	testutil.Equal(t, resp.MemPercent, float64(50))
	testutil.True(t, resp.MemAvail)
	testutil.Equal(t, resp.DiskPath, "/home/.argus")
	testutil.False(t, resp.LoadAvail)
	// Session counts come from the runner live; with no running sessions both are 0.
	testutil.Equal(t, resp.Sessions.Running, 0)
	testutil.Equal(t, resp.Sessions.Idle, 0)
}

func TestHandleSystemMetrics_RequiresAuth(t *testing.T) {
	srv, _ := testServer(t)
	// Exercise the auth middleware wrapper, not the bare mux.
	handler := authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/")

	req := httptest.NewRequest("GET", "/api/system-metrics", nil) // no Authorization header
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusUnauthorized)
}

func TestHandleSystemMetrics_HostSleep(t *testing.T) {
	srv, _ := testServer(t)
	srv.hostSleep = hostsleep.NewMonitorForTest(func(apiEnabled bool, pid int) hostsleep.Report {
		testutil.True(t, apiEnabled)
		testutil.Equal(t, pid, os.Getpid())
		return hostsleep.Report{Status: hostsleep.AtRisk, Message: "sleeps", Fix: hostsleep.PermanentFix}
	})
	mux := srv.routes()

	get := func() systemMetricsResponse {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("GET", "/api/system-metrics", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		var resp systemMetricsResponse
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp
	}

	// First request returns the placeholder; the async check lands shortly after.
	get()
	deadline := time.Now().Add(2 * time.Second)
	for get().HostSleep.Status != hostsleep.AtRisk && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := get().HostSleep
	testutil.Equal(t, got.Status, hostsleep.AtRisk)
	testutil.Equal(t, got.Fix, hostsleep.PermanentFix)
}
