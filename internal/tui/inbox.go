package tui

import (
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/uxlog"
)

// openInbox shows the read-only inbox viewer for taskID, restoring the
// previous page on close. Local mode only; remote mode shows a notice.
func (a *App) openInbox(taskID, taskName string) {
	if a.inboxModal != nil || taskID == "" {
		return
	}
	a.inboxPrevPage, _ = a.pages.GetFrontPage()
	a.inboxTaskID = taskID
	title := "Inbox - " + taskName
	if taskName == "" {
		title = "Inbox - " + taskID
	}
	a.inboxModal = modal.NewInboxModal(title)
	a.mode = modeInbox
	a.pages.AddPage("inbox", a.inboxModal, true, true)
	a.pages.SwitchToPage("inbox")
	a.tapp.SetFocus(a.inboxModal)
	uxlog.Log("[inbox] open task=%s", taskID)
	a.loadInbox()
}

// openInboxForTaskID resolves the task's display name, then opens its inbox.
func (a *App) openInboxForTaskID(taskID string) {
	name := ""
	if t, err := a.db.Get(taskID); err == nil && t != nil {
		name = t.Name
	}
	a.openInbox(taskID, name)
}

// loadInbox reads both message stores off the UI thread. A result is dropped
// if the modal it was requested for has since been closed or replaced.
func (a *App) loadInbox() {
	m, taskID := a.inboxModal, a.inboxTaskID
	d, ok := a.db.(*db.DB)
	if !ok {
		m.SetUnavailable("Inbox viewer is not available in remote mode.")
		uxlog.Log("[inbox] skipped load task=%s: remote mode", taskID)
		return
	}
	m.SetLoading()
	go func() {
		entries, err := loadInboxEntries(d, taskID)
		if err != nil {
			uxlog.Log("[inbox] load task=%s failed: %v", taskID, err)
		} else {
			uxlog.Log("[inbox] loaded task=%s messages=%d", taskID, len(entries))
		}
		a.tapp.QueueUpdateDraw(func() {
			if a.inboxModal != m {
				uxlog.Log("[inbox] dropped stale load task=%s", taskID)
				return
			}
			if err != nil {
				m.SetError(err.Error())
				return
			}
			m.SetEntries(entries)
		})
	}()
}

// loadInboxEntries merges task_messages addressed to taskID with hera messages
// addressed to every role taskID is or was bound to, oldest first. Read-only:
// nothing here acks or marks read.
func loadInboxEntries(d *db.DB, taskID string) ([]modal.InboxEntry, error) {
	taskMsgs, err := d.Inbox(taskID, db.InboxFilter{Limit: db.MaxInboxLimit})
	if err != nil {
		return nil, err
	}
	heraMsgs, err := d.HeraMessagesForTask(taskID, db.HeraMaxUnreadPerRole)
	if err != nil {
		return nil, err
	}

	taskNames := map[string]string{}
	taskName := func(id string) string {
		if id == model.SystemTaskID {
			return "system"
		}
		if n, ok := taskNames[id]; ok {
			return n
		}
		n := id
		if t, err := d.Get(id); err == nil && t != nil && t.Name != "" {
			n = t.Name
		}
		taskNames[id] = n
		return n
	}
	roleNames := map[int64]string{}
	roleName := func(id int64) string {
		if n, ok := roleNames[id]; ok {
			return n
		}
		n := "(deleted role)"
		if r, err := d.HeraRole(id); err == nil && r != nil {
			n = r.Name
		}
		roleNames[id] = n
		return n
	}

	out := make([]modal.InboxEntry, 0, len(taskMsgs)+len(heraMsgs))
	for _, m := range taskMsgs {
		out = append(out, taskInboxEntry(m, taskName(m.From)))
	}
	for _, m := range heraMsgs {
		out = append(out, heraInboxEntry(m, roleName(m.FromRoleID), roleName(m.ToRoleID)))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func taskInboxEntry(m *model.TaskMessage, from string) modal.InboxEntry {
	summary := string(m.Kind)
	if m.InReplyTo != "" {
		summary += " (reply to " + m.InReplyTo + ")"
	}
	return modal.InboxEntry{
		At:      m.CreatedAt,
		Source:  "task",
		From:    from,
		Summary: summary,
		Unread:  m.ReadAt.IsZero(),
		ReadAt:  m.ReadAt,
		Body:    m.Body,
	}
}

func heraInboxEntry(m *db.HeraMessage, from, to string) modal.InboxEntry {
	e := modal.InboxEntry{
		At:       m.SentAt,
		Source:   "hera",
		From:     from,
		To:       to,
		Summary:  m.Tldr,
		Unread:   m.ReadAt == nil,
		Delivery: m.DeliveryMode,
		Body:     m.Body,
	}
	if m.ReadAt != nil {
		e.ReadAt = *m.ReadAt
	}
	if m.DeliveredAt != nil {
		e.Delivery += " at " + m.DeliveredAt.Local().Format("01-02 15:04:05")
	}
	return e
}

// handleInboxKey routes keys to the inbox modal, then acts on close/reload.
func (a *App) handleInboxKey(event *tcell.EventKey) {
	a.inboxModal.InputHandler()(event, func(p tview.Primitive) {})
	if a.inboxModal.Closed() {
		a.closeInbox()
		return
	}
	if a.inboxModal.TakeReload() {
		uxlog.Log("[inbox] reload task=%s", a.inboxTaskID)
		a.loadInbox()
	}
}

// closeInbox dismisses the inbox viewer and restores the prior page.
func (a *App) closeInbox() {
	uxlog.Log("[inbox] close task=%s", a.inboxTaskID)
	a.mode = modeTaskList
	a.inboxModal = nil
	a.inboxTaskID = ""
	a.pages.RemovePage("inbox")
	prev := a.inboxPrevPage
	a.inboxPrevPage = ""
	if prev == "inbox" {
		prev = ""
	}
	a.restorePageFocus(prev)
}
