package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type stubTerminalInputSession struct {
	writes [][]byte
	err    error
}

func (s *stubTerminalInputSession) WriteInput(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	s.writes = append(s.writes, cp)
	return len(p), s.err
}

type stubTerminalResizeSession struct {
	rows uint16
	cols uint16
	err  error
}

func (s *stubTerminalResizeSession) Resize(rows, cols uint16) error {
	s.rows = rows
	s.cols = cols
	return s.err
}

func TestPTYSizeForPanel(t *testing.T) {
	rows, cols := ptySizeForPanel(40, 72)
	if rows != 38 || cols != 68 {
		t.Fatalf("ptySizeForPanel(40, 72) = (%d, %d), want (38, 68)", rows, cols)
	}
}

func TestPTYSizeForPanel_ClampsMinimums(t *testing.T) {
	rows, cols := ptySizeForPanel(1, 1)
	if rows != minPTYRows || cols != minPTYCols {
		t.Fatalf("ptySizeForPanel(1, 1) = (%d, %d), want (%d, %d)", rows, cols, minPTYRows, minPTYCols)
	}
}

func TestResizeSessionToPanel(t *testing.T) {
	sess := &stubTerminalResizeSession{}
	if err := resizeSessionToPanel(sess, 18, 44); err != nil {
		t.Fatalf("resizeSessionToPanel returned error: %v", err)
	}
	if sess.rows != 16 || sess.cols != 40 {
		t.Fatalf("Resize called with (%d, %d), want (16, 40)", sess.rows, sess.cols)
	}
}

func TestForwardKeyToSession(t *testing.T) {
	sess := &stubTerminalInputSession{}
	ok := forwardKeyToSession(sess, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !ok {
		t.Fatal("forwardKeyToSession returned false for encodable key")
	}
	if len(sess.writes) != 1 || string(sess.writes[0]) != "x" {
		t.Fatalf("writes = %q, want [\"x\"]", sess.writes)
	}
}

func TestForwardKeyToSession_ReturnsFalseOnWriteError(t *testing.T) {
	sess := &stubTerminalInputSession{err: errors.New("boom")}
	ok := forwardKeyToSession(sess, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if ok {
		t.Fatal("forwardKeyToSession returned true after write failure")
	}
}
