package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// altScreenPromptFrame is a fullscreen (alt-screen) permission-prompt frame —
// the SAME shape agent.altScreenPromptFrame models (unexported there): the ❯
// cursor is painted at an absolute position AFTER "1. Yes"/"2. No", so raw-byte
// detection misses it and only vt emulation reconstructs "❯ 1." (BUG-033).
// secs/glyph vary the spinner chrome so successive ticks of the SAME parked
// prompt differ in raw bytes without representing new agent output.
func altScreenPromptFrame(secs, glyph string) string {
	return "\x1b[?1049h\x1b[2J" +
		"\x1b[1;1H" + glyph + " Brewed for " + secs +
		"\x1b[3;5H\x1b[38;2;200;200;200mDo you want to proceed?\x1b[39m" +
		"\x1b[5;5H1. Yes" +
		"\x1b[6;5H2. No" +
		"\x1b[5;3H\x1b[38;2;177;185;249m❯\x1b[39m" +
		"\x1b[8;1H\x1b[?25l"
}

// altScreenPromptFrameBlinkOff is altScreenPromptFrame with the ❯ selection
// cursor glyph omitted for this one frame — Claude's own fullscreen redraw can
// produce this on a genuinely blinking/animated cursor, and independently,
// readSessionLogTailBytes has no synchronization with the daemon's concurrent
// log-file writer, so an occasional read can land on a torn/partial redraw.
// Either way, the agent is still genuinely parked at the SAME unanswered
// prompt — only this one frame's capture is momentarily incomplete.
func altScreenPromptFrameBlinkOff(secs, glyph string) string {
	return "\x1b[?1049h\x1b[2J" +
		"\x1b[1;1H" + glyph + " Brewed for " + secs +
		"\x1b[3;5H\x1b[38;2;200;200;200mDo you want to proceed?\x1b[39m" +
		"\x1b[5;5H1. Yes" +
		"\x1b[6;5H2. No" +
		"\x1b[8;1H\x1b[?25l"
}

// driveNeedsInputTick runs one real tick of the needs-input pipeline exactly as
// onTick does (detect from disk log → filter to in_progress/hera-managed →
// feed the hera page → schedule the rail rebuild), returning the fresh
// needsInputIDs set so the caller can thread it back as prevNeedsInput on the
// next tick.
func driveNeedsInputTick(t *testing.T, app *App, running, prevNeedsInput []string) []string {
	t.Helper()
	var out []string
	readUI(t, app.tapp, func() {
		out = app.detectNeedsInputSticky(nil, running, prevNeedsInput)
		workers, coordinators := app.readHeraRoles()
		heraManaged := mergeManagedFromMeta(workers, coordinators)
		app.heraPage.SetNeedsInput(needsInputForHeraRail(out, app.tasks, heraManaged))
		app.heraPage.Refresh()
	})
	forceDraw(t, app)
	return out
}

func seedHeraWorker(t *testing.T, d *db.DB, orchID int64, name, taskID string) {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: taskID, Name: name, Status: model.StatusInProgress, Project: "p"}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
}

// TestBUG060_Integration_IntermittentDetectionMissesDoNotPreventNeedsInput is
// the root-cause repro + regression guard: a hera worker is CONTINUOUSLY
// parked at a live, unanswered permission prompt, but its captured frame
// intermittently (every 3rd tick) misses the selection-cursor glyph — a
// blinking cursor, or a torn read of the concurrently-written session log
// racing the daemon's writer. Before the fix, agent.EscalateParkedSelection
// reset its consecutive-tick counter to zero on ANY miss, so a session whose
// detection missed roughly once every few ticks could NEVER reach
// NeedsInputEscalationTicks — the rail never showed "(?)" despite the prompt
// staying live in the pane the whole time. After the fix, an isolated miss is
// held in a one-tick grace period and the streak still converges.
func TestBUG060_Integration_IntermittentDetectionMissesDoNotPreventNeedsInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	const wkrTask = "blink-wkr"
	seedHeraWorker(t, d, o.ID, "wkr", wkrTask)

	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	write := func(content string) {
		t.Helper()
		testutil.NoError(t, os.WriteFile(agent.SessionLogPath(wkrTask), []byte(content), 0o644))
	}

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	running := []string{wkrTask}
	var prev []string
	// Unrecognized spinner glyph throughout (forces reliance on the BUG-029
	// escalation fallback, not the fast 2-tick fingerprint path); every 3rd
	// tick is a blink-off frame. Stops at i=10 (a QUALIFYING tick, not itself
	// a blink-off) so the extra tick appended below is a genuinely ISOLATED
	// miss, not a second consecutive one.
	for i := 0; i < 11; i++ {
		var frame string
		if i%3 == 2 {
			frame = altScreenPromptFrameBlinkOff(fmt.Sprintf("%ds", 20+i), "✷")
		} else {
			frame = altScreenPromptFrame(fmt.Sprintf("%ds", 20+i), "✷")
		}
		write(frame)
		prev = driveNeedsInputTick(t, app, running, prev)
	}
	if !containsStr(prev, wkrTask) {
		t.Fatalf("BUG-060 REGRESSION: worker never reached needs-input despite being continuously parked at a live prompt: %v", prev)
	}
	if app.header.ActiveTab() != widget.TabHera {
		t.Fatalf("setup: expected TabHera, got %v", app.header.ActiveTab())
	}
	readUI(t, app.tapp, func() { app.heraPage.Rail().ToggleCollapse() })
	forceDraw(t, app)
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("rail did not render the needs-input glyph %q for a continuously-parked worker with intermittent detection misses", theme.IconNeedsInput)
	}

	// One more blink-off tick: the flag must NOT flicker off now that it has
	// already escalated (a second consecutive miss would confirm a real break,
	// but a lone one must not).
	write(altScreenPromptFrameBlinkOff("33s", "✷"))
	prev = driveNeedsInputTick(t, app, running, prev)
	if !containsStr(prev, wkrTask) {
		t.Errorf("BUG-060 REGRESSION: an already-escalated worker flickered off needs-input on a single grace-held miss")
	}
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("rail flickered off the needs-input glyph on a single-tick detection miss after already escalating")
	}
}

// TestBUG060_Integration_SecondSiblingEscalatesAlongsideFirst mirrors the live
// bug-bash report exactly: under one coordinator, sib1 (recognized spinner
// glyph) converges quickly via the fast fingerprint path and stays flagged,
// while sib2 (unrecognized spinner glyph, sharing the same App-level
// agent.ScreenRenderer within every tick) must independently escalate and
// ALSO surface "(?)" — the reported symptom was that only the FIRST sibling
// to hit a permission prompt ever lit up the rail; later siblings' own live
// prompts never did.
func TestBUG060_Integration_SecondSiblingEscalatesAlongsideFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	const sib1 = "sib1-wkr"
	const sib2 = "sib2-wkr"
	seedHeraWorker(t, d, o.ID, "sib1", sib1)
	seedHeraWorker(t, d, o.ID, "sib2", sib2)

	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	write := func(task, content string) {
		t.Helper()
		testutil.NoError(t, os.WriteFile(agent.SessionLogPath(task), []byte(content), 0o644))
	}

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	running := []string{sib1, sib2}
	var prev []string
	for i := 0; i < 8; i++ {
		g1 := "✻"
		if i%2 == 1 {
			g1 = "✶"
		}
		write(sib1, altScreenPromptFrame(fmt.Sprintf("%ds", i), g1))
		g2 := []string{"✷", "✸", "✦", "●"}[i%4]
		write(sib2, altScreenPromptFrame(fmt.Sprintf("%ds", 30+i), g2))
		prev = driveNeedsInputTick(t, app, running, prev)
	}
	if !containsStr(prev, sib1) {
		t.Errorf("sib1 (first sibling) not flagged needs-input: %v", prev)
	}
	if !containsStr(prev, sib2) {
		t.Errorf("BUG-060 REGRESSION: sib2 (second sibling) never flagged needs-input despite its own live, continuously-parked prompt: %v", prev)
	}

	readUI(t, app.tapp, func() { app.heraPage.Rail().ToggleCollapse() })
	forceDraw(t, app)
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("rail did not render the needs-input glyph for either sibling")
	}
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
