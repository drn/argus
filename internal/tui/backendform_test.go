package tui

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestBackendForm_Draw(t *testing.T) {
	bf := NewBackendForm()
	bf.SetRect(0, 0, 80, 24)
	bf.Draw(drawSim(t))
}

func TestBackendForm_Draw_EditWithError(t *testing.T) {
	bf := NewBackendForm()
	bf.LoadBackend("claude", config.Backend{Command: "claude", PromptFlag: "--"})
	bf.SetError("oh no")
	bf.SetRect(0, 0, 80, 24)
	bf.Draw(drawSim(t))
}

func TestBackendForm_Draw_TinyRect(t *testing.T) {
	bf := NewBackendForm()
	bf.SetRect(0, 0, 0, 0)
	bf.Draw(drawSim(t))
}

func TestBackendForm_SetError(t *testing.T) {
	bf := NewBackendForm()
	bf.SetError("bad")
	testutil.Equal(t, bf.errMsg, "bad")
}
