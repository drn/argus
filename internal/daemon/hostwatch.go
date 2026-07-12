package daemon

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// hostSuspendInterval is the host-suspend watchdog's tick cadence. Each tick does
// almost nothing (one wall-clock comparison), so a short interval is cheap. After
// a wake the frozen ticker's pending tick fires immediately, so detection latency
// at wake is near zero regardless — the interval mainly sets the "normal gap"
// baseline the threshold is measured against.
const hostSuspendInterval = 30 * time.Second

// hostSuspendThreshold is the wall-clock gap between consecutive watchdog ticks
// that is treated as a host suspend (laptop sleep / hibernate / VM pause). It is
// 6x hostSuspendInterval — comfortably above any ordinary scheduler/GC jitter
// (sub-second to low-seconds even on a hammered machine) yet far below a real
// host sleep (minutes to hours). Deliberately generous to avoid false positives.
const hostSuspendThreshold = 3 * time.Minute

// hostSuspendMessageType is the body `type` marker of the system note broadcast
// to every running task when a host suspend is detected — a sibling of
// ARGUS_BOUNCED (see sendBounceSignals) so tooling/tests can detect either.
const hostSuspendMessageType = "ARGUS_HOST_SUSPENDED"

// detectSuspendGap reports the gap between two consecutive watchdog ticks and
// whether it exceeds the suspend threshold. Both inputs MUST be WALL-CLOCK times
// (monotonic reading stripped via time.Now().Round(0) at the call site) so the
// delta reflects real elapsed time INCLUDING any window the host was asleep: Go's
// t.Sub(u) uses the monotonic reading when both operands carry one, and the
// monotonic clock does NOT advance during host sleep on macOS — a monotonic delta
// would under-report the gap to ~the tick interval and never detect the suspend.
// Pure; both the loop and the tests drive it. A negative gap (wall-clock stepped
// backward, e.g. an NTP correction) is below any positive threshold and does not
// fire.
func detectSuspendGap(prev, now time.Time, threshold time.Duration) (time.Duration, bool) {
	gap := now.Sub(prev)
	return gap, gap >= threshold
}

// hostSuspendPayload is the JSON body of the ARGUS_HOST_SUSPENDED note: a
// machine-detectable type (sibling to ARGUS_BOUNCED), the approximate gap in both
// human and second forms, and a human-readable note the recipient agent reads
// verbatim from its inbox.
type hostSuspendPayload struct {
	Type             string `json:"type"`
	ApproxGap        string `json:"approx_gap"`
	ApproxGapSeconds int64  `json:"approx_gap_seconds"`
	Note             string `json:"note"`
}

// hostSuspendNote is the guidance an agent reads verbatim. It is written so a
// coordinator specifically knows not to misread the spanning silence as a stuck
// worker. Plain sentences (no dashes) so it renders cleanly in any agent context.
func hostSuspendNote(gap time.Duration) string {
	return "This host appears to have been asleep or suspended for approximately " +
		gap.String() + ". Every argus session, including any workers you are " +
		"coordinating, was paused equally during that window and resumed together; " +
		"no real work-time elapsed for the agents. Do NOT treat worker silence that " +
		"spans this gap as staleness, a stuck agent, or a dead worker. Give workers a " +
		"chance to report back before creating a duplicate retry node, a retry plan " +
		"node, or re-spawning work that is still in flight."
}

// hostSuspendBody builds the JSON note body for a detected suspend of the given
// approximate duration. Built via json.Marshal so the embedded note text can
// never produce invalid JSON; the marshal cannot fail for these field types, so
// the error branch is an unreachable minimal-body fallback.
func hostSuspendBody(gap time.Duration) string {
	rounded := gap.Round(time.Second)
	b, err := json.Marshal(hostSuspendPayload{
		Type:             hostSuspendMessageType,
		ApproxGap:        rounded.String(),
		ApproxGapSeconds: int64(rounded.Seconds()),
		Note:             hostSuspendNote(rounded),
	})
	if err != nil {
		return `{"type":"` + hostSuspendMessageType + `"}`
	}
	return string(b)
}

// sendHostSuspendSignals posts an ARGUS_HOST_SUSPENDED note (carrying the
// approximate suspend duration) into each given task's inbox, skipping tasks that
// no longer exist or are archived. Returns the count actually sent. A direct
// sibling of sendBounceSignals — same SystemTaskID sender, KindNote, and
// InsertSystemMessage path — so both daemon-originated agent signals share one
// durable, inbox-poll-visible delivery shape (no notifier / PTY push). Per-task
// failures are logged and skipped so one bad row never blocks the rest.
func sendHostSuspendSignals(database *db.DB, ids []string, gap time.Duration) int {
	if len(ids) == 0 {
		return 0
	}
	body := hostSuspendBody(gap)
	sent := 0
	for _, id := range ids {
		t, err := database.Get(id)
		if err != nil || t == nil || t.Archived {
			slog.Info("hostwatch: skipping task", "task", id)
			continue
		}
		msg := &model.TaskMessage{
			From: SystemTaskID,
			To:   id,
			Kind: model.KindNote,
			Body: body,
		}
		if _, err := database.InsertSystemMessage(msg); err != nil {
			slog.Warn("hostwatch: failed to send suspend note", "task", id, "err", err)
			continue
		}
		sent++
	}
	return sent
}

// hostSuspendTick runs one watchdog pass: detect a suspend gap between prev and
// now (wall-clock), broadcast the note to every running task when one is found,
// and return `now` as the new baseline. Returning the baseline UNCONDITIONALLY
// (fire or not) is what makes the notice one-shot per suspend with NO dedup
// bookkeeping — the tick immediately after a suspend sees a normal-cadence gap
// and stays silent. Runner failures surface as an empty running set (a no-op
// broadcast), never a panic.
func (d *Daemon) hostSuspendTick(prev, now time.Time) time.Time {
	gap, suspended := detectSuspendGap(prev, now, hostSuspendThreshold)
	if !suspended {
		return now
	}
	ids := d.runner.Running()
	sent := sendHostSuspendSignals(d.db, ids, gap)
	slog.Info("hostwatch: host suspend detected", "gap", gap.Round(time.Second).String(), "running", len(ids), "notified", sent)
	return now
}

// runHostSuspendWatcher is the host-suspend detection loop. It ticks on a fixed
// cadence and, each tick, compares the current wall-clock time against the
// previous tick's. A gap far larger than the interval means the host (and thus
// EVERY argus process — daemon, coordinators, workers) was suspended and resumed
// together (laptop sleep / hibernate). On detection it broadcasts an
// ARGUS_HOST_SUSPENDED note to every running task so a coordinator does not
// misread the concurrent silence as a stuck worker (the live repro: duplicate
// "-retry"/"-retry2" plan nodes). d.done gates the loop for prompt shutdown.
//
// Runs UNCONDITIONALLY (not gated on cfg.Hera.Enabled): a bare-worker coordinator
// with no plan-DAG misjudges silence exactly the same way, so the signal is not
// plan-DAG-specific. The per-tick logic is factored into hostSuspendTick so tests
// drive a single pass with synthetic times instead of sleeping.
//
// The baseline is stamped BEFORE the loop with the monotonic reading stripped
// (Round(0)) — see detectSuspendGap on why monotonic would under-report a sleep
// gap — so the first tick compares against a real, recent timestamp rather than a
// zero value (no "first tick with no baseline" false positive by construction).
func (d *Daemon) runHostSuspendWatcher() {
	ticker := time.NewTicker(hostSuspendInterval)
	defer ticker.Stop()

	prev := time.Now().Round(0)
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			prev = d.hostSuspendTick(prev, time.Now().Round(0))
		}
	}
}
