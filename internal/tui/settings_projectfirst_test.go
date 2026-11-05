package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestSettings_ProjectFirst(t *testing.T) {
	sv := makeSettings(t)
	testutil.Equal(t, sv.database.Config().UI.ProjectFirst, false)
	for _, key := range []tcell.Key{tcell.KeyEnter, tcell.KeyRight} {
		t.Run(tcell.NewEventKey(key, 0, 0).Name(), func(t *testing.T) {
			selectRowInCategory(t, sv, catAppearance, srProjectFirst, "")
			testutil.Equal(t, sv.HandleKey(tcell.NewEventKey(key, 0, 0)), true)
			testutil.Equal(t, sv.database.Config().UI.ProjectFirst, key == tcell.KeyEnter)
			sv.Refresh()
			selectRowInCategory(t, sv, catAppearance, srProjectFirst, "")
			sv.SetRect(0, 0, 100, 30)
			sv.Draw(drawSim(t))
		})
	}
	selectRowInCategory(t, sv, catAppearance, srProjectFirst, "")
	testutil.Equal(t, sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0)), true)
	testutil.Equal(t, sv.focus, focusRail)
	testutil.Equal(t, sv.database.Config().UI.ProjectFirst, false)
}

func TestSettings_ProjectFirstSaveFailure(t *testing.T) {
	d := testDB(t)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catAppearance, srProjectFirst, "")
	testutil.NoError(t, d.Close())
	sv.toggleProjectFirst()
	testutil.Equal(t, sv.projectFirst, false)
	testutil.Contains(t, sv.projectFirstError, "Could not save:")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
	sv.SetRect(0, 0, 100, 3)
	sv.Draw(drawSim(t))
}

func TestSettings_ProjectFirstRemoteMatchesAppearance(t *testing.T) {
	d := testDB(t)
	sv := NewSettingsView(d)
	sv.SetRemote(true)
	sv.Refresh()
	testutil.NoError(t, d.Close()) // the same unavailable write as apistore's ui.* path
	sv.toggleProjectFirst()
	testutil.Equal(t, sv.projectFirst, true)
	testutil.Equal(t, sv.projectFirstError, "")
}
