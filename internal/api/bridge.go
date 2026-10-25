package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/usagebudget"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/uxlog"
)

// maxBridgeBody caps a bridge event batch. The mod buffers at most a couple
// hundred small events, so 1 MiB is generous.
const maxBridgeBody = 1 << 20

// handleBridgeEvents ingests a batch of argus-bridge mod events for a task
// (POST /api/tasks/{id}/bridge/events). Current launch reports feed watcher
// verdicts and persist measurement accounting without writing to the PTY.
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
	s.bridgeIngestMu.Lock()
	accepted := 0
	for _, ev := range batch.Events {
		// Buffered posts from an exited process must never replace the current
		// process's reports. Legacy generation-less reports remain diagnostic.
		if ev.Generation != "" {
			sess := s.runner.Get(id)
			if sess == nil || !sess.Alive() || agent.SessionBridgeGeneration(sess) != ev.Generation {
				continue
			}
		}
		n := s.bridgeHub.Ingest(id, []bridge.Event{ev}, time.Now())
		accepted += n
		if n > 0 && ev.Type == bridge.TypeMeasure {
			if st, live := s.bridgeState(id, time.Now()); live {
				s.stampBridgeMeasurement(id, st)
			}
		}
	}
	s.bridgeIngestMu.Unlock()
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
	st, live := s.bridgeState(id, time.Now())
	ok := st.TaskID != ""
	if !ok {
		st, ok = s.bridgeHub.Get(id)
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no bridge state for task", nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		bridge.State
		Live bool `json:"live"`
	}{st, live})
}

func (s *Server) bridgeState(id string, now time.Time) (bridge.State, bool) {
	if s.bridgeHub == nil || s.runner == nil || s.db == nil || !s.db.Config().Bridge.Enabled {
		return bridge.State{}, false
	}
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		return bridge.State{}, false
	}
	// Running tasks have a pinned backend. Avoid usage-based spawn routing
	// here: it may itself ask this server for a bridge usage reading.
	cfg := s.db.Config()
	name := task.Backend
	if name == "" {
		name = cfg.Projects[task.Project].Backend
	}
	if name == "" {
		name = cfg.Defaults.Backend
	}
	backend, exists := cfg.Backends[name]
	if !exists || !agent.FamilyOf(backend.Command).BridgeMod {
		return bridge.State{}, false
	}
	sess := s.runner.Get(id)
	if sess == nil || !sess.Alive() || s.runner.HasPendingRestart(id) {
		return bridge.State{}, false
	}
	return s.bridgeHub.Authority(id, agent.SessionBridgeGeneration(sess), now)
}

func (s *Server) bridgeLive(id string, now time.Time) bool {
	_, ok := s.bridgeState(id, now)
	return ok
}

// bridgeVerdict resolves live state against raw scraper input. Diagnostic and
// measurement readers can keep using bridgeState to inspect the original report.
func (s *Server) bridgeVerdict(id string, now time.Time) (bridge.State, bool) {
	st, live := s.bridgeState(id, now)
	s.bridgeScraperMu.RLock()
	st = bridge.Resolve(st, s.bridgeScraperNeeds[id])
	s.bridgeScraperMu.RUnlock()
	return st, live
}

// BridgeStates is a batched authoritative snapshot for daemon-connected TUI
// clients. It resolves uncertain permission input against the last raw scraper
// snapshot, does no terminal reads, and excludes stale/old-generation reports.
func (s *Server) BridgeStates() map[string]bridge.State {
	out := make(map[string]bridge.State)
	if s.runner == nil {
		return out
	}
	for _, id := range s.runner.Running() {
		task, err := s.db.Get(id)
		if err != nil || task == nil || task.Archived {
			continue
		}
		if st, ok := s.bridgeVerdict(id, time.Now()); ok {
			out[id] = st
		}
	}
	return out
}

func bridgeSourceLog(id string, live bool) {
	source := "scraper"
	if live {
		source = "bridge"
	}
	uxlog.Log("[bridge] source task=%s source=%s", id, source)
	slog.Info("[bridge] source", "task", id, "source", source)
}

// BridgeUsage selects the newest fresh weekly reading on exactly this
// account. Heartbeats keep the session live, never the measurement fresh.
func (s *Server) BridgeUsage(account string) (usagebudget.Reading, bool) {
	if account == "default" {
		account = ""
	}
	var best usagebudget.Reading
	now := time.Now()
	for id, st := range s.BridgeStates() {
		task, err := s.db.Get(id)
		if err != nil || task == nil {
			continue
		}
		a := task.Account
		if a == "default" {
			a = ""
		}
		if a != account || st.LastMeasureAt.IsZero() || now.Sub(st.LastMeasureAt) >= usagebudget.CacheMaxAge {
			continue
		}
		for _, limit := range st.RateLimits {
			if limit.Kind != "seven_day" || limit.PercentUsed < 0 || limit.PercentUsed > 100 || math.IsNaN(limit.PercentUsed) || math.IsInf(limit.PercentUsed, 0) {
				continue
			}
			reset, err := time.Parse(time.RFC3339, limit.ResetsAt)
			if err != nil || !reset.After(now) {
				continue
			}
			if best.LastProbedAt.Before(st.LastMeasureAt) {
				best = usagebudget.Reading{Percentage: limit.PercentUsed, ResetAt: reset, LastProbedAt: st.LastMeasureAt}
			}
		}
	}
	return best, !best.LastProbedAt.IsZero()
}

func (s *Server) stampBridgeMeasurement(id string, st bridge.State) {
	if st.LastMeasureAt.IsZero() {
		return
	}
	if st.Context != nil {
		entries := make(map[string]string)
		if st.Context.Tokens != nil {
			entries[db.HeraMetaKeyContextSize] = strconv.FormatInt(*st.Context.Tokens, 10)
		}
		if st.Context.Percent != nil {
			entries["context_percent"] = strconv.FormatFloat(*st.Context.Percent, 'f', -1, 64)
		}
		if err := s.db.SetMetaBatch(id, db.HeraMetaNamespace, entries); err != nil {
			//nolint:gosec // G706: id is a DB task identifier; structured slog attributes encode control characters.
			slog.Warn("[bridge] context stamp failed", "task", id, "err", err)
		}
	}
	if st.CostUSD != nil {
		binding, err := s.db.HeraLiveBindingByTask(id)
		if err != nil {
			//nolint:gosec // G706: id is a DB task identifier; structured slog attributes encode control characters.
			slog.Debug("[bridge] cost skipped: no unambiguous binding", "task", id, "err", err)
			return
		}
		if err := s.db.AccrueHeraBridgeCost(binding.ID, st.SessionID, *st.CostUSD, st.Generation); err != nil {
			//nolint:gosec // G706: id is a DB task identifier; structured slog attributes encode control characters.
			slog.Warn("[bridge] cost stamp failed", "task", id, "err", err)
		} else {
			uxlog.Log("[bridge] measurement task=%s session=%s cumulative_usd=%f", id, st.SessionID, *st.CostUSD)
			//nolint:gosec // G706: DB task/session identifiers are structured slog attributes with control characters encoded.
			slog.Debug("[bridge] measurement", "task", id, "session", st.SessionID, "cumulative_usd", *st.CostUSD)
		}
	}
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
	s.bridgeShadow.Observe(now, running, idle, needs, s.bridgeState)
}
