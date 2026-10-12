package api

import (
	"net/http"
	"os"

	"github.com/drn/argus/internal/hostsleep"
	"github.com/drn/argus/internal/sysmetrics"
)

// systemMetricsResponse is the wire shape of GET /api/system-metrics: the cached
// host-load snapshot (embedded, so its fields flatten into the JSON object) plus
// the live active/idle agent-session counts read at request time.
type systemMetricsResponse struct {
	sysmetrics.Snapshot
	Sessions sessionCounts `json:"sessions"`
	// HostSleep is the host's sleep posture against this (necessarily enabled)
	// API: at_risk means the web app drops out whenever the Mac idle-sleeps.
	HostSleep hostsleep.Report `json:"host_sleep"`
}

// handleSystemMetrics serves the latest cached host-load snapshot. CPU/mem/swap/
// disk/uptime come from the collector's background sample; the session counts are
// read live from the runner so they reflect current state, not the sample time.
func (s *Server) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	var snap sysmetrics.Snapshot
	if s.metrics != nil {
		snap = s.metrics.Latest()
	}
	running, idle := s.runner.RunningAndIdle()
	writeJSON(w, http.StatusOK, systemMetricsResponse{
		Snapshot:  snap,
		Sessions:  sessionCounts{Running: len(running), Idle: len(idle)},
		HostSleep: s.hostSleep.Get(true, os.Getpid()),
	})
}
