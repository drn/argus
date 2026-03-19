package tui2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	xvt "github.com/charmbracelet/x/vt"
)

func TestXVTDAHang(t *testing.T) {
	sequences := map[string][]byte{
		"DA1":       []byte("\x1b[c"),
		"DA2":       []byte("\x1b[>c"),
		"DA3":       []byte("\x1b[=c"),
		"XTVERSION": []byte("\x1b[>0q"),
		"DSR_pos":   []byte("\x1b[6n"),
		"DSR_dev":   []byte("\x1b[5n"),
	}

	for name, seq := range sequences {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				emu := xvt.NewSafeEmulator(80, 24)
				emu.Write(seq)
				close(done)
			}()

			select {
			case <-done:
				t.Logf("%s: OK (no hang)", name)
			case <-time.After(2 * time.Second):
				t.Logf("%s: HANG (expected for DA1/DA2/DSR)", name)
			}
		})
	}
}

func TestStripTerminalQueries(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{"no queries", []byte("hello world"), []byte("hello world")},
		{"DA1", []byte("before\x1b[cafter"), []byte("beforeafter")},
		{"DA1 with 0", []byte("before\x1b[0cafter"), []byte("beforeafter")},
		{"DA2", []byte("before\x1b[>cafter"), []byte("beforeafter")},
		{"DA2 with 0", []byte("before\x1b[>0cafter"), []byte("beforeafter")},
		{"DSR5", []byte("before\x1b[5nafter"), []byte("beforeafter")},
		{"DSR6", []byte("before\x1b[6nafter"), []byte("beforeafter")},
		{"non-query CSI", []byte("before\x1b[38;5;174mafter"), []byte("before\x1b[38;5;174mafter")},
		{"multiple queries", []byte("\x1b[c\x1b[6nhello"), []byte("hello")},
		{"XTVERSION kept", []byte("before\x1b[>0qafter"), []byte("before\x1b[>0qafter")},
		{"DA3 kept", []byte("before\x1b[=cafter"), []byte("before\x1b[=cafter")},
		{"empty", []byte{}, []byte{}},
		{"incomplete ESC at end", []byte("hello\x1b"), []byte("hello\x1b")},
		{"incomplete CSI at end", []byte("hello\x1b["), []byte("hello\x1b[")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTerminalQueries(tt.input)
			if string(got) != string(tt.want) {
				t.Errorf("stripTerminalQueries(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripTerminalQueriesFixesHang(t *testing.T) {
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".argus", "sessions", "1773949319275631000.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Skipf("session log not found: %v", err)
	}

	if len(data) > 2300 {
		data = data[:2300]
	}

	clean := stripTerminalQueries(data)
	t.Logf("original: %d bytes, stripped: %d bytes (removed %d)", len(data), len(clean), len(data)-len(clean))

	done := make(chan struct{})
	go func() {
		emu := xvt.NewSafeEmulator(192, 84)
		emu.Write(clean)
		close(done)
	}()

	select {
	case <-done:
		t.Log("Write completed successfully after stripping queries")
	case <-time.After(5 * time.Second):
		t.Fatal("HANG: emu.Write still hangs after stripping queries")
	}
}
