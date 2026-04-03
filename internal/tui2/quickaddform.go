package tui2

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

// qaMaxVisible is the maximum number of repo/autocomplete items shown at once.
const qaMaxVisible = 12

// QuickAddForm is a modal for bulk-importing git projects from a directory.
// Phase 0: user types a directory path (with tab-completion).
// Phase 1: user selects which discovered repos to import.
type QuickAddForm struct {
	*tview.Box

	// Phase 0: directory input.
	dirPath   []rune
	dirCursor int

	// Directory autocomplete.
	acMatches []string // full paths of matching dirs
	acIdx     int
	acOpen    bool

	// Phase 1: repo selection.
	repos     []repoCandidate
	cursor    int
	scrollOff int

	phase    int // 0 = dir input, 1 = selection
	scanning bool // true while background scan is running
	done     bool
	canceled bool
	errMsg   string

	// Existing projects for dedup.
	existingPaths map[string]bool
	existingNames map[string]bool

	// OnScan is called to run the directory scan in a background goroutine.
	// The callback receives the directory to scan; results are delivered
	// back to the form via SetScanResult on the tview goroutine.
	OnScan func(dir string)
}

type repoCandidate struct {
	name     string // display name (may have -2 suffix)
	dirName  string // original directory basename
	path     string // absolute path
	selected bool
}

// NewQuickAddForm creates a new quick-add form.
// existingProjects is used for dedup (by path and name).
func NewQuickAddForm(existingProjects map[string]config.Project) *QuickAddForm {
	paths := make(map[string]bool, len(existingProjects))
	names := make(map[string]bool, len(existingProjects))
	for name, p := range existingProjects {
		paths[p.Path] = true
		names[name] = true
	}
	return &QuickAddForm{
		Box:           tview.NewBox(),
		existingPaths: paths,
		existingNames: names,
	}
}

// Done returns true if the form was submitted.
func (f *QuickAddForm) Done() bool { return f.done }

// Canceled returns true if the form was canceled.
func (f *QuickAddForm) Canceled() bool { return f.canceled }

// SetScanResult delivers the result of a background directory scan.
// Called on the tview goroutine via QueueUpdateDraw.
func (f *QuickAddForm) SetScanResult(repos []repoCandidate, errMsg string) {
	f.scanning = false
	if errMsg != "" {
		f.errMsg = errMsg
		return
	}
	if len(repos) == 0 {
		f.errMsg = "No new git repos found"
		return
	}
	f.repos = repos
	f.cursor = 0
	f.scrollOff = 0
	f.phase = 1
}

// SelectedRepos returns the repos the user chose to import.
func (f *QuickAddForm) SelectedRepos() []repoCandidate {
	var result []repoCandidate
	for _, r := range f.repos {
		if r.selected {
			result = append(result, r)
		}
	}
	return result
}

// --- Directory autocomplete ---

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// collapseTilde replaces the home dir prefix with ~ for display.
func collapseTilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~/" + path[len(home)+1:]
	}
	return path
}

// updateDirAutocomplete computes directory completions for the current input.
func (f *QuickAddForm) updateDirAutocomplete() {
	raw := string(f.dirPath)
	if raw == "" {
		f.acOpen = false
		f.acMatches = nil
		return
	}

	expanded := expandTilde(raw)

	// If path ends with /, list children of that directory.
	// Otherwise, list siblings matching the prefix.
	var parentDir, prefix string
	if strings.HasSuffix(expanded, "/") {
		parentDir = expanded
		prefix = ""
	} else {
		parentDir = filepath.Dir(expanded)
		prefix = filepath.Base(expanded)
	}

	entries, err := os.ReadDir(parentDir)
	if err != nil {
		f.acOpen = false
		f.acMatches = nil
		return
	}

	var matches []string
	lowerPrefix := strings.ToLower(prefix)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip hidden dirs
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		fullPath := filepath.Join(parentDir, name)
		matches = append(matches, fullPath)
	}
	sort.Strings(matches)

	if len(matches) == 0 {
		f.acOpen = false
		f.acMatches = nil
		return
	}

	// Don't show autocomplete if the input already exactly matches one entry
	// (with or without trailing slash).
	if len(matches) == 1 && (matches[0] == expanded || matches[0]+"/" == expanded) {
		f.acOpen = false
		f.acMatches = nil
		return
	}

	f.acMatches = matches
	f.acOpen = true
	if f.acIdx >= len(f.acMatches) {
		f.acIdx = 0
	}
}

// acceptAutocomplete replaces the dir input with the selected autocomplete match.
func (f *QuickAddForm) acceptAutocomplete() {
	if !f.acOpen || f.acIdx >= len(f.acMatches) {
		return
	}
	path := collapseTilde(f.acMatches[f.acIdx]) + "/"
	f.dirPath = []rune(path)
	f.dirCursor = len(f.dirPath)
	f.acOpen = false
	f.acMatches = nil
	f.updateDirAutocomplete()
}

// --- Directory scanning ---

// scanDirectory scans a directory for git repos (immediate children with .git).
// Returns sorted candidates, filtering out already-registered projects.
func scanDirectory(dir string, existingPaths, existingNames map[string]bool) ([]repoCandidate, error) {
	expanded := expandTilde(dir)
	expanded = strings.TrimRight(expanded, "/")

	absDir, err := filepath.Abs(expanded)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	// Track names we'll assign to detect conflicts within this batch.
	usedNames := make(map[string]bool)
	for n := range existingNames {
		usedNames[n] = true
	}

	var candidates []repoCandidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		childPath := filepath.Join(absDir, e.Name())

		// Resolve symlinks to get the real path for dedup and safety.
		realPath, err := filepath.EvalSymlinks(childPath)
		if err != nil {
			continue
		}

		// Check for .git (file or directory).
		if _, err := os.Stat(filepath.Join(realPath, ".git")); err != nil {
			continue
		}

		// Skip already-registered projects (by resolved path).
		if existingPaths[realPath] {
			continue
		}

		// Derive unique name.
		baseName := e.Name()
		name := baseName
		if usedNames[name] {
			for i := 2; ; i++ {
				candidate := baseName + "-" + itoa(i)
				if !usedNames[candidate] {
					name = candidate
					break
				}
			}
		}
		usedNames[name] = true

		candidates = append(candidates, repoCandidate{
			name:     name,
			dirName:  baseName,
			path:     realPath,
			selected: true,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].dirName < candidates[j].dirName
	})

	return candidates, nil
}

// --- Key handling ---

// HandleKey processes key events for the quick-add form.
func (f *QuickAddForm) HandleKey(ev *tcell.EventKey) {
	f.errMsg = ""
	if f.phase == 0 {
		f.handleDirInputKey(ev)
	} else {
		f.handleSelectionKey(ev)
	}
}

func (f *QuickAddForm) handleDirInputKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		if f.acOpen {
			f.acOpen = false
			return
		}
		f.canceled = true
		return

	case tcell.KeyTab:
		if f.acOpen {
			f.acceptAutocomplete()
			return
		}
		f.updateDirAutocomplete()
		if f.acOpen {
			f.acceptAutocomplete()
		}
		return

	case tcell.KeyEnter:
		if f.acOpen {
			f.acceptAutocomplete()
			return
		}
		if f.scanning {
			return
		}
		// Submit directory — trigger async scan.
		dir := strings.TrimSpace(string(f.dirPath))
		if dir == "" {
			f.errMsg = "Enter a directory path"
			return
		}
		f.scanning = true
		f.errMsg = ""
		if f.OnScan != nil {
			f.OnScan(dir)
		}
		return

	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if f.dirCursor > 0 {
			f.dirPath = append(f.dirPath[:f.dirCursor-1], f.dirPath[f.dirCursor:]...)
			f.dirCursor--
			f.updateDirAutocomplete()
		}
		return

	case tcell.KeyDelete:
		if f.dirCursor < len(f.dirPath) {
			f.dirPath = append(f.dirPath[:f.dirCursor], f.dirPath[f.dirCursor+1:]...)
			f.updateDirAutocomplete()
		}
		return

	case tcell.KeyLeft:
		if f.dirCursor > 0 {
			f.dirCursor--
		}
		return

	case tcell.KeyRight:
		if f.dirCursor < len(f.dirPath) {
			f.dirCursor++
		}
		return

	case tcell.KeyHome, tcell.KeyCtrlA:
		f.dirCursor = 0
		return

	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.dirCursor = len(f.dirPath)
		return

	case tcell.KeyCtrlU:
		f.dirPath = f.dirPath[f.dirCursor:]
		f.dirCursor = 0
		f.updateDirAutocomplete()
		return

	case tcell.KeyCtrlK:
		f.dirPath = f.dirPath[:f.dirCursor]
		f.updateDirAutocomplete()
		return

	case tcell.KeyDown:
		if f.acOpen && len(f.acMatches) > 0 {
			f.acIdx = (f.acIdx + 1) % len(f.acMatches)
			// scroll handled in draw
		}
		return

	case tcell.KeyUp:
		if f.acOpen && len(f.acMatches) > 0 {
			if f.acIdx == 0 {
				f.acIdx = len(f.acMatches) - 1
			} else {
				f.acIdx--
			}
		}
		return

	case tcell.KeyRune:
		r := ev.Rune()
		f.dirPath = append(f.dirPath[:f.dirCursor], append([]rune{r}, f.dirPath[f.dirCursor:]...)...)
		f.dirCursor++
		f.updateDirAutocomplete()
		return
	}
}

func (f *QuickAddForm) handleSelectionKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlQ:
		// Go back to dir input.
		f.phase = 0
		f.repos = nil
		return

	case tcell.KeyEnter:
		// Check if any selected.
		hasSelected := false
		for _, r := range f.repos {
			if r.selected {
				hasSelected = true
				break
			}
		}
		if !hasSelected {
			f.errMsg = "No repos selected"
			return
		}
		f.done = true
		return

	case tcell.KeyUp:
		if f.cursor > 0 {
			f.cursor--
		}
		return

	case tcell.KeyDown:
		if f.cursor < len(f.repos)-1 {
			f.cursor++
		}
		return

	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			if f.cursor < len(f.repos) {
				f.repos[f.cursor].selected = !f.repos[f.cursor].selected
			}
		case 'j':
			if f.cursor < len(f.repos)-1 {
				f.cursor++
			}
		case 'k':
			if f.cursor > 0 {
				f.cursor--
			}
		case 'a':
			// Select all.
			for i := range f.repos {
				f.repos[i].selected = true
			}
		case 'x':
			// Deselect all.
			for i := range f.repos {
				f.repos[i].selected = false
			}
		}
		return
	}
}

// PasteHandler handles bracketed paste events.
func (f *QuickAddForm) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return f.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		if f.phase != 0 {
			return
		}
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		newPath := make([]rune, 0, len(f.dirPath)+len(runes))
		newPath = append(newPath, f.dirPath[:f.dirCursor]...)
		newPath = append(newPath, runes...)
		newPath = append(newPath, f.dirPath[f.dirCursor:]...)
		f.dirPath = newPath
		f.dirCursor += len(runes)
		f.updateDirAutocomplete()
	})
}

// --- Draw ---

// Draw renders the quick-add form as a modal.
func (f *QuickAddForm) Draw(screen tcell.Screen) {
	f.Box.DrawForSubclass(screen, f)
	sx, sy, sw, sh := f.GetInnerRect()
	if sw <= 0 || sh <= 0 {
		return
	}

	if f.phase == 0 {
		f.drawDirInput(screen, sx, sy, sw, sh)
	} else {
		f.drawSelection(screen, sx, sy, sw, sh)
	}
}

func (f *QuickAddForm) drawDirInput(screen tcell.Screen, sx, sy, sw, sh int) {
	modalW := min(70, sw-4)
	if modalW < 30 {
		return
	}
	innerW := modalW - 4

	// Compute height: border(2) + title(1) + gap(1) + label(1) + input(1) + ac + gap(1) + help(1) + err
	acRows := 0
	if f.acOpen && len(f.acMatches) > 0 {
		acRows = len(f.acMatches)
		if acRows > qaMaxVisible {
			acRows = qaMaxVisible
		}
	}
	modalH := 8 + acRows
	if f.scanning {
		modalH++
	}
	if f.errMsg != "" {
		modalH++
	}
	if modalH > sh {
		modalH = sh
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	// Clear and draw border.
	f.clearArea(screen, mx, my, modalW, modalH)
	drawBorder(screen, mx, my, modalW, modalH, StyleFocusedBorder)

	// Title.
	title := " Quick Add Projects "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// Label.
	drawText(screen, innerX, row, innerW, "Directory:", StyleTitle)
	row++

	// Input field with cursor.
	val := string(f.dirPath)
	before := string(f.dirPath[:f.dirCursor])
	after := string(f.dirPath[f.dirCursor:])

	inputStyle := tcell.StyleDefault.Foreground(ColorNormal)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	// Scroll input if wider than field.
	displayBefore := before
	displayAfter := after
	beforeW := utf8.RuneCountInString(displayBefore)
	if beforeW >= innerW {
		// Scroll to keep cursor visible.
		runes := []rune(displayBefore)
		start := beforeW - innerW + 1
		displayBefore = string(runes[start:])
		beforeW = innerW - 1
	}

	col := 0
	for _, r := range displayBefore {
		if col >= innerW {
			break
		}
		screen.SetContent(innerX+col, row, r, nil, inputStyle)
		col++
	}
	// Cursor.
	if col < innerW {
		cursorChar := ' '
		if len(displayAfter) > 0 {
			cursorChar, _ = utf8.DecodeRuneInString(displayAfter)
			displayAfter = displayAfter[utf8.RuneLen(cursorChar):]
		}
		screen.SetContent(innerX+col, row, cursorChar, nil, cursorStyle)
		col++
	}
	for _, r := range displayAfter {
		if col >= innerW {
			break
		}
		screen.SetContent(innerX+col, row, r, nil, inputStyle)
		col++
	}
	// Clear rest of input area.
	for col < innerW {
		screen.SetContent(innerX+col, row, ' ', nil, tcell.StyleDefault)
		col++
	}

	// Placeholder when empty.
	if len(val) == 0 {
		placeholder := "~/Development"
		placeholderStyle := tcell.StyleDefault.Foreground(ColorDimmed)
		drawText(screen, innerX+1, row, innerW-1, placeholder, placeholderStyle)
		// Redraw cursor at position 0.
		screen.SetContent(innerX, row, ' ', nil, cursorStyle)
	}
	row++

	// Autocomplete dropdown.
	if f.acOpen && len(f.acMatches) > 0 {
		visible := len(f.acMatches)
		if visible > qaMaxVisible {
			visible = qaMaxVisible
		}
		// Ensure selected item is visible.
		acScroll := 0
		if f.acIdx >= visible {
			acScroll = f.acIdx - visible + 1
		}

		selectedStyle := tcell.StyleDefault.Bold(true).Foreground(ColorSelected)
		for vi := 0; vi < visible; vi++ {
			idx := acScroll + vi
			if idx >= len(f.acMatches) {
				break
			}
			display := collapseTilde(f.acMatches[idx])
			isSelected := idx == f.acIdx

			indicator := "  "
			if isSelected {
				indicator = "> "
			}
			line := indicator + display
			st := StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			lineRunes := []rune(line)
			for c := 0; c < innerW && c < len(lineRunes); c++ {
				screen.SetContent(innerX+c, row+vi, lineRunes[c], nil, st)
			}
		}
		row += visible
	}

	row++ // gap

	// Scanning indicator.
	if f.scanning {
		drawText(screen, innerX, row, innerW, "Scanning...", tcell.StyleDefault.Foreground(ColorTitle))
		row++
	}

	// Error.
	if f.errMsg != "" {
		drawText(screen, innerX, row, innerW, f.errMsg, StyleError)
		row++
	}

	// Help.
	drawText(screen, innerX, row, innerW, "Tab complete  Enter scan  Esc cancel", StyleDimmed)
}

func (f *QuickAddForm) drawSelection(screen tcell.Screen, sx, sy, sw, sh int) {
	modalW := min(70, sw-4)
	if modalW < 30 {
		return
	}
	innerW := modalW - 4

	visibleRepos := len(f.repos)
	if visibleRepos > qaMaxVisible {
		visibleRepos = qaMaxVisible
	}

	// Height: border(2) + title(1) + gap(1) + repos + gap(1) + summary(1) + help(1)
	modalH := 7 + visibleRepos
	if f.errMsg != "" {
		modalH++
	}
	if modalH > sh {
		modalH = sh
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	f.clearArea(screen, mx, my, modalW, modalH)
	drawBorder(screen, mx, my, modalW, modalH, StyleFocusedBorder)

	// Title.
	dir := collapseTilde(strings.TrimRight(string(f.dirPath), "/"))
	title := " " + dir + " "
	if utf8.RuneCountInString(title) > modalW-4 {
		title = " Quick Add "
	}
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// Ensure cursor is visible.
	if f.cursor < f.scrollOff {
		f.scrollOff = f.cursor
	}
	if f.cursor >= f.scrollOff+visibleRepos {
		f.scrollOff = f.cursor - visibleRepos + 1
	}

	// Repo list.
	for vi := 0; vi < visibleRepos; vi++ {
		idx := f.scrollOff + vi
		if idx >= len(f.repos) {
			break
		}
		repo := f.repos[idx]
		isCursor := idx == f.cursor

		check := "[ ] "
		if repo.selected {
			check = "[x] "
		}

		label := check + repo.name
		if repo.name != repo.dirName {
			label += " (" + repo.dirName + ")"
		}

		st := tcell.StyleDefault.Foreground(ColorNormal)
		if !repo.selected {
			st = StyleDimmed
		}
		if isCursor {
			st = st.Bold(true).Foreground(ColorSelected)
		}

		lineRunes := []rune(label)
		c := 0
		for ; c < innerW && c < len(lineRunes); c++ {
			screen.SetContent(innerX+c, row+vi, lineRunes[c], nil, st)
		}
		for ; c < innerW; c++ {
			screen.SetContent(innerX+c, row+vi, ' ', nil, tcell.StyleDefault)
		}
	}
	row += visibleRepos

	row++ // gap

	// Summary.
	selected := 0
	for _, r := range f.repos {
		if r.selected {
			selected++
		}
	}
	summary := itoa(selected) + "/" + itoa(len(f.repos)) + " selected"
	drawText(screen, innerX, row, innerW, summary, tcell.StyleDefault.Foreground(ColorTitle))
	row++

	// Error.
	if f.errMsg != "" {
		drawText(screen, innerX, row, innerW, f.errMsg, StyleError)
		row++
	}

	// Help.
	drawText(screen, innerX, row, innerW, "Space toggle  a all  x none  Enter add  Esc back", StyleDimmed)
}

func (f *QuickAddForm) clearArea(screen tcell.Screen, x, y, w, h int) {
	clearStyle := tcell.StyleDefault
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}
}
