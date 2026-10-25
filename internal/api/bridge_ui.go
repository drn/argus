package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

type bridgeUIBinding struct {
	BindingID    int64  `json:"binding_id"`
	Role         string `json:"role"`
	Kind         string `json:"kind"`
	Orchestrator string `json:"orchestrator"`
	Status       string `json:"status"`
	Unread       int    `json:"unread"`
	Budget       int    `json:"budget"`
}

type bridgeUIMessage struct {
	ID     string    `json:"id"`
	Sender string    `json:"sender"`
	Tldr   string    `json:"tldr"`
	SentAt time.Time `json:"sent_at"`
}

type bridgeUISnapshot struct {
	Settings bridgeSettings    `json:"settings"`
	Live     bool              `json:"live"`
	Context  *bridge.Context   `json:"context"`
	Bindings []bridgeUIBinding `json:"bindings"`
	Unread   int               `json:"unread"`
	Messages []bridgeUIMessage `json:"messages"`
}

// handleBridgeUI only uses read APIs: inspecting mail must never acknowledge
// durable messages, cancel pending doorbells or claim a delivery.
func (s *Server) handleBridgeUI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fail := func(err error) {
		uxlog.Log("[bridge-ui] snapshot failed task=%q error=%q", id, err.Error())
		slog.Warn("[bridge-ui] snapshot failed", "task", strconv.Quote(id), "error", strconv.Quote(err.Error()))
		writeErr(w, http.StatusInternalServerError, "bridge UI unavailable", err)
	}
	// Distinguish a missing task from an unavailable database.
	task, err := s.db.Get(id)
	if errors.Is(err, db.ErrTaskNotFound) {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "task not found", nil)
		return
	}
	cfg := s.db.Config()
	st, live := s.bridgeState(id, time.Now())
	out := bridgeUISnapshot{Settings: bridgeSettings{Enabled: cfg.Bridge.Enabled, Delivery: cfg.Bridge.Delivery, Band: cfg.Bridge.Band, Toasts: cfg.Bridge.Toasts}, Live: live, Context: st.Context, Bindings: []bridgeUIBinding{}, Messages: []bridgeUIMessage{}}
	bindings, err := s.db.ListHeraLiveBindingsByTask(id)
	if err != nil {
		fail(err)
		return
	}
	for _, binding := range bindings {
		role, err := s.db.HeraRole(binding.RoleID)
		if err != nil {
			fail(err)
			return
		}
		orch, err := s.db.HeraOrchestrator(binding.OrchestratorID)
		if err != nil {
			fail(err)
			return
		}
		status, err := s.db.HeraRoleStatusFor(role.ID)
		if err != nil && !errors.Is(err, db.ErrHeraNotFound) {
			fail(err)
			return
		}
		mail, err := s.db.HeraInbox(role.ID)
		if err != nil {
			fail(err)
			return
		}
		item := bridgeUIBinding{BindingID: binding.ID, Role: role.Name, Kind: string(role.Kind), Orchestrator: orch.Name, Status: "idle", Unread: len(mail)}
		if status != nil {
			item.Status = string(status.Status)
		}
		if role.Kind == db.HeraKindCoordinator {
			item.Budget = cfg.Hera.CoordinatorContextBudget
		}
		out.Bindings = append(out.Bindings, item)
		for _, m := range mail {
			sender, err := s.db.HeraRole(m.FromRoleID)
			if err != nil {
				fail(err)
				return
			}
			out.Messages = append(out.Messages, bridgeUIMessage{ID: fmt.Sprintf("hera:%d", m.ID), Sender: sender.Name, Tldr: m.Tldr, SentAt: m.SentAt})
		}
	}
	mail, err := s.db.Inbox(id, db.InboxFilter{UnreadOnly: true, Limit: db.MaxInboxLimit})
	if err != nil {
		fail(err)
		return
	}
	for _, m := range mail {
		sender := m.From
		if from, err := s.db.Get(m.From); err != nil && !errors.Is(err, db.ErrTaskNotFound) {
			fail(err)
			return
		} else if from != nil {
			sender = from.Name
		}
		summary := strings.Join(strings.Fields(m.Body), " ")
		chars := []rune(summary)
		if len(chars) > 120 {
			summary = string(chars[:117]) + "..."
		}
		out.Messages = append(out.Messages, bridgeUIMessage{ID: "task:" + m.ID, Sender: sender, Tldr: summary, SentAt: m.CreatedAt})
	}
	sort.Slice(out.Messages, func(i, j int) bool {
		if out.Messages[i].SentAt.Equal(out.Messages[j].SentAt) {
			return out.Messages[i].ID < out.Messages[j].ID
		}
		return out.Messages[i].SentAt.Before(out.Messages[j].SentAt)
	})
	out.Unread = len(out.Messages)
	uxlog.Log("[bridge-ui] snapshot task=%s bindings=%d unread=%d live=%t", id, len(out.Bindings), out.Unread, out.Live)
	writeJSON(w, http.StatusOK, out)
}
