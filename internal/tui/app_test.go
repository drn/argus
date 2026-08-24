package tui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/apistore"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/macapps"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/store"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestIsRedundantAttach(t *testing.T) {
	// Regression: reopening the agent view at the same panel cols must not
	// re-trigger the rerender kick — otherwise Claude's in-flight
	// AskUserQuestion UI is destroyed by the --session-id restart. Genuine
	// resizes (different cols from the cached value) must still fall through
	// to the predicate.
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	const taskID = "rerender-gate"

	// First attach at 120 cols: no cached value, must NOT skip.
	if app.isRedundantAttach(taskID, 120) {
		t.Fatal("first attach should not skip — no cached cols yet")
	}
	// Reopen at the same size: must skip.
	if !app.isRedundantAttach(taskID, 120) {
		t.Fatal("reopen at same cols (120) should skip — gate failed")
	}
	// Reopen again at the same size: still skip (gate is idempotent).
	if !app.isRedundantAttach(taskID, 120) {
		t.Fatal("reopen at same cols (120) should still skip on third call")
	}
	// Genuine resize to 140: must NOT skip; cache must update.
	if app.isRedundantAttach(taskID, 140) {
		t.Fatal("resize to 140 should not skip — cols changed")
	}
	// Reopen at 140: must skip now that 140 is cached.
	if !app.isRedundantAttach(taskID, 140) {
		t.Fatal("reopen at same cols (140) should skip after resize")
	}
	// Per-task isolation: a different task's cache is empty.
	if app.isRedundantAttach("other-task", 140) {
		t.Fatal("different task should not skip — separate cache entry")
	}

	// Invalidation API contract: every non-Skip "could have kicked but
	// didn't" outcome in maybeKickRerender's goroutine (RerenderDeferBusy,
	// RerenderDeferPrompt, sess.Stop() error) calls `invalidateAttachCache(taskID)` so the next
	// reopen at the same cols re-evaluates instead of permanently short-
	// circuiting. Drive the helper directly to pin the invariant — if any
	// production branch stops invoking invalidateAttachCache, the cache
	// will stay populated and the gate will incorrectly skip subsequent
	// retries.
	app.invalidateAttachCache(taskID)
	if app.isRedundantAttach(taskID, 140) {
		t.Fatal("after invalidateAttachCache, reopen at 140 should proceed (not skip)")
	}
	if !app.isRedundantAttach(taskID, 140) {
		t.Fatal("after invalidate + re-cache, reopen at 140 should skip again")
	}
	// invalidateAttachCache is idempotent on a missing key.
	app.invalidateAttachCache("never-cached")
	if app.isRedundantAttach("never-cached", 200) {
		t.Fatal("invalidating a never-cached entry should leave it absent (next call proceeds)")
	}
}

func TestSessionBlockedOnPrompt(t *testing.T) {
	// The TUI's needsInput computation for maybeKickRerender: idle AND a
	// selection-UI / trailing-question marker in the on-disk session log.
	// This is the gate that keeps a genuine resize from killing a session
	// that's actually waiting on a question (the re-entry-on-resize bug).
	t.Setenv("HOME", t.TempDir())

	writeLog := func(t *testing.T, taskID, content string) {
		t.Helper()
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(logPath[:strings.LastIndex(logPath, "/")], 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}

	t.Run("not idle is never blocked even with a prompt marker", func(t *testing.T) {
		writeLog(t, "busy-task", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		testutil.Equal(t, sessionBlockedOnPrompt("busy-task", false), false)
	})
	t.Run("idle with selection-UI marker is blocked", func(t *testing.T) {
		writeLog(t, "prompt-task", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		testutil.Equal(t, sessionBlockedOnPrompt("prompt-task", true), true)
	})
	t.Run("idle with plain output is not blocked", func(t *testing.T) {
		writeLog(t, "plain-task", "Reading foo.go\nDone.\n")
		testutil.Equal(t, sessionBlockedOnPrompt("plain-task", true), false)
	})
	t.Run("idle with no log file is not blocked", func(t *testing.T) {
		testutil.Equal(t, sessionBlockedOnPrompt("missing-task", true), false)
	})
}

// TestDetectNeedsInputSticky_ContentStability covers BUG-032: a worker parked
// at a permission prompt that NEVER reaches the idle set (it emits continuous
// redraw/animation bytes) must still be flagged needs-input via the
// content-stability pass — and a session whose content is still shifting must
// not be (the streaming false-positive guard).
func TestDetectNeedsInputSticky_ContentStability(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(logPath[:strings.LastIndex(logPath, "/")], 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}

	// A fullscreen permission prompt: the worker is running but never idle.
	const parked = "⏺ Do you want to make this edit?\r✻ Brewed for 4s\r\r❯ 1. Yes\r  2. No\r\r"
	writeLog("wkr", parked)
	a := &App{}

	t.Run("first tick records fingerprint without flagging a never-idle session", func(t *testing.T) {
		got := a.detectNeedsInputSticky(nil /* not idle */, []string{"wkr"}, nil)
		testutil.Equal(t, len(got), 0)
		if _, ok := a.needsInputFP["wkr"]; !ok {
			t.Fatal("expected fingerprint recorded for the prompt-showing session")
		}
	})

	t.Run("second tick flags it once content is stable across ticks", func(t *testing.T) {
		// Only the animation chrome advanced between ticks (4s → 9s spinner).
		writeLog("wkr", "⏺ Do you want to make this edit?\r✶ Brewed for 9s\r\r❯ 1. Yes\r  2. No\r\r")
		got := a.detectNeedsInputSticky(nil, []string{"wkr"}, nil)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0], "wkr")
	})

	t.Run("a streaming session producing new content is never flagged", func(t *testing.T) {
		b := &App{}
		writeLog("stream", "⏺ Reading a.go\r✻ Brewed for 1s\r\r❯ 1. Yes\r  2. No\r\r")
		got := b.detectNeedsInputSticky(nil, []string{"stream"}, nil)
		testutil.Equal(t, len(got), 0)
		// Next tick: new transcript content arrived → fingerprint differs →
		// still not flagged.
		writeLog("stream", "⏺ Reading a.go\r⏺ Editing b.go\r✻ Brewed for 2s\r\r❯ 1. Yes\r  2. No\r\r")
		got = b.detectNeedsInputSticky(nil, []string{"stream"}, nil)
		testutil.Equal(t, len(got), 0)
	})

	t.Run("a content-stable WORKING agent ending in a question is never flagged", func(t *testing.T) {
		// endsInQuestion true and NO selection widget, but the "esc to interrupt"
		// working affordance is present → still generating, not awaiting. The
		// idle-gate-less stability pass must not flag it even when stable
		// (BUG-035: the working-affordance-absent gate is the BUG-032 guard).
		c := &App{}
		const working = "⏺ Want me to ship it?\r✻ Cogitating… (12s · esc to interrupt)\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"
		writeLog("q", working)
		testutil.Equal(t, len(c.detectNeedsInputSticky(nil, []string{"q"}, nil)), 0)
		// Stable second tick: still not flagged.
		writeLog("q", working)
		testutil.Equal(t, len(c.detectNeedsInputSticky(nil, []string{"q"}, nil)), 0)
	})

	t.Run("a content-stable AWAITING agent ending in a question is flagged (BUG-035 GAP A)", func(t *testing.T) {
		// Same trailing-question shape but NO working affordance → genuinely
		// awaiting input. A fullscreen agent here never goes idle, so the
		// content-stability pass is the only thing that catches it.
		d := &App{}
		const awaiting = "⏺ Want me to ship it?\r✻ Brewed for 12s\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"
		writeLog("aq", awaiting)
		// First tick records the fingerprint without flagging.
		testutil.Equal(t, len(d.detectNeedsInputSticky(nil, []string{"aq"}, nil)), 0)
		// Stable second tick → flagged.
		writeLog("aq", awaiting)
		got := d.detectNeedsInputSticky(nil, []string{"aq"}, nil)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0], "aq")
	})
}

// TestDetectNeedsInputSticky_DirtyCheck covers the Stat()-based dirty check
// (dedupe-redundant-needsinput-reads, Fix 2): when a session's on-disk log
// hasn't been written to at all since the previous tick (identical size AND
// mtime — the log is append-only outside the one O_TRUNC at session start, so
// this is a safe proxy for byte-identical content), the expensive tail
// read + VT re-emulation is skipped and last tick's own computed raw signal is
// replayed instead — but every tick-counter step function
// (EscalateParkedSelection/ResumeActivityTick/SustainedActivityTick/
// SettleTick) must still advance exactly once per tick, and a GENUINE content
// change (forced via a deliberately-distinct mtime, so the assertions never
// depend on real wall-clock write timing or filesystem mtime granularity)
// must be caught on the very tick it happens — never delayed, never missed.
func TestDetectNeedsInputSticky_DirtyCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLogAt := func(taskID, content string, mtime time.Time) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
		testutil.NoError(t, os.Chtimes(logPath, mtime, mtime))
	}

	t.Run("escalation counter advances identically across unchanged replayed ticks; a genuine change is caught the same tick it happens", func(t *testing.T) {
		a := &App{}
		const parked = "❯ 1. Yes\n  2. No\n"
		const busy = "Reading foo.go\nDone.\n"
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

		// Tick 1: first time this id is seen — no previous Stat snapshot
		// exists, so the dirty check can never short-circuit this one; a
		// genuinely fresh read + escalation step.
		writeLogAt("wkr", parked, base)
		a.detectNeedsInputSticky(nil, []string{"wkr"}, nil)
		testutil.Equal(t, a.needsInputEscalation["wkr"], 1)

		// Ticks 2-10: the file is NEVER rewritten (identical size+mtime as
		// tick 1) — the dirty check must short-circuit the read+re-emulation
		// and replay tick 1's cached `parked` reading, yet the counter must
		// still advance by exactly one per tick — identically to what a fresh
		// re-read of this UNCHANGED content would also produce, since the
		// content is static and ParkedSelectionSignal would return the same
		// `true` every time regardless of whether it's freshly computed or
		// replayed.
		var got []string
		for i := 2; i <= 10; i++ {
			got = a.detectNeedsInputSticky(nil, []string{"wkr"}, got)
			testutil.Equal(t, a.needsInputEscalation["wkr"], i)
		}
		testutil.Equal(t, a.needsInputEscalation["wkr"], 10) // past NeedsInputEscalationTicks(8) — already escalated, still incrementing

		// Tick 11: rewrite to non-qualifying content with a DEFINITELY
		// different mtime (base + 1h — never ambiguous under any filesystem's
		// mtime granularity). The dirty check must detect this as changed and
		// do a fresh read, catching the change on THIS tick.
		writeLogAt("wkr", busy, base.Add(time.Hour))
		got = a.detectNeedsInputSticky(nil, []string{"wkr"}, got)
		// A single non-qualifying tick after an ongoing (already-escalated)
		// streak is a BUG-060 one-tick grace period: held as a negative
		// sentinel, magnitude preserved.
		testutil.Equal(t, a.needsInputEscalation["wkr"], -10)

		// Tick 12: file untouched again (same mtime as tick 11) — the dirty
		// check must replay tick 11's cached `parked=false` reading, not some
		// stale earlier value. A SECOND consecutive non-qualifying tick
		// confirms a genuine break.
		a.detectNeedsInputSticky(nil, []string{"wkr"}, got)
		if _, ok := a.needsInputEscalation["wkr"]; ok {
			t.Fatalf("expected escalation counter to reset to absent (confirmed break) after two consecutive non-qualifying ticks, got present: %d", a.needsInputEscalation["wkr"])
		}
	})

	t.Run("resumed-activity and sustained-active counters advance identically across unchanged replayed ticks (Fix 1 within-tick reuse composed with Fix 2 cross-tick reuse)", func(t *testing.T) {
		a := &App{}
		const working = "⏺ Want me to ship it?\r✻ Cogitating… (12s · esc to interrupt)\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"
		const notWorking = "⏺ Want me to ship it?\r✻ Brewed for 12s\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"
		base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

		writeLogAt("res", working, base)
		a.detectNeedsInputSticky(nil, []string{"res"}, nil)
		testutil.Equal(t, a.needsInputResume["res"], 1)
		testutil.Equal(t, a.sustainedActiveTicks["res"], 1)

		// Ticks 2-5: file untouched — both counters (independently tracked,
		// but fed the SAME shared `working` reading per Fix 1) must advance
		// in lockstep whether freshly computed or replayed from cache.
		var got []string
		for i := 2; i <= 5; i++ {
			got = a.detectNeedsInputSticky(nil, []string{"res"}, got)
			testutil.Equal(t, a.needsInputResume["res"], i)
			testutil.Equal(t, a.sustainedActiveTicks["res"], i)
		}

		// A genuine change to non-working content, forced-distinct mtime.
		writeLogAt("res", notWorking, base.Add(time.Hour))
		a.detectNeedsInputSticky(nil, []string{"res"}, got)
		// ResumeActivityTick has NO grace period — a single non-working tick
		// resets it outright to absent.
		if _, ok := a.needsInputResume["res"]; ok {
			t.Fatalf("expected resume counter to reset to absent (no grace period), got present: %d", a.needsInputResume["res"])
		}
		// SustainedActivityTick DOES have a one-tick grace — the prior streak
		// (5) is held as a negative sentinel, not discarded.
		testutil.Equal(t, a.sustainedActiveTicks["res"], -5)
	})

	t.Run("settlement counter advances identically across unchanged replayed idle ticks; going non-idle resets immediately regardless of the log", func(t *testing.T) {
		a := &App{}
		const plain = "Reading foo.go\nDone.\n"
		base := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

		writeLogAt("settle", plain, base)
		a.detectNeedsInputSticky([]string{"settle"}, []string{"settle"}, nil)
		testutil.Equal(t, a.needsInputSettle["settle"], 1)

		// Untouched idle ticks — replayed from cache (awaitingComputed was
		// true and idleNow still holds), counter still advances by one.
		got := a.detectNeedsInputSticky([]string{"settle"}, []string{"settle"}, nil)
		testutil.Equal(t, a.needsInputSettle["settle"], 2)
		got = a.detectNeedsInputSticky([]string{"settle"}, []string{"settle"}, got)
		testutil.Equal(t, a.needsInputSettle["settle"], 3) // past NeedsInputSettleTicks(2)

		// idleNow is ALWAYS read fresh from the caller-supplied idle set,
		// never gated by the dirty check — going non-idle (no file rewrite at
		// all) must reset the counter to absent on this SAME tick.
		a.detectNeedsInputSticky(nil /* no longer idle */, []string{"settle"}, got)
		if _, ok := a.needsInputSettle["settle"]; ok {
			t.Fatalf("expected settle counter to reset to absent once no longer idle, got present: %d", a.needsInputSettle["settle"])
		}
	})
}

// TestDetectNeedsInputSticky_ArchivePrefilter covers the archive pre-filter
// (dedupe-redundant-needsinput-reads, Fix 3): archiving a task does NOT stop
// its live session (db.SetArchived / the REST and MCP archive endpoints are
// pure DB flag flips — see internal/api/handlers.go's setArchive and
// internal/mcp/server.go's toolTaskArchive, neither of which touches the
// session), so an archived task CAN still appear in runningIDs. Filtering it
// out of the expensive passes never changes the RETURNED needs-input set —
// agent.NeedsInputClear already drops every archived candidate
// unconditionally regardless of what the passes compute for it — but it DOES
// mean an archived-while-running task's internal counters/signal caches reset
// to a cold start rather than continuing to accumulate silently in the
// background. This test pins the ACCEPTED, bounded trade-off that causes: a
// short transient re-detection delay after archive→unarchive-without-restart,
// but NEVER a permanent miss and NEVER a false positive.
func TestDetectNeedsInputSticky_ArchivePrefilter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("c1")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	const parked = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"
	testutil.NoError(t, os.WriteFile(logPath, []byte(parked), 0o644))

	task := &model.Task{ID: "c1", Archived: false}
	a := &App{tasks: []*model.Task{task}}
	running := []string{"c1"}

	// Ticks 1-2: not archived — ordinary 2-tick content-stability flagging.
	got := a.detectNeedsInputSticky(nil, running, nil)
	testutil.Equal(t, len(got), 0)
	if _, ok := a.needsInputFP["c1"]; !ok {
		t.Fatal("expected fingerprint recorded on first tick")
	}
	got = a.detectNeedsInputSticky(nil, running, got)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0], "c1")

	// Archive the task WHILE STILL RUNNING (session never stopped) — the
	// pre-filter must exclude it from the expensive passes from this tick
	// onward: its raw-signal/fingerprint/escalation cache entries are DROPPED
	// (not merely "not surfaced"), and — independently, already guaranteed by
	// agent.NeedsInputClear's own unconditional archivedOf check regardless of
	// this filter — it must never appear in the returned set while archived.
	task.Archived = true
	got = a.detectNeedsInputSticky(nil, running, got)
	testutil.Equal(t, len(got), 0)
	if _, ok := a.needsInputFP["c1"]; ok {
		t.Fatal("expected fingerprint entry dropped for an archived-but-running task (pre-filter skip)")
	}
	// Stays out while archived over several more ticks, log untouched.
	for i := 0; i < 3; i++ {
		got = a.detectNeedsInputSticky(nil, running, got)
		testutil.Equal(t, len(got), 0)
	}

	// Un-archive WITHOUT restarting the session (log content never changed
	// throughout). The accepted trade-off: this is a COLD START, exactly like
	// the task's very first-ever tick — not yet flagged this tick, needs one
	// more stable observation.
	task.Archived = false
	got = a.detectNeedsInputSticky(nil, running, got)
	testutil.Equal(t, len(got), 0)
	if _, ok := a.needsInputFP["c1"]; !ok {
		t.Fatal("expected fingerprint re-recorded on the first post-unarchive tick (cold restart, not a permanent miss)")
	}
	// The very next tick (content still unchanged) re-converges and flags —
	// proving the trade-off is a bounded, transient delay, never a permanent
	// miss.
	got = a.detectNeedsInputSticky(nil, running, got)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0], "c1")
}

// TestDetectNeedsInputSticky_Escalation covers BUG-029: a session parked at a
// selection prompt whose surrounding tail ALSO carries an unrelated line that
// changes every tick (an unrecognized status/counter, or genuinely new but
// irrelevant output elsewhere in the 16 KB window) never lets the ordinary
// 2-tick content-fingerprint match converge — the fingerprint differs every
// tick even though the prompt itself never changes. Without the bounded
// escalation fallback this worker would show the active spinner forever
// instead of "(?)". The escalation counter is independent of the fingerprint
// and must fire once the qualifying combination (selection shape present,
// working affordance absent) holds for NeedsInputEscalationTicks consecutive
// ticks — and must NOT fire for a merely transient/coincidental match.
func TestDetectNeedsInputSticky_Escalation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(logPath[:strings.LastIndex(logPath, "/")], 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}

	// The selection prompt never changes; the leading "progress" line does, on
	// every tick, so the full-tail fingerprint never converges tick-to-tick.
	frame := func(n int) string {
		return fmt.Sprintf("progress: %d files scanned\r⏺ Do you want to make this edit?\r✻ Brewed for 4s\r\r❯ 1. Yes\r  2. No\r\r", n)
	}

	t.Run("never-converging fingerprint escalates to flagged after N consecutive ticks", func(t *testing.T) {
		a := &App{}
		var got []string
		for i := 0; i < agent.NeedsInputEscalationTicks-1; i++ {
			writeLog("wkr", frame(i))
			got = a.detectNeedsInputSticky(nil, []string{"wkr"}, got)
			testutil.Equal(t, len(got), 0)
		}
		writeLog("wkr", frame(agent.NeedsInputEscalationTicks-1))
		got = a.detectNeedsInputSticky(nil, []string{"wkr"}, got)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0], "wkr")
	})

	t.Run("counter resets when the selection prompt scrolls away before the window elapses", func(t *testing.T) {
		b := &App{}
		var got []string
		half := agent.NeedsInputEscalationTicks / 2
		for i := 0; i < half; i++ {
			writeLog("q", frame(i))
			got = b.detectNeedsInputSticky(nil, []string{"q"}, got)
			testutil.Equal(t, len(got), 0)
		}
		// The prompt disappears for one tick (still working, no selection UI) —
		// the streak must reset, not merely pause at `half`.
		writeLog("q", fmt.Sprintf("progress: %d files scanned\rStill generating…\r", half))
		got = b.detectNeedsInputSticky(nil, []string{"q"}, got)
		testutil.Equal(t, len(got), 0)
		// Resume the prompt: must NOT immediately re-escalate from `half`.
		writeLog("q", frame(half+1))
		got = b.detectNeedsInputSticky(nil, []string{"q"}, got)
		testutil.Equal(t, len(got), 0)
	})
}

// TestDetectNeedsInputSticky_AltScreen covers BUG-033 end-to-end through the
// TUI tick: a FULLSCREEN (alt-screen) agent paints its prompt cursor-addressed,
// so the raw on-disk log tail does NOT contain a linear `❯ 1.` (StripANSI drops
// the cursor moves without applying them) — the old raw detector silently
// missed it until the user opened the pane and a SIGWINCH forced a linear
// repaint. Detection against the EMULATED screen flags it without any repaint.
func TestDetectNeedsInputSticky_AltScreen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(logPath[:strings.LastIndex(logPath, "/")], 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}

	// Alt-screen frame: the option text is painted first, then the ❯ cursor is
	// painted LAST at an absolute position to its left — so in byte order ❯
	// trails "1." and the raw regex misses; only emulation lines them up.
	altFrame := func(secs, glyph string) string {
		return "\x1b[?1049h\x1b[2J" +
			"\x1b[1;1H" + glyph + " Brewed for " + secs +
			"\x1b[3;5HDo you want to make this edit?" +
			"\x1b[5;5H1. Yes\x1b[6;5H2. No" +
			"\x1b[5;3H❯" +
			"\x1b[8;1H\x1b[?25l"
	}

	a := &App{}
	t.Run("raw detection alone misses the alt-screen prompt", func(t *testing.T) {
		// Prove the precondition: without emulation the tail is invisible.
		testutil.Equal(t, agent.DetectNeedsInput([]byte(altFrame("4s", "✻"))), false)
	})

	t.Run("first tick records fingerprint without flagging", func(t *testing.T) {
		writeLog("alt", altFrame("4s", "✻"))
		got := a.detectNeedsInputSticky(nil /* never idle */, []string{"alt"}, nil)
		testutil.Equal(t, len(got), 0)
		if _, ok := a.needsInputFP["alt"]; !ok {
			t.Fatal("expected fingerprint recorded for the emulated alt-screen prompt")
		}
	})

	t.Run("second tick flags it once the emulated screen is stable", func(t *testing.T) {
		// Only the spinner chrome advanced (4s/✻ → 9s/✶); the rendered prompt
		// screen is unchanged.
		writeLog("alt", altFrame("9s", "✶"))
		got := a.detectNeedsInputSticky(nil, []string{"alt"}, nil)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0], "alt")
	})

	t.Run("a streaming alt-screen agent without a prompt is never flagged", func(t *testing.T) {
		b := &App{}
		writeLog("altbusy", "\x1b[?1049h\x1b[2J\x1b[2;5HApplying edit 1 of 3\x1b[3;5HRunning tests...")
		testutil.Equal(t, len(b.detectNeedsInputSticky(nil, []string{"altbusy"}, nil)), 0)
		writeLog("altbusy", "\x1b[?1049h\x1b[2J\x1b[2;5HApplying edit 2 of 3\x1b[3;5HRunning tests...")
		testutil.Equal(t, len(b.detectNeedsInputSticky(nil, []string{"altbusy"}, nil)), 0)
	})
}

// TestContentIdleSignalOf covers dedupe-redundant-contentidle-reads: the
// App.contentIdleSignalOf closure refreshTasksWithIDs hands to
// agent.ContentIdle as its cachedSignal parameter, sourced from
// a.needsInputRawSignals — the SAME per-tick cache detectNeedsInputSticky's
// resumed-activity (working + contentFP) and content-stability (parked)
// passes already populate moments earlier in the same tick.
func TestContentIdleSignalOf(t *testing.T) {
	t.Run("a session with a recorded tail reading is served straight from the cache", func(t *testing.T) {
		a := &App{needsInputRawSignals: map[string]needsInputRawSignals{
			"w": {hasTail: true, working: true, parked: false, contentFP: 12345},
		}}
		sig, ok := a.contentIdleSignalOf("w")
		testutil.Equal(t, ok, true)
		testutil.Equal(t, sig.FP, uint64(12345))
		testutil.Equal(t, sig.Working, true)
		testutil.Equal(t, sig.Parked, false)
	})

	t.Run("an id absent from the map falls back (e.g. an archived-but-running task)", func(t *testing.T) {
		a := &App{needsInputRawSignals: map[string]needsInputRawSignals{
			"other": {hasTail: true},
		}}
		_, ok := a.contentIdleSignalOf("archived-but-running")
		testutil.Equal(t, ok, false)
	})

	t.Run("an id recorded with an empty tail this tick falls back rather than serving zero values", func(t *testing.T) {
		a := &App{needsInputRawSignals: map[string]needsInputRawSignals{
			"empty": {hasTail: false},
		}}
		_, ok := a.contentIdleSignalOf("empty")
		testutil.Equal(t, ok, false)
	})

	t.Run("a nil map (detectNeedsInputSticky never ran) always falls back", func(t *testing.T) {
		a := &App{}
		_, ok := a.contentIdleSignalOf("w")
		testutil.Equal(t, ok, false)
	})
}

// TestRefreshTasksWithIDs_ContentIdleReusesStickySignals is an end-to-end
// differential test for dedupe-redundant-contentidle-reads: it drives the
// REAL refreshTasksWithIDs (detectNeedsInputSticky followed by the wired
// agent.ContentIdle(..., a.contentIdleSignalOf) call) against a fullscreen
// parked-prompt session and checks that the cache a.contentIdleSignalOf
// serves matches — value for value — an independent computation of
// agent.ContentIdleFingerprint/agent.ParkedSelectionSignal over the identical
// tail, proving the production wiring hands ContentIdle the SAME reading
// detectNeedsInputSticky already computed rather than something else.
func TestRefreshTasksWithIDs_ContentIdleReusesStickySignals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	const taskID = "parked"
	testutil.NoError(t, d.Add(&model.Task{ID: taskID, Name: "parked", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	logPath := agent.SessionLogPath(taskID)
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	const frame = "\x1b[?1049h\x1b[2J\x1b[1;1H✻ Brewed for 3s\x1b[3;5HDo you want to proceed?\x1b[5;5H1. Yes\x1b[6;5H2. No\x1b[5;3H❯\x1b[8;1H\x1b[?25l"
	testutil.NoError(t, os.WriteFile(logPath, []byte(frame), 0o644))

	// A fullscreen permission prompt: never raw-idle, so ContentIdle (not the
	// raw idle set) is what has to classify it. false: this test only wants
	// the sticky pass + cache wiring to run, not the cached-tasks gate.
	app.refreshTasksWithIDs([]string{taskID}, nil, false)

	sig, ok := app.contentIdleSignalOf(taskID)
	if !ok {
		t.Fatal("expected a cached content-idle signal after refreshTasksWithIDs ran detectNeedsInputSticky")
	}

	// Independent computation over the IDENTICAL tail bytes, completely
	// decoupled from the app's own needsInputScreen renderer instance.
	independentScreen := &agent.ScreenRenderer{}
	wantFP, wantWorking := agent.ContentIdleFingerprint(independentScreen, []byte(frame), int(agent.DefaultTermCols), int(agent.DefaultTermRows))
	wantParked := agent.ParkedSelectionSignal(independentScreen, []byte(frame), int(agent.DefaultTermCols), int(agent.DefaultTermRows))

	testutil.Equal(t, sig.FP, wantFP)
	testutil.Equal(t, sig.Working, wantWorking)
	testutil.Equal(t, sig.Parked, wantParked)
}

// fakeKickSession is a minimal agent.SessionHandle for driving
// App.maybeKickRerender without a real PTY. Only the fields the rerender
// predicate reads are meaningful; everything else returns zero values.
type fakeKickSession struct {
	idle       bool
	alive      bool
	initCols   int
	stopCalled atomic.Bool
}

func (f *fakeKickSession) PID() int                                              { return 0 }
func (f *fakeKickSession) WriteInput([]byte, agentview.InputOrigin) (int, error) { return 0, nil }
func (f *fakeKickSession) Resize(uint16, uint16) error                           { return nil }
func (f *fakeKickSession) RecentOutput() []byte                                  { return nil }
func (f *fakeKickSession) RecentOutputTail(int) []byte                           { return nil }
func (f *fakeKickSession) RecentOutputTailWithTotal(int) ([]byte, uint64)        { return nil, 0 }
func (f *fakeKickSession) TotalWritten() uint64                                  { return 0 }
func (f *fakeKickSession) IsIdle() bool                                          { return f.idle }
func (f *fakeKickSession) LastInput() time.Time                                  { return time.Time{} }
func (f *fakeKickSession) LastUserInput() time.Time                              { return time.Time{} }
func (f *fakeKickSession) Alive() bool                                           { return f.alive }
func (f *fakeKickSession) PTYSize() (int, int)                                   { return 0, 0 }
func (f *fakeKickSession) InitialPTYSize() (int, int)                            { return f.initCols, 24 }
func (f *fakeKickSession) Done() <-chan struct{}                                 { return make(chan struct{}) }
func (f *fakeKickSession) Err() error                                            { return nil }
func (f *fakeKickSession) WorkDir() string                                       { return "" }
func (f *fakeKickSession) Stop() error                                           { f.stopCalled.Store(true); return nil }
func (f *fakeKickSession) AddWriter(io.Writer)                                   {}
func (f *fakeKickSession) AddWriterFrom(io.Writer, uint64)                       {}
func (f *fakeKickSession) AddWriterFromTolerant(io.Writer, uint64)               {}
func (f *fakeKickSession) RemoveWriter(io.Writer)                                {}

func TestMaybeKickRerender_TUIDefersWhenBlockedOnPrompt(t *testing.T) {
	// End-to-end for the TUI's RerenderDeferPrompt switch branch: a genuine
	// resize (initCols 20 ≪ panel) on an idle session that's blocked on a
	// prompt must NOT kick — it must invalidate the attach cache and leave
	// no pending restart, so the question survives. Drives the real
	// maybeKickRerender goroutine + QueueUpdateDraw dispatch with a fake
	// session (no PTY, no idle-wait — IsIdle is forced true).
	t.Setenv("HOME", t.TempDir())
	const taskID = "tui-blocked"
	logPath := agent.SessionLogPath(taskID)
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	testutil.NoError(t, os.WriteFile(logPath, []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n"), 0o644))

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	task := &model.Task{ID: taskID, Name: "blocked", Status: model.StatusInProgress, SessionID: "sid-resume", Worktree: t.TempDir()}
	sess := &fakeKickSession{idle: true, alive: true, initCols: 20}

	// maybeKickRerender reads the panel rect on the tview goroutine, then
	// spawns its own goroutine that dispatches the decision via
	// QueueUpdateDraw — so invoke it on the tview goroutine.
	readUI(t, app.tapp, func() { app.maybeKickRerender(task, sess) })

	// Poll until the DeferPrompt side effects settle: attach cache cleared
	// (invalidateAttachCache) and no pending restart queued.
	deadline := time.Now().Add(uiTimeout)
	settled := false
	for time.Now().Before(deadline) {
		var cacheCleared, noPending bool
		readUI(t, app.tapp, func() {
			_, cached := app.lastAttachCols[taskID]
			cacheCleared = !cached
			noPending = !app.pendingRerenderRestart[taskID]
		})
		if cacheCleared && noPending {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !settled {
		t.Fatal("RerenderDeferPrompt side effects never settled (cache not invalidated or restart queued)")
	}
	// The blocked session must never be stopped — that's the dismissed-question bug.
	testutil.Equal(t, sess.stopCalled.Load(), false)
}

// TestMaybeKickRerenderAtWidth_KicksOnGenuineDrift drives the shared core
// (extracted from maybeKickRerender so the Hera view can supply its own
// panel width — see heraKickRerender) all the way to the RerenderKick
// branch: an idle session with no prompt marker and a width delta past
// RerenderMargin must be stopped, with pendingRerenderRestart set so the
// exit-handler resumes it. Regression coverage for the extraction — this
// exact branch previously had no TUI-level test (only the pure predicate in
// internal/agent/rerender_test.go and the DeferPrompt branch above).
func TestMaybeKickRerenderAtWidth_KicksOnGenuineDrift(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no session log — no prompt marker to find
	const taskID = "tui-kick"
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	task := &model.Task{ID: taskID, Name: "kick-me", Status: model.StatusInProgress, SessionID: "sid-resume", Worktree: t.TempDir()}
	sess := &fakeKickSession{idle: true, alive: true, initCols: 20}

	readUI(t, app.tapp, func() { app.maybeKickRerenderAtWidth(task, sess, 120, nil) })

	deadline := time.Now().Add(uiTimeout)
	stopped := false
	for time.Now().Before(deadline) {
		if sess.stopCalled.Load() {
			stopped = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("RerenderKick never called sess.Stop()")
	}
	readUI(t, app.tapp, func() {
		if !app.pendingRerenderRestart[taskID] {
			t.Error("pendingRerenderRestart not set after a successful kick")
		}
	})
}

// TestMaybeKickRerenderAtWidth_CallsOnDeferredWhenBusy is the BUG-077
// regression at the App layer: RerenderDeferBusy's own doc comment
// (internal/agent/rerender.go) requires the caller to "retry on the next
// opportunity" — this proves maybeKickRerenderAtWidth actually invokes the
// supplied onDeferred callback on that branch (a busy agent, width past the
// margin), which is the signal a Hera pane's kickedFor/pending bookkeeping
// depends on to ever get a second try. The main agent view passes nil here
// (see maybeKickRerender's doc comment) since it retries via its own
// re-entry path instead.
func TestMaybeKickRerenderAtWidth_CallsOnDeferredWhenBusy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const taskID = "tui-busy"
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	task := &model.Task{ID: taskID, Name: "busy", Status: model.StatusInProgress, SessionID: "sid-resume", Worktree: t.TempDir()}
	sess := &fakeKickSession{idle: false, alive: true, initCols: 20} // busy: not idle

	var deferred atomic.Bool
	readUI(t, app.tapp, func() {
		app.maybeKickRerenderAtWidth(task, sess, 120, func() { deferred.Store(true) })
	})

	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) && !deferred.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !deferred.Load() {
		t.Fatal("onDeferred was never invoked for a busy agent past the rerender margin")
	}
	testutil.Equal(t, sess.stopCalled.Load(), false) // must not kill a busy agent
}

// TestMaybeKickRerenderAtWidth_LaterNarrowRebindStillKicksAfterDeferredWideBind
// is the fix-committed-width-drift regression (BUG-078), reproducing the live
// ux.log evidence exactly: a session starts at initCols=80, a pane binds WIDE
// (142) while the agent is busy — the predicate matches but defers, and it
// never lands (mirrors 15 real minutes of "busy"/"blocked on prompt" retries
// with the agent never going idle at 142). The pane then moves on; LATER, a
// DIFFERENT bind lands at 90 — close enough to initCols (80) that the margin
// check alone would skip (delta 10 < RerenderMargin), and ALSO the exact
// width some UNRELATED earlier viewer already cached via isRedundantAttach
// (simulated directly here, matching the real trace: the main agent view had
// independently visited this task at 90). Before the fix, BOTH gates silently
// swallowed this forever — the coordinator's pane stayed permanently garbled.
// After the fix, committedCols (recorded from the deferred 142 bind) makes
// both gates recognize the drift and the kick fires.
func TestMaybeKickRerenderAtWidth_LaterNarrowRebindStillKicksAfterDeferredWideBind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const taskID = "tui-drift"
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	task := &model.Task{ID: taskID, Name: "drift", Status: model.StatusInProgress, SessionID: "sid-resume", Worktree: t.TempDir()}
	sess := &fakeKickSession{idle: false, alive: true, initCols: 80} // busy: bind-142 defers

	var deferred atomic.Bool
	readUI(t, app.tapp, func() {
		app.maybeKickRerenderAtWidth(task, sess, 142, func() { deferred.Store(true) })
	})
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) && !deferred.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !deferred.Load() {
		t.Fatal("bind at 142 while busy never deferred — test setup is broken")
	}
	testutil.Equal(t, sess.stopCalled.Load(), false) // never kicked — stayed deferred, as in the live repro

	readUI(t, app.tapp, func() {
		if got := app.committedCols[taskID]; got != 142 {
			t.Errorf("committedCols after a deferred wide bind = %d, want 142", got)
		}
		// Simulate an UNRELATED earlier viewer (e.g. the main agent view, or a
		// different Hera pane) independently caching this EXACT width from a
		// visit that didn't cross the margin against initCols (80 vs 90 =
		// delta 10 < 15). Without the committedCols-aware bypass, this alone
		// would make isRedundantAttach short-circuit the next check forever.
		app.lastAttachCols[taskID] = 90
	})

	// The agent goes idle and a pane rebinds narrower (90) — still close to
	// initCols (80) but nowhere near the 142 the content actually committed
	// to while busy.
	sess.idle = true
	readUI(t, app.tapp, func() { app.maybeKickRerenderAtWidth(task, sess, 90, nil) })

	deadline = time.Now().Add(uiTimeout)
	stopped := false
	for time.Now().Before(deadline) {
		if sess.stopCalled.Load() {
			stopped = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("a later narrow rebind (90) did not kick despite the session's committed width having drifted to 142 during a deferred wide bind")
	}
	readUI(t, app.tapp, func() {
		if _, cached := app.committedCols[taskID]; cached {
			t.Error("committedCols should be cleared once a kick actually fires")
		}
	})
}

// TestHeraKickRerender_UnknownTaskIsNoop proves the Hera-facing entry point
// no-ops cleanly (no panic, no decision made) when the task ID doesn't
// resolve — e.g. a stale/torn-down binding racing the callback.
func TestHeraKickRerender_UnknownTaskIsNoop(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.heraKickRerender("does-not-exist", 88, nil) // must not panic
}

// TestHeraKickRerender_NoRunnerSessionIsNoop proves a task that exists in the
// DB but has no live runner session (e.g. a completed/dead task shown in a
// Hera pane via replay) is a clean no-op — mirrors maybeKickRerenderAtWidth's
// own nil-session guard, exercised through the Hera entry point specifically.
func TestHeraKickRerender_NoRunnerSessionIsNoop(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{Name: "dead-in-hera", Status: model.StatusComplete, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task))
	app.heraKickRerender(task.ID, 88, nil) // no runner session registered — must not panic
}

func TestHandleSessionExitUI_SkipsTransitionWhenPendingRestart(t *testing.T) {
	// Regression test for the TUI-during-API-kick race: if a kick-restart is
	// in flight, handleSessionExitUI must not flip the row to InReview —
	// otherwise the resumed session runs with the wrong status. Replaces the
	// previous fix that synchronously RPC'd HasPendingRestart from the tview
	// main goroutine; pendingRestart now arrives as an arg captured by the
	// caller from a non-RPC source.
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{Name: "kick-deferred", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task))

	app.handleSessionExitUI(task.ID, false /* cleanExit */, true /* pendingRestart */)

	fresh, _ := d.Get(task.ID)
	if fresh == nil {
		t.Fatal("task disappeared")
	}
	if fresh.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress when pendingRestart=true, got %s", fresh.Status)
	}

	// Same skip behavior even on a clean exit during a kick window (rare but
	// valid) — pendingRestart always wins, the row stays InProgress.
	task2 := &model.Task{Name: "kick-pending-clean", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task2))
	app.handleSessionExitUI(task2.ID, true /* cleanExit */, true /* pendingRestart */)
	fresh2, _ := d.Get(task2.ID)
	if fresh2.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress when pendingRestart=true and cleanExit=true, got %s", fresh2.Status)
	}

	// Without pendingRestart, a non-clean exit (stop/crash/fast-fail) → InReview.
	task3 := &model.Task{Name: "non-clean-review", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task3))
	app.handleSessionExitUI(task3.ID, false /* cleanExit */, false /* pendingRestart */)
	fresh3, _ := d.Get(task3.ID)
	if fresh3.Status != model.StatusInReview {
		t.Errorf("expected status InReview on non-clean exit, got %s", fresh3.Status)
	}

	// Without pendingRestart, a clean self-exit → Complete.
	task4 := &model.Task{Name: "clean-complete", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task4))
	app.handleSessionExitUI(task4.ID, true /* cleanExit */, false /* pendingRestart */)
	fresh4, _ := d.Get(task4.ID)
	if fresh4.Status != model.StatusComplete {
		t.Errorf("expected status Complete on clean exit, got %s", fresh4.Status)
	}
}

// TestHandleSessionExitUI_ClaudeRefreshesSessionID pins the /clear fix end to
// end in the TUI: a Claude task with a pinned SessionID exits, a newer
// transcript (the post-/clear conversation) sits in ~/.claude/projects, and the
// background capture goroutine must persist the newer UUID via QueueUpdateDraw.
func TestHandleSessionExitUI_ClaudeRefreshesSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app) // running event loop so QueueUpdateDraw fires
	defer stop()

	wt := filepath.Join(home, "claude-tui-wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	// Mirror claudeEncodeCwd (package-private to agent): non-alphanumeric → '-'.
	enc := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, wt)
	projDir := filepath.Join(home, ".claude", "projects", enc)
	testutil.NoError(t, os.MkdirAll(projDir, 0o755))

	original := "11111111-1111-7111-9111-111111111111"
	postClear := "22222222-2222-7222-9222-222222222222"
	orig := filepath.Join(projDir, original+".jsonl")
	newer := filepath.Join(projDir, postClear+".jsonl")
	testutil.NoError(t, os.WriteFile(orig, []byte("{}\n"), 0o644))
	testutil.NoError(t, os.WriteFile(newer, []byte("{}\n"), 0o644))
	past := time.Now().Add(-1 * time.Hour)
	testutil.NoError(t, os.Chtimes(orig, past, past))

	task := &model.Task{Name: "claude-clear", Status: model.StatusInProgress, Worktree: wt, Backend: "claude", SessionID: original}
	testutil.NoError(t, d.Add(task))

	app.handleSessionExitUI(task.ID, false /* cleanExit */, false /* pendingRestart */)

	// Capture runs in a goroutine then persists via QueueUpdateDraw; poll until
	// the row reflects the refreshed UUID (or time out).
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if got, _ := d.Get(task.ID); got != nil && got.SessionID == postClear {
			break
		}
		syncUI(t, app.tapp)
	}
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, postClear)
}

func TestNew(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	if app.tapp == nil {
		t.Error("tview.Application should not be nil")
	}
	if app.header == nil {
		t.Error("header should not be nil")
	}
	if app.statusbar == nil {
		t.Error("statusbar should not be nil")
	}
	if app.tasklist == nil {
		t.Error("tasklist should not be nil")
	}
	if app.mode != modeTaskList {
		t.Errorf("initial mode = %v, want modeTaskList", app.mode)
	}
	if app.daemonConnected {
		t.Error("daemonConnected should be false")
	}
}

func TestSwitchTab(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.switchTab(widget.TabSettings)
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings", app.header.ActiveTab())
	}

	app.switchTab(widget.TabTasks)
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks", app.header.ActiveTab())
	}
}

func TestOnTaskSelect(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:   "test-1",
		Name: "test task",
	}

	app.onTaskSelect(task, true)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent", app.mode)
	}
	if app.agentState.TaskID != "test-1" {
		t.Errorf("agentState.TaskID = %q, want %q", app.agentState.TaskID, "test-1")
	}
}

func TestOnTaskSelectAutoStart(t *testing.T) {
	t.Run("auto-start without session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:   "t-no-sid",
			Name: "no session id",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Auto-start was attempted — the runner.Start will fail (no worktree),
		// which reverts the task to Pending. Proves auto-start was triggered
		// even without a SessionID.
		got, _ := d.Get("t-no-sid")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
	})

	t.Run("no auto-start for completed task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-complete",
			Name:      "completed task",
			SessionID: "sess-123",
		}
		task.SetStatus(model.StatusComplete)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Completed tasks should not auto-start.
		got, _ := d.Get("t-complete")
		if got.Status != model.StatusComplete {
			t.Errorf("status = %v, want Complete", got.Status)
		}
	})

	t.Run("auto-start for in-review task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-resume",
			Name:      "resumable task",
			SessionID: "sess-456",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// startSession was attempted — the runner.Start will fail (no
		// worktree), which reverts the task to Pending. Verify the revert
		// happened (proves auto-start was triggered).
		got, _ := d.Get("t-resume")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
	})

	t.Run("no auto-start for archived task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-archived",
			Name:      "archived task",
			SessionID: "sess-arc",
			Archived:  true,
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Archived tasks should not auto-start.
		got, _ := d.Get("t-archived")
		if got.Status != model.StatusInReview {
			t.Errorf("status = %v, want InReview (archived tasks should not auto-start)", got.Status)
		}
	})

	t.Run("auto-start for pending task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-pending",
			Name:      "pending resumable",
			SessionID: "sess-789",
		}
		task.SetStatus(model.StatusPending)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// startSession was attempted — verifies auto-start triggers for
		// Pending tasks with a SessionID (daemon restart scenario). After a
		// failed start, the task reverts to Pending but the pre-existing
		// SessionID must be preserved so the next retry can --resume.
		got, _ := d.Get("t-pending")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
		if got.SessionID != "sess-789" {
			t.Errorf("SessionID = %q, want %q preserved across failed restart", got.SessionID, "sess-789")
		}
	})

	t.Run("failed restart preserves pre-existing SessionID but clears self-generated one", func(t *testing.T) {
		// Regression for the "lost-conversation after daemon+TUI reboot" bug.
		// When startSession generates the SessionID in this call and Start
		// then fails, the ID must be cleared (it points to nothing). When the
		// SessionID was already on the task before this call (e.g., a Claude
		// task being resumed after a daemon restart), Start failure must
		// preserve it so the next retry can --resume the conversation.

		t.Run("preserves pre-existing", func(t *testing.T) {
			d := testDB(t)
			runner := agent.NewRunner(nil)
			app := New(d, runner, false)

			task := &model.Task{
				ID:        "t-resume-fail",
				Name:      "resume failure",
				SessionID: "sess-preexisting",
				Project:   "p",
			}
			task.SetStatus(model.StatusInReview)
			d.Add(task) //nolint:errcheck

			app.startSession(task) // runner.Start will fail (no worktree)

			got, _ := d.Get("t-resume-fail")
			if got.SessionID != "sess-preexisting" {
				t.Errorf("SessionID = %q, want %q preserved", got.SessionID, "sess-preexisting")
			}
			if got.Status != model.StatusPending {
				t.Errorf("status = %v, want Pending", got.Status)
			}
		})

		t.Run("clears self-generated", func(t *testing.T) {
			d := testDB(t)
			runner := agent.NewRunner(nil)
			app := New(d, runner, false)

			task := &model.Task{
				ID:      "t-fresh-fail",
				Name:    "fresh failure",
				Project: "p", // no SessionID — startSession will generate one
			}
			task.SetStatus(model.StatusPending)
			d.Add(task) //nolint:errcheck

			app.startSession(task) // runner.Start will fail (no worktree)

			got, _ := d.Get("t-fresh-fail")
			if got.SessionID != "" {
				t.Errorf("SessionID = %q, want cleared (self-generated, never used)", got.SessionID)
			}
		})
	})

	t.Run("no auto-start when autoStart is false", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-navigate",
			Name:      "navigate target",
			SessionID: "sess-nav",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, false)

		// autoStart=false suppresses session start (used by navigateAgentTask).
		got, _ := d.Get("t-navigate")
		if got.Status != model.StatusInReview {
			t.Errorf("status = %v, want InReview (autoStart=false should not start)", got.Status)
		}
	})
}

// TestOnTaskSelectAutoStart_SkipsClosedOutHeraWorker is the plain-Tasks-tab
// half of the Frontend Parity fix (add-enter-closeout-guard-parity): viewing
// a hera worker/freelance task awaiting coordinator close-out must NOT
// auto-revive its dead session, exactly like the Hera tab (which never
// auto-restarts on mere navigation either — only an explicit Enter attempts
// reattach, see heraReattach). Before this fix, onTaskSelect's autoStart
// path had no closed-out awareness at all and would silently flip the task
// straight to InProgress.
func TestOnTaskSelectAutoStart_SkipsClosedOutHeraWorker(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.SetStatus("tw", model.StatusInReview))
	testutil.NoError(t, d.SetMeta("tw", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

	task, err := d.Get("tw")
	testutil.NoError(t, err)

	app.onTaskSelect(task, true)

	// No start attempt at all — status is untouched (not flipped to
	// InProgress, and not reverted to Pending by a failed-start path either).
	got, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	if app.runner.Get("tw") != nil {
		t.Error("no session should have been started for a closed-out task")
	}
	// Mirrors the Hera tab: mere navigation does not arm the banner either —
	// only an explicit Enter (handleAgentKey) does.
	testutil.Equal(t, app.agentPane.ClosedOutBannerShown(), false)
}

// TestHandleAgentKey_EnterOnClosedOutHeraWorkerTogglesBanner is the plain-
// Tasks-tab sibling of TestSmoke_HeraReattachClosedOutTogglesBannerThenReadOnly
// (heraactions_test.go) — the SAME task reached through the flat Tasks tab
// must refuse an Enter-to-restart the same way the Hera tab does, on the
// SAME a.agentPane the flat tab actually renders (not a.heraPage.AgentPane(),
// which this tab never binds).
func TestHandleAgentKey_EnterOnClosedOutHeraWorkerTogglesBanner(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.SetStatus("tw", model.StatusComplete))
	testutil.NoError(t, d.SetMeta("tw", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

	app.mode = modeAgent
	app.agentState.Reset("tw", "wkr")
	app.agentPane.SetTaskID("tw")

	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	app.handleAgentKey(ev) // first Enter → should arm the banner, not start

	testutil.Equal(t, app.agentPane.ClosedOutBannerShown(), true)
	testutil.Contains(t, app.statusbar.Error(), "closed out")
	if app.runner.Get("tw") != nil {
		t.Error("no session should have been started for a closed-out task")
	}

	app.handleAgentKey(ev) // second, immediately-following Enter → dismisses

	testutil.Equal(t, app.agentPane.ClosedOutBannerShown(), false)
	testutil.Contains(t, app.statusbar.Info(), "read-only")

	// Neither press touched the task: no session started, status untouched.
	got, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "")
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestExitAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	// A transient info notice (e.g. the remote-fork "context not carried"
	// message) must be cleared on exit so it doesn't linger on the task list.
	app.statusbar.SetInfo("Forked (remote: source context not carried)")
	app.exitAgentView()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
	testutil.Equal(t, app.statusbar.Info(), "")
}

func TestTcellKeyToBytes(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		rune rune
		mod  tcell.ModMask
		want []byte
	}{
		{"enter", tcell.KeyEnter, 0, 0, []byte{'\r'}},
		{"shift-enter", tcell.KeyEnter, 0, tcell.ModShift, []byte{0x1b, '\r'}},
		{"alt-enter", tcell.KeyEnter, 0, tcell.ModAlt, []byte{0x1b, '\r'}},
		{"tab", tcell.KeyTab, 0, 0, []byte{'\t'}},
		{"shift-tab", tcell.KeyBacktab, 0, 0, []byte("\x1b[Z")},
		{"backspace", tcell.KeyBackspace2, 0, 0, []byte{0x7f}},
		{"up", tcell.KeyUp, 0, 0, []byte("\x1b[A")},
		{"down", tcell.KeyDown, 0, 0, []byte("\x1b[B")},
		{"right", tcell.KeyRight, 0, 0, []byte("\x1b[C")},
		{"left", tcell.KeyLeft, 0, 0, []byte("\x1b[D")},
		{"ctrl-c", tcell.KeyCtrlC, 0, 0, []byte{0x03}},
		{"ctrl-d", tcell.KeyCtrlD, 0, 0, []byte{0x04}},
		{"escape", tcell.KeyEscape, 0, 0, []byte{0x1b}},
		{"rune-a", tcell.KeyRune, 'a', 0, []byte("a")},
		{"rune-alt-a", tcell.KeyRune, 'a', tcell.ModAlt, []byte{0x1b, 'a'}},
		{"delete", tcell.KeyDelete, 0, 0, []byte("\x1b[3~")},
		// Alt+arrow keys for word navigation
		{"alt-left", tcell.KeyLeft, 0, tcell.ModAlt, []byte("\x1b[1;3D")},
		{"alt-right", tcell.KeyRight, 0, tcell.ModAlt, []byte("\x1b[1;3C")},
		{"alt-up", tcell.KeyUp, 0, tcell.ModAlt, []byte("\x1b[1;3A")},
		{"alt-down", tcell.KeyDown, 0, tcell.ModAlt, []byte("\x1b[1;3B")},
		{"alt-backspace", tcell.KeyBackspace2, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
		{"alt-delete", tcell.KeyDelete, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, tt.rune, tt.mod)
			got := tcellKeyToBytes(ev)
			if string(got) != string(tt.want) {
				t.Errorf("tcellKeyToBytes(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestArrowKeysDoNotSwitchTabs pins the removal of arrow-key tab navigation.
// Left/Right no longer cycle the top-level tabs (they conflicted with horizontal
// navigation inside views like the Hera rail); tab switching is via 1/2/3 only.
// Settings retains Left/Right for its own rail↔pane navigation.
func TestArrowKeysDoNotSwitchTabs(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	if app.header.ActiveTab() != widget.TabTasks {
		t.Fatalf("initial tab = %v, want widget.TabTasks", app.header.ActiveTab())
	}

	// Right arrow on Tasks → does NOT advance the tab, and is not consumed
	// globally (returned to tview for the focused view to handle).
	right := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	if result := app.handleGlobalKey(right); result != right {
		t.Error("right arrow should fall through (return the event), not be consumed for tab switching")
	}
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks (right must not switch tabs)", app.header.ActiveTab())
	}

	// Left arrow on Tasks → no tab change, falls through.
	left := tcell.NewEventKey(tcell.KeyLeft, 0, 0)
	if result := app.handleGlobalKey(left); result != left {
		t.Error("left arrow should fall through, not be consumed for tab switching")
	}
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks (left must not switch tabs)", app.header.ActiveTab())
	}

	// 1/2/3 remain the way to switch tabs.
	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, '2', 0))
	if app.header.ActiveTab() != widget.TabHera {
		t.Errorf("tab = %v, want widget.TabHera after '2'", app.header.ActiveTab())
	}

	// Right AND Left on the Hera tab do not switch tabs either (freed for the
	// rail). In the old scheme Left from TabHera switched to TabTasks.
	if result := app.handleGlobalKey(left); result != left {
		t.Error("left arrow on Hera tab should fall through")
	}
	if app.header.ActiveTab() != widget.TabHera {
		t.Errorf("tab = %v, want widget.TabHera (left must not switch tabs)", app.header.ActiveTab())
	}
	if result := app.handleGlobalKey(right); result != right {
		t.Error("right arrow on Hera tab should fall through")
	}
	if app.header.ActiveTab() != widget.TabHera {
		t.Errorf("tab = %v, want widget.TabHera (right must not switch tabs)", app.header.ActiveTab())
	}

	// On the Settings tab, Left/Right still drive the settings rail↔pane focus
	// (consumed via the settings routing) without switching tabs. A fresh
	// SettingsView starts focused on the pane (settings.go).
	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, '3', 0))
	if app.header.ActiveTab() != widget.TabSettings {
		t.Fatalf("tab = %v, want widget.TabSettings after '3'", app.header.ActiveTab())
	}
	// Left from the pane → back to the rail; consumed, tab unchanged.
	if result := app.handleGlobalKey(left); result != nil {
		t.Error("left arrow on settings pane should be consumed by settings")
	}
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings (settings consumes left)", app.header.ActiveTab())
	}
	// Left AGAIN, now from the rail (left-most) → NOT consumed (settings
	// declines), falls through to tview, and crucially does NOT switch to the
	// previous tab the way the old arrow-nav did. This is the one intentional
	// behavior change for Settings.
	if result := app.handleGlobalKey(left); result != left {
		t.Error("left arrow on settings rail should fall through, not switch tabs")
	}
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings (left from rail must NOT switch to previous tab)", app.header.ActiveTab())
	}
	// Right from the rail → focus the pane; consumed, tab unchanged.
	if result := app.handleGlobalKey(right); result != nil {
		t.Error("right arrow on settings rail should be consumed by settings")
	}
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings (settings consumes right)", app.header.ActiveTab())
	}
}

func TestCtrlCForwardsToAgentPTY(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Start a real process so we have a live session.
	task := &model.Task{
		ID:       "ctrl-c-test",
		Name:     "ctrl-c-test",
		Status:   model.StatusInProgress,
		Worktree: t.TempDir(),
		Backend:  "test",
	}
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatalf("runner.Start: %v", err)
	}
	defer runner.Stop(task.ID)

	// Enter agent mode with the session wired up
	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	app.agentPane.SetSession(sess)

	if !sess.Alive() {
		t.Fatal("session should be alive")
	}

	// ctrl+c in agent mode with live session should be consumed (forwarded to PTY)
	// and NOT stop the app.
	ev := tcell.NewEventKey(tcell.KeyCtrlC, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("ctrl+c in agent mode with live session should be consumed")
	}
	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after ctrl+c with live session", app.mode)
	}
}

func TestCtrlCNoopInAgentViewDeadSession(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Agent mode with no session — ctrl+c should be consumed but not exit
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	ev := tcell.NewEventKey(tcell.KeyCtrlC, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("ctrl+c in agent mode with dead session should be consumed")
	}
	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after ctrl+c with no session", app.mode)
	}
}

func TestCtrlDExitsAgentViewWhenSessionDead(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// No session running — ctrl+d should exit agent view
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	app.handleAgentKey(ev)

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList after ctrl+d with no session", app.mode)
	}
}

func TestEscapeStaysInAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal

	// No session running — escape should be consumed, NOT exit agent view
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, 0)
	result := app.handleAgentKey(ev)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after escape with no session", app.mode)
	}
	if result != nil {
		t.Error("escape should return nil (consumed), not pass through to tview")
	}
}

func TestCtrlLOpensLinkPicker(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	result := app.handleAgentKey(tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModNone))
	if result != nil {
		t.Error("Ctrl+L should return nil (consumed)")
	}
}

func TestFilePanelKeyRouting(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Enter agent mode with file panel focused
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusFiles
	app.filePanel.SetFocused(true)

	// Set the file panel rect so CursorDown can compute visible rows
	app.filePanel.SetRect(0, 0, 40, 20)

	// Populate files
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	app.filePanel.SetFiles(files)

	// Verify initial state
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Fatalf("initial selected file = %v, want a.go", f)
	}

	// Press Down arrow — should move cursor to b.go
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down arrow in file panel should be consumed (return nil)")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after Down: selected = %v, want b.go", f)
	}

	// Press Up arrow — should move cursor back to a.go
	ev = tcell.NewEventKey(tcell.KeyUp, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Up arrow in file panel should be consumed (return nil)")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("after Up: selected = %v, want a.go", f)
	}
}

func TestDiffModeArrowsNavigateFiles(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Enter agent mode
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal
	app.filePanel.SetRect(60, 0, 40, 20)

	// Populate files
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	app.filePanel.SetFiles(files)

	// Enter diff mode (simulate viewing a.go's diff)
	app.agentPane.EnterDiffMode("+line1\n-line2\n context", "a.go")
	if !app.agentPane.InDiffMode() {
		t.Fatal("should be in diff mode")
	}

	// Verify cursor starts on a.go
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Fatalf("initial = %v, want a.go", f)
	}

	// Press Down arrow — should move file cursor to b.go (not scroll diff)
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down in diff mode should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after Down: selected = %v, want b.go", f)
	}

	// Press Up arrow — should move file cursor back to a.go
	ev = tcell.NewEventKey(tcell.KeyUp, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Up in diff mode should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("after Up: selected = %v, want a.go", f)
	}
}

func TestFilePanelMouseFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Enter agent mode with terminal focused (default)
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal

	// Set up file panel with rect and files
	app.filePanel.SetRect(60, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
	}
	app.filePanel.SetFiles(files)

	// Simulate clicking on the file panel — OnClick should switch agentFocus
	if app.filePanel.OnClick == nil {
		t.Fatal("OnClick callback not wired")
	}
	app.filePanel.OnClick()

	if app.agentFocus != focusFiles {
		t.Errorf("after click: agentFocus = %v, want focusFiles", app.agentFocus)
	}
	if !app.filePanel.Focused() {
		t.Error("after click: file panel should be focused")
	}

	// Now Up/Down should navigate files (key routing test)
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down arrow after mouse focus should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after click+Down: selected = %v, want b.go", f)
	}

	// Click on terminal pane should switch focus back
	if app.agentPane.OnClick == nil {
		t.Fatal("TerminalPane OnClick not wired")
	}
	app.agentPane.OnClick()

	if app.agentFocus != focusTerminal {
		t.Errorf("after terminal click: agentFocus = %v, want focusTerminal", app.agentFocus)
	}
}

// TestArrowsRoutedToAgentInAgentMode pins that in agent mode Left/Right are NOT
// consumed for tab switching but are routed to handleAgentKey (which forwards
// them to the PTY when a session is live). With no live session here,
// handleAgentKey returns the event — so handleGlobalKey returns it too, proving
// the global handler did not swallow the key. This was byte-identical before and
// after removing the global arrow tab-nav (the old KeyLeft/KeyRight cases only
// acted when mode != modeAgent), and the assertion makes that contract explicit.
func TestArrowsRoutedToAgentInAgentMode(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	for _, k := range []tcell.Key{tcell.KeyRight, tcell.KeyLeft} {
		ev := tcell.NewEventKey(k, 0, 0)
		got := app.handleGlobalKey(ev)
		// Not consumed by global tab nav — falls through to handleAgentKey,
		// which (no live session) returns the event unchanged.
		if got != ev {
			t.Errorf("key %v: handleGlobalKey should route to agent (return event), got %v", k, got)
		}
		// And the tab must not change.
		if app.header.ActiveTab() != widget.TabTasks {
			t.Errorf("key %v: tab changed in agent mode: %v", k, app.header.ActiveTab())
		}
	}
}

// ptySizeForPanel is tested inline below.

func TestRefreshTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Add a task
	task := &model.Task{
		ID:        "t1",
		Name:      "task one",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)

	app.refreshTasks()

	if len(app.tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(app.tasks))
	}
	if !app.tasklist.HasTasks() {
		t.Error("tasklist should have tasks")
	}
}

func TestConfirmDeleteModal(t *testing.T) {
	task := &model.Task{
		ID:       "t1",
		Name:     "test task",
		Worktree: "/some/path",
		Branch:   "argus/test-task",
	}

	t.Run("cancel", func(t *testing.T) {
		m := modal.NewConfirmDeleteModal(task)
		if m.Confirmed() || m.Canceled() {
			t.Error("modal should not be confirmed or canceled initially")
		}

		// Press Esc
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})

		if !m.Canceled() {
			t.Error("modal should be canceled after Esc")
		}
		if m.Confirmed() {
			t.Error("modal should not be confirmed after Esc")
		}
	})

	t.Run("ctrl+q cancels", func(t *testing.T) {
		m := modal.NewConfirmDeleteModal(task)

		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})

		if !m.Canceled() {
			t.Error("modal should be canceled after Ctrl+Q")
		}
		if m.Confirmed() {
			t.Error("modal should not be confirmed after Ctrl+Q")
		}
	})

	t.Run("confirm", func(t *testing.T) {
		m := modal.NewConfirmDeleteModal(task)

		// Press Enter
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})

		if !m.Confirmed() {
			t.Error("modal should be confirmed after Enter")
		}
		if m.Canceled() {
			t.Error("modal should not be canceled after Enter")
		}
	})

	t.Run("task preserved", func(t *testing.T) {
		m := modal.NewConfirmDeleteModal(task)
		if m.Task().ID != "t1" {
			t.Errorf("Task().ID = %q, want %q", m.Task().ID, "t1")
		}
	})
}

func TestOpenConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	app.openConfirmDelete(task)

	if app.mode != modeConfirmDelete {
		t.Errorf("mode = %v, want modeConfirmDelete", app.mode)
	}
	if app.confirmDeleteModal == nil {
		t.Error("confirmDeleteModal should not be nil")
	}
}

func TestCloseConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	// Open then close
	app.openConfirmDelete(task)
	app.closeConfirmDelete()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
	if app.confirmDeleteModal != nil {
		t.Error("confirmDeleteModal should be nil after close")
	}
}

func TestOpenHelp(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	app.openHelp()

	if app.mode != modeHelp {
		t.Errorf("mode = %v, want modeHelp", app.mode)
	}
	if app.helpModal == nil {
		t.Error("helpModal should not be nil")
	}
	// Calling again is a no-op (no second modal allocated).
	first := app.helpModal
	app.openHelp()
	if app.helpModal != first {
		t.Error("openHelp must be idempotent while modal is visible")
	}
}

func TestCloseHelp(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	app.openHelp()
	app.closeHelp()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
	if app.helpModal != nil {
		t.Error("helpModal should be nil after close")
	}
	if app.helpPrevPage != "" {
		t.Errorf("helpPrevPage = %q, want empty", app.helpPrevPage)
	}
}

func TestHandleHelpKeyClosesOnEscape(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	app.openHelp()
	app.handleHelpKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
	if app.helpModal != nil {
		t.Error("helpModal should be nil after Esc")
	}
}

func TestCloseHelpRestoresActiveTab(t *testing.T) {
	for _, tc := range []struct {
		name string
		tab  widget.Tab
	}{
		{"tasks", widget.TabTasks},
		{"hera", widget.TabHera},
		{"settings", widget.TabSettings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDB(t)
			app := New(d, agent.NewRunner(nil), false)
			app.switchTab(tc.tab)
			app.openHelp()
			app.closeHelp()
			if app.mode != modeTaskList {
				t.Errorf("mode = %v, want modeTaskList", app.mode)
			}
			if app.header.ActiveTab() != tc.tab {
				t.Errorf("active tab = %v, want %v after close", app.header.ActiveTab(), tc.tab)
			}
		})
	}
}

func TestDeleteTask(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	if len(app.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(app.tasks))
	}

	app.deleteTask(task)

	if len(app.tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(app.tasks))
	}

	// Verify task is gone from DB
	tasks, _ := d.Tasks()
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in DB, got %d", len(tasks))
	}
}

func TestRefreshTasksLocal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	d.Add(&model.Task{ID: "t1", Name: "task1", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "task2", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	if len(app.tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(app.tasks))
	}

	// Delete from DB, then use refreshTasksLocal (no RPC)
	d.Delete("t1")
	app.refreshTasksLocal()

	if len(app.tasks) != 1 {
		t.Errorf("expected 1 task after local refresh, got %d", len(app.tasks))
	}
	if app.tasks[0].ID != "t2" {
		t.Errorf("expected t2, got %s", app.tasks[0].ID)
	}
}

// countingTaskStore wraps a *db.DB and counts Tasks() calls, so a test can
// prove the add-tasks-fetch-dirty-check gate actually skipped (or didn't
// skip) the underlying fetch — not just that the returned task list happens
// to be right, which would be true either way.
type countingTaskStore struct {
	*db.DB
	tasksCalls int
}

var _ store.Store = (*countingTaskStore)(nil)

func (c *countingTaskStore) Tasks() ([]*model.Task, error) {
	c.tasksCalls++
	return c.DB.Tasks()
}

// TestRefreshTasksWithIDs_GatedFetch covers add-tasks-fetch-dirty-check: the
// onTick-driven path (allowCachedTasks=true) skips the full a.db.Tasks()
// fetch when the store's PRAGMA data_version fingerprint hasn't moved since
// the last fetch, and correctly refetches once a genuinely EXTERNAL write (a
// different connection to the same file — the daemon, the REST API, another
// argus process) bumps it. Mirrors hera.HeraPage's own shouldRebuild tests.
func TestRefreshTasksWithIDs_GatedFetch(t *testing.T) {
	t.Run("skips the fetch across repeated ticks with no DB change", func(t *testing.T) {
		d := testDB(t)
		wrapped := &countingTaskStore{DB: d}
		app := New(wrapped, agent.NewRunner(nil), false) // New() itself does one initial (ungated) fetch
		baseline := wrapped.tasksCalls

		testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "task1", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
		// Prime the snapshot with an UNGATED call — mirrors the real
		// post-mutation refresh a UI handler makes right after its own write
		// (this is what a live mutation always pairs with; a gated call alone
		// can't be expected to see a same-connection write it has no way to
		// observe — see shouldRefetchTasks's doc comment).
		app.refreshTasksWithIDs(nil, nil, false)
		testutil.Equal(t, wrapped.tasksCalls, baseline+1)
		testutil.Equal(t, len(app.tasks), 1)

		// Repeated GATED calls with nothing changed since: must keep skipping
		// and reusing the cached snapshot, not re-fetch every tick.
		for i := 0; i < 3; i++ {
			app.refreshTasksWithIDs(nil, nil, true)
			testutil.Equal(t, wrapped.tasksCalls, baseline+1)
			testutil.Equal(t, len(app.tasks), 1)
		}
	})

	t.Run("a genuinely external write is detected and triggers a refetch", func(t *testing.T) {
		// A real file-backed DB (not OpenInMemory's private-per-connection
		// :memory:) so a SECOND connection can exercise a true cross-connection
		// write — see db.TestDB_DataVersion.
		dir := t.TempDir()
		path := filepath.Join(dir, "data.sql")
		d1, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d1.Close() })
		d2, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d2.Close() })

		wrapped := &countingTaskStore{DB: d1}
		app := New(wrapped, agent.NewRunner(nil), false)
		baseline := wrapped.tasksCalls

		testutil.NoError(t, d1.Add(&model.Task{ID: "t1", Name: "task1", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
		// Prime via d1's own connection with an ungated call (same-connection
		// blind-spot rationale as above).
		app.refreshTasksWithIDs(nil, nil, false)
		testutil.Equal(t, wrapped.tasksCalls, baseline+1)
		testutil.Equal(t, len(app.tasks), 1)

		// Nothing changed: the very next gated tick must still skip.
		app.refreshTasksWithIDs(nil, nil, true)
		testutil.Equal(t, wrapped.tasksCalls, baseline+1)

		// A write through the OTHER connection to the same file — genuinely
		// external, and (unlike d1's own writes above) correctly visible via
		// d1's own DataVersion() reads (db.TestDB_DataVersion).
		testutil.NoError(t, d2.Add(&model.Task{ID: "t2", Name: "task2", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))

		app.refreshTasksWithIDs(nil, nil, true)
		testutil.Equal(t, wrapped.tasksCalls, baseline+2)
		testutil.Equal(t, len(app.tasks), 2)
	})

	t.Run("allowCachedTasks=false always fetches, regardless of DB state", func(t *testing.T) {
		// Every non-onTick caller (refreshTasksLocal, refreshTasksAsync, a
		// direct UI mutation reacting to its own write) must keep this
		// pre-existing unconditional-fetch behavior — see shouldRefetchTasks's
		// doc comment for why they can never rely on the gate.
		d := testDB(t)
		wrapped := &countingTaskStore{DB: d}
		app := New(wrapped, agent.NewRunner(nil), false)
		baseline := wrapped.tasksCalls

		testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "task1", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))

		for i := 1; i <= 3; i++ {
			app.refreshTasksWithIDs(nil, nil, false)
			testutil.Equal(t, wrapped.tasksCalls, baseline+i)
		}
	})

	t.Run("the reconciliation SetStatus write invalidates the gate for the next gated tick", func(t *testing.T) {
		// A real file-backed DB, not OpenInMemory: db.TestDB_DataVersion proves
		// a write made through THIS SAME connection never bumps what this same
		// connection reads back afterward (the documented same-connection
		// blind spot) — so this test only means something against a real
		// single-connection *db.DB, matching production. Without the explicit
		// invalidateTasksChangeGate call, the gated tick below would wrongly
		// believe nothing changed and skip.
		dir := t.TempDir()
		path := filepath.Join(dir, "data.sql")
		d, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d.Close() })

		wrapped := &countingTaskStore{DB: d}
		app := New(wrapped, agent.NewRunner(nil), true) // daemonConnected=true: required for reconciliation to run
		baseline := wrapped.tasksCalls

		testutil.NoError(t, d.Add(&model.Task{ID: "stale", Name: "stale", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

		// Prime the snapshot with an UNGATED call so the freshly-added
		// InProgress task is actually visible to a.tasks (same-connection
		// blind-spot rationale as the first two subtests above) — this is also
		// the call whose reconciliation pass performs the SetStatus write and
		// the invalidate under test, exactly like a real onTick would.
		app.refreshTasksWithIDs([]string{}, nil, false)
		testutil.Equal(t, wrapped.tasksCalls, baseline+1)
		testutil.Equal(t, app.tasks[0].Status, model.StatusInReview)

		// A gated tick right after: must refetch (proving the invalidate
		// fired) despite no OTHER write having happened since — a naive gate
		// relying on DataVersion() alone would see the same (pre-write)
		// fingerprint and wrongly skip.
		app.refreshTasksWithIDs([]string{}, nil, true)
		testutil.Equal(t, wrapped.tasksCalls, baseline+2)

		// A further gated tick with nothing changed since: must settle and
		// skip again, proving this isn't just "always refetch after any
		// reconciliation pass ran."
		app.refreshTasksWithIDs([]string{}, nil, true)
		testutil.Equal(t, wrapped.tasksCalls, baseline+2)
	})
}

// TestHeraRefresh_InvalidatesTasksChangeGate pins one of the write sites a
// code review of add-tasks-fetch-dirty-check found missing from the initial
// implementation: several hera mutations (heraNukeRole/heraDoCascadeNuke/
// heraNukeArchivedRole's a.db.SetStatus/SetArchived via heraReclaimAndArchiveTask,
// heraStatusStep's RollHeraWorkerToReview/ClearHeraReadyToClose) write the
// plain tasks table through a.db and then call ONLY heraRefresh() — which
// invalidates the SEPARATE HeraPage gate but, before this fix, had no effect
// on the newer plain-task-list gate at all. Simulates that shape directly:
// write via d (the same connection as a.db) then call heraRefresh(), exactly
// like heraReclaimAndArchiveTask does.
func TestHeraRefresh_InvalidatesTasksChangeGate(t *testing.T) {
	d := testDB(t)
	wrapped := &countingTaskStore{DB: d}
	app := New(wrapped, agent.NewRunner(nil), false)
	baseline := wrapped.tasksCalls

	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "t1", Status: model.StatusInReview, Project: "p", CreatedAt: time.Now()}))
	app.refreshTasksWithIDs(nil, nil, false) // prime, mirrors a normal tick
	testutil.Equal(t, wrapped.tasksCalls, baseline+1)
	testutil.Equal(t, app.tasks[0].Archived, false)

	// Mirrors heraReclaimAndArchiveTask: archives the task through the SAME
	// a.db connection, then calls heraRefresh — NOT any task-list refresh.
	testutil.NoError(t, d.SetArchived("t1", true))
	app.heraRefresh()

	// A gated tick right after must see the archived flag — proving
	// heraRefresh's invalidate fired despite the same-connection blind spot.
	app.refreshTasksWithIDs(nil, nil, true)
	testutil.Equal(t, wrapped.tasksCalls, baseline+2)
	testutil.Equal(t, app.tasks[0].Archived, true)
}

// TestStartSession_InvalidatesTasksChangeGate pins another write site the
// same review found missing: startSession persists status/SessionID/AgentPID
// via a.db.Update but is called from five sites, only two of which
// separately force a refetch afterward (heraReattach and the plain
// Enter-to-restart / auto-start paths do not). Fixed by invalidating INSIDE
// startSession itself so every caller is covered uniformly.
func TestStartSession_InvalidatesTasksChangeGate(t *testing.T) {
	d := testDB(t)
	wrapped := &countingTaskStore{DB: d}
	app := New(wrapped, agent.NewRunner(nil), false)
	baseline := wrapped.tasksCalls

	// No worktree configured — runner.Start will fail, exercising
	// startSession's failure-path a.db.Update (the generated-SessionID write
	// fires first, then the failure-revert write).
	task := &model.Task{ID: "t1", Name: "t1", Project: "p", CreatedAt: time.Now()}
	task.SetStatus(model.StatusPending)
	testutil.NoError(t, d.Add(task))
	app.refreshTasksWithIDs(nil, nil, false) // prime
	testutil.Equal(t, wrapped.tasksCalls, baseline+1)

	app.startSession(task) // writes through a.db, no refresh call of its own

	app.refreshTasksWithIDs(nil, nil, true)
	testutil.Equal(t, wrapped.tasksCalls, baseline+2)
}

// TestSwitchSession_LiveSessionInvalidatesTasksChangeGate pins the third
// write site the same review found: switchSession's LIVE-session branch
// persists the new SessionID then stops the session and returns — the actual
// restart (and its own refresh) happens later, asynchronously, once
// handleSessionExitUI's exit notification arrives. In between, a gated tick
// must not show the pre-switch SessionID.
func TestSwitchSession_LiveSessionInvalidatesTasksChangeGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	wrapped := &countingTaskStore{DB: d}
	runner := agent.NewRunner(nil)
	app := New(wrapped, runner, false)

	task := &model.Task{
		ID: "sw-live", Name: "live", Status: model.StatusInProgress,
		Worktree: t.TempDir(), Backend: "test", SessionID: "old-session",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.refreshTasksWithIDs(nil, nil, false) // prime
	baseline := wrapped.tasksCalls
	testutil.Equal(t, app.tasks[0].SessionID, "old-session")

	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sh -c 'sleep 30'"}
	sess, err := runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	defer runner.Stop(task.ID) //nolint:errcheck

	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	app.agentPane.SetSession(sess)
	if !sess.Alive() {
		t.Fatal("expected a live session")
	}

	app.switchSession("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "New convo")

	// A gated tick right after must see the newly-persisted SessionID.
	app.refreshTasksWithIDs(nil, nil, true)
	testutil.Equal(t, wrapped.tasksCalls, baseline+1)
	testutil.Equal(t, app.tasks[0].SessionID, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
}

// TestHandleSessionExitUI_RecaptureInvalidatesTasksChangeGate pins the fourth
// write site the same review found: the async session-ID recapture goroutine
// dispatched at the end of handleSessionExitUI writes a.db.Update well AFTER
// that function's own unconditional refreshTasksAsync call already ran (the
// goroutine does a disk scan first), so that earlier refresh cannot have
// covered it.
func TestHandleSessionExitUI_RecaptureInvalidatesTasksChangeGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := t.TempDir()
	const oldID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const newID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	seedClaudeSession(t, home, worktree, newID, "Recaptured title")

	d := testDB(t)
	wrapped := &countingTaskStore{DB: d}
	app := New(wrapped, agent.NewRunner(nil), false)

	task := &model.Task{
		ID: "t1", Name: "t1", Status: model.StatusInProgress,
		Project: "p", Worktree: worktree, SessionID: oldID, CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.refreshTasksWithIDs(nil, nil, false) // prime
	baseline := wrapped.tasksCalls

	_, stop := wireApp(t, app)
	defer stop()

	app.handleSessionExitUI(task.ID, true /* cleanExit */, false)
	syncUI(t, app.tapp) // let the function's OWN refreshTasksAsync land first

	// Poll for the async capture goroutine's QueueUpdateDraw to land — it does
	// a disk scan before writing, so it lands strictly after the above.
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		if got.SessionID == newID {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, newID)

	// A gated tick right after must see the recaptured SessionID. Two fetches
	// happened by this point: handleSessionExitUI's own (unconditional)
	// refreshTasksAsync, and this call itself — gated, but forced to refetch
	// by the recapture goroutine's invalidateTasksChangeGate despite the
	// write having gone through the same connection.
	app.refreshTasksWithIDs(nil, nil, true)
	testutil.Equal(t, wrapped.tasksCalls, baseline+2)
	testutil.Equal(t, app.tasks[0].SessionID, newID)
}

func TestCtrlDOpensConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	// Ctrl+D on task list should open confirm modal
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	result := app.handleGlobalKey(ev)

	if result != nil {
		t.Error("Ctrl+D should be consumed (return nil)")
	}
	if app.mode != modeConfirmDelete {
		t.Errorf("mode = %v, want modeConfirmDelete", app.mode)
	}
}

func TestCtrlDDoesNotDeleteInAgentMode(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// Ctrl+D in agent mode with no session exits agent view (not delete modal)
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	app.handleGlobalKey(ev)

	// Should return to task list, NOT open confirm delete modal
	if app.mode == modeConfirmDelete {
		t.Error("Ctrl+D in agent mode should not open delete modal")
	}
}

func TestPruneCompletedTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir() // isolate from real worktrees

	// Add tasks with various statuses
	d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "done1", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t3", Name: "in-progress", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t4", Name: "done2", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	if len(app.tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(app.tasks))
	}

	app.pruneCompletedTasks()

	if len(app.tasks) != 2 {
		t.Errorf("expected 2 tasks after prune, got %d", len(app.tasks))
	}

	// Only non-complete tasks should remain
	for _, task := range app.tasks {
		if task.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", task.Name)
		}
	}
}

// TestPruneCompletedTasks_SkipsLiveHeraBoundAndNotifies verifies the TUI
// prune flow leaves a completed task with a live Hera binding in place and
// tells the operator why via a statusbar notice (BUG: Ctrl+R used to silently
// orphan live Hera roles since hera_bindings has no FK to tasks).
func TestPruneCompletedTasks_SkipsLiveHeraBoundAndNotifies(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir()

	testutil.NoError(t, d.Add(&model.Task{ID: "bound", Name: "bound", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: "unbound", Name: "unbound", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))

	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "role", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "bound", WorktreePath: "/tmp/wt/bound",
	})
	testutil.NoError(t, err)

	app.refreshTasks()
	app.pruneCompletedTasks()

	if _, err := d.Get("bound"); err != nil {
		t.Errorf("live-hera-bound task should not have been pruned: %v", err)
	}
	if _, err := d.Get("unbound"); err == nil {
		t.Error("unbound completed task should have been pruned")
	}
	testutil.Contains(t, app.statusbar.Info(), "still active in Hera")
}

// TestPruneCompletedTasks_AllSkippedStillNotifies covers the branch where
// every completed task is Hera-bound and nothing is actually pruned — the
// operator should still learn why nothing happened instead of silence.
func TestPruneCompletedTasks_AllSkippedStillNotifies(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir()

	testutil.NoError(t, d.Add(&model.Task{ID: "bound", Name: "bound", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))

	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "role", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "bound", WorktreePath: "/tmp/wt/bound",
	})
	testutil.NoError(t, err)

	app.refreshTasks()
	app.pruneCompletedTasks()

	if _, err := d.Get("bound"); err != nil {
		t.Errorf("live-hera-bound task should not have been pruned: %v", err)
	}
	testutil.Contains(t, app.statusbar.Info(), "still active in Hera")
}

func TestPruneDoesNotDoubleCountWorktrees(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	wtRoot := t.TempDir()
	app.wtRoot = wtRoot

	// Create a worktree directory on disk for the completed task.
	wtPath := filepath.Join(wtRoot, "p", "done-task")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	d.Add(&model.Task{
		ID: "t1", Name: "done-task", Status: model.StatusComplete,
		Project: "p", Worktree: wtPath, CreatedAt: time.Now(),
	})
	d.Add(&model.Task{
		ID: "t2", Name: "active", Status: model.StatusPending,
		Project: "p", CreatedAt: time.Now(),
	})
	app.refreshTasks()

	app.pruneCompletedTasks()

	// The header notice should show 1 total, not 2.
	// Before the fix, the worktree was counted once as a pruned task
	// AND once as an orphan (because PruneCompleted deletes the DB
	// record before WorktreePaths runs).
	notice := app.header.Notice()
	if notice == "" {
		t.Fatal("expected header notice to be shown")
	}
	if !strings.Contains(notice, "0/1") {
		t.Errorf("header notice = %q, want progress showing total of 1 (not double-counted)", notice)
	}
}

// nonRemoteStore is a store.Store that is neither *db.DB (so prune routes to
// the remote path) nor a remotePruner (so the remote path hits its defensive
// fallback). Embedding *db.DB supplies every store.Store method; db.DB's
// PruneCompleted has a different signature, so the wrapper does NOT satisfy
// remotePruner.
type nonRemoteStore struct{ *db.DB }

func TestPruneCompleted_RemoteFallbackWhenNoPruner(t *testing.T) {
	app := New(&nonRemoteStore{testDB(t)}, agent.NewRunner(nil), false)
	app.wtRoot = t.TempDir()

	app.pruneCompletedTasks()

	testutil.Contains(t, app.statusbar.Error(), "requires local mode")
}

func TestPruneCompleted_RemoteDelegatesToServer(t *testing.T) {
	hit := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/maintenance/prune-completed", func(w http.ResponseWriter, r *http.Request) {
		hit <- r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pruned":2,"worktrees":1,"orphans":0}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	app := New(apistore.New(c), agent.NewRunner(nil), false)
	app.wtRoot = t.TempDir()

	app.pruneCompletedTasks()

	// The HTTP round trip runs in pruneCompletedRemote's goroutine; the
	// post-request refresh is dispatched via QueueUpdateDraw (no-op without a
	// running event loop), but the request itself fires regardless.
	select {
	case method := <-hit:
		testutil.Equal(t, method, "POST")
	case <-time.After(2 * time.Second):
		t.Fatal("expected prune-completed request to reach the server")
	}
	testutil.Equal(t, app.header.Notice(), "Pruning completed tasks…")
}

func TestApp_RunScheduleNowRemote_DelegatesToServer(t *testing.T) {
	hit := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/schedules/s1/run", func(w http.ResponseWriter, r *http.Request) {
		hit <- r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t42"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	app := New(apistore.New(c), agent.NewRunner(nil), false)

	// Run in a goroutine: in production this helper is always invoked from
	// runScheduleNow's goroutine, and it ends with QueueUpdateDraw, which
	// blocks without a running event loop. The HTTP request fires (and signals
	// `hit`) before that point, so the channel receive observes the call.
	go app.runScheduleNowRemote("s1")

	select {
	case method := <-hit:
		testutil.Equal(t, method, "POST")
	case <-time.After(2 * time.Second):
		t.Fatal("expected run-schedule request to reach the server")
	}
}

func TestApp_RunScheduleNowRemote_FallbackNoRunner(t *testing.T) {
	// nonRemoteStore is neither *db.DB nor a remoteScheduleRunner, so the
	// defensive branch (log-only) runs. Must not panic or set an error.
	app := New(&nonRemoteStore{testDB(t)}, agent.NewRunner(nil), false)
	app.runScheduleNowRemote("s1")
	testutil.Equal(t, app.statusbar.Error(), "")
}

func TestApp_ExecuteForkRemote_DelegatesToServer(t *testing.T) {
	hit := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks/src1/fork", func(w http.ResponseWriter, r *http.Request) {
		hit <- r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"f1","name":"fork-x","status":"in_progress"}`))
	})
	mux.HandleFunc("/api/tasks/f1/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"f1","name":"fork-x"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	app := New(apistore.New(c), agent.NewRunner(nil), false)

	// Goroutine for the same reason as the schedule test: executeForkRemote is
	// invoked from executeFork's goroutine in production and ends with a
	// blocking QueueUpdateDraw. The fork POST signals `hit` before that.
	go app.executeForkRemote(&model.Task{ID: "src1", Name: "alpha", Project: "proj"}, "proj", "fork-x")

	select {
	case method := <-hit:
		testutil.Equal(t, method, "POST")
	case <-time.After(2 * time.Second):
		t.Fatal("expected fork request to reach the server")
	}
}

func TestApp_ExecuteForkRemote_FallbackNoForker(t *testing.T) {
	// nonRemoteStore satisfies neither *db.DB nor remoteForker — the defensive
	// log-only branch runs. Must not panic.
	app := New(&nonRemoteStore{testDB(t)}, agent.NewRunner(nil), false)
	app.executeForkRemote(&model.Task{ID: "src1", Project: "p"}, "p", "fork-x")
}

// permissiveTaskMux serves the endpoints the remote success paths touch
// (fork/create + the follow-up raw fetch + the refresh/select round trips),
// with a catch-all so onTaskSelect's incidental calls don't 404-panic.
func permissiveTaskMux(t *testing.T, forkResp string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks/src1/fork", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(forkResp))
	})
	mux.HandleFunc("/api/tasks/f1/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"f1","name":"fork-x","status":"in_progress","project":"proj"}`))
	})
	mux.HandleFunc("/api/tasks-raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks":[{"id":"f1","name":"fork-x","status":"in_progress","project":"proj"}]}`))
	})
	mux.HandleFunc("/api/sessions/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":[],"idle":[]}`))
	})
	// Catch-all: anything else onTaskSelect/refresh incidentally hits is benign.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	return mux
}

// TestApp_ExecuteForkRemote_SuccessPathUpdatesUI drives the success branch
// under a running event loop so the QueueUpdateDraw closure actually executes,
// then asserts the user-visible effects: recentStarts populated and the
// degraded-fork info notice surfaced.
func TestApp_ExecuteForkRemote_SuccessPathUpdatesUI(t *testing.T) {
	srv := httptest.NewServer(permissiveTaskMux(t, `{"id":"f1","name":"fork-x","status":"in_progress"}`))
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	app := New(apistore.New(c), agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	// With a running loop, QueueUpdateDraw unblocks, so this returns after the
	// success closure has run.
	app.executeForkRemote(&model.Task{ID: "src1", Name: "alpha", Project: "proj"}, "proj", "fork-x")

	var hasStart bool
	var info string
	readUI(t, app.tapp, func() {
		_, hasStart = app.recentStarts["f1"]
		info = app.statusbar.Info()
	})
	testutil.Equal(t, hasStart, true)
	testutil.Contains(t, info, "context not carried")
}

// TestApp_RunScheduleNowRemote_SuccessPathRefreshes drives the schedule-fire
// success branch under a running loop and asserts it took the success path
// (no error surfaced) rather than the error branch.
func TestApp_RunScheduleNowRemote_SuccessPathRefreshes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/schedules/s1/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t42"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	app := New(apistore.New(c), agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	app.runScheduleNowRemote("s1")

	var errText string
	readUI(t, app.tapp, func() { errText = app.statusbar.Error() })
	testutil.Equal(t, errText, "")
}

func TestCtrlROpensPruneConfirm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir() // isolate from real worktrees

	d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "done", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	ev := tcell.NewEventKey(tcell.KeyCtrlR, 0, 0)
	result := app.handleGlobalKey(ev)

	if result != nil {
		t.Error("Ctrl+R should be consumed (return nil)")
	}
	// Ctrl+R only opens the confirmation gate — it must NOT prune yet.
	if app.mode != modeConfirmPrune || app.confirmPruneModal == nil {
		t.Fatalf("expected confirm-prune modal open, got mode=%v modal=%v", app.mode, app.confirmPruneModal)
	}
	if len(app.tasks) != 2 {
		t.Errorf("expected 2 tasks before confirmation, got %d", len(app.tasks))
	}
}

func TestPruneConfirm_YPrunes(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir()

	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: "t2", Name: "done", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))
	app.refreshTasks()

	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlR, 0, 0))
	// Confirm with 'y'.
	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'y', 0))

	if app.mode != modeTaskList || app.confirmPruneModal != nil {
		t.Fatalf("expected modal dismissed after confirm, got mode=%v modal=%v", app.mode, app.confirmPruneModal)
	}
	if len(app.tasks) != 1 {
		t.Errorf("expected 1 task after confirmed prune, got %d", len(app.tasks))
	}
	for _, task := range app.tasks {
		if task.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", task.Name)
		}
	}
}

func TestPruneConfirm_NCancels(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir()

	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: "t2", Name: "done", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))
	app.refreshTasks()

	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlR, 0, 0))
	// Cancel with Esc.
	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	if app.mode != modeTaskList || app.confirmPruneModal != nil {
		t.Fatalf("expected modal dismissed after cancel, got mode=%v modal=%v", app.mode, app.confirmPruneModal)
	}
	// Nothing pruned — both tasks survive.
	if len(app.tasks) != 2 {
		t.Errorf("expected 2 tasks after canceled prune, got %d", len(app.tasks))
	}
}

func TestPruneConfirm_NoCompletedSkipsModal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.wtRoot = t.TempDir()

	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
	app.refreshTasks()

	app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlR, 0, 0))

	// No completed tasks — the modal must not open.
	if app.mode != modeTaskList || app.confirmPruneModal != nil {
		t.Fatalf("expected no modal when nothing to prune, got mode=%v modal=%v", app.mode, app.confirmPruneModal)
	}
	if len(app.tasks) != 1 {
		t.Errorf("expected 1 task untouched, got %d", len(app.tasks))
	}
}

func TestReconcileSkipsOnNilRunning(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Simulate daemon mode
	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "active-agent", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "also-active", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Pass nil runningIDs (simulates RPC failure) — should NOT reconcile
	app.refreshTasksWithIDs(nil, nil, false)

	for _, task := range app.tasks {
		if task.Status != model.StatusInProgress {
			t.Errorf("task %q was wrongly reconciled (%s) on nil runningIDs; must stay InProgress", task.Name, task.Status)
		}
	}
}

// TestReconcileSkipsNonInProgress pins the guard that protects the daemon's
// authoritative exit-driven flip: reconciliation must NEVER touch a row that has
// already left InProgress. This is the race backstop — if the daemon (or the
// exit handler) has already flipped a row to Complete or InReview, a later tick
// that still sees the session absent must leave it alone, never re-flip it.
func TestReconcileSkipsNonInProgress(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.daemonConnected = true

	testutil.NoError(t, d.Add(&model.Task{ID: "done", Name: "already-complete", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: "rev", Name: "already-review", Status: model.StatusInReview, Project: "p", CreatedAt: time.Now()}))

	// Empty (non-nil) running set: neither task has a live session, but both have
	// already left InProgress, so reconciliation must skip them entirely.
	// Hold a.mu to honor refreshTasksWithIDs' documented caller contract (its
	// production callers lock first).
	app.mu.Lock()
	app.refreshTasksWithIDs([]string{}, []string{}, false)
	app.mu.Unlock()

	got, _ := d.Get("done")
	testutil.Equal(t, got.Status, model.StatusComplete) // daemon's clean-exit Complete preserved
	got, _ = d.Get("rev")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestReconcileWorksOnEmptyRunning(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Simulate daemon mode
	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "stale-task", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Pass empty non-nil runningIDs (daemon confirmed nothing running) — should
	// reconcile to InReview (inferred absence, no observed exit → never Complete).
	app.refreshTasksWithIDs([]string{}, []string{}, false)

	found := false
	for _, task := range app.tasks {
		if task.ID == "t1" && task.Status == model.StatusInReview {
			found = true
		}
		if task.ID == "t1" && task.Status == model.StatusComplete {
			t.Error("reconciliation must NOT mark an inferred-absent task Complete")
		}
	}
	if !found {
		t.Error("stale task should have been reconciled to InReview with empty (non-nil) runningIDs")
	}
}

// Covers the happy path (InProgress flipped) and idempotency on rows already
// in a terminal state. The database.Tasks() error path is not exercised
// directly — propagation is straight pass-through and the helper has no
// other behavior on top of it.
func TestReconcileStaleSessionsFlipsInProgress(t *testing.T) {
	d := testDB(t)

	d.Add(&model.Task{ID: "t1", Name: "was-running", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "was-pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t3", Name: "was-review", Status: model.StatusInReview, Project: "p", CreatedAt: time.Now()})

	n, err := agent.ReconcileStaleSessions(d)
	if err != nil {
		t.Fatalf("ReconcileStaleSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	tasks, _ := d.Tasks()
	for _, task := range tasks {
		switch task.ID {
		case "t1":
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got %s, want in_review", task.Name, task.Status)
			}
		case "t2":
			if task.Status != model.StatusPending {
				t.Errorf("task %q: got %s, want pending (untouched)", task.Name, task.Status)
			}
		case "t3":
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got %s, want in_review (untouched)", task.Name, task.Status)
			}
		}
	}
}

// TestReconcileSkipsOnStaleStartGen and TestReconcileWorksWhenStartGenUnchanged
// replicate the startGen guard logic from onTick's QueueUpdateDraw callback
// inline. This is intentional — onTick involves a tick goroutine + RPC +
// QueueUpdateDraw pipeline that isn't unit-testable. If the guard condition
// in onTick changes, these tests must be updated in lockstep.
func TestReconcileSkipsOnStaleStartGen(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "just-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate the race: tick captures startGen=0, then startSession bumps it.
	startGen := app.startGen.Load()
	app.startGen.Add(1) // simulates startSession

	// Stale runningIDs (empty — captured before session existed).
	runningIDs := []string{}

	// Simulate what onTick's QueueUpdateDraw callback does:
	// startGen changed → pass nil to skip reconciliation.
	if app.startGen.Load() != startGen {
		runningIDs = nil
	}
	app.refreshTasksWithIDs(runningIDs, []string{}, false)

	for _, task := range app.tasks {
		if task.ID == "t1" {
			// Should NOT be reconciled — startGen mismatch skipped it.
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

// TestRefreshTasksAsyncStartGenGuard replicates the startGen guard in
// refreshTasksAsync. Before the fix, refreshTasksAsync had no guard — a
// session exit calling refreshTasksAsync while a new task was starting would
// capture stale runningIDs and reconcile the new task to Complete.
func TestRefreshTasksAsyncStartGenGuard(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "just-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate: refreshTasksAsync captures startGen before RPC...
	startGen := app.startGen.Load()

	// ...then startSession bumps it while the RPC is in-flight.
	app.startGen.Add(1)

	// RPC returns stale empty runningIDs (new session not yet registered).
	runningIDs := []string{}

	// Simulate what refreshTasksAsync's QueueUpdateDraw callback now does:
	if app.startGen.Load() != startGen {
		runningIDs = nil
	}
	app.refreshTasksWithIDs(runningIDs, []string{}, false)

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

func TestReconcileWorksWhenStartGenUnchanged(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "stale-task", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// No startGen change — runningIDs are fresh and trustworthy.
	// (No guard needed; startGen unchanged means reconciliation proceeds normally.)
	app.refreshTasksWithIDs([]string{}, []string{}, false)

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInReview)
		}
	}
}

// TestReconcileGracePeriodProtectsRecentStarts verifies that tasks started
// within the last 5 seconds are not reconciled to Complete even if they are
// not in the running set. This protects against restart cascade races where
// ListSessions returns stale data.
func TestReconcileGracePeriodProtectsRecentStarts(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "recently-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate startSession recording the start time.
	app.recentStarts["t1"] = time.Now()

	// Empty running set — session not yet visible to ListSessions.
	app.refreshTasksWithIDs([]string{}, []string{}, false)

	// Task should be protected by grace period.
	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

// TestReconcileGracePeriodExpiresAfterTimeout verifies that the grace period
// expires and allows reconciliation after the timeout.
func TestReconcileGracePeriodExpiresAfterTimeout(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "old-start", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Set start time in the past (beyond grace period).
	app.recentStarts["t1"] = time.Now().Add(-10 * time.Second)

	app.refreshTasksWithIDs([]string{}, []string{}, false)

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInReview)
		}
	}
}

func TestWorktreeSubdir(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/foo/.argus/worktrees/proj/task", true},
		{"/Users/foo/.claude/worktrees/proj/task", true},
		{"/Users/foo/projects/repo", false},
		{"/tmp/foo", false},
	}
	for _, tt := range tests {
		if got := agent.IsWorktreeSubdir(tt.path); got != tt.want {
			t.Errorf("agent.IsWorktreeSubdir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestPTYSizeFromHostTerm(t *testing.T) {
	cases := []struct {
		name               string
		tw, th             int
		err                error
		wantRows, wantCols uint16
	}{
		// Zoomed default: full host width minus the 2-col pane border.
		{"typical wide", 320, 100, nil, 96, 318},
		{"standard 80x24", 80, 24, nil, 20, 78},
		// 50-col host: 50-2 = 48 ⇒ no clamp.
		{"narrow 50x20", 50, 20, nil, 16, 48},
		// Pathological tiny host triggers both clamps.
		{"tiny clamps both floors", 18, 8, nil, 5, 20},
		// Real-world reproduction of the original bug. Anything works as long
		// as it isn't 20x8 — the PTY size that left Claude rendering narrow.
		{"realistic iTerm2 split", 200, 60, nil, 56, 198},
		// Unusable signals: function must yield 0,0 so callers fall back.
		{"err short-circuits", 320, 100, errFakeNoTTY, 0, 0},
		{"zero width", 0, 100, nil, 0, 0},
		{"zero height", 320, 0, nil, 0, 0},
		{"negative", -1, -1, nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRows, gotCols := ptySizeFromHostTerm(tc.tw, tc.th, tc.err)
			testutil.Equal(t, gotRows, tc.wantRows)
			testutil.Equal(t, gotCols, tc.wantCols)
		})
	}
}

func TestPTYSizeFromPaneRect(t *testing.T) {
	cases := []struct {
		name               string
		pw, ph             int
		wantRows, wantCols uint16
	}{
		// The bug: tview's NewBox returns 15x10 before Flex lays it out.
		// Reading that as authoritative produced a 20x8 PTY.
		{"tview Box default rejected", 15, 10, 0, 0},
		// Anything at-or-below the threshold falls through too.
		{"30x10 still rejected", 30, 10, 0, 0},
		{"20x8 (pre-fix output) rejected", 20, 8, 0, 0},
		// Genuinely laid-out panes pass.
		{"laid-out wide pane", 192, 84, 82, 190},
		{"31x11 (just above floor)", 31, 11, 9, 29},
		// Zero / negative are noise.
		{"zero rejected", 0, 0, 0, 0},
		{"negative rejected", -1, -1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRows, gotCols := ptySizeFromPaneRect(tc.pw, tc.ph)
			testutil.Equal(t, gotRows, tc.wantRows)
			testutil.Equal(t, gotCols, tc.wantCols)
		})
	}
}

func TestPTYSizeForRect(t *testing.T) {
	cases := []struct {
		name               string
		rect               Rect
		wantRows, wantCols uint16
	}{
		{"typical center pane", Rect{X: 0, Y: 0, W: 192, H: 84}, 82, 190},
		{"tiny rect clamped to floors", Rect{X: 0, Y: 0, W: 10, H: 4}, 5, 20},
		{"zero width rejected", Rect{W: 0, H: 24}, 0, 0},
		{"zero height rejected", Rect{W: 80, H: 0}, 0, 0},
		{"negative rejected", Rect{W: -5, H: -2}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, cols := PTYSizeForRect(tc.rect)
			testutil.Equal(t, rows, tc.wantRows)
			testutil.Equal(t, cols, tc.wantCols)
		})
	}
}

// errFakeNoTTY stands in for term.GetSize's "inappropriate ioctl for device"
// error when stdout isn't a TTY.
var errFakeNoTTY = &fakeErr{msg: "inappropriate ioctl for device"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// (TestShouldKickNarrowRerender lived here when the kick-narrow-rerender
// decision was made client-side. Master moved it into the daemon's
// KickRerender path; the new behavior is covered by
// TestRunner_KickRerender_* in internal/agent/runner_test.go.)

func TestApp_OpenAndCloseProjectForm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(false, "", config.Project{})
	testutil.Equal(t, app.mode, modeProjectForm)
	if app.projectForm == nil {
		t.Fatal("projectForm should be non-nil")
	}

	app.closeProjectForm()
	testutil.Equal(t, app.mode, modeTaskList)
	if app.projectForm != nil {
		t.Error("projectForm should be cleared")
	}
}

func TestApp_OpenAndCloseProjectForm_Edit(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(true, "myproj", config.Project{Path: "/tmp"})
	if app.projectForm == nil {
		t.Fatal("projectForm should be non-nil")
	}
	testutil.Equal(t, app.projectForm.editMode, true)
}

func TestApp_OpenAndCloseAppleEventsPicker(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Pre-populate the macapps cache so openAppleEventsPicker doesn't kick
	// off a background filesystem scan inside the test (the bg goroutine
	// would race the closeAppleEventsPicker call below and corrupt state
	// timing across runs).
	app.macAppsCache = []macapps.App{
		{Name: "Messages", BundleID: "com.apple.MobileSMS", Scriptable: true},
	}

	app.openAppleEventsPicker("forge", config.Project{
		Path: "/tmp/forge",
		Sandbox: config.ProjectSandboxConfig{
			AllowAppleEvents: []string{"com.apple.iChat"},
		},
	})
	testutil.Equal(t, app.mode, modeAppleEventsPicker)
	if app.appleEventsPicker == nil {
		t.Fatal("appleEventsPicker should be non-nil")
	}
	testutil.Equal(t, app.appleEventsPickerProject, "forge")
	// Preselected value must flow through.
	if _, ok := app.appleEventsPicker.selected["com.apple.iChat"]; !ok {
		t.Error("expected com.apple.iChat preselected from project config")
	}

	app.closeAppleEventsPicker()
	testutil.Equal(t, app.mode, modeTaskList)
	if app.appleEventsPicker != nil {
		t.Error("appleEventsPicker should be cleared after close")
	}
	testutil.Equal(t, app.appleEventsPickerProject, "")
}

func TestApp_HandleAppleEventsPickerKey_EscapeCancels(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.macAppsCache = []macapps.App{
		{Name: "Messages", BundleID: "com.apple.MobileSMS", Scriptable: true},
	}

	app.openAppleEventsPicker("forge", config.Project{Path: "/tmp/forge"})
	app.handleAppleEventsPickerKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	// Esc cancels — mode back to task list, no DB write.
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleAppleEventsPickerKey_EnterSavesToDB(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	// Seed an existing project so SetProject's UPDATE path is exercised.
	testutil.NoError(t, d.SetProject("forge", config.Project{Path: "/tmp/forge"}))
	// Production macAppsCache comes from macapps.Scan, which sorts by
	// lowercase name — set the same order here so the picker's row layout
	// matches production. Cursor starts at row 0 (Finder).
	app.macAppsCache = []macapps.App{
		{Name: "Finder", BundleID: "com.apple.finder", Scriptable: true},
		{Name: "Messages", BundleID: "com.apple.MobileSMS", Scriptable: true},
	}

	app.openAppleEventsPicker("forge", config.Project{Path: "/tmp/forge"})
	// Space toggles the cursor row (Finder, at row 0).
	app.handleAppleEventsPickerKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	// Enter saves + closes.
	app.handleAppleEventsPickerKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.mode, modeTaskList)

	// Verify persistence.
	projects, err := d.Projects()
	testutil.NoError(t, err)
	got := projects["forge"].Sandbox.AllowAppleEvents
	if len(got) != 1 || got[0] != "com.apple.finder" {
		t.Errorf("expected [com.apple.finder] saved, got %v", got)
	}
}

func TestApp_OpenAppleEventsPicker_FromSettingsCallback(t *testing.T) {
	// Pin that the SettingsView callback wired in New() lands at
	// openAppleEventsPicker with the right project name. Defends against
	// a future refactor that mis-routes the callback or breaks the modal
	// open path.
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.macAppsCache = []macapps.App{{Name: "X", BundleID: "com.x", Scriptable: true}}

	if app.settings.OnEditProjectAppleEvents == nil {
		t.Fatal("OnEditProjectAppleEvents not wired by App.New")
	}
	app.settings.OnEditProjectAppleEvents("forge", config.Project{Path: "/tmp/forge"})
	testutil.Equal(t, app.mode, modeAppleEventsPicker)
	testutil.Equal(t, app.appleEventsPickerProject, "forge")
	app.closeAppleEventsPicker()
}

func TestApp_HandleProjectFormKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(false, "", config.Project{})
	app.handleProjectFormKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleProjectFormKey_DoneEmptyName(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(false, "", config.Project{})

	app.projectForm.focused = pfFieldProfile // last field — advance submits
	app.handleProjectFormKey(formAdvanceKey)
	testutil.Equal(t, app.projectForm.done, false)
	testutil.Contains(t, app.projectForm.errMsg, "Name cannot be empty")
}

func TestApp_HandleProjectFormKey_DoneEmptyPath(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(false, "", config.Project{})
	app.projectForm.fields[pfFieldName] = []rune("name")
	app.projectForm.focused = pfFieldProfile // last field — advance submits
	app.handleProjectFormKey(formAdvanceKey)
	testutil.Equal(t, app.projectForm.done, false)
	testutil.Contains(t, app.projectForm.errMsg, "Path cannot be empty")
}

func TestApp_HandleProjectFormKey_DoneSuccess(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openProjectForm(false, "", config.Project{})
	app.projectForm.fields[pfFieldName] = []rune("newproj")
	app.projectForm.fields[pfFieldPath] = []rune(t.TempDir())
	app.projectForm.focused = pfFieldProfile // last field — advance submits
	app.handleProjectFormKey(formAdvanceKey)
	testutil.Equal(t, app.mode, modeTaskList)
	cfg := d.Config()
	if _, ok := cfg.Projects["newproj"]; !ok {
		t.Error("project should be saved")
	}
}

func TestApp_OpenAndCloseScheduleForm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openScheduleForm(nil)
	testutil.Equal(t, app.mode, modeScheduleForm)
	if app.scheduleForm == nil {
		t.Fatal("scheduleForm should be non-nil")
	}

	app.closeScheduleForm()
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_OpenScheduleForm_Edit(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	s := &model.ScheduledTask{ID: "id1", Name: "x", Project: "p", Schedule: "@daily", Prompt: "go"}
	app.openScheduleForm(s)
	testutil.Equal(t, app.scheduleForm.editMode, true)
}

func TestApp_HandleScheduleFormKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openScheduleForm(nil)
	app.handleScheduleFormKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleScheduleFormKey_DoneInvalid(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openScheduleForm(nil)
	app.scheduleForm.done = true

	app.handleScheduleFormKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, app.scheduleForm.done, false)
	if app.scheduleForm.errMsg == "" {
		t.Error("expected validation error")
	}
}

func TestApp_HandleScheduleFormKey_DoneCreate(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.openScheduleForm(nil)
	app.scheduleForm.fields[sfFieldName] = []rune("test-sched")
	app.scheduleForm.fields[sfFieldPrompt] = []rune("hello")

	app.scheduleForm.done = true
	app.handleScheduleFormKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, app.mode, modeTaskList)

	scheds, _ := d.Schedules()
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
}

func TestApp_HandleScheduleFormKey_DoneUpdate(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	existing := &model.ScheduledTask{
		ID: "sid", Name: "old", Project: "p", Prompt: "x", Schedule: "@daily", Enabled: true,
	}
	d.AddSchedule(existing)

	app.openScheduleForm(existing)

	app.scheduleForm.fields[sfFieldName] = []rune("renamed")
	app.scheduleForm.done = true
	app.handleScheduleFormKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, app.mode, modeTaskList)

	updated, _ := d.GetSchedule("sid")
	testutil.Equal(t, updated.Name, "renamed")
}

func TestApp_DeleteSchedule(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)

	app.deleteSchedule("id")
	scheds, _ := d.Schedules()
	testutil.Equal(t, len(scheds), 0)
}

func TestApp_DeleteSchedule_NotFound(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.deleteSchedule("nope")
}

func TestApp_RunScheduleNow_NotFound(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.runScheduleNow("nope")
}

func TestApp_RunScheduleNow_InvalidSchedule(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	s := &model.ScheduledTask{
		ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "not a cron",
	}
	d.AddSchedule(s)
	app.runScheduleNow("id")

	got, _ := d.GetSchedule("id")
	if got.LastError == "" {
		t.Error("expected LastError to be set")
	}
}

func TestApp_OpenAndCloseQuickAddForm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openQuickAddForm()
	testutil.Equal(t, app.mode, modeQuickAdd)
	if app.quickAddForm == nil {
		t.Fatal("quickAddForm should be non-nil")
	}

	app.closeQuickAddForm()
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleQuickAddKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openQuickAddForm()
	app.handleQuickAddKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleQuickAddKey_Done(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openQuickAddForm()
	app.quickAddForm.repos = []repoCandidate{
		{name: "p1", path: "/tmp/p1", selected: true},
	}
	app.quickAddForm.phase = 1
	app.quickAddForm.done = true
	app.handleQuickAddKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, app.mode, modeTaskList)

	if _, err := d.Projects(); err != nil {
		t.Fatal(err)
	}
}

func TestApp_DeleteProject_OpensConfirmModal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("myproj", config.Project{Path: t.TempDir()})
	d.Add(&model.Task{ID: "t1", Project: "myproj", Name: "n", Status: model.StatusPending, CreatedAt: time.Now()})
	app.refreshTasks()

	app.deleteProject("myproj")
	testutil.Equal(t, app.mode, modeConfirmDeleteProject)
	if app.confirmDeleteProjectModal == nil {
		t.Fatal("confirmDeleteProjectModal should be set")
	}
}

func TestApp_HandleConfirmDeleteProjectKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.openConfirmDeleteProject("p", 0)
	app.handleConfirmDeleteProjectKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleConfirmDeleteProjectKey_Confirm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.openConfirmDeleteProject("p", 0)
	app.handleConfirmDeleteProjectKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)

	cfg := d.Config()
	if _, ok := cfg.Projects["p"]; ok {
		t.Error("project should be deleted")
	}
}

func TestApp_OpenForkModal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	task := &model.Task{ID: "t1", Project: "p", Name: "n", Worktree: "/tmp/wt"}
	app.openForkModal(task)
	testutil.Equal(t, app.mode, modeForkTask)
	if app.forkModal == nil {
		t.Fatal("forkModal should be set")
	}

	app.closeForkModal()
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleForkTaskKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	task := &model.Task{ID: "t1", Project: "p", Name: "n"}
	app.openForkModal(task)
	app.handleForkTaskKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_OpenAndCloseRenameModal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "old"}
	d.Add(task)

	app.openRenameModal(task)
	testutil.Equal(t, app.mode, modeRenameTask)
	if app.renameModal == nil {
		t.Fatal("renameModal should be set")
	}

	app.closeRenameModal()
	testutil.Equal(t, app.mode, modeTaskList)
	if app.renameModal != nil {
		t.Error("renameModal should be cleared")
	}
}

func TestApp_HandleRenameTaskKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "old"}
	d.Add(task)

	app.openRenameModal(task)
	app.handleRenameTaskKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleRenameTaskKey_DoneEmptyName(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "old"}
	d.Add(task)

	app.openRenameModal(task)

	app.renameModal.name = nil
	app.renameModal.cursor = 0
	app.handleRenameTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.mode, modeRenameTask)
	if app.renameModal.errMsg == "" {
		t.Error("expected error message")
	}
}

func TestApp_HandleRenameTaskKey_DoneNoChange(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "old"}
	d.Add(task)

	app.openRenameModal(task)

	app.handleRenameTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleRenameTaskKey_DoneNewName(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "old", Project: "p"}
	d.Add(task)
	app.refreshTasks()

	app.openRenameModal(task)

	for _, r := range "-new" {
		app.renameModal.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	app.handleRenameTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)

	updated, _ := d.Get("t1")
	testutil.Equal(t, updated.Name, "old-new")
}

func TestApp_HandleConfirmDeleteKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "x"}
	d.Add(task)

	app.openConfirmDelete(task)
	app.handleConfirmDeleteKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleConfirmDeleteKey_Confirm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Name: "x"}
	d.Add(task)
	app.refreshTasks()

	app.openConfirmDelete(task)
	app.handleConfirmDeleteKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)

	if got, _ := d.Get("t1"); got != nil {
		t.Error("task should be deleted")
	}
}

func TestApp_HandleLinkPickerKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openLinkPickerModal([]Link{{Label: "X", URL: "https://x.com"}})
	app.handleLinkPickerKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestSanitizeTaskName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"hello\nworld", "hello world"},
		{"   trim   ", "trim"},
		{"with\x01control", "withcontrol"},
		{"tab\there", "tab here"},
		{"crlf\r\n", "crlf"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := sanitizeTaskName(tt.in)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestApp_ResolveSandboxed(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	testutil.Equal(t, app.resolveSandboxed(nil), false)

	task := &model.Task{ID: "t1", Project: "p"}

	app.resolveSandboxed(task)
}

func TestApp_RestartedClient(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	testutil.Nil(t, app.RestartedClient())
}

func TestApp_NotifySessionExit(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	t.Cleanup(stop)
	_ = sim

	done := make(chan struct{})
	go func() {
		// Clean in-process exit: err=nil, stopped=false → cleanExit → Complete.
		app.NotifySessionExit("t1", nil, false, []byte("done"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("NotifySessionExit blocked")
	}
	syncUI(t, app.tapp)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusComplete)
}

// TestApp_NotifySessionExit_CrashGoesToInReview pins the original bug class end
// to end through the in-process entry point: a non-zero / missing-binary exit
// arrives with err != nil (stopped=false) and MUST land InReview, never Complete.
func TestApp_NotifySessionExit_CrashGoesToInReview(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		app.NotifySessionExit("t1", errors.New("exit status 127"), false /* not stopped */, []byte("claude: command not found"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("NotifySessionExit blocked")
	}
	syncUI(t, app.tapp)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestApp_HandleSessionExit_StreamLost(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	testutil.NoError(t, d.Add(task))

	// StreamLost means "stream disconnected but the process may still be alive" —
	// HandleSessionExit must return early WITHOUT flipping status (not InReview,
	// not Complete). The row stays InProgress so a reconnect can resume cleanly.
	app.HandleSessionExit("t1", daemon.ExitInfo{StreamLost: true})

	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInProgress)
}

// TestApp_HandleSessionExit_NonCleanGoesToInReview pins the daemon-client entry
// point for a non-clean exit (non-empty Err, not stopped — e.g. a crash / exit
// 127): CleanExit() is false → the row must land InReview, not Complete.
func TestApp_HandleSessionExit_NonCleanGoesToInReview(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		app.HandleSessionExit("t1", daemon.ExitInfo{Err: "exit status 127"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("HandleSessionExit blocked")
	}
	syncUI(t, app.tapp)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestApp_HandleSessionExit_DispatchesToUI(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)
	app.refreshTasks()

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		// Clean daemon-reported exit (not stopped, no err) → CleanExit() → Complete.
		app.HandleSessionExit("t1", daemon.ExitInfo{Stopped: false, LastOutput: []byte("done")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("HandleSessionExit blocked")
	}
	syncUI(t, app.tapp)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestApp_HandleSessionExitUI_TaskNotFound(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.handleSessionExitUI("nonexistent", false, false)
}

func TestApp_HandleSessionExitUI_FlipToComplete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)

	app.handleSessionExitUI("t1", true /* cleanExit */, false)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestApp_HandleSessionExitUI_FlipToInReview(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)

	app.handleSessionExitUI("t1", false /* cleanExit → non-clean */, false)
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

// TestApp_IsViewingTaskSession is the BUG-076 regression at the unit level:
// the classic fullscreen agent view (modeAgent) is one way to be "viewing" a
// task's live session, but the native Hera view — which never sets
// a.mode to modeAgent, staying modeTaskList with ActiveTab()==TabHera — is
// another, and the old check only recognized the first. A task bound to
// either the coordinator or agent/worker pane while the Hera tab is active
// must count as viewed; an unbound task, a different tab, or a Hera binding
// while some other tab is active must not.
func TestApp_IsViewingTaskSession(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	t.Run("classic agent view", func(t *testing.T) {
		app.mode = modeAgent
		app.agentState.Reset("t1", "n")
		testutil.Equal(t, app.isViewingTaskSession("t1"), true)
		testutil.Equal(t, app.isViewingTaskSession("other"), false)
	})

	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	app.heraPage.Refresh()

	t.Run("hera tab with worker selected: both coord and worker panes count as viewed", func(t *testing.T) {
		app.mode = modeTaskList
		app.header.SetTab(widget.TabHera)
		// The worker's row lives under the orchestrator's fold — expand it first
		// (mirrors JumpToTask's own ancestor-expand step) so SelectByTaskID finds it.
		app.heraPage.Rail().EnsureAncestorsExpanded(orch)
		if !app.heraPage.Rail().SelectByTaskID("t-wkr") {
			t.Fatal("expected a rail row for t-wkr")
		}
		testutil.Equal(t, app.isViewingTaskSession("t-wkr"), true)
		testutil.Equal(t, app.isViewingTaskSession("t-coord"), true)
		testutil.Equal(t, app.isViewingTaskSession("unrelated"), false)
	})

	t.Run("hera binding present but a different tab is active", func(t *testing.T) {
		app.mode = modeTaskList
		app.header.SetTab(widget.TabTasks)
		testutil.Equal(t, app.isViewingTaskSession("t-wkr"), false)
		testutil.Equal(t, app.isViewingTaskSession("t-coord"), false)
	})
}

// TestHandleSessionExitUI_RerenderGateEntersOnNonCleanExit pins that the
// pendingRerenderRestart gate (now keyed on !cleanExit, not "stopped") is
// entered for ANY non-clean exit — including a crash (err!=nil), not just a
// deliberate Stop. With the user no longer viewing the task, the gate consumes
// (deletes) the rerender flag and falls through to the normal exit path, leaving
// the task InReview. This exercises the widened guard introduced by the cleanExit
// refactor without needing a live session/startSession to restart.
func TestHandleSessionExitUI_RerenderGateEntersOnNonCleanExit(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	testutil.NoError(t, d.Add(task))

	// Rerender restart was queued, but the user is NOT viewing this task's pane.
	app.pendingRerenderRestart["t1"] = true
	app.mode = modeTaskList // not viewing → gate clears flag, falls through

	app.handleSessionExitUI("t1", false /* cleanExit → non-clean (crash or stop) */, false)

	// The rerender flag was consumed and the task settled InReview.
	if app.pendingRerenderRestart["t1"] {
		t.Error("pendingRerenderRestart flag should have been consumed by the gate")
	}
	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestApp_OnTaskCursorChange_Nil(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.onTaskCursorChange(nil)
	testutil.Equal(t, app.taskPreview.TaskID(), "")
}

func TestApp_OnTaskCursorChange_WithTask(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n"}
	app.onTaskCursorChange(task)
	testutil.Equal(t, app.taskPreview.TaskID(), "t1")
}

func TestApp_OnTaskCursorChange_WithWorktree(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Worktree: t.TempDir()}
	app.onTaskCursorChange(task)
	testutil.Equal(t, app.taskPreview.TaskID(), "t1")
}

func TestApp_EnterPendingAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "pending-1", Name: "creating"}
	app.enterPendingAgentView(task)

	testutil.Equal(t, app.mode, modeAgent)
	testutil.Equal(t, app.agentState.TaskID, "pending-1")
}

func TestApp_NavigateAgentTask_NoNext(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusPending, CreatedAt: time.Now()}
	d.Add(task)
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.navigateAgentTask(1)
	testutil.Equal(t, app.agentState.TaskID, "t1")
}

func TestApp_NavigateAgentTask_HasNext(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	now := time.Now()
	d.Add(&model.Task{ID: "t1", Project: "p", Name: "a", Status: model.StatusPending, CreatedAt: now})
	d.Add(&model.Task{ID: "t2", Project: "p", Name: "b", Status: model.StatusPending, CreatedAt: now.Add(time.Second)})
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("t1", "a")
	app.navigateAgentTask(1)

	testutil.Equal(t, app.agentState.TaskID, "t2")
}

func TestApp_RefreshPreview_NoSession_NoLog(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.taskPreview.SetRect(0, 0, 60, 20)

	app.taskPreview.Draw(drawSim(t))
	t.Setenv("HOME", t.TempDir())
	app.refreshPreview("nonexistent-task")
}

func TestApp_RefreshPreview_ZeroSize(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.refreshPreview("anything")
}

func TestApp_HandleSessionExitUI_NoStatusFlipForNonInProgress(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInReview, CreatedAt: time.Now()}
	d.Add(task)

	app.handleSessionExitUI("t1", false, false)
	got, _ := d.Get("t1")

	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestApp_ExecuteFork_NoProjectPath(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("ghost", config.Project{})

	source := &model.Task{ID: "src", Name: "task", Project: "ghost"}
	app.executeFork(source, "ghost")
	tasks, _ := d.Tasks()
	testutil.Equal(t, len(tasks), 0)
}

func TestApp_HandleLinkPickerKey_Selects(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openLinkPickerModal([]Link{{Label: "X", URL: "https://x.com"}})

	app.handleLinkPickerKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleFuzzyLinkPickerKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.openFuzzyLinkPickerModal([]Link{{Label: "X", URL: "https://x.com"}})
	app.handleFuzzyLinkPickerKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeAgent)
}

func TestApp_HandleFuzzyLinkPickerKey_Selects(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.openFuzzyLinkPickerModal([]Link{{Label: "X", URL: "https://x.com"}})
	app.handleFuzzyLinkPickerKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
}

func TestApp_HandleForkTaskKey_Confirmed(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	source := &model.Task{ID: "src", Project: "p", Name: "n", Worktree: "/tmp/wt"}
	app.openForkModal(source)
	app.handleForkTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleRestartDaemonKey_Skip(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.SetSkew(true, false, "", "")
	app.openSkewPrompt()
	app.handleRestartDaemonKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleSessionExitUI_ViewingExitsAgent(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)

	app.mode = modeAgent
	app.agentState.Reset("t1", "n")

	// Clean exit (→ Complete) while viewing → navigate back to the task list.
	app.handleSessionExitUI("t1", true /* cleanExit */, false)
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleSessionExitUI_ViewingStoppedClearsSession(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInProgress, CreatedAt: time.Now()}
	d.Add(task)

	app.mode = modeAgent
	app.agentState.Reset("t1", "n")

	// Non-clean exit (→ InReview) while viewing → STAY in the agent pane so the
	// user sees the exited state and can resume in place (no bounce to the list).
	app.handleSessionExitUI("t1", false /* cleanExit → non-clean */, false)
	testutil.Equal(t, app.mode, modeAgent)
}

func TestApp_RefreshPreview_DeadSessionWithLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("preview-task")
	parentDir := logPath[:strings.LastIndex(logPath, "/")]
	os.MkdirAll(parentDir, 0o755)
	os.WriteFile(logPath, []byte("output content"), 0o644)

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.taskPreview.SetRect(0, 0, 60, 20)
	app.taskPreview.Draw(drawSim(t))

	app.refreshPreview("preview-task")
}

func TestApp_HandleFilePanelKey_NavWithFiles(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentFocus = focusFiles
	app.filePanel.SetRect(0, 0, 40, 20)
	app.filePanel.SetFiles([]gitutil.ChangedFile{
		{Status: "M", Path: "a/b.go"},
		{Status: "A", Path: "a/c.go"},
	})

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
}

func TestApp_OpenAgentLinks_WithTaskID(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.agentState.Reset("test-task", "n")

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		app.openAgentLinks()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("openAgentLinks blocked")
	}
}

func TestApp_CopyToClipboard(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.clipboardWriter = func(string) error { return nil }

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{}, 1)
	app.copyToClipboard("hello", "msg", func() {
		select {
		case done <- struct{}{}:
		default:
		}
	})

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
}

func TestApp_HandleNewTaskKey_Done_NoProjectPath(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{})

	app.onNewTask()

	for _, r := range "hello" {
		app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	app.newTaskForm.done = true

	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	tasks, _ := d.Tasks()
	if len(tasks) == 0 {
		t.Error("expected a task to be added")
	}
}

func TestApp_OpenProjectForm_LoadsBranches(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.openProjectForm(true, "p", config.Project{Path: t.TempDir()})

	if app.projectForm.OnBranchFocus == nil {
		t.Error("OnBranchFocus should be wired")
	}
}

func TestTcellKeyToBytes_MoreCases(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		mod  tcell.ModMask
		want []byte
	}{
		{"home", tcell.KeyHome, 0, []byte("\x1b[H")},
		{"end", tcell.KeyEnd, 0, []byte("\x1b[F")},
		{"pgup", tcell.KeyPgUp, 0, []byte("\x1b[5~")},
		{"pgdn", tcell.KeyPgDn, 0, []byte("\x1b[6~")},
		{"ctrl-a", tcell.KeyCtrlA, 0, []byte{0x01}},
		{"ctrl-b", tcell.KeyCtrlB, 0, []byte{0x02}},
		{"ctrl-e", tcell.KeyCtrlE, 0, []byte{0x05}},
		{"ctrl-f", tcell.KeyCtrlF, 0, []byte{0x06}},
		{"ctrl-g", tcell.KeyCtrlG, 0, []byte{0x07}},
		{"ctrl-h", tcell.KeyCtrlH, 0, []byte{0x08}},
		{"ctrl-k", tcell.KeyCtrlK, 0, []byte{0x0b}},
		{"ctrl-n", tcell.KeyCtrlN, 0, []byte{0x0e}},
		{"ctrl-o", tcell.KeyCtrlO, 0, []byte{0x0f}},
		{"ctrl-p", tcell.KeyCtrlP, 0, []byte{0x10}},
		{"ctrl-r", tcell.KeyCtrlR, 0, []byte{0x12}},
		{"ctrl-s", tcell.KeyCtrlS, 0, []byte{0x13}},
		{"ctrl-t", tcell.KeyCtrlT, 0, []byte{0x14}},
		{"ctrl-u", tcell.KeyCtrlU, 0, []byte{0x15}},
		{"ctrl-v", tcell.KeyCtrlV, 0, []byte{0x16}},
		{"ctrl-w", tcell.KeyCtrlW, 0, []byte{0x17}},
		{"ctrl-x", tcell.KeyCtrlX, 0, []byte{0x18}},
		{"ctrl-y", tcell.KeyCtrlY, 0, []byte{0x19}},
		{"ctrl-z", tcell.KeyCtrlZ, 0, []byte{0x1a}},
		{"alt-backspace", tcell.KeyBackspace, tcell.ModAlt, []byte{0x1b, 0x7f}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, 0, tt.mod)
			got := tcellKeyToBytes(ev)
			testutil.Equal(t, string(got), string(tt.want))
		})
	}
}

// recordingScreen is a tcell.Screen test double that counts Sync() calls.
// Only Size and Sync are exercised by the tests below; the embedded
// nil-interface Screen is unused and will panic if any other method is
// invoked, which is the intended invariant for these tests.
type recordingScreen struct {
	tcell.Screen
	w, h      int
	syncCount int
}

func (r *recordingScreen) Size() (int, int) { return r.w, r.h }
func (r *recordingScreen) Sync()            { r.syncCount++ }

// TestApp_ForceRedrawDoesNotSync pins the post-cleanup contract: forceRedraw
// is a log-only debug helper. It must NOT call screen.Sync() — Sync is
// reserved for the two intentional callsites (Ctrl+L, focus regain) that
// invoke a.screen.Sync() directly.
//
// This test exists specifically to catch the regression where a future
// maintainer accidentally restores `pendingSync.Store(true)` or wires
// forceRedraw back into a Sync-triggering path. The entire premise of the
// May 2026 cleanup (commit c5b537b) is that forceRedraw is observational
// only — if that premise breaks, every cursor move starts flashing again.
//
// See gotchas/ui-threading.md for the post-mortem.
func TestApp_ForceRedrawDoesNotSync(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.screen = &lazyScreen{Screen: &recordingScreen{w: 80, h: 24}}

	// Call forceRedraw many times with various reasons. None should reach
	// screen.Sync() — only the two intentional direct callsites do.
	for range 50 {
		app.forceRedraw("test reason")
	}
	app.forceRedraw("another reason")
	app.forceRedraw("yet another")

	// The embedded screen is a recordingScreen wrapped by lazyScreen.
	// Reach through to verify zero Sync calls.
	rec := app.screen.Screen.(*recordingScreen)
	testutil.Equal(t, rec.syncCount, 0)
}

// TestApp_RedrawLoopGenSupersedesStaleLoop pins the fix for the
// startAgentRedrawLoop leak: leaving and returning to the same task's agent
// view faster than the loop's 200ms wake-up used to leave the old loop
// believing it was still current forever (stillViewing alone can't detect the
// departure it never observed). A newer startAgentRedrawLoop call for the
// same task must bump redrawLoopGen so the stale loop's very next check
// reports "should exit" regardless of what stillViewing reads.
func TestApp_RedrawLoopGenSupersedesStaleLoop(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	const taskID = "redraw-loop-task"
	app.mode = modeAgent
	app.agentState.TaskID = taskID

	// Simulate the first startAgentRedrawLoop call's generation bump.
	app.mu.Lock()
	app.redrawLoopGen[taskID]++
	staleGen := app.redrawLoopGen[taskID]
	app.mu.Unlock()

	// Still current: the user hasn't left, and no newer loop has started.
	testutil.Equal(t, app.redrawLoopShouldExit(taskID, staleGen), false)

	// A revisit to the same task's agent view (fast enough that the stale
	// loop never observed the departure) triggers a second
	// startAgentRedrawLoop call, bumping the generation again.
	app.mu.Lock()
	app.redrawLoopGen[taskID]++
	app.mu.Unlock()

	// stillViewing still reads true (we're back on the same task), but the
	// stale loop must exit anyway because it's been superseded.
	testutil.Equal(t, app.redrawLoopShouldExit(taskID, staleGen), true)
}

// TestApp_RedrawLoopGenClearedOnDelete pins the map cleanup that keeps
// redrawLoopGen from accumulating one entry per task forever in long-lived
// TUI sessions, mirroring the existing pendingRerenderRestart/committedCols
// cleanup on task delete.
func TestApp_RedrawLoopGenClearedOnDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{ID: "del-me", Name: "del-me", Status: model.StatusPending}
	testutil.NoError(t, d.Add(task))

	app.redrawLoopGen[task.ID] = 3

	app.deleteTask(task)

	if _, ok := app.redrawLoopGen[task.ID]; ok {
		t.Fatal("expected redrawLoopGen entry to be cleared on task delete")
	}
}

// TestApp_AfterDrawSyncsOnResizeOnly pins the post-cleanup contract for
// afterDraw: it Syncs exactly once per resize event and never otherwise.
// The full pendingSync/forceRedraw/OnContentChange scaffolding is deleted
// — afterDraw only re-emits the screen when the terminal physically
// changed size (the one "repair screen damage" case tview's Clear+Show
// diff cycle can't handle on its own). Without this, a window resize
// leaves stacked status bars and stale layout artifacts visible.
func TestApp_AfterDrawSyncsOnResizeOnly(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.lastScreenW = 80
	app.lastScreenH = 24

	rec := &recordingScreen{w: 80, h: 24}

	// Same size as last recorded → no Sync.
	app.afterDraw(rec)
	testutil.Equal(t, rec.syncCount, 0)

	// Width change → one Sync.
	rec.w = 100
	app.afterDraw(rec)
	testutil.Equal(t, rec.syncCount, 1)

	// Same size again → no Sync.
	app.afterDraw(rec)
	testutil.Equal(t, rec.syncCount, 1)

	// Height change → one more Sync.
	rec.h = 30
	app.afterDraw(rec)
	testutil.Equal(t, rec.syncCount, 2)

	// Both width and height change → one Sync.
	rec.w = 120
	rec.h = 40
	app.afterDraw(rec)
	testutil.Equal(t, rec.syncCount, 3)

	// Same size again → no Sync.
	for range 50 {
		app.afterDraw(rec)
	}
	testutil.Equal(t, rec.syncCount, 3)
}

// TestHandleSessionExitUI_HeraWorkerFinishPolicy mirrors the daemon's BUG-050
// rule on the TUI flip site: a worker-bound task never self-completes, even on
// a clean exit, and gets the ready_to_close mark. Coordinator/non-hera tasks
// follow the unchanged #707 rule. This keeps the two flip sites in lockstep so
// they can never disagree.
func TestHandleSessionExitUI_HeraWorkerFinishPolicy(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	bind := func(taskID, name string, kind db.HeraRoleKind) {
		o, err := d.CreateHeraOrchestrator("o-"+taskID, "")
		testutil.NoError(t, err)
		r, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: name, Kind: kind, ArgusProject: "p"})
		testutil.NoError(t, err)
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
		testutil.NoError(t, err)
	}
	readyToClose := func(taskID string) bool {
		meta, err := d.ListMeta(taskID, db.HeraMetaNamespace)
		testutil.NoError(t, err)
		for _, e := range meta {
			if e.Key == db.HeraMetaKeyReadyToClose && e.Value == "true" {
				return true
			}
		}
		return false
	}

	t.Run("worker clean exit -> in_review + ready_to_close", func(t *testing.T) {
		task := &model.Task{Name: "w", Status: model.StatusInProgress, Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))
		bind(task.ID, "w", db.HeraKindWorker)

		app.handleSessionExitUI(task.ID, true /* cleanExit */, false /* pendingRestart */)

		got, _ := d.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInReview)
		testutil.Equal(t, readyToClose(task.ID), true)
	})

	t.Run("coordinator clean exit -> complete (not auto-rolled)", func(t *testing.T) {
		task := &model.Task{Name: "c", Status: model.StatusInProgress, Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))
		bind(task.ID, "coord", db.HeraKindCoordinator)

		app.handleSessionExitUI(task.ID, true, false)

		got, _ := d.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusComplete)
		testutil.Equal(t, readyToClose(task.ID), false)
	})

	t.Run("worker pendingRestart -> no flip, no mark", func(t *testing.T) {
		task := &model.Task{Name: "wr", Status: model.StatusInProgress, Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))
		bind(task.ID, "wr", db.HeraKindWorker)

		app.handleSessionExitUI(task.ID, true /* cleanExit */, true /* pendingRestart */)

		got, _ := d.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInProgress)
		testutil.Equal(t, readyToClose(task.ID), false)
	})
}

// TestHandleGlobalKey_HeraRailFilterSwallowsQuitAndHelp pins the global-handler
// guard for the Hera rail `/` filter: while the rail is in search input mode,
// the global rune shortcuts `q` (quit) and `?` (help) must NOT fire — they are
// filter input. The `1` tab-switch case is covered by the smoke test; this adds
// direct coverage of the higher-stakes quit/help paths (a leaked `q` would quit
// the app mid-filter). The control assertions prove the guard is what suppressed
// them: with filtering off, `?` opens help again.
func TestHandleGlobalKey_HeraRailFilterSwallowsQuitAndHelp(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.header.SetTab(widget.TabHera)
	app.mode = modeTaskList

	// Enter rail filter input mode (the rail is the FocusRail region).
	app.heraPage.Rail().InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, app.heraPage.RailFiltering(), true)

	// `q` while filtering is NOT consumed as the global quit — handleGlobalKey
	// returns the event (passed through to the view as filter input), no Stop().
	gotQ := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	testutil.Equal(t, gotQ != nil, true)
	testutil.Equal(t, app.mode != modeHelp, true)

	// `?` while filtering does NOT open the help modal.
	gotHelp := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	testutil.Equal(t, gotHelp != nil, true)
	testutil.Equal(t, app.mode != modeHelp, true)
	testutil.Nil(t, app.helpModal)

	// Control: clear the filter (Esc) → with filtering off, `?` is consumed
	// globally and opens help, proving the guard suppressed it above.
	app.heraPage.Rail().InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, app.heraPage.RailFiltering(), false)
	gotHelp2 := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	testutil.Nil(t, gotHelp2) // consumed globally
	testutil.Equal(t, app.mode == modeHelp, true)
}

// fakeInputSession is a fakeKickSession with a settable LastUserInput, for
// driving the BUG-034 clear-on-input filter in detectNeedsInputSticky. The
// filter clears on USER input only (LastUserInput), so `last` models a user
// keystroke — NOT a system reliable-notify delivery, which never advances it.
type fakeInputSession struct {
	*fakeKickSession
	last time.Time
}

func (f *fakeInputSession) LastUserInput() time.Time { return f.last }

// fakeInputRunner is an agent.SessionProvider whose Get returns canned sessions,
// so detectNeedsInputSticky can read a controllable LastInput per task.
type fakeInputRunner struct {
	*agent.Runner
	sessions map[string]agent.SessionHandle
}

func (r *fakeInputRunner) Get(id string) agent.SessionHandle { return r.sessions[id] }

// TestDetectNeedsInputSticky_ClearOnInput covers BUG-034 in the TUI: a free-text
// question flags (?), persists with no input, then clears once the user delivers
// input to that session — even though the question still sits in the log tail
// (the stale-tail crux) — while input to a different session does not clear it.
func TestDetectNeedsInputSticky_ClearOnInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	// Free-text question (endsInQuestion via the ╭ prompt box) — NO selection
	// widget. This is the exact BUG-034 scenario.
	const question = "⏺ Should I ship it?\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"
	writeLog("c1", question)
	writeLog("c2", question)

	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	s2 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1, "c2": s2},
	}}

	// Tick 1: both idle on a question, no input since → both flagged.
	got := a.detectNeedsInputSticky([]string{"c1", "c2"}, []string{"c1", "c2"}, nil)
	testutil.Equal(t, len(got), 2)

	// Tick 2: no input on either → persists (no decay).
	got = a.detectNeedsInputSticky([]string{"c1", "c2"}, []string{"c1", "c2"}, got)
	testutil.Equal(t, len(got), 2)

	// Tick 3: user responds to c1 only. c1 clears despite the stale question
	// still in its log; c2 stays flagged (cross-session input must not clear it).
	s1.last = t1
	got = a.detectNeedsInputSticky([]string{"c1", "c2"}, []string{"c1", "c2"}, got)
	testutil.DeepEqual(t, got, []string{"c2"})
}

// TestDetectNeedsInputSticky_ClearOnArchive covers BUG-034: an archived task is
// dropped from the needs-input set regardless of its detection signal.
func TestDetectNeedsInputSticky_ClearOnArchive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("c1")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	testutil.NoError(t, os.WriteFile(logPath, []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n"), 0o644))

	a := &App{tasks: []*model.Task{{ID: "c1", Archived: true}}}
	got := a.detectNeedsInputSticky([]string{"c1"}, []string{"c1"}, nil)
	testutil.Equal(t, len(got), 0)
}

// TestDetectNeedsInputSticky_BUG063_StaleReflagDoesNotReStick reproduces the
// exact race through the REAL detectNeedsInputSticky (not just the pure
// agent.NeedsInputClear unit): a task clears on genuine user input, then a
// LATER tick — after a gap tick with no candidacy at all — re-presents the
// SAME stale, already-answered prompt content while the session's session is
// still running and no new input has arrived. Before the BUG-063 fix, the
// gap tick would have forgotten the task's baseline entirely, so the stale
// re-candidacy would recapture baseline == lastInput(id) and get stuck
// flagged forever.
func TestDetectNeedsInputSticky_BUG063_StaleReflagDoesNotReStick(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const prompt = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"
	const plain = "Reading foo.go\nDone.\n"

	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	t2 := time.Unix(3000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1},
	}}
	running := []string{"c1"}

	// Tick 1: idle on a selection prompt, no input since → flagged.
	writeLog("c1", prompt)
	got := a.detectNeedsInputSticky([]string{"c1"}, running, nil)
	testutil.DeepEqual(t, got, []string{"c1"})

	// Tick 2: user responds (lastUserInput advances past the baseline). The
	// stale prompt is STILL in the log (unchanged) — must clear anyway.
	s1.last = t1
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	testutil.Equal(t, len(got), 0)

	// Tick 3: a genuine gap — the log shows plain, non-blocking output, so
	// NEITHER the idle-gated pass nor the content-fingerprint pass sees any
	// signal at all. The session is still running throughout.
	writeLog("c1", plain)
	got = a.detectNeedsInputSticky(nil, running, got)
	testutil.Equal(t, len(got), 0)

	// Tick 4: the log reverts to the EXACT SAME already-answered prompt (a
	// stale re-detection — e.g. a rendering catch-up artifact), with NO new
	// input since t1. This must NOT re-stick the flag.
	writeLog("c1", prompt)
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	if len(got) != 0 {
		t.Fatalf("BUG-063 REGRESSION: stale re-candidacy at the same input timestamp re-stuck the flag: %v", got)
	}

	// It stays clear across further stale re-candidacies too, not just once.
	for i := 0; i < 3; i++ {
		got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
		if len(got) != 0 {
			t.Fatalf("BUG-063 REGRESSION: flag re-stuck on a later tick: %v", got)
		}
	}

	// A genuinely newer input finally arrives → re-arms normally.
	s1.last = t2
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	testutil.DeepEqual(t, got, []string{"c1"})
}

// workingBurstLog is a log tail showing Claude's "working" affordance ("esc to
// interrupt") — the same discriminator BUG-035/036 use for "genuinely
// generating or executing a tool, not merely idling/animating". Used to drive
// the resumed-activity pass (agent.ResumeActivityTick) in the tests below.
const workingBurstLog = "⏺ Want me to ship it?\r\r✻ Cogitating… (12s · esc to interrupt)\r\r╭───╮\r│ > │\r╰───╯\r  ? for shortcuts\r"

// TestDetectNeedsInputSticky_ResumedActivityClears reproduces the live hera-
// worker bug through the REAL detectNeedsInputSticky (not just the pure
// agent.NeedsInputClear unit): a coordinator relays the human's real answer
// via reliable-notify delivery (WriteInputSystem), which never advances
// LastUserInput — so the BUG-034 clear-on-input path can never fire — yet the
// worker demonstrably resumes real work (Claude's "working" affordance,
// sustained across ticks). The flag must clear via the resumed-activity pass
// alone.
func TestDetectNeedsInputSticky_ResumedActivityClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const prompt = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"

	t0 := time.Unix(1000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1},
	}}
	running := []string{"c1"}

	// Tick 1: idle on the selection prompt → flagged.
	writeLog("c1", prompt)
	got := a.detectNeedsInputSticky([]string{"c1"}, running, nil)
	testutil.DeepEqual(t, got, []string{"c1"})

	// The worker resumes real work: it is no longer idle, and its log now
	// shows Claude's "working" affordance, sustained across several ticks.
	// s1.last (LastUserInput) never advances — simulating a coordinator's
	// relayed answer (WriteInputSystem) — only sustained activity can clear
	// this.
	writeLog("c1", workingBurstLog)
	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		got = a.detectNeedsInputSticky(nil, running, got)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d)", agent.NeedsInputResumeTicks, i+1)
		}
	}
	got = a.detectNeedsInputSticky(nil, running, got)
	if len(got) != 0 {
		t.Fatalf("expected the resumed-activity pass to clear the flag after %d sustained working ticks, got %v", agent.NeedsInputResumeTicks, got)
	}
}

// TestDetectNeedsInputSticky_ResumedActivityBriefBurstDoesNotClear guards the
// BUG-034 regression this fix must not reintroduce: a brief working burst —
// fewer than agent.NeedsInputResumeTicks consecutive ticks — followed by
// re-parking at the EXACT SAME blocking prompt must not clear the flag. An
// unrelated system nudge to a genuinely still-parked agent must stay flagged;
// only SUSTAINED resumed activity clears it.
func TestDetectNeedsInputSticky_ResumedActivityBriefBurstDoesNotClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const prompt = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"

	t0 := time.Unix(1000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1},
	}}
	running := []string{"c1"}

	writeLog("c1", prompt)
	got := a.detectNeedsInputSticky([]string{"c1"}, running, nil)
	testutil.DeepEqual(t, got, []string{"c1"})

	// A brief burst of working ticks — one short of the threshold — then it
	// re-parks at the identical blocking prompt.
	writeLog("c1", workingBurstLog)
	for i := 0; i < agent.NeedsInputResumeTicks-2; i++ {
		got = a.detectNeedsInputSticky(nil, running, got)
		if len(got) == 0 {
			t.Fatalf("cleared too early, during the brief working burst (tick %d)", i+1)
		}
	}
	writeLog("c1", prompt)
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	if len(got) != 1 || got[0] != "c1" {
		t.Fatalf("BUG-034 REGRESSION: a brief working burst falsely cleared a still-parked agent, got %v", got)
	}
}

// TestDetectNeedsInputSticky_SettledActivityClears reproduces BUG-072 through
// the REAL detectNeedsInputSticky: a worker resolves its own block and settles
// into idle FASTER than agent.NeedsInputResumeTicks consecutive ticks of
// visible work — too fast for the resumed-activity pass to ever fire (going
// idle drives workingNow false, resetting that streak, and an idle session
// never shows the working affordance again) — with no recorded user input
// either (models a coordinator-relayed resolution, or simply no keystroke).
// Only the settlement pass (agent.SettleTick) can resolve this — the exact
// live repro: the Details pane already showed the session idle and the task
// in_review, yet the rail still showed "(?)" until an incidental keystroke.
func TestDetectNeedsInputSticky_SettledActivityClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const prompt = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"
	const settledLog = "Reading foo.go\nDone.\n"

	t0 := time.Unix(1000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1},
	}}
	running := []string{"c1"}

	// Tick 1: idle on the selection prompt → flagged.
	writeLog("c1", prompt)
	got := a.detectNeedsInputSticky([]string{"c1"}, running, nil)
	testutil.DeepEqual(t, got, []string{"c1"})

	// The worker resolves its own block and goes idle with the blocking signal
	// gone from the log — for FEWER than agent.NeedsInputSettleTicks
	// consecutive ticks, so it must not clear yet. s1.last (LastUserInput)
	// never advances throughout.
	writeLog("c1", settledLog)
	for i := 0; i < agent.NeedsInputSettleTicks-1; i++ {
		got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
		if len(got) == 0 {
			t.Fatalf("cleared too early, before sustaining %d settled ticks (tick %d)", agent.NeedsInputSettleTicks, i+1)
		}
	}
	// The Nth consecutive settled tick clears it.
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	if len(got) != 0 {
		t.Fatalf("expected the settlement pass to clear the flag after %d settled ticks, got %v", agent.NeedsInputSettleTicks, got)
	}
}

// TestDetectNeedsInputSticky_StillBlockedIdleDoesNotSettle guards the BUG-072
// regression this fix must not introduce: an idle session whose log STILL
// shows the identical blocking signal must never be cleared by the settlement
// pass, however many consecutive idle ticks elapse.
func TestDetectNeedsInputSticky_StillBlockedIdleDoesNotSettle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const prompt = "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"
	writeLog("c1", prompt)

	a := &App{}
	running := []string{"c1"}
	var got []string
	for i := 0; i < agent.NeedsInputSettleTicks+3; i++ {
		got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
		if len(got) != 1 || got[0] != "c1" {
			t.Fatalf("BUG-072 REGRESSION: a still-blocked idle session settled and cleared on tick %d: %v", i+1, got)
		}
	}
}

// TestDetectNeedsInputSticky_BUG067_DistinctSequentialPromptReflags reproduces
// the live repro (orchestrator "sketch-handoffs", roles
// 12a-blueprint-ui-lifecycle / 13a-blueprint-restore-version, 2026-07-21):
// Claude's /brainstorm flow asks several DISTINCT AskUserQuestion-style
// prompts in sequence within one session. The user answers question 1
// directly in the pane (a real keystroke, advancing LastUserInput) — a
// genuine, correct clear. Question 2 — a different, still-unanswered prompt —
// then appears before the user types anything else, so LastUserInput is
// UNCHANGED since the question-1 clear. Before this fix, BUG-063's
// stale-recandidacy guard could not tell "the same already-answered content
// re-detected" apart from "a distinct new prompt at the same timestamp" and
// suppressed question 2 forever, even though the agent sat there genuinely
// waiting on it.
func TestDetectNeedsInputSticky_BUG067_DistinctSequentialPromptReflags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeLog := func(taskID, content string) {
		logPath := agent.SessionLogPath(taskID)
		testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
		testutil.NoError(t, os.WriteFile(logPath, []byte(content), 0o644))
	}
	const q1 = "Where should the four lifecycle affordances live in the UI?\n❯ 1. List-centric (card kebab menu)\n  2. Detail-page only\n"
	const q2 = "What should the 'New Blueprint' button do?\n❯ 1. Bare create, open viewer\n  2. Bare create + copy hint\n"

	t0 := time.Unix(1000, 0)
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: t0}
	a := &App{runner: &fakeInputRunner{
		Runner:   agent.NewRunner(nil),
		sessions: map[string]agent.SessionHandle{"c1": s1},
	}}
	running := []string{"c1"}

	// Tick 1: question 1 shown, idle on the selection prompt → flagged.
	writeLog("c1", q1)
	got := a.detectNeedsInputSticky([]string{"c1"}, running, nil)
	testutil.DeepEqual(t, got, []string{"c1"})

	// Tick 2: the user answers question 1 directly in the pane (LastUserInput
	// advances past the baseline) → a genuine, correct clear.
	t1 := time.Unix(2000, 0)
	s1.last = t1
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	testutil.Equal(t, len(got), 0)

	// Tick 3: Claude asks question 2 — a DIFFERENT, still-unanswered prompt —
	// moments later. No further input has arrived (LastUserInput is still
	// t1, identical to the question-1 clear's timestamp). Must re-flag.
	writeLog("c1", q2)
	got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
	if len(got) != 1 || got[0] != "c1" {
		t.Fatalf("BUG-067 REGRESSION: a distinct, unanswered second prompt was suppressed by the BUG-063 stale-marker guard: got %v", got)
	}

	// It stays flagged across subsequent ticks too (not just a one-tick blip).
	for i := 0; i < 3; i++ {
		got = a.detectNeedsInputSticky([]string{"c1"}, running, got)
		if len(got) != 1 || got[0] != "c1" {
			t.Fatalf("BUG-067 REGRESSION: flag dropped on a later tick: got %v", got)
		}
	}
}

// mkHeraBlockedRole creates an orchestrator + worker role bound live to
// taskID, with hera_status already set to "blocked" — the fixture shared by
// the autoClearBlockedHeraRoles tests below. Returns the role's live status
// row (for its BlockedAt timestamp).
func mkHeraBlockedRole(t *testing.T, d *db.DB, taskID string) *db.HeraRoleStatus {
	t.Helper()
	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "worker", Kind: db.HeraKindWorker, ArgusProject: "proj",
	})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/w/" + taskID,
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusBlocked))
	st, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	return st
}

// TestAutoClearBlockedHeraRoles_DirectReply reproduces the exact live repro
// (root-cause-and-fix-a-live): a hera role marks itself "blocked" (e.g.
// awaiting a check-in), the user answers DIRECTLY in the pane — a real
// keystroke, advancing LastUserInput — and the agent's own follow-up reply is
// brief ("Sounds good. Standing by."). hera_status must auto-clear back to
// "working" immediately: it must NOT depend on the resumed-activity threshold,
// since a brief acknowledgment reply may never sustain
// agent.NeedsInputResumeTicks of Claude's "working" affordance. Before this
// fix, hera_status had no auto-clear at all — RoleView.needsInputOwn ORs it
// into the rail's "(?)" alongside the separate, auto-clearing PTY needs-input
// flag, so the glyph stayed lit forever.
func TestAutoClearBlockedHeraRoles_DirectReply(t *testing.T) {
	d := testDB(t)
	st := mkHeraBlockedRole(t, d, "c1")

	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: st.UpdatedAt.Add(-time.Second)}
	a := &App{
		db: d,
		runner: &fakeInputRunner{
			Runner:   agent.NewRunner(nil),
			sessions: map[string]agent.SessionHandle{"c1": s1},
		},
	}

	// Before the user replies: still blocked.
	a.autoClearBlockedHeraRoles([]string{"c1"})
	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusBlocked)

	// The user replies directly in the pane.
	s1.last = st.UpdatedAt.Add(time.Second)
	a.autoClearBlockedHeraRoles([]string{"c1"})
	got, err = d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusWorking)
}

// TestAutoClearBlockedHeraRoles_ResumedActivityClears covers the symmetric
// BUG-065-style case: the block was resolved via a coordinator-relayed answer
// (WriteInputSystem), which never advances LastUserInput, so the direct-reply
// condition can never fire — only sustained resumed activity can clear it.
func TestAutoClearBlockedHeraRoles_ResumedActivityClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	st := mkHeraBlockedRole(t, d, "c1")

	logPath := agent.SessionLogPath("c1")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	testutil.NoError(t, os.WriteFile(logPath, []byte(workingBurstLog), 0o644))

	// LastUserInput never advances past the block — models a system-relayed
	// answer, not a direct keystroke.
	s1 := &fakeInputSession{fakeKickSession: &fakeKickSession{}, last: st.UpdatedAt.Add(-time.Second)}
	a := &App{
		db: d,
		runner: &fakeInputRunner{
			Runner:   agent.NewRunner(nil),
			sessions: map[string]agent.SessionHandle{"c1": s1},
		},
	}

	for i := 0; i < agent.NeedsInputResumeTicks-1; i++ {
		a.autoClearBlockedHeraRoles([]string{"c1"})
		got, err := d.HeraRoleStatusFor(st.RoleID)
		testutil.NoError(t, err)
		if got.Status != db.HeraStatusBlocked {
			t.Fatalf("cleared too early, before sustaining %d working ticks (tick %d): %v", agent.NeedsInputResumeTicks, i+1, got.Status)
		}
	}
	a.autoClearBlockedHeraRoles([]string{"c1"})
	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusWorking)
}

// TestAutoClearBlockedHeraRoles_NotRunningStaysBlocked guards against clearing
// a role whose bound task isn't in the running set at all (e.g. the session
// has exited) — no signal should be trusted for a task that isn't live.
func TestAutoClearBlockedHeraRoles_NotRunningStaysBlocked(t *testing.T) {
	d := testDB(t)
	st := mkHeraBlockedRole(t, d, "c1")
	a := &App{db: d, runner: &fakeInputRunner{Runner: agent.NewRunner(nil), sessions: map[string]agent.SessionHandle{}}}

	a.autoClearBlockedHeraRoles(nil)

	got, err := d.HeraRoleStatusFor(st.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, db.HeraStatusBlocked)
}

// TestAutoClearBlockedHeraRoles_RemoteModeNoOp guards the local-only
// type-assert: in --remote mode a.db is *apistore.Store (no hera methods), so
// this must be a silent no-op, not a panic.
func TestAutoClearBlockedHeraRoles_RemoteModeNoOp(t *testing.T) {
	a := &App{}
	a.autoClearBlockedHeraRoles([]string{"c1"}) // must not panic
}
