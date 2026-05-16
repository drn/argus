package tui

import (
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// clipboardAccessor is satisfied by `*dclient.Client`. The in-process Runner
// does NOT implement it — when the TUI runs in fallback (no daemon) mode,
// type assertion fails and the agent-staged clipboard feature stays dormant.
// The OS clipboard write helper (copyToClipboard) still works in both modes.
type clipboardAccessor interface {
	ClipboardGet(taskID string) (string, bool)
	ClipboardClear(taskID string) error
}

// copyToClipboard hands text to `a.clipboardWriter` on a goroutine and flashes
// a notice in the global header. Caller passes an optional onSuccess callback
// (e.g. for uxlog logging that depends on caller-side IDs). Tests that exercise
// this code path MUST overwrite `a.clipboardWriter` with a no-op writer
// immediately after `New()` — otherwise the production `pbcopyWriter` runs and
// clobbers the developer's real clipboard. See the field comment on
// `App.clipboardWriter` for the full contract.
func (a *App) copyToClipboard(text, notice string, onSuccess func()) {
	writer := a.clipboardWriter
	go func() {
		if err := writer(text); err != nil {
			uxlog.Log("[tui] clipboard copy failed: %v", err)
			return
		}
		if onSuccess != nil {
			onSuccess()
		}
		a.tapp.QueueUpdateDraw(func() {
			a.header.SetNotice(notice)
		})
		time.Sleep(2 * time.Second)
		a.tapp.QueueUpdateDraw(func() {
			if a.header.Notice() == notice {
				a.header.ClearNotice()
			}
		})
	}()
}

// refreshClipboardCache polls the daemon for the agent-staged payload for
// the given task, updates `a.clipboardPending*`, and toggles the agentHeader
// hint. Called from the tick loop callback (already on the tview goroutine
// inside QueueUpdateDraw, so direct field writes are safe). No-op if the
// runner is not daemon-backed.
func (a *App) refreshClipboardCache(taskID string) {
	acc, ok := a.runner.(clipboardAccessor)
	if !ok {
		return
	}
	text, present := acc.ClipboardGet(taskID)
	prevText := a.clipboardPending
	prevTask := a.clipboardPendingTask
	if !present {
		text = ""
	}
	if text == prevText && taskID == prevTask {
		return
	}
	a.clipboardPending = text
	a.clipboardPendingTask = taskID
	a.agentHeader.SetClipboardHint(text != "")
}

// copyStagedClipboard is the ctrl+y handler. Copies the cached pending
// payload via `a.clipboardWriter` (the configured OS-clipboard writer),
// clears the daemon-side state, and flashes "Copied". Returns true if a
// payload was copied, false if nothing was staged (caller should fall
// through to PTY pass-through).
func (a *App) copyStagedClipboard() bool {
	if a.clipboardPending == "" {
		return false
	}
	text := a.clipboardPending
	taskID := a.clipboardPendingTask
	// Optimistic local clear so the agentHeader hint disappears immediately.
	a.clipboardPending = ""
	a.clipboardPendingTask = ""
	a.agentHeader.SetClipboardHint(false)
	a.copyToClipboard(text, "Copied", func() {
		uxlog.Log("[tui] copied agent-staged clipboard: task %s (%d bytes)", taskID, len(text))
	})
	if acc, ok := a.runner.(clipboardAccessor); ok && taskID != "" {
		go func() {
			if err := acc.ClipboardClear(taskID); err != nil {
				uxlog.Log("[tui] clipboard clear failed: task=%s err=%v", taskID, err)
			}
		}()
	}
	return true
}
