package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func typeSandboxText(sv *SettingsView, s string) {
	for _, r := range s {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
}

func sandboxPathRows(sv *SettingsView) int {
	n := 0
	for _, r := range sv.rows {
		if r.kind == srSandboxPath {
			n++
		}
	}
	return n
}

func TestSettingsView_SandboxExtraWrite_AddEditDelete(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSandbox)
	sv.focus = focusPane

	// Add.
	testutil.True(t, sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0)))
	testutil.True(t, sv.IsEditing())
	typeSandboxText(sv, "~/Downloads")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.False(t, sv.IsEditing())
	testutil.DeepEqual(t, sv.database.Config().Sandbox.ExtraWrite, []string{"~/Downloads"})
	testutil.Equal(t, sandboxPathRows(sv), 1)

	// Edit the row.
	sv.cursor = 1
	testutil.True(t, sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'e', 0)))
	typeSandboxText(sv, "/x")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.DeepEqual(t, sv.database.Config().Sandbox.ExtraWrite, []string{"~/Downloads/x"})

	// Escape cancels without persisting.
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0))
	typeSandboxText(sv, "/tmp/nope")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, sandboxPathRows(sv), 1)

	// Invalid paths, duplicates and blanks are rejected.
	for _, bad := range []string{"/a,/b", "/", "~", "rel/path"} {
		sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0))
		typeSandboxText(sv, bad)
		sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
		testutil.Contains(t, sv.sandboxPathErr, "Rejected")
	}
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0))
	typeSandboxText(sv, "~/Downloads/x")
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0))
	sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, sandboxPathRows(sv), 1)

	// Delete.
	sv.cursor = 1
	testutil.True(t, sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'd', 0)))
	testutil.Equal(t, sandboxPathRows(sv), 0)
	testutil.Equal(t, len(sv.database.Config().Sandbox.ExtraWrite), 0)
}

func TestSettingsView_SandboxExtraWrite_PasteAndBackspace(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSandbox)
	sv.focus = focusPane
	sv.handleNewSandboxPath()
	sv.editSandboxBuf += "/p"
	sv.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, sv.editSandboxBuf, "/")
	testutil.True(t, sv.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0)))
}

func TestSettingsView_SandboxExtraWrite_CategoryChangeCancelsEdit(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSandbox)
	sv.handleNewSandboxPath()
	testutil.True(t, sv.IsEditing())
	sv.setCategory(catSystem)
	testutil.False(t, sv.IsEditing())
}
