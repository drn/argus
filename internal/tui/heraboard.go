package tui

import (
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

func (a *App) mutateHeraBoard(d *db.DB, roleID, itemID int64, updatedAt time.Time, dismiss bool) {
	var err error
	if dismiss {
		err = d.ResolveHeraBoardItem(roleID, itemID, "human", "")
	} else {
		err = d.ReadHeraBoardItemAt(roleID, itemID, updatedAt)
	}
	uxlog.Log("[board] human mutation role=%d item=%d dismiss=%v err=%v", roleID, itemID, dismiss, err)
	a.tapp.QueueUpdateDraw(func() {
		if err != nil {
			a.statusbar.SetError("Board update failed: " + err.Error())
			return
		}
		a.heraPage.Refresh()
	})
}
