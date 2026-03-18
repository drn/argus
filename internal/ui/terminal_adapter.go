package ui

import tea "github.com/charmbracelet/bubbletea"

const (
	terminalPanelBorderHeight = 2
	terminalPanelBorderWidth  = 4
	minPTYRows                = 5
	minPTYCols                = 20
)

type terminalInputSession interface {
	WriteInput(p []byte) (int, error)
}

type terminalResizeSession interface {
	Resize(rows, cols uint16) error
}

// ptySizeForPanel converts the bordered center-panel dimensions into a PTY size.
func ptySizeForPanel(contentH, centerW int) (rows, cols uint16) {
	rows = uint16(max(contentH-terminalPanelBorderHeight, minPTYRows))
	cols = uint16(max(centerW-terminalPanelBorderWidth, minPTYCols))
	return rows, cols
}

func resizeSessionToPanel(sess terminalResizeSession, contentH, centerW int) error {
	rows, cols := ptySizeForPanel(contentH, centerW)
	return sess.Resize(rows, cols)
}

func forwardKeyToSession(sess terminalInputSession, msg tea.KeyMsg) bool {
	b := keyMsgToBytes(msg)
	if len(b) == 0 {
		return false
	}
	if _, err := sess.WriteInput(b); err != nil {
		return false
	}
	return true
}
