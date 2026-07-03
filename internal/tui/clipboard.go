package tui

import (
	"github.com/drn/argus/internal/uxlog"
)

// clipboardAccessor is satisfied by `*dclient.Client` (local daemon mode) and
// `*apiclient.Provider` (--remote mode). The in-process Runner does NOT
// implement it — when the TUI runs in fallback (no daemon) mode, type
// assertion fails and the agent-staged clipboard feature stays dormant. The
// OS clipboard write helper (copyToClipboard) still works in all modes.
type clipboardAccessor interface {
	ClipboardGet(taskID string) (string, bool)
	ClipboardClear(taskID string) error
}

// copyToClipboard hands text to `a.clipboardWriter` on a goroutine and flashes
// a notice in the global header. The notice auto-clears on its own via
// `Header`'s expiresAt/tick model (`widget.HeaderNoticeTTL`) — no timer or
// goroutine needed here beyond the one already marshaling the write off the
// UI thread; see `gotchas/ui-threading.md` for why a second timer goroutine
// would be redundant. Caller passes an optional onSuccess callback (e.g. for
// uxlog logging that depends on caller-side IDs). Tests that exercise this
// code path MUST overwrite `a.clipboardWriter` with a no-op writer immediately
// after `New()` — otherwise the production `pbcopyWriter` runs and clobbers
// the developer's real clipboard. See the field comment on
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
	}()
}

// flashNotice sets a transient header notice; it auto-clears via `Header`'s
// own expiresAt/tick model, so this is a plain synchronous call — callers run
// on the tview UI goroutine (a key-dispatch callback).
func (a *App) flashNotice(notice string) {
	a.header.SetNotice(notice)
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

// copyStagedClipboardForHeraPane copies the agent-staged clipboard payload for
// a Hera pane's task to the OS clipboard, then clears the daemon-side slot. It
// is the Hera-view analogue of copyStagedClipboard (agent view), but there is no
// single "active" task in the Hera view — it shows several at once — so the
// caller (HeraPage's ctrl+y) passes the FOCUSED pane's task ID and we look the
// payload up directly rather than through the activeAgentTaskID cache. The key
// is always intercepted (see page.go's ctrl+y trap): with the runner not
// daemon-backed (in-process fallback) or nothing staged for that task, this
// flashes a "Nothing to copy" notice instead of copying. Reuses copyToClipboard
// so the "Copied" flash and writer contract match the agent view exactly.
func (a *App) copyStagedClipboardForHeraPane(taskID string) {
	if taskID == "" {
		return
	}
	acc, ok := a.runner.(clipboardAccessor)
	if !ok {
		uxlog.Log("[hera] clipboard copy skipped: runner not daemon-backed (task=%s)", taskID)
		a.flashNotice("Nothing to copy")
		return
	}
	text, present := acc.ClipboardGet(taskID)
	if !present || text == "" {
		uxlog.Log("[hera] clipboard copy skipped: nothing staged (task=%s)", taskID)
		a.flashNotice("Nothing to copy")
		return
	}
	a.copyToClipboard(text, "Copied", func() {
		uxlog.Log("[hera] copied agent-staged clipboard: task %s (%d bytes)", taskID, len(text))
	})
	go func() {
		if err := acc.ClipboardClear(taskID); err != nil {
			uxlog.Log("[hera] clipboard clear failed: task=%s err=%v", taskID, err)
		}
	}()
}

// refreshHeraClipboardHint polls the agent-staged clipboard for the Hera view's
// focused terminal pane and toggles the pane's `(ctrl+y copy)` border-title
// affordance, mirroring refreshClipboardCache for the main agent view. It looks
// at a single task per tick (the focused pane's), so there is no extra RPC
// chattiness beyond the agent view's own per-tick poll. The hint is purely
// discoverability now — ctrl+y is always intercepted regardless of its state
// (see page.go's ctrl+y trap). No-op (hint off) when no terminal pane is
// focused or the runner is not daemon-backed.
func (a *App) refreshHeraClipboardHint() {
	id := a.heraPage.FocusedTerminalTaskID()
	if id == "" {
		a.heraPage.SetClipboardHint(false)
		return
	}
	acc, ok := a.runner.(clipboardAccessor)
	if !ok {
		a.heraPage.SetClipboardHint(false)
		return
	}
	text, present := acc.ClipboardGet(id)
	a.heraPage.SetClipboardHint(present && text != "")
}

// copyStagedClipboard is the ctrl+y handler. Copies the cached pending
// payload via `a.clipboardWriter` (the configured OS-clipboard writer),
// clears the daemon-side state, and flashes "Copied". Returns true if a
// payload was copied, false if nothing was staged — ctrl+y is always
// intercepted (never falls through to the PTY), so the caller flashes a
// "Nothing to copy" notice on false instead.
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
