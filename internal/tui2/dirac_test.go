package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

// Uses setupACDirs from projectform_test.go (same package).

func TestDirAC_Update(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")
	var ac dirAC

	t.Run("opens on valid input", func(t *testing.T) {
		ac.Update(root + "/")
		testutil.Equal(t, ac.Open(), true)
		testutil.Equal(t, len(ac.matches), 2)
	})

	t.Run("filters by prefix", func(t *testing.T) {
		ac.Update(root + "/a")
		testutil.Equal(t, ac.Open(), true)
		testutil.Equal(t, len(ac.matches), 1)
		testutil.Contains(t, ac.matches[0], "alpha")
	})

	t.Run("closes on no match", func(t *testing.T) {
		ac.Update(root + "/zzz")
		testutil.Equal(t, ac.Open(), false)
	})

	t.Run("closes on empty input", func(t *testing.T) {
		ac.Update("")
		testutil.Equal(t, ac.Open(), false)
	})
}

func TestDirAC_Accept(t *testing.T) {
	root := setupACDirs(t, "alpha")
	var ac dirAC
	ac.Update(root + "/a")

	testutil.Equal(t, ac.Open(), true)

	path := ac.Accept()
	testutil.Contains(t, path, "alpha/")
}

func TestDirAC_AcceptWhenClosed(t *testing.T) {
	var ac dirAC
	path := ac.Accept()
	testutil.Equal(t, path, "")
}

func TestDirAC_Close(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")
	var ac dirAC
	ac.Update(root + "/")

	testutil.Equal(t, ac.Open(), true)
	ac.Close()
	testutil.Equal(t, ac.Open(), false)
	testutil.Equal(t, len(ac.matches), 0)
	testutil.Equal(t, ac.idx, 0)
}

func TestDirAC_HandleKey_Tab(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	t.Run("tab triggers and accepts when closed", func(t *testing.T) {
		var ac dirAC
		ev := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		consumed, accepted := ac.HandleKey(ev, root+"/a")
		testutil.Equal(t, consumed, true)
		testutil.Contains(t, accepted, "alpha/")
	})

	t.Run("tab accepts when open", func(t *testing.T) {
		var ac dirAC
		ac.Update(root + "/")
		ev := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		consumed, accepted := ac.HandleKey(ev, root+"/")
		testutil.Equal(t, consumed, true)
		testutil.Contains(t, accepted, "alpha/")
	})
}

func TestDirAC_HandleKey_Enter(t *testing.T) {
	root := setupACDirs(t, "alpha")

	t.Run("enter accepts when open", func(t *testing.T) {
		var ac dirAC
		ac.Update(root + "/a")
		ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
		consumed, accepted := ac.HandleKey(ev, root+"/a")
		testutil.Equal(t, consumed, true)
		testutil.Contains(t, accepted, "alpha/")
	})

	t.Run("enter passes through when closed", func(t *testing.T) {
		var ac dirAC
		ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
		consumed, _ := ac.HandleKey(ev, "")
		testutil.Equal(t, consumed, false)
	})
}

func TestDirAC_HandleKey_Escape(t *testing.T) {
	root := setupACDirs(t, "alpha")

	t.Run("escape closes when open", func(t *testing.T) {
		var ac dirAC
		ac.Update(root + "/")
		ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
		consumed, _ := ac.HandleKey(ev, root+"/")
		testutil.Equal(t, consumed, true)
		testutil.Equal(t, ac.Open(), false)
	})

	t.Run("escape passes through when closed", func(t *testing.T) {
		var ac dirAC
		ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
		consumed, _ := ac.HandleKey(ev, "")
		testutil.Equal(t, consumed, false)
	})
}

func TestDirAC_HandleKey_CtrlQ(t *testing.T) {
	root := setupACDirs(t, "alpha")

	t.Run("ctrlq closes when open", func(t *testing.T) {
		var ac dirAC
		ac.Update(root + "/")
		ev := tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
		consumed, _ := ac.HandleKey(ev, root+"/")
		testutil.Equal(t, consumed, true)
		testutil.Equal(t, ac.Open(), false)
	})

	t.Run("ctrlq passes through when closed", func(t *testing.T) {
		var ac dirAC
		ev := tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
		consumed, _ := ac.HandleKey(ev, "")
		testutil.Equal(t, consumed, false)
	})
}

func TestDirAC_HandleKey_UpDown(t *testing.T) {
	root := setupACDirs(t, "aaa", "bbb", "ccc")
	var ac dirAC
	ac.Update(root + "/")

	testutil.Equal(t, ac.idx, 0)

	down := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	up := tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)

	consumed, _ := ac.HandleKey(down, root+"/")
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, ac.idx, 1)

	ac.HandleKey(down, root+"/")
	testutil.Equal(t, ac.idx, 2)

	// Wrap around.
	ac.HandleKey(down, root+"/")
	testutil.Equal(t, ac.idx, 0)

	// Up wraps to end.
	consumed, _ = ac.HandleKey(up, root+"/")
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, ac.idx, 2)
}

func TestDirAC_HandleKey_UpDown_WhenClosed(t *testing.T) {
	var ac dirAC
	ev := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	consumed, _ := ac.HandleKey(ev, "")
	testutil.Equal(t, consumed, false)
}

func TestDirAC_Len(t *testing.T) {
	root := setupACDirs(t, "a", "b", "c", "d", "e")
	var ac dirAC

	t.Run("zero when closed", func(t *testing.T) {
		testutil.Equal(t, ac.Len(8), 0)
	})

	t.Run("capped at maxVisible", func(t *testing.T) {
		ac.Update(root + "/")
		testutil.Equal(t, ac.Len(3), 3)
		testutil.Equal(t, ac.Len(10), 5)
	})
}
