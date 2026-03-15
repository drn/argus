package ui

import tea "github.com/charmbracelet/bubbletea"

// keyByteMap maps Bubble Tea key types to their raw terminal byte sequences.
var keyByteMap = map[tea.KeyType][]byte{
	tea.KeySpace:     {' '},
	tea.KeyEnter:     {'\r'},
	tea.KeyBackspace: {0x7f},
	tea.KeyTab:       {'\t'},
	tea.KeyShiftTab:  []byte("\x1b[Z"),
	tea.KeyEscape:    {0x1b},
	tea.KeyHome:      []byte("\x1b[H"),
	tea.KeyEnd:       []byte("\x1b[F"),
	tea.KeyPgUp:      []byte("\x1b[5~"),
	tea.KeyPgDown:    []byte("\x1b[6~"),
	tea.KeyDelete:    []byte("\x1b[3~"),
	tea.KeyCtrlA:     {0x01},
	tea.KeyCtrlB:     {0x02},
	tea.KeyCtrlC:     {0x03},
	tea.KeyCtrlD:     {0x04},
	tea.KeyCtrlE:     {0x05},
	tea.KeyCtrlF:     {0x06},
	tea.KeyCtrlG:     {0x07},
	tea.KeyCtrlH:     {0x08},
	tea.KeyCtrlK:     {0x0b},
	tea.KeyCtrlL:     {0x0c},
	tea.KeyCtrlN:     {0x0e},
	tea.KeyCtrlO:     {0x0f},
	tea.KeyCtrlP:     {0x10},
	tea.KeyCtrlR:     {0x12},
	tea.KeyCtrlS:     {0x13},
	tea.KeyCtrlT:     {0x14},
	tea.KeyCtrlU:     {0x15},
	tea.KeyCtrlV:     {0x16},
	tea.KeyCtrlW:     {0x17},
	tea.KeyCtrlX:     {0x18},
	tea.KeyCtrlY:     {0x19},
	tea.KeyCtrlZ:     {0x1a},
	tea.KeyF1:        []byte("\x1bOP"),
	tea.KeyF2:        []byte("\x1bOQ"),
	tea.KeyF3:        []byte("\x1bOR"),
	tea.KeyF4:        []byte("\x1bOS"),
	tea.KeyF5:        []byte("\x1b[15~"),
	tea.KeyF6:        []byte("\x1b[17~"),
	tea.KeyF7:        []byte("\x1b[18~"),
	tea.KeyF8:        []byte("\x1b[19~"),
	tea.KeyF9:        []byte("\x1b[20~"),
	tea.KeyF10:       []byte("\x1b[21~"),
	tea.KeyF11:       []byte("\x1b[23~"),
	tea.KeyF12:       []byte("\x1b[24~"),
}

// altArrowMap maps arrow key types to their Alt-modified escape sequences.
var altArrowMap = map[tea.KeyType][]byte{
	tea.KeyUp:    []byte("\x1b[1;3A"),
	tea.KeyDown:  []byte("\x1b[1;3B"),
	tea.KeyRight: []byte("\x1b[1;3C"),
	tea.KeyLeft:  []byte("\x1b[1;3D"),
}

// arrowMap maps arrow key types to their standard escape sequences.
var arrowMap = map[tea.KeyType][]byte{
	tea.KeyUp:    []byte("\x1b[A"),
	tea.KeyDown:  []byte("\x1b[B"),
	tea.KeyRight: []byte("\x1b[C"),
	tea.KeyLeft:  []byte("\x1b[D"),
}

// keyMsgToBytes converts a Bubble Tea key message to raw terminal bytes.
func keyMsgToBytes(msg tea.KeyMsg) []byte {
	if msg.Type == tea.KeyRunes {
		b := []byte(string(msg.Runes))
		if msg.Alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	if msg.Alt {
		if b, ok := altArrowMap[msg.Type]; ok {
			return b
		}
	}

	if b, ok := arrowMap[msg.Type]; ok {
		if msg.Alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	if b, ok := keyByteMap[msg.Type]; ok {
		if msg.Alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	if s := msg.String(); s != "" {
		return []byte(s)
	}
	return nil
}
