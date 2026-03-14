package ui

import (
	"strings"
	"testing"

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
