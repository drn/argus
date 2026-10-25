package config

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestCoordinatorBoardSetting(t *testing.T) {
	base := DefaultConfig()
	testutil.Equal(t, base.UI.CoordinatorBoard, true)
	loader := NewFileLoader(writeFile(t, "[ui]\ncoordinator_board = false\n[keybindings.hera_board]\nreply = 'R'\n"))
	got := loader.Apply(base)
	testutil.NoError(t, loader.Err())
	testutil.Equal(t, got.UI.CoordinatorBoard, false)
	testutil.Equal(t, got.Keybindings.HeraBoard["reply"], "R")
	testutil.Equal(t, base.UI.CoordinatorBoard, true)
}
