package tui2

import (
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

	bh := bannerHeight()
	if height <= bh+3 {
		// Not enough room for banner — just draw settings directly.
		sp.settings.SetRect(x, y, width, height)
		sp.settings.Draw(screen)
		return
	}

	// Draw banner.
	drawBanner(screen, x, y, width)

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
