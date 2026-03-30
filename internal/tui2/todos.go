package tui2

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// ToDosView is the top-level To Dos tab with a three-panel layout
// matching the task view: list | preview | detail.
type ToDosView struct {
	*tview.Box
	inner *tview.Flex

	list    *ToDoListPanel
	preview *ToDoPreviewPanel
	detail  *ToDoDetailPanel

	vaultPath string
	items     []ToDoItem
	taskMap   map[string]*model.Task // todoPath -> most recent linked task
	tapp      *tview.Application     // for async refresh

	// Callback when the user presses Enter to launch a to-do as a task.
	OnLaunch func(item ToDoItem)
	// Callback when the user changes a linked task's status via s/S keys.
	OnStatusChange func(task *model.Task)
	// Callback when the user presses 'o' and multiple links are found.
	OnOpenLinks func(links []Link)
}

// NewToDosView creates the To Dos tab with three-panel layout.
func NewToDosView() *ToDosView {
	v := &ToDosView{
		Box:     tview.NewBox(),
		list:    NewToDoListPanel(),
		preview: NewToDoPreviewPanel(),
		detail:  NewToDoDetailPanel(),
	}

	v.inner = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(v.list, 0, 1, true).
		AddItem(v.preview, 0, 3, false).
		AddItem(v.detail, 0, 1, false)

	v.list.OnCursorChange = func(item *ToDoItem) {
		v.preview.SetItem(item)
		v.detail.SetItem(item)
	}

	return v
}

// VaultPath returns the configured vault directory path.
func (v *ToDosView) VaultPath() string {
	return v.vaultPath
}

// SetVaultPath sets the vault directory and triggers a scan.
func (v *ToDosView) SetVaultPath(path string) {
	v.vaultPath = path
	v.Refresh()
}

// Refresh rescans the vault directory for to-do notes synchronously.
// Use RefreshAsync for calls from the tview event goroutine.
func (v *ToDosView) Refresh() {
	items, err := ScanVaultToDos(v.vaultPath)
	if err != nil {
		uxlog.Log("[todos] scan error: %v", err)
		return
	}
	v.items = items
	v.list.SetItems(items)
	uxlog.Log("[todos] scanned %d items from %s", len(items), v.vaultPath)
}

// SetApp stores the tview application for async refresh support.
func (v *ToDosView) SetApp(tapp *tview.Application) {
	v.tapp = tapp
}

// RefreshAsync rescans the vault in a background goroutine and delivers
// results via QueueUpdateDraw, avoiding UI thread blocking on disk I/O.
func (v *ToDosView) RefreshAsync(tapp *tview.Application) {
	go func() {
		items, err := ScanVaultToDos(v.vaultPath)
		if err != nil {
			uxlog.Log("[todos] scan error: %v", err)
			return
		}
		tapp.QueueUpdateDraw(func() {
			v.items = items
			v.list.SetItems(items)
			uxlog.Log("[todos] scanned %d items from %s", len(items), v.vaultPath)
		})
	}()
}

// SelectedItem returns the currently selected to-do item, or nil.
func (v *ToDosView) SelectedItem() *ToDoItem {
	return v.list.SelectedItem()
}

// HasItems returns whether there are any to-do items.
func (v *ToDosView) HasItems() bool {
	return len(v.items) > 0
}

// SyncTasks updates the task map used to display linked task status on todo items.
func (v *ToDosView) SyncTasks(taskMap map[string]*model.Task) {
	v.taskMap = taskMap
	v.list.SetTaskMap(taskMap)
}

// CompletedItems returns todo items whose linked task is complete.
func (v *ToDosView) CompletedItems() []ToDoItem {
	var result []ToDoItem
	for _, item := range v.items {
		if t, ok := v.taskMap[item.Path]; ok && t.Status == model.StatusComplete {
			result = append(result, item)
		}
	}
	return result
}

// HandleKey processes key events for the To Dos tab. Returns true if consumed.
func (v *ToDosView) HandleKey(event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyUp:
		v.list.MoveUp()
		return true
	case tcell.KeyDown:
		v.list.MoveDown()
		return true
	case tcell.KeyEnter:
		if item := v.list.SelectedItem(); item != nil && v.OnLaunch != nil {
			v.OnLaunch(*item)
		}
		return true
	case tcell.KeyRune:
		switch event.Rune() {
		case 'j':
			v.list.MoveDown()
			return true
		case 'k':
			v.list.MoveUp()
			return true
		case 'R':
			if v.tapp != nil {
				v.RefreshAsync(v.tapp)
			}
			return true
		case 's':
			if item := v.list.SelectedItem(); item != nil {
				if t, ok := v.taskMap[item.Path]; ok {
					t.SetStatus(t.Status.Next())
					if v.OnStatusChange != nil {
						v.OnStatusChange(t)
					}
				}
			}
			return true
		case 'S':
			if item := v.list.SelectedItem(); item != nil {
				if t, ok := v.taskMap[item.Path]; ok {
					t.SetStatus(t.Status.Prev())
					if v.OnStatusChange != nil {
						v.OnStatusChange(t)
					}
				}
			}
			return true
		case 'o':
			v.openLinks()
			return true
		}
	}
	return false
}

// openLinks extracts links from the selected to-do and either opens the
// single link directly or invokes OnOpenLinks for the user to pick one.
func (v *ToDosView) openLinks() {
	item := v.list.SelectedItem()
	if item == nil {
		return
	}

	// Collect links: PR URL from linked task + URLs from markdown content.
	var links []Link
	if t, ok := v.taskMap[item.Path]; ok && t.PRURL != "" {
		links = append(links, Link{Label: "PR: " + t.PRURL, URL: t.PRURL})
	}
	links = append(links, ExtractLinks(item.Content)...)

	if len(links) == 0 {
		return
	}
	if len(links) == 1 {
		openURL(links[0].URL)
		return
	}
	if v.OnOpenLinks != nil {
		v.OnOpenLinks(links)
	}
}

// Draw renders the three-panel layout or an empty state.
func (v *ToDosView) Draw(screen tcell.Screen) {
	v.Box.DrawForSubclass(screen, v)
	x, y, width, height := v.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	if v.HasItems() {
		v.inner.SetRect(x, y, width, height)
		v.inner.Draw(screen)
		return
	}

	// Empty state
	hint := "No to-do notes found"
	if v.vaultPath != "" {
		hint += fmt.Sprintf(" in %s", v.vaultPath)
	}
	hintY := y + height/2
	hintPad := max((width-len(hint))/2, 0)
	drawText(screen, x+hintPad, hintY, width-hintPad, hint, StyleDimmed)
}

// Focus delegates to the inner flex.
func (v *ToDosView) Focus(delegate func(p tview.Primitive)) {
	v.inner.Focus(delegate)
}

// HasFocus delegates to the inner flex.
func (v *ToDosView) HasFocus() bool {
	return v.inner.HasFocus()
}

// InputHandler delegates to the inner flex.
func (v *ToDosView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return v.inner.InputHandler()
}

// MouseHandler intercepts mouse events so that clicks on non-interactive
// panels (preview, detail) always redirect focus to the list panel.
func (v *ToDosView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return v.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		guardedSetFocus := func(p tview.Primitive) {
			setFocus(v.list)
		}
		innerHandler := v.inner.MouseHandler()
		if innerHandler != nil {
			return innerHandler(action, event, guardedSetFocus)
		}
		return false, nil
	})
}

// ---------------------------------------------------------------------------
// ToDoListPanel — left panel showing to-do names
// ---------------------------------------------------------------------------

// ToDoListPanel displays a scrollable list of to-do notes.
type ToDoListPanel struct {
	*tview.Box
	items   []ToDoItem
	taskMap map[string]*model.Task
	cursor  int
	offset  int

	OnCursorChange func(item *ToDoItem)
}

// SetTaskMap updates the task map used for status-aware bullet rendering.
func (p *ToDoListPanel) SetTaskMap(m map[string]*model.Task) {
	p.taskMap = m
}

// NewToDoListPanel creates a to-do list panel.
func NewToDoListPanel() *ToDoListPanel {
	return &ToDoListPanel{Box: tview.NewBox()}
}

// SetItems updates the list contents.
func (p *ToDoListPanel) SetItems(items []ToDoItem) {
	p.items = items
	if p.cursor >= len(items) {
		p.cursor = max(len(items)-1, 0)
	}
	p.fireCursorChange()
}

// SelectedItem returns the item under the cursor, or nil.
func (p *ToDoListPanel) SelectedItem() *ToDoItem {
	if p.cursor >= 0 && p.cursor < len(p.items) {
		return &p.items[p.cursor]
	}
	return nil
}

// MoveUp moves the cursor up one item.
func (p *ToDoListPanel) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
		p.ensureVisible()
		p.fireCursorChange()
	}
}

// MoveDown moves the cursor down one item.
func (p *ToDoListPanel) MoveDown() {
	if p.cursor < len(p.items)-1 {
		p.cursor++
		p.ensureVisible()
		p.fireCursorChange()
	}
}

func (p *ToDoListPanel) fireCursorChange() {
	if p.OnCursorChange != nil {
		p.OnCursorChange(p.SelectedItem())
	}
}

func (p *ToDoListPanel) ensureVisible() {
	_, _, _, height := p.GetInnerRect()
	innerH := max(height-2, 1) // account for border
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+innerH {
		p.offset = p.cursor - innerH + 1
	}
}

// Draw renders the to-do list panel with a border.
func (p *ToDoListPanel) Draw(screen tcell.Screen) {
	p.Box.DrawForSubclass(screen, p)
	x, y, width, height := p.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	title := fmt.Sprintf(" To Dos (%d) ", len(p.items))
	inner := drawBorderedPanel(screen, x, y, width, height, title, StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if len(p.items) == 0 {
		drawText(screen, inner.X, inner.Y, inner.W, "No items", StyleDimmed)
		return
	}

	for i := 0; i < inner.H; i++ {
		idx := p.offset + i
		if idx >= len(p.items) {
			break
		}
		item := p.items[idx]
		isCursor := idx == p.cursor

		col := inner.X
		prefix := "  "
		drawText(screen, col, inner.Y+i, 2, prefix, StyleDefault)
		col += 2

		// Status-aware bullet
		bullet := '○'
		bulletStyle := StyleDimmed
		if p.taskMap != nil {
			if t, ok := p.taskMap[item.Path]; ok {
				switch t.Status {
				case model.StatusPending:
					bullet = '○'
					bulletStyle = StylePending
				case model.StatusInProgress:
					bullet = '●'
					bulletStyle = StyleInProgress
				case model.StatusInReview:
					bullet = 0xF186
					bulletStyle = StyleInReview
				case model.StatusComplete:
					bullet = '✓'
					bulletStyle = StyleComplete
				}
			}
		}
		screen.SetContent(col, inner.Y+i, bullet, nil, bulletStyle)
		col += 2

		// Name
		nameStyle := StyleNormal
		if isCursor {
			nameStyle = StyleSelected
		}
		nameStr := item.Name
		maxNameW := inner.W - (col - inner.X)
		if maxNameW > 0 && len(nameStr) > maxNameW {
			nameStr = nameStr[:maxNameW-1] + "…"
		}
		drawText(screen, col, inner.Y+i, maxNameW, nameStr, nameStyle)
	}
}

// ---------------------------------------------------------------------------
// ToDoPreviewPanel — center panel showing note content
// ---------------------------------------------------------------------------

// ToDoPreviewPanel displays the markdown content of the selected to-do note.
type ToDoPreviewPanel struct {
	*tview.Box
	item   *ToDoItem
	offset int // scroll offset for content
}

// NewToDoPreviewPanel creates a to-do preview panel.
func NewToDoPreviewPanel() *ToDoPreviewPanel {
	return &ToDoPreviewPanel{Box: tview.NewBox()}
}

// SetItem updates the displayed item and resets scroll.
func (p *ToDoPreviewPanel) SetItem(item *ToDoItem) {
	p.item = item
	p.offset = 0
}

// Draw renders the note content.
func (p *ToDoPreviewPanel) Draw(screen tcell.Screen) {
	p.Box.DrawForSubclass(screen, p)
	x, y, width, height := p.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := drawBorderedPanel(screen, x, y, width, height, " Preview ", StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if p.item == nil {
		drawText(screen, inner.X, inner.Y, inner.W, "No item selected", StyleDimmed)
		return
	}

	// Word-wrap content and render
	lines := wrapTextLines(p.item.Content, inner.W)
	for i := 0; i < inner.H; i++ {
		li := p.offset + i
		if li >= len(lines) {
			break
		}
		drawText(screen, inner.X, inner.Y+i, inner.W, lines[li], StyleNormal)
	}
}

// ---------------------------------------------------------------------------
// ToDoDetailPanel — right panel showing note metadata
// ---------------------------------------------------------------------------

// ToDoDetailPanel displays metadata for the selected to-do note.
type ToDoDetailPanel struct {
	*tview.Box
	item *ToDoItem
}

// NewToDoDetailPanel creates a to-do detail panel.
func NewToDoDetailPanel() *ToDoDetailPanel {
	return &ToDoDetailPanel{Box: tview.NewBox()}
}

// SetItem updates the displayed item.
func (p *ToDoDetailPanel) SetItem(item *ToDoItem) {
	p.item = item
}

// Draw renders the to-do detail panel.
func (p *ToDoDetailPanel) Draw(screen tcell.Screen) {
	p.Box.DrawForSubclass(screen, p)
	x, y, width, height := p.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := drawBorderedPanel(screen, x, y, width, height, " Details ", StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if p.item == nil {
		drawText(screen, inner.X, inner.Y, inner.W, "No item selected", StyleDimmed)
		return
	}

	row := inner.Y

	// Name
	name := p.item.Name
	if len(name) > inner.W-1 {
		name = name[:inner.W-4] + "..."
	}
	drawText(screen, inner.X, row, inner.W, name, StyleTitle)
	row += 2

	// File path (truncated)
	path := p.item.Path
	maxLen := inner.W - 6
	if maxLen > 3 && len(path) > maxLen {
		path = "..." + path[len(path)-maxLen+3:]
	}
	drawField(screen, inner.X, row, inner.W, "File", path, StyleNormal)
	row++

	// Modified date
	drawField(screen, inner.X, row, inner.W, "Modified", p.item.ModTime.Format(time.DateOnly), StyleNormal)
	row++

	// Content size
	size := fmt.Sprintf("%d chars", len(p.item.Content))
	drawField(screen, inner.X, row, inner.W, "Size", size, StyleNormal)
}

// drawField renders "Label: Value" for the detail panel.
func drawField(screen tcell.Screen, x, row, w int, label, value string, valStyle tcell.Style) {
	labelStr := fmt.Sprintf("%s: ", label)
	drawText(screen, x, row, len(labelStr), labelStr, StyleDimmed)
	drawText(screen, x+len(labelStr), row, w-len(labelStr), value, valStyle)
}

// wrapTextLines wraps a multi-line string to fit within maxWidth.
func wrapTextLines(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	var result []string
	for _, rawLine := range strings.Split(text, "\n") {
		if len(rawLine) == 0 {
			result = append(result, "")
			continue
		}
		words := strings.Fields(rawLine)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > maxWidth {
				result = append(result, line)
				line = w
			} else {
				line += " " + w
			}
		}
		result = append(result, line)
	}
	return result
}
