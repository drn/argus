package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/mergesafety"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// mergesafety.go wires the add-merge-safety-review popup (mergesafetypopup.go)
// into its two entry points: the single-role nuke (heraOpenDelete, wired from
// heraactions.go) and the global Cleanup command-palette action. It also owns
// the small local REST client the global Cleanup action needs to reach the
// daemon's own maintenance endpoints (that classification + caching logic is
// daemon-side, built in parallel under internal/api/internal/db — this file
// only calls it over HTTP, never reimplements it).

// --- Tier-A-only classification (single-role nuke, cascade, clear-archive) --

// classifyNukeCandidate runs the merge-safety classifier's Tier A (local git,
// no network) check for one task about to be nuked. MUST be called off the UI
// thread — it shells out to git (gitutil.ResolveDefaultBranch /
// mergesafety.Classify's local-ancestor check) — never call this inline on
// the tview main goroutine (CLAUDE.md's git-off-UI-thread rule).
//
// RepoSlug is deliberately left empty, which makes mergesafety.Classify
// Tier-A-only (see internal/mergesafety/classify.go): a nuke is an
// interactive, common action that must never wait on a gh/GitHub network
// call. RepoDir prefers the task's own worktree (the actual checkout the
// branch lives in) over the project's shared checkout path, falling back to
// the latter only when the task has no worktree of its own (e.g. an
// already-archived task reached via the global Cleanup backlog).
func (a *App) classifyNukeCandidate(taskID string) mergesafety.Verdict {
	t, err := a.db.Get(taskID)
	if err != nil || t == nil {
		return mergesafety.Verdict{Reason: "task not found"}
	}
	cfg := a.db.Config()
	repoDir := t.Worktree
	if repoDir == "" {
		repoDir = agent.ResolveDir(t, cfg)
	}
	proj := cfg.Projects[t.Project]
	defaultShort, defaultRef, err := gitutil.ResolveDefaultBranch(repoDir, proj.Branch)
	if err != nil {
		return mergesafety.Verdict{Reason: "could not resolve default branch: " + err.Error()}
	}
	v, err := mergesafety.Classify(context.Background(), mergesafety.Params{
		RepoDir:      repoDir,
		Branch:       t.Branch,
		DefaultRef:   defaultRef,
		DefaultShort: defaultShort,
		// RepoSlug intentionally empty — Tier A only, see doc above.
	})
	if err != nil {
		return mergesafety.Verdict{Reason: "classify error: " + err.Error()}
	}
	return v
}

// maxClassifyWorkers bounds the concurrent Tier-A classification fan-out for
// a cascade nuke / clear-archive sweep — a subtree or a coordinator's hidden
// archive can hold dozens of tasks, and firing one goroutine per task would
// be an unbounded fan-out for what's meant to stay a fast, local-only check.
const maxClassifyWorkers = 8

// classifyTasksConcurrently runs classifyNukeCandidate for every task ID in
// ids across a bounded worker pool and returns how many classified Safe.
// Blocks until every classification completes, so callers MUST invoke this
// from a goroutine, never inline on the tview main thread.
func (a *App) classifyTasksConcurrently(ids []string) (confirmed int) {
	sem := make(chan struct{}, maxClassifyWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(taskID string) {
			defer wg.Done()
			defer func() { <-sem }()
			if a.classifyNukeCandidateFn(taskID).Safe {
				mu.Lock()
				confirmed++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return confirmed
}

// --- single-role nuke: async classify-then-open-popup ----------------------

// heraOpenSingleNukeReview runs the Tier-A-only merge-safety check for a
// sole-bound role's task off the UI thread, then opens the merge-safety
// review popup with that ONE task as its sole candidate (add-merge-safety-
// review). A staleness guard re-reads the CURRENT rail selection when the
// goroutine completes — mirroring app.go's fetchGitStatus pattern — so that
// if the operator moved on (a different role now selected, or this role no
// longer resolves) the popup is silently dropped rather than opening over a
// stale target.
func (a *App) heraOpenSingleNukeReview(r *hera.RoleView) {
	roleID, taskID, name := r.RoleID, r.TaskID, r.Name
	go func() {
		verdict := a.classifyNukeCandidateFn(taskID)
		a.tapp.QueueUpdateDraw(func() {
			cur := a.heraPage.SelectionContext()
			if cur.Role == nil || cur.Role.RoleID != roleID {
				uxlog.Log("[hera-view] nuke review: selection changed before classify finished, dropping popup (role=%d)", roleID)
				return
			}
			cand := mergeSafetyCandidate{TaskID: taskID, Name: name, Safe: verdict.Safe, Reason: verdict.Reason}
			curRole := cur.Role
			a.openMergeSafetyPopup(" Nuke "+name+"? ", []mergeSafetyCandidate{cand}, func(scope mergeSafetyScope, cands []mergeSafetyCandidate) {
				if scope == mergeSafetyScopeSafe && len(cands) > 0 && !cands[0].Safe {
					// "Clean safe" on a NOT-SAFE sole candidate is a no-op — the
					// operator is never blocked, but nothing happens either
					// (design.md: "Clean safe acts only on the SAFE section").
					return
				}
				a.heraNukeRole(curRole)
			})
		})
	}()
}

// --- popup modal plumbing (mirrors openHeraConfirm/handleHeraConfirmKey/
// closeHeraConfirm exactly) --------------------------------------------------

// openMergeSafetyPopup shows the merge-safety review popup over candidates.
// onClean is called with the operator's chosen scope + the popup's full
// candidate list on Clean safe/Clean all; never called on Cancel. The popup
// is a pure display/choice widget — filtering to the SAFE subset (Clean
// safe) is onClean's own responsibility.
func (a *App) openMergeSafetyPopup(title string, candidates []mergeSafetyCandidate, onClean func(mergeSafetyScope, []mergeSafetyCandidate)) {
	a.mergeSafetyGen++
	a.mergeSafetyPopup = NewMergeSafetyPopup(title, candidates)
	a.mergeSafetyPopupOnClean = onClean
	a.mode = modeMergeSafetyPopup
	a.pages.AddPage("mergesafety", a.mergeSafetyPopup, true, true)
	a.pages.SwitchToPage("mergesafety")
	a.tapp.SetFocus(a.mergeSafetyPopup)
}

// handleMergeSafetyPopupKey processes keys while the merge-safety review
// popup is open.
func (a *App) handleMergeSafetyPopupKey(event *tcell.EventKey) {
	a.mergeSafetyPopup.InputHandler()(event, func(tview.Primitive) {})
	if a.mergeSafetyPopup.Confirmed() {
		scope := a.mergeSafetyPopup.Scope()
		cands := a.mergeSafetyPopup.Candidates()
		onClean := a.mergeSafetyPopupOnClean
		a.closeMergeSafetyPopup()
		if onClean != nil {
			onClean(scope, cands)
		}
		return
	}
	if a.mergeSafetyPopup.Canceled() {
		uxlog.Log("[hera-view] merge-safety popup canceled")
		a.closeMergeSafetyPopup()
	}
}

// closeMergeSafetyPopup dismisses the popup and restores the Hera tab.
// Bumps mergeSafetyGen so any in-flight classification/poll goroutine
// targeting the closed popup detects it and stops rather than reopening or
// repainting a stale dialog.
func (a *App) closeMergeSafetyPopup() {
	a.mergeSafetyGen++
	a.mode = modeTaskList
	a.mergeSafetyPopup = nil
	a.mergeSafetyPopupOnClean = nil
	a.pages.RemovePage("mergesafety")
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}

// --- global Cleanup action (Ctrl+K palette) ---------------------------------

// localMaintenanceClient is the minimal HTTP client the global Cleanup action
// uses to reach the local daemon's own REST maintenance endpoints. The
// classification + caching this action drives is daemon-side
// (add-merge-safety-review's rest-api delta, built under internal/api /
// internal/db) — even a local, direct-DB TUI has no in-process equivalent to
// call instead, so this always goes over HTTP, mirroring
// cmd/argus/coord_hook.go's own token-file discovery (same well-known path)
// but resolving the port from the already-loaded local config rather than an
// extra daemon RPC round trip.
type localMaintenanceClient struct {
	baseURL string
	token   string
	hc      *http.Client
}

// newLocalMaintenanceClient builds a localMaintenanceClient against the
// daemon's local REST API. Local mode only: a --remote TUI runs on a
// different machine than the daemon it's controlling, so a local
// ~/.argus/api-token read (and a 127.0.0.1 dial) would be meaningless there —
// today this is moot in practice (the Cleanup action's CtxHeraRail palette
// row is only offered while !heraPage.IsRemote(), per
// paletteApplicableActions), but this guard keeps the function honest if that
// gating ever changes, rather than silently doing the wrong thing.
// Otherwise errors when the API token file doesn't exist yet (the daemon
// only creates it when `api.enabled` is turned on in Settings — see
// internal/daemon/daemon.go) — surfaced to the operator as a clear "turn the
// API on first" message rather than an opaque connection-refused error once
// the HTTP call itself is attempted.
func (a *App) newLocalMaintenanceClient() (*localMaintenanceClient, error) {
	if _, ok := a.db.(*db.DB); !ok {
		return nil, fmt.Errorf("cleanup requires local mode")
	}
	cfg := a.db.Config()
	port := cfg.API.HTTPPort
	if port == 0 {
		port = 7743
	}
	tokenPath := filepath.Join(db.DataDir(), "api-token")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("REST API token not found (enable the API in Settings first): %w", err)
	}
	return &localMaintenanceClient{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		token:   strings.TrimSpace(string(data)),
		hc:      &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// do issues an authenticated REST call, decoding a JSON response into out
// (nil to discard the body). Non-2xx responses are returned as an error.
func (c *localMaintenanceClient) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-to-completion below; close error is non-actionable
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

// cleanupCandidateJSON mirrors the per-task classification row returned by
// GET /api/maintenance/cleanup-candidates (internal/api/cleanup_candidates.go's
// cleanupCandidateJSON — the daemon-side implementation, built in parallel
// with this file, is the actual wire contract; the field tags here must
// match it exactly). Tier is present on the wire but unused for display here.
type cleanupCandidateJSON struct {
	ID      string `json:"task_id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Safe    bool   `json:"safe"`
	Reason  string `json:"reason"`
	Pending bool   `json:"pending"`
}

// cleanupCandidatesResp is GET /api/maintenance/cleanup-candidates' response
// envelope.
type cleanupCandidatesResp struct {
	Candidates []cleanupCandidateJSON `json:"candidates"`
	Computing  bool                   `json:"computing"`
}

// cleanupCleanResp is POST /api/maintenance/cleanup-candidates/clean's
// response envelope.
type cleanupCleanResp struct {
	Cleaned int `json:"cleaned"`
	Skipped int `json:"skipped"`
}

func (c *localMaintenanceClient) computeCleanupCandidates(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/maintenance/cleanup-candidates/compute", nil, nil)
}

func (c *localMaintenanceClient) listCleanupCandidates(ctx context.Context) (cleanupCandidatesResp, error) {
	var resp cleanupCandidatesResp
	err := c.do(ctx, http.MethodGet, "/api/maintenance/cleanup-candidates", nil, &resp)
	return resp, err
}

func (c *localMaintenanceClient) cleanCleanupCandidates(ctx context.Context, scope mergeSafetyScope) (cleanupCleanResp, error) {
	body, err := json.Marshal(map[string]string{"scope": string(scope)})
	if err != nil {
		return cleanupCleanResp{}, err
	}
	var resp cleanupCleanResp
	err = c.do(ctx, http.MethodPost, "/api/maintenance/cleanup-candidates/clean", bytes.NewReader(body), &resp)
	return resp, err
}

// cleanupPollInterval is how often heraOpenGlobalCleanup polls the cached
// classification while a compute pass is in flight.
var cleanupPollInterval = 700 * time.Millisecond

// heraOpenGlobalCleanup is the CtxHeraRail Cleanup action (`c`,
// add-merge-safety-review): opens the merge-safety review popup over the
// FULL cross-project stuck-task backlog, classified daemon-side (Tier A +
// Tier B — this is a deliberate, occasional maintenance action, not an
// interactive hot path, so it may block on gh/GitHub). Selection-independent
// (mirrors heraNewCoordinator) — it fires even on an empty rail selection,
// since the candidate set is never scoped to a coordinator.
func (a *App) heraOpenGlobalCleanup(hera.Selection) {
	client, err := a.maintenanceClientFactory()
	if err != nil {
		a.statusbar.SetError("Cleanup: " + err.Error())
		return
	}
	a.openMergeSafetyPopup(" Cleanup stuck tasks (all projects) ", nil, a.heraDoGlobalClean)
	a.mergeSafetyPopup.SetScanning(true)
	gen := a.mergeSafetyGen
	go a.pollCleanupCandidates(client, gen)
}

// pollCleanupCandidates triggers a compute pass then polls the cached results
// until the daemon reports it's done, feeding each tick into the still-open
// popup via SetCandidates/SetScanning. gen guards against updating a popup
// that has since been closed/replaced (mirrors the staleness guard other
// async App flows use, e.g. fetchGitStatus's TaskID check) — the loop stops
// the instant it observes a generation mismatch or a nil popup.
func (a *App) pollCleanupCandidates(client *localMaintenanceClient, gen int) {
	ctx := context.Background()
	if err := client.computeCleanupCandidates(ctx); err != nil {
		a.tapp.QueueUpdateDraw(func() {
			if a.mergeSafetyGen != gen || a.mergeSafetyPopup == nil {
				return
			}
			a.statusbar.SetError("Cleanup: compute failed: " + err.Error())
		})
		return
	}
	for {
		resp, err := client.listCleanupCandidates(ctx)
		stop := false
		a.tapp.QueueUpdateDraw(func() {
			if a.mergeSafetyGen != gen || a.mergeSafetyPopup == nil {
				stop = true
				return
			}
			if err != nil {
				a.statusbar.SetError("Cleanup: list failed: " + err.Error())
				stop = true
				return
			}
			a.mergeSafetyPopup.SetCandidates(cleanupCandidatesToRows(resp.Candidates))
			a.mergeSafetyPopup.SetScanning(resp.Computing)
			a.forceRedraw("cleanup candidates updated")
			if !resp.Computing {
				stop = true
			}
		})
		if stop {
			return
		}
		time.Sleep(cleanupPollInterval)
	}
}

// cleanupCandidatesToRows converts the REST response shape into the popup
// widget's candidate shape, folding the project name into the display label
// (the global Cleanup backlog spans every project, unlike the single-role
// site's one-task popup).
func cleanupCandidatesToRows(candidates []cleanupCandidateJSON) []mergeSafetyCandidate {
	rows := make([]mergeSafetyCandidate, len(candidates))
	for i, c := range candidates {
		name := c.Name
		if c.Project != "" {
			name = c.Name + "  ·  " + c.Project
		}
		rows[i] = mergeSafetyCandidate{TaskID: c.ID, Name: name, Safe: c.Safe, Reason: c.Reason, Pending: c.Pending}
	}
	return rows
}

// heraDoGlobalClean fires the master-gated clean endpoint for the operator's
// chosen scope. The server acts on its own last-computed cached snapshot
// (never a fresh reclassification) and re-verifies the stuck-task predicate +
// live-binding guard per task immediately before deleting, skipping (not
// erroring) anything that no longer qualifies — see the rest-api delta.
func (a *App) heraDoGlobalClean(scope mergeSafetyScope, _ []mergeSafetyCandidate) {
	client, err := a.maintenanceClientFactory()
	if err != nil {
		a.statusbar.SetError("Cleanup: " + err.Error())
		return
	}
	a.header.SetNotice("Cleaning stuck tasks…")
	go func() {
		resp, err := client.cleanCleanupCandidates(context.Background(), scope)
		a.tapp.QueueUpdateDraw(func() {
			a.header.ClearNotice()
			if err != nil {
				uxlog.Log("[hera-view] cleanup: clean failed: %v", err)
				a.statusbar.SetError("Cleanup failed: " + err.Error())
				return
			}
			uxlog.Log("[hera-view] cleanup: cleaned=%d skipped=%d scope=%s", resp.Cleaned, resp.Skipped, scope)
			a.statusbar.SetInfo(fmt.Sprintf("Cleaned %d task(s)", resp.Cleaned))
			a.refreshTasksLocal()
			a.heraRefresh()
		})
	}()
}
