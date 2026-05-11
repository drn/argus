package tui

import (
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SettingsPage wraps SettingsView with the ASCII banner on top.
type SettingsPage struct {
	*tview.Box
	settings *SettingsView
}

// NewSettingsPage creates a settings page with banner.
func NewSettingsPage(sv *SettingsView) *SettingsPage {
	return &SettingsPage{
		Box:      tview.NewBox(),
		settings: sv,
	}
}

func (sp *SettingsPage) Draw(screen tcell.Screen) {
	sp.Box.DrawForSubclass(screen, sp)
	x, y, width, height := sp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	bh := widget.BannerHeight()
	if height <= bh+3 {
		// Not enough room for banner — just draw settings directly.
		sp.settings.SetRect(x, y, width, height)
		sp.settings.Draw(screen)
		return
	}

	// Draw banner.
	widget.DrawBanner(screen, x, y, width)

	// Draw settings below banner with centered margins matching old BT layout:
	// 20% margin | 20% left | 40% right | 20% margin
	settingsY := y + bh
	settingsH := height - bh

	marginW := width / 5
	innerW := width - 2*marginW
	if innerW < 50 {
		// Too narrow for margins.
		marginW = 0
		innerW = width
	}

	sp.settings.SetRect(x+marginW, settingsY, innerW, settingsH)
	sp.settings.Draw(screen)
}

// PasteHandler forwards bracket-paste events to the settings view.
//
// tview routes paste events to the focused widget's PasteHandler. Since
// the app focuses sp (the page wrapper, not the inner SettingsView), the
// default Box no-op would silently drop pastes during inline vault-path
// or source-path editing. CLAUDE.md's "every widget that accepts text
// input must implement PasteHandler" applies at the focus boundary.
func (sp *SettingsPage) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return sp.settings.PasteHandler()
}

// MouseHandler delegates mouse events to the settings view.
//
// On left click, we route the click to the settings view so it can shift
// focus and (for clicks in the left rail) jump to the clicked category.
// We always redirect focus back to the settings page — never to the
// non-interactive SettingsPage Box default — because tview's default
// Box.MouseHandler steals focus on click and a non-interactive parent
// would silently drop all keyboard input. See gotchas/tasklist-ui.md and
// the page-wrapper MouseHandler rule in CLAUDE.md.
func (sp *SettingsPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return sp.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		if action == tview.MouseLeftClick && event != nil {
			mx, my := event.Position()
			sp.settings.HandleClick(mx, my)
			setFocus(sp)
			return true, nil
		}
		if sp.settings.HandleMouse(action) {
			return true, nil
		}
		return false, nil
	})
}
