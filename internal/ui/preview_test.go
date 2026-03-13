package ui

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestRenderLine_NoCursor(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello"))
	vt.Lock()
	defer vt.Unlock()

	line := renderLine(vt, 0, 20, -1)
	stripped := stripANSI(line)
	if stripped != "hello" {
		t.Errorf("renderLine without cursor = %q, want %q", stripped, "hello")
	}
	// Should NOT contain reverse video escape
	if strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine without cursor should not contain reverse video")
	}
}

func TestRenderLine_WithCursor(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello"))
	vt.Lock()
	defer vt.Unlock()

	// Cursor at position 2 (on the 'l')
	line := renderLine(vt, 0, 20, 2)
	// Should contain reverse video escape for the cursor
	if !strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine with cursor should contain reverse video escape")
	}
}

func TestRenderLine_CursorBeyondText(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hi"))
	vt.Lock()
	defer vt.Unlock()

	// Cursor at position 5, beyond "hi" (at position 2 would be after text)
	line := renderLine(vt, 0, 20, 5)
	// Should still render cursor (extends lastCol)
	if !strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine with cursor beyond text should still render cursor")
	}
}
