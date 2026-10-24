package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/uxlog"
)

// maxBridgeBody caps a bridge event batch. The mod buffers at most a couple
// hundred small events, so 1 MiB is generous.
const maxBridgeBody = 1 << 20

// handleBridgeEvents ingests a batch of argus-bridge mod events for a task
// (POST /api/tasks/{id}/bridge/events). Ingest only records state: it never
// touches the session, the task row or any verdict (phase 1 is report-only).
func (s *Server) handleBridgeEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBridgeBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body", err)
		return
	}
	var batch bridge.Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body", err)
		return
	}
	accepted := s.bridgeHub.Ingest(id, batch.Events, time.Now())
	if accepted < len(batch.Events) {
		// uxlog is a no-op in the daemon (it never calls uxlog.Init), so also
		// log through slog, which does reach daemon.log.
		uxlog.Log("[bridge] ingest task=%s accepted=%d of %d (duplicates/unknown types dropped)", id, accepted, len(batch.Events))
		//nolint:gosec // G706: task.ID comes from the db.Get lookup above and slog encodes attributes, escaping control characters.
		slog.Info("[bridge] ingest partial", "task", task.ID, "accepted", accepted, "received", len(batch.Events))
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": accepted, "received": len(batch.Events)})
}

// handleBridgeState returns the latest bridge-reported state of a task
// (GET /api/tasks/{id}/bridge); 404 until the mod has reported.
func (s *Server) handleBridgeState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, ok := s.bridgeHub.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "no bridge state for task", nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		bridge.State
		Live bool `json:"live"`
	}{st, time.Since(st.LastEventAt) <= bridge.LiveWindow})
}

// handleBridgeShadow serves the agree/disagree counters of the shadow
// comparison between screen-reading verdicts and the bridge
// (GET /api/bridge/shadow).
func (s *Server) handleBridgeShadow(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.bridgeShadow.Stats(time.Now()))
}

// observeBridge hands one watcher tick's FINAL screen-reading verdicts to the
// shadow comparator. Observation only — nothing it does feeds back into the
// verdicts, events or pushes of the tick. Skipped when [bridge] is disabled.
func (s *Server) observeBridge(now time.Time, running []string, idle, needs map[string]bool) {
	if s.db != nil && !s.db.Config().Bridge.Enabled {
		return
	}
	s.bridgeShadow.Observe(now, running, idle, needs)
}
