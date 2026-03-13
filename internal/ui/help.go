package ui

import (
	"strings"
)

// HelpView renders the full keybinding help overlay.
type HelpView struct {
	keys  KeyMap
	theme Theme
}

func NewHelpView(keys KeyMap, theme Theme) HelpView {
	return HelpView{keys: keys, theme: theme}
}

func (h HelpView) View() string {
	var b strings.Builder

	b.WriteString(h.theme.Title.Render("Keybindings"))
	b.WriteString("\n\n")

	groups := h.keys.FullHelp()
	for _, group := range groups {
		for _, k := range group {
			help := k.Help()
			keyStr := h.theme.Selected.Render(help.Key)
			desc := h.theme.Normal.Render(help.Desc)
			b.WriteString("  " + keyStr + "  " + desc + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(h.theme.Help.Render("  Press any key to close"))
	return b.String()
}
