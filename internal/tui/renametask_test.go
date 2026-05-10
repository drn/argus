package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRenameTaskForm_Draw(t *testing.T) {
	rf := NewRenameTaskForm("hello")
	rf.SetRect(0, 0, 80, 24)
	rf.Draw(drawSim(t))
}

func TestRenameTaskForm_Draw_LongNameAndError(t *testing.T) {
	rf := NewRenameTaskForm(strings.Repeat("x", 200))
	rf.SetError("name too long")
	rf.SetRect(0, 0, 80, 24)
	rf.Draw(drawSim(t))
}

func TestRenameTaskForm_Draw_TinyRect(t *testing.T) {
	rf := NewRenameTaskForm("x")
	rf.SetRect(0, 0, 0, 0)
	rf.Draw(drawSim(t))
}

func TestRenameTaskForm_SetError(t *testing.T) {
	rf := NewRenameTaskForm("x")
	rf.SetError("nope")
	testutil.Equal(t, rf.errMsg, "nope")
	rf.ResetDone()
	testutil.Equal(t, rf.Done(), false)
}
