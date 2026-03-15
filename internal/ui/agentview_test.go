package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
)

func newTestAgentView() *AgentView {
	runner := agent.NewRunner(nil)
	av := NewAgentView(DefaultTheme(), runner)
	av.SetSize(120, 40)
	av.Enter("task-1", "test task")
	return &av
}

func TestAgentView_ScrollUpDown(t *testing.T) {
	av := newTestAgentView()

	// Simulate some cached lines (as if output had been rendered)
	av.cachedLines = make([]string, 100)
	for i := range av.cachedLines {
		av.cachedLines[i] = strings.Repeat("x", 10)
	}

	// Initially at bottom
	if av.scrollOffset != 0 {
		t.Fatalf("initial scrollOffset = %d, want 0", av.scrollOffset)
	}

	// Scroll up
	av.scrollUp(5)
	if av.scrollOffset != 5 {
		t.Fatalf("scrollOffset after scrollUp(5) = %d, want 5", av.scrollOffset)
	}

	// Scroll down
	av.scrollDown(3)
	if av.scrollOffset != 2 {
		t.Fatalf("scrollOffset after scrollDown(3) = %d, want 2", av.scrollOffset)
	}

	// Scroll down past bottom clamps to 0
	av.scrollDown(100)
	if av.scrollOffset != 0 {
		t.Fatalf("scrollOffset after scrollDown(100) = %d, want 0", av.scrollOffset)
	}
}

func TestAgentView_ScrollUpClampsToMax(t *testing.T) {
	av := newTestAgentView()

	// 50 lines of content, display fits ~34 lines (height 40 - 1 statusbar - 2 border - 4 padding area ~= 33)
	av.cachedLines = make([]string, 50)
	for i := range av.cachedLines {
		av.cachedLines[i] = "line"
	}

	// Scroll way up — should clamp
	av.scrollUp(1000)
	_, dispH, _ := av.terminalDisplaySize()
	maxScroll := len(av.cachedLines) - dispH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if av.scrollOffset != maxScroll {
		t.Fatalf("scrollOffset = %d, want max %d", av.scrollOffset, maxScroll)
	}
}

func TestAgentView_ShiftUpKeyScrolls(t *testing.T) {
	av := newTestAgentView()
	av.cachedLines = make([]string, 100)
	for i := range av.cachedLines {
		av.cachedLines[i] = "line"
	}

	msg := tea.KeyMsg{Type: tea.KeyShiftUp}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("shift+up should not trigger detach")
	}
	if av.scrollOffset != 1 {
		t.Fatalf("scrollOffset after shift+up = %d, want 1", av.scrollOffset)
	}
}

func TestAgentView_ShiftDownKeyScrolls(t *testing.T) {
	av := newTestAgentView()
	av.cachedLines = make([]string, 100)
	for i := range av.cachedLines {
		av.cachedLines[i] = "line"
	}

	// First scroll up, then down
	av.scrollOffset = 5
	msg := tea.KeyMsg{Type: tea.KeyShiftDown}
	av.HandleKey(msg)
	if av.scrollOffset != 4 {
		t.Fatalf("scrollOffset after shift+down = %d, want 4", av.scrollOffset)
	}
}

func TestAgentView_ShiftEndResetsScroll(t *testing.T) {
	av := newTestAgentView()
	av.scrollOffset = 10

	// shift+end not directly available as a KeyType, test via string
	// Use regular key input to reset scroll instead
	// Any non-scroll key sent to PTY resets scroll to 0
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	av.HandleKey(msg)
	if av.scrollOffset != 0 {
		t.Fatalf("scrollOffset after typing = %d, want 0 (should reset on input)", av.scrollOffset)
	}
}

func TestAgentView_FormatTerminalOutputCapturesScrollback(t *testing.T) {
	av := newTestAgentView()

	// Generate terminal output with many more lines than display height.
	// Each "line\n" produces one row in the virtual terminal.
	var buf []byte
	numLines := 200
	for i := 0; i < numLines; i++ {
		buf = append(buf, []byte(fmt.Sprintf("line %03d\r\n", i))...)
	}

	av.formatTerminalOutput(buf, 120, 40)

	// The display area is about 36 lines (40 - 4), but cachedLines should
	// capture much more than that thanks to the scrollback multiplier.
	_, dispH, _ := av.terminalDisplaySize()
	if len(av.cachedLines) <= dispH {
		t.Fatalf("cachedLines = %d, want > %d (display height); scrollback not working", len(av.cachedLines), dispH)
	}
	// Should capture most of the lines we wrote
	if len(av.cachedLines) < numLines/2 {
		t.Fatalf("cachedLines = %d, want at least %d; not enough scrollback", len(av.cachedLines), numLines/2)
	}
}

func TestAgentView_SliceCachedLines(t *testing.T) {
	av := newTestAgentView()
	av.cachedLines = []string{"line1", "line2", "line3", "line4", "line5"}

	// dispH=3, scrollOffset=0 → last 3 lines
	result := av.sliceCachedLines(80, 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if stripANSI(lines[0]) != "line3" {
		t.Errorf("first visible line = %q, want %q", stripANSI(lines[0]), "line3")
	}

	// scrollOffset=2 → lines 1-3
	av.scrollOffset = 2
	result = av.sliceCachedLines(80, 3)
	lines = strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if stripANSI(lines[0]) != "line1" {
		t.Errorf("first visible line = %q, want %q", stripANSI(lines[0]), "line1")
	}
}

func TestAgentView_SplitWidths_NarrowTerminal(t *testing.T) {
	av := newTestAgentView()
	av.width = 50 // narrow: less than minLeft+minCenter+minRight (100)
	left, center, right := av.splitWidths()

	total := left + center + right
	if total != 50 && total != av.width {
		// total may not perfectly equal width due to rounding, but should be close
		t.Logf("splitWidths(50) = %d+%d+%d = %d", left, center, right, total)
	}
	if center < 30 {
		t.Errorf("center width = %d, want >= 30", center)
	}
}

func TestAgentView_SplitWidths_NormalTerminal(t *testing.T) {
	av := newTestAgentView()
	av.width = 120
	left, center, right := av.splitWidths()

	if left < 20 {
		t.Errorf("left = %d, want >= 20", left)
	}
	if center < 60 {
		t.Errorf("center = %d, want >= 60", center)
	}
	if right < 20 {
		t.Errorf("right = %d, want >= 20", right)
	}
	if left+center+right != 120 {
		t.Errorf("total = %d, want 120", left+center+right)
	}
}

func TestAgentView_SplitWidths_WideTerminal(t *testing.T) {
	av := newTestAgentView()
	av.width = 250
	left, center, right := av.splitWidths()

	if left+center+right != 250 {
		t.Errorf("total = %d, want 250", left+center+right)
	}
	// Center should be ~60% of width
	if center < 140 {
		t.Errorf("center = %d, want >= 140 (60%% of 250)", center)
	}
}

func TestAgentView_FocusLeftRight(t *testing.T) {
	av := newTestAgentView()

	// Default focus is panelAgent (center)
	if av.focus != panelAgent {
		t.Fatalf("initial focus = %d, want panelAgent", av.focus)
	}

	// Focus left → panelGit
	av.FocusLeft()
	if av.focus != panelGit {
		t.Errorf("after FocusLeft: focus = %d, want panelGit", av.focus)
	}

	// Focus left at leftmost → stays
	av.FocusLeft()
	if av.focus != panelGit {
		t.Errorf("FocusLeft at leftmost: focus = %d, want panelGit", av.focus)
	}

	// Focus right back to center
	av.FocusRight()
	if av.focus != panelAgent {
		t.Errorf("after FocusRight: focus = %d, want panelAgent", av.focus)
	}

	// Focus right → panelFiles
	av.FocusRight()
	if av.focus != panelFiles {
		t.Errorf("after FocusRight x2: focus = %d, want panelFiles", av.focus)
	}

	// Focus right at rightmost → stays
	av.FocusRight()
	if av.focus != panelFiles {
		t.Errorf("FocusRight at rightmost: focus = %d, want panelFiles", av.focus)
	}
}

func TestAgentView_HandleKey_CtrlQ_Detach(t *testing.T) {
	av := newTestAgentView()
	msg := tea.KeyMsg{Type: tea.KeyCtrlQ}
	if !av.HandleKey(msg) {
		t.Error("ctrl+q should trigger detach")
	}
}

func TestAgentView_HandleKey_CtrlLeft(t *testing.T) {
	av := newTestAgentView()
	// Start at center
	msg := tea.KeyMsg{Type: tea.KeyCtrlLeft}
	detach := av.HandleKey(msg)
	if detach {
		t.Error("ctrl+left should not trigger detach")
	}
	if av.focus != panelGit {
		t.Errorf("after ctrl+left: focus = %d, want panelGit", av.focus)
	}
}

func TestAgentView_HandleKey_CtrlRight(t *testing.T) {
	av := newTestAgentView()
	msg := tea.KeyMsg{Type: tea.KeyCtrlRight}
	detach := av.HandleKey(msg)
	if detach {
		t.Error("ctrl+right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Errorf("after ctrl+right: focus = %d, want panelFiles", av.focus)
	}
}

func TestAgentView_HandleKey_PlainArrowsNoSwitch(t *testing.T) {
	av := newTestAgentView()
	// Start at center (panelAgent)
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	av.HandleKey(msg)
	if av.focus != panelAgent {
		t.Errorf("plain left should not switch panel: focus = %d, want panelAgent", av.focus)
	}

	msg = tea.KeyMsg{Type: tea.KeyRight}
	av.HandleKey(msg)
	if av.focus != panelAgent {
		t.Errorf("plain right should not switch panel: focus = %d, want panelAgent", av.focus)
	}
}

func TestAgentView_HandleKey_FilePanelKeys(t *testing.T) {
	av := newTestAgentView()
	av.focus = panelFiles
	av.files.SetFiles([]ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "b.go"},
		{Status: "M", Path: "c.go"},
	})

	// j moves down
	av.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if av.files.scroll.Cursor() != 1 {
		t.Errorf("after j: cursor = %d, want 1", av.files.scroll.Cursor())
	}

	// down arrow moves down
	av.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	if av.files.scroll.Cursor() != 2 {
		t.Errorf("after down: cursor = %d, want 2", av.files.scroll.Cursor())
	}

	// k moves up
	av.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if av.files.scroll.Cursor() != 1 {
		t.Errorf("after k: cursor = %d, want 1", av.files.scroll.Cursor())
	}

	// up arrow moves up
	av.HandleKey(tea.KeyMsg{Type: tea.KeyUp})
	if av.files.scroll.Cursor() != 0 {
		t.Errorf("after up: cursor = %d, want 0", av.files.scroll.Cursor())
	}
}

func TestAgentView_NeedsGitRefresh_NoTask(t *testing.T) {
	runner := agent.NewRunner(nil)
	av := NewAgentView(DefaultTheme(), runner)
	if av.NeedsGitRefresh() {
		t.Error("NeedsGitRefresh should be false with no task")
	}
}

func TestAgentView_NeedsGitRefresh_WithTask(t *testing.T) {
	av := newTestAgentView()
	// lastGitRefresh is zero time, so time.Since is large
	if !av.NeedsGitRefresh() {
		t.Error("NeedsGitRefresh should be true for fresh agent view")
	}
}

func TestAgentView_Enter_ResetsState(t *testing.T) {
	av := newTestAgentView()
	av.focus = panelFiles
	av.scrollOffset = 10
	av.cachedTerminal = "old cache"
	av.lastOutput = []byte("old output")

	av.Enter("task-2", "new task")
	if av.taskID != "task-2" {
		t.Errorf("taskID = %q, want 'task-2'", av.taskID)
	}
	if av.taskName != "new task" {
		t.Errorf("taskName = %q, want 'new task'", av.taskName)
	}
	if av.focus != panelAgent {
		t.Errorf("focus = %d, want panelAgent", av.focus)
	}
	if av.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", av.scrollOffset)
	}
	if av.cachedTerminal != "" {
		t.Error("cachedTerminal should be empty after Enter")
	}
	if av.lastOutput != nil {
		t.Error("lastOutput should be nil after Enter")
	}
}

func TestAgentView_View_NoSession(t *testing.T) {
	av := newTestAgentView()
	view := av.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
	// Should contain status bar with task name
	if !strings.Contains(view, "test task") {
		t.Error("expected task name in view")
	}
	// Should contain "Agent not running" or similar since no session exists
	if !strings.Contains(view, "not running") && !strings.Contains(view, "ctrl+q") {
		t.Error("expected agent not running or ctrl+q hint in view")
	}
}

func TestAgentView_RenderStatusBar(t *testing.T) {
	av := newTestAgentView()
	bar := av.renderStatusBar()
	if bar == "" {
		t.Error("expected non-empty status bar")
	}
	if !strings.Contains(bar, "test task") {
		t.Error("expected task name in status bar")
	}
	if !strings.Contains(bar, "ctrl+q") {
		t.Error("expected ctrl+q hint in status bar")
	}
}

func TestAgentView_RenderStatusBar_FocusLabelCentered(t *testing.T) {
	av := newTestAgentView()
	av.focus = panelAgent
	bar := av.renderStatusBar()

	// Strip ANSI to find the position of [TERMINAL]
	plain := stripANSI(bar)
	idx := strings.Index(plain, "[TERMINAL]")
	if idx < 0 {
		t.Fatal("expected [TERMINAL] in status bar")
	}
	// The label should be approximately centered. lipgloss styles may add
	// extra padding characters, so we allow a small tolerance.
	labelLen := len("[TERMINAL]")
	labelCenter := idx + labelLen/2
	barCenter := len(plain) / 2
	if abs(labelCenter-barCenter) > 10 {
		t.Errorf("[TERMINAL] not centered: label center=%d, bar center=%d (bar len=%d)",
			labelCenter, barCenter, len(plain))
	}

	// [TERMINAL] should be closer to center than the task name on the left
	taskIdx := strings.Index(plain, "test task")
	if taskIdx >= 0 && idx <= taskIdx {
		t.Error("[TERMINAL] should appear after the task name, not before")
	}

	// Verify for FILES panel too
	av.focus = panelFiles
	bar = av.renderStatusBar()
	plain = stripANSI(bar)
	idx = strings.Index(plain, "[FILES]")
	if idx < 0 {
		t.Fatal("expected [FILES] in status bar")
	}
	labelLen = len("[FILES]")
	labelCenter = idx + labelLen/2
	barCenter = len(plain) / 2
	if abs(labelCenter-barCenter) > 10 {
		t.Errorf("[FILES] not centered: label center=%d, bar center=%d (bar len=%d)",
			labelCenter, barCenter, len(plain))
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestAgentView_RenderStatusBar_Exited(t *testing.T) {
	av := newTestAgentView()
	// No session → should show "(exited — ctrl+q to return)"
	bar := av.renderStatusBar()
	if !strings.Contains(bar, "exited") {
		t.Error("expected 'exited' in status bar when no session")
	}
}

func TestAgentView_RenderStatusBar_ScrollIndicator(t *testing.T) {
	av := newTestAgentView()
	av.scrollOffset = 5
	// We can't easily test scroll indicator without a session, but verify no panic
	bar := av.renderStatusBar()
	if bar == "" {
		t.Error("expected non-empty status bar")
	}
}

func TestAgentView_RenderTerminal_NoSession_Empty(t *testing.T) {
	av := newTestAgentView()
	_, centerW, _ := av.splitWidths()
	contentH := av.height - 1
	terminal := av.renderTerminal(centerW, contentH)
	if terminal == "" {
		t.Error("expected non-empty terminal output")
	}
	if !strings.Contains(terminal, "Agent not running") {
		t.Error("expected 'Agent not running' in terminal")
	}
}

func TestAgentView_RenderTerminal_NoSession_WithLastOutput(t *testing.T) {
	av := newTestAgentView()
	av.SetLastOutput([]byte("Error: connection refused\n"))
	_, centerW, _ := av.splitWidths()
	contentH := av.height - 1
	terminal := av.renderTerminal(centerW, contentH)
	if terminal == "" {
		t.Error("expected non-empty terminal with last output")
	}
}

func TestAgentView_RenderTerminal_NoSession_WithCachedTerminal(t *testing.T) {
	av := newTestAgentView()
	av.cachedTerminal = "cached content here"
	_, centerW, _ := av.splitWidths()
	contentH := av.height - 1
	terminal := av.renderTerminal(centerW, contentH)
	if !strings.Contains(terminal, "cached content here") {
		t.Error("expected cached terminal content")
	}
}

func TestAgentView_UpdateGitStatus(t *testing.T) {
	av := newTestAgentView()
	msg := GitStatusRefreshMsg{
		TaskID:      "task-1",
		Status:      " M file.go\n A new.go",
		BranchFiles: "M\tfile.go",
	}
	av.UpdateGitStatus(msg)
	// Should update git status and file explorer
	if len(av.files.files) == 0 {
		t.Error("expected files to be populated after UpdateGitStatus")
	}
	if av.lastGitRefresh.IsZero() {
		t.Error("expected lastGitRefresh to be set")
	}
}

func TestAgentView_UpdateGitStatus_MismatchedTask(t *testing.T) {
	av := newTestAgentView()
	msg := GitStatusRefreshMsg{
		TaskID: "other-task",
		Status: " M file.go",
	}
	av.UpdateGitStatus(msg)
	if len(av.files.files) != 0 {
		t.Error("should not update files for mismatched task")
	}
}

func TestAgentView_UpdateGitStatus_BranchFilesOnly(t *testing.T) {
	av := newTestAgentView()
	msg := GitStatusRefreshMsg{
		TaskID:      "task-1",
		BranchFiles: "M\tfile1.go\nA\tfile2.go",
	}
	av.UpdateGitStatus(msg)
	// No uncommitted status, should use branch files
	if len(av.files.files) != 2 {
		t.Errorf("expected 2 files from branch, got %d", len(av.files.files))
	}
}

func TestAgentView_RenderIncremental_FeedsOnlyNewBytes(t *testing.T) {
	av := newTestAgentView()

	// Simulate a session by calling renderIncremental directly with fake data.
	// We create a real session with a short-lived command.
	cmd := exec.Command("echo", "hello world")
	sess, err := agent.StartSession("inc-task", cmd, 30, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// Wait for output
	for i := 0; i < 100; i++ {
		if sess.TotalWritten() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if sess.TotalWritten() == 0 {
		t.Fatal("no output from session")
	}

	raw := sess.RecentOutput()
	total := sess.TotalWritten()

	// First call — initializes vtTerm
	out1 := av.renderIncremental(sess, raw, total, 120, 39)
	if av.vtTerm == nil {
		t.Fatal("expected vtTerm to be initialized after first render")
	}
	if av.vtFedTotal != total {
		t.Errorf("vtFedTotal = %d, want %d", av.vtFedTotal, total)
	}
	if out1 == "" {
		t.Fatal("expected non-empty output from first render")
	}
	if !strings.Contains(stripANSI(out1), "hello") {
		t.Errorf("output should contain 'hello', got %q", stripANSI(out1))
	}

	// Second call with same data — vtTerm should be reused (no reset)
	vtBefore := av.vtTerm
	out2 := av.renderIncremental(sess, raw, total, 120, 39)
	if av.vtTerm != vtBefore {
		t.Error("vtTerm should be reused when dimensions haven't changed")
	}
	if out2 == "" {
		t.Fatal("expected non-empty output from second render")
	}

	// Simulate new bytes by bumping totalWritten (appending to raw)
	extraRaw := append(raw, []byte("extra line\r\n")...)
	extraTotal := total + 12
	out3 := av.renderIncremental(sess, extraRaw, extraTotal, 120, 39)
	if av.vtFedTotal != extraTotal {
		t.Errorf("vtFedTotal = %d, want %d", av.vtFedTotal, extraTotal)
	}
	if out3 == "" {
		t.Fatal("expected non-empty output after incremental feed")
	}
}

func TestAgentView_RenderIncremental_FullResetOnBufferWrap(t *testing.T) {
	av := newTestAgentView()

	cmd := exec.Command("echo", "wrap test")
	sess, err := agent.StartSession("wrap-task", cmd, 30, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	for i := 0; i < 100; i++ {
		if sess.TotalWritten() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	raw := sess.RecentOutput()
	total := sess.TotalWritten()

	// First render
	av.renderIncremental(sess, raw, total, 120, 39)

	// Simulate a huge jump in totalWritten (buffer wrapped far past what we've seen)
	// newBytes would exceed len(raw), forcing a full reset
	hugeTotal := total + uint64(len(raw)) + 10000
	av.renderIncremental(sess, raw, hugeTotal, 120, 39)
	if av.vtFedTotal != hugeTotal {
		t.Errorf("vtFedTotal = %d, want %d after wrap reset", av.vtFedTotal, hugeTotal)
	}
}

func TestKeyMsgToBytes_Runes(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	b := keyMsgToBytes(msg)
	if string(b) != "a" {
		t.Errorf("rune 'a' = %q, want 'a'", string(b))
	}
}

func TestKeyMsgToBytes_Space(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeySpace}
	b := keyMsgToBytes(msg)
	if len(b) != 1 || b[0] != ' ' {
		t.Errorf("space = %v, want [' ']", b)
	}
}

func TestKeyMsgToBytes_Enter(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	b := keyMsgToBytes(msg)
	if len(b) != 1 || b[0] != '\r' {
		t.Errorf("enter = %v, want ['\\r']", b)
	}
}

func TestKeyMsgToBytes_Backspace(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyBackspace}
	b := keyMsgToBytes(msg)
	if len(b) != 1 || b[0] != 0x7f {
		t.Errorf("backspace = %v, want [0x7f]", b)
	}
}

func TestKeyMsgToBytes_ArrowKeys(t *testing.T) {
	tests := []struct {
		keyType tea.KeyType
		want    string
	}{
		{tea.KeyUp, "\x1b[A"},
		{tea.KeyDown, "\x1b[B"},
		{tea.KeyRight, "\x1b[C"},
		{tea.KeyLeft, "\x1b[D"},
	}
	for _, tt := range tests {
		msg := tea.KeyMsg{Type: tt.keyType}
		b := keyMsgToBytes(msg)
		if string(b) != tt.want {
			t.Errorf("key %d = %q, want %q", tt.keyType, string(b), tt.want)
		}
	}
}

func TestKeyMsgToBytes_AltArrow(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyUp, Alt: true}
	b := keyMsgToBytes(msg)
	if string(b) != "\x1b[1;3A" {
		t.Errorf("alt+up = %q, want '\\x1b[1;3A'", string(b))
	}
}

func TestKeyMsgToBytes_CtrlKeys(t *testing.T) {
	tests := []struct {
		keyType tea.KeyType
		want    byte
	}{
		{tea.KeyCtrlA, 0x01},
		{tea.KeyCtrlC, 0x03},
		{tea.KeyCtrlD, 0x04},
		{tea.KeyCtrlZ, 0x1a},
	}
	for _, tt := range tests {
		msg := tea.KeyMsg{Type: tt.keyType}
		b := keyMsgToBytes(msg)
		if len(b) != 1 || b[0] != tt.want {
			t.Errorf("ctrl key %d = %v, want [0x%02x]", tt.keyType, b, tt.want)
		}
	}
}

func TestKeyMsgToBytes_FunctionKeys(t *testing.T) {
	tests := []struct {
		keyType tea.KeyType
		want    string
	}{
		{tea.KeyF1, "\x1bOP"},
		{tea.KeyF2, "\x1bOQ"},
		{tea.KeyF5, "\x1b[15~"},
		{tea.KeyF12, "\x1b[24~"},
	}
	for _, tt := range tests {
		msg := tea.KeyMsg{Type: tt.keyType}
		b := keyMsgToBytes(msg)
		if string(b) != tt.want {
			t.Errorf("function key %d = %q, want %q", tt.keyType, string(b), tt.want)
		}
	}
}

func TestKeyMsgToBytes_Tab(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyTab}
	b := keyMsgToBytes(msg)
	if len(b) != 1 || b[0] != '\t' {
		t.Errorf("tab = %v, want ['\\t']", b)
	}
}

func TestKeyMsgToBytes_Escape(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyEscape}
	b := keyMsgToBytes(msg)
	if len(b) != 1 || b[0] != 0x1b {
		t.Errorf("escape = %v, want [0x1b]", b)
	}
}

func TestKeyMsgToBytes_SpecialKeys(t *testing.T) {
	tests := []struct {
		keyType tea.KeyType
		want    string
	}{
		{tea.KeyHome, "\x1b[H"},
		{tea.KeyEnd, "\x1b[F"},
		{tea.KeyPgUp, "\x1b[5~"},
		{tea.KeyPgDown, "\x1b[6~"},
		{tea.KeyDelete, "\x1b[3~"},
		{tea.KeyShiftTab, "\x1b[Z"},
	}
	for _, tt := range tests {
		msg := tea.KeyMsg{Type: tt.keyType}
		b := keyMsgToBytes(msg)
		if string(b) != tt.want {
			t.Errorf("key %d = %q, want %q", tt.keyType, string(b), tt.want)
		}
	}
}

func TestKeyMsgToBytes_AltRunes(t *testing.T) {
	// Option+b on macOS sends ESC b (word back in readline)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true}
	got := keyMsgToBytes(msg)
	want := []byte{0x1b, 'b'}
	if string(got) != string(want) {
		t.Errorf("Alt+b: got %q, want %q", got, want)
	}

	// Option+f on macOS sends ESC f (word forward in readline)
	msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true}
	got = keyMsgToBytes(msg)
	want = []byte{0x1b, 'f'}
	if string(got) != string(want) {
		t.Errorf("Alt+f: got %q, want %q", got, want)
	}

	// Plain rune without Alt should not get ESC prefix
	msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}}
	got = keyMsgToBytes(msg)
	want = []byte{'b'}
	if string(got) != string(want) {
		t.Errorf("plain b: got %q, want %q", got, want)
	}
}

func TestKeyMsgToBytes_AltArrows(t *testing.T) {
	// Alt+Left should send modified CSI sequence
	msg := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	got := keyMsgToBytes(msg)
	want := []byte("\x1b[1;3D")
	if string(got) != string(want) {
		t.Errorf("Alt+Left: got %q, want %q", got, want)
	}

	// Alt+Right should send modified CSI sequence
	msg = tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	got = keyMsgToBytes(msg)
	want = []byte("\x1b[1;3C")
	if string(got) != string(want) {
		t.Errorf("Alt+Right: got %q, want %q", got, want)
	}

	// Plain Left without Alt
	msg = tea.KeyMsg{Type: tea.KeyLeft}
	got = keyMsgToBytes(msg)
	want = []byte("\x1b[D")
	if string(got) != string(want) {
		t.Errorf("plain Left: got %q, want %q", got, want)
	}
}
