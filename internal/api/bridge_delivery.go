package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/uxlog"
)

func (s *Server) bridgeDeliveryRoute(id string) notify.BridgeRoute {
	if !s.db.Config().Bridge.Delivery {
		return notify.BridgeRoute{}
	}
	// Delivery writes into the session: raw pending permission must block even
	// when display corroboration suppresses it.
	st, live := s.bridgeState(id, time.Now())
	return notify.BridgeRoute{Live: live, Idle: !st.Busy, NeedsInput: st.NeedsInput, Generation: st.Generation, Boot: st.Boot, Session: st.SessionID}
}

type bridgeDeliveryRequest struct {
	Generation string `json:"generation"`
	Boot       string `json:"boot"`
	Token      string `json:"token"`
}

func (s *Server) readBridgeDelivery(w http.ResponseWriter, r *http.Request) (bridgeDeliveryRequest, bool) {
	var req bridgeDeliveryRequest
	if s.notifier == nil {
		writeErr(w, 503, "notifier unavailable", nil)
		return req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Generation == "" || req.Boot == "" {
		writeErr(w, 400, "invalid bridge delivery request", err)
		return req, false
	}
	return req, true
}

func (s *Server) handleBridgeDeliveries(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readBridgeDelivery(w, r)
	if !ok {
		return
	}
	// Expired claims block new work until the normal watcher reconciles them.
	// Never run the PTY fallback inline in a mod hook request.
	claim := s.notifier.ClaimBridge(r.PathValue("id"), req.Generation, req.Boot, time.Now())
	writeJSON(w, 200, map[string]any{"delivery": claim})
}

func (s *Server) handleBridgeAuthorize(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readBridgeDelivery(w, r)
	if !ok {
		return
	}
	allowed := s.notifier.AuthorizeBridge(r.PathValue("id"), req.Token, req.Generation, req.Boot, time.Now())
	writeJSON(w, 200, map[string]bool{"authorized": allowed})
}

func (s *Server) handleBridgeAck(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readBridgeDelivery(w, r)
	if !ok {
		return
	}
	acked := s.notifier.AckBridge(r.PathValue("id"), req.Token, time.Now())
	writeJSON(w, 200, map[string]bool{"acked": acked})
}

func (s *Server) bridgeDeliveryEvidence(c notify.BridgeClaim) notify.BridgeEvidence {
	task, err := s.db.Get(c.TaskID)
	if err != nil || task == nil || task.Worktree == "" || c.Session == "" || filepath.Base(c.Session) != c.Session {
		return notify.BridgeUnknown
	}
	cfgDir, err := agent.ClaudeConfigDirForTask(task, s.db.Config())
	if err != nil {
		return notify.BridgeUnknown
	}
	paths := []string{task.Worktree}
	if real, err := filepath.EvalSymlinks(task.Worktree); err == nil && real != task.Worktree {
		paths = append(paths, real)
	}
	for _, path := range paths {
		project, err := claudesession.ProjectDirIn(cfgDir, path)
		if err != nil {
			continue
		}
		f, err := os.Open(filepath.Join(project, c.Session+".jsonl"))
		if err != nil {
			continue
		}
		evidence := scanBridgeEvidence(f, c)
		_ = f.Close()
		if evidence == notify.BridgeFound {
			return evidence
		}
		// A live process may still hold a queued plugin prompt. Positive
		// absence alone cannot revoke that engine-side queue entry.
		if evidence == notify.BridgeAbsent {
			session := s.runner.Get(c.TaskID)
			st, live := s.bridgeState(c.TaskID, time.Now())
			// A different authoritative launch also proves the original
			// claimant's process is gone, including resume on the same transcript.
			if session == nil || !session.Alive() || (live && st.Generation != c.Generation) || bridgeSettledAfterClaim(st, live, c) {
				return evidence
			}
		}
	}
	return notify.BridgeUnknown
}

func bridgeSettledAfterClaim(st bridge.State, live bool, c notify.BridgeClaim) bool {
	return live && st.SessionID == c.Session && st.Generation == c.Generation && !st.Busy && !st.NeedsInput && st.LastHeartbeatAt.After(c.Expires.Add(200*time.Millisecond))
}

// A complete JSONL row at/after expiry establishes transcript window coverage.
// Malformed/truncated rows and read errors cannot establish positive absence.
func scanBridgeEvidence(r io.Reader, c notify.BridgeClaim) notify.BridgeEvidence {
	reader := bufio.NewReader(r)
	covered, valid := false, true
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var row struct {
				Type      string    `json:"type"`
				Timestamp time.Time `json:"timestamp"`
				Message   struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &row) != nil || line[len(line)-1] != '\n' {
				valid = false
			} else {
				if row.Type == "user" && strings.Contains(string(row.Message.Content), notify.BridgeMarker(c.DeliveryID)) {
					return notify.BridgeFound
				}
				if !row.Timestamp.Before(c.Expires) {
					covered = true
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return notify.BridgeUnknown
		}
	}
	if valid && covered {
		return notify.BridgeAbsent
	}
	return notify.BridgeUnknown
}

func (s *Server) handleBridgeBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	route := s.bridgeDeliveryRoute(id)
	var req struct {
		Size int    `json:"size"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil || req.Size < 0 || req.Text == "" {
		writeErr(w, 400, "invalid budget delivery", err)
		return
	}
	queued := false
	if route.Live && s.notifier != nil && req.Size >= s.db.Config().Hera.CoordinatorContextBudget {
		bindings, err := s.db.ListHeraLiveBindingsByTask(id)
		if err == nil {
			for _, b := range bindings {
				role, err := s.db.HeraRole(b.RoleID)
				if err != nil || role.Kind != db.HeraKindCoordinator {
					continue
				}
				entries, err := s.db.ListMeta(id, db.HeraMetaNamespace)
				if err != nil {
					break
				}
				meta := map[string]string{}
				for _, entry := range entries {
					meta[entry.Key] = entry.Value
				}
				if meta[db.HeraMetaKeyPendingRecycle] != "true" {
					deliveryID := fmt.Sprintf("budget:%s:%s:%d", route.Generation, route.Session, req.Size)
					s.notifier.ReliableNotify(id, req.Text, deliveryID, notify.NotifyOpts{})
					queued = true
					uxlog.Log("[bridge] budget nudge queued task=%s size=%d", id, req.Size)
					//nolint:gosec // G706: DB task id is encoded as a structured attribute.
					slog.Info("[bridge] budget nudge queued", "task", id, "size", req.Size)
				}
				break
			}
		}
	}
	writeJSON(w, 200, map[string]bool{"enabled": queued})
}
