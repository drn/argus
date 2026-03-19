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

func TestDrainedEmulatorNoHang(t *testing.T) {
	sequences := map[string][]byte{
		"DA1":     []byte("\x1b[c"),
		"DA2":     []byte("\x1b[>c"),
		"DSR_pos": []byte("\x1b[6n"),
		"DSR_dev": []byte("\x1b[5n"),
	}

	for name, seq := range sequences {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				emu := newDrainedEmulator(80, 24)
				emu.Write(seq)
				close(done)
			}()

			select {
			case <-done:
				// Expected — the drain goroutine prevents the hang.
			case <-time.After(2 * time.Second):
				t.Fatalf("%s: HANG — drain goroutine did not prevent blocking", name)
			}
		})
	}
}

func TestDrainedEmulatorWithSessionData(t *testing.T) {
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".argus", "sessions", "1773949319275631000.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Skipf("session log not found: %v", err)
	}

	if len(data) > 2300 {
		data = data[:2300]
	}
	t.Logf("feeding %d bytes to drained emulator(192, 84)", len(data))

	done := make(chan struct{})
	go func() {
		emu := newDrainedEmulator(192, 84)
		emu.Write(data)
		close(done)
	}()

	select {
	case <-done:
		t.Log("Write completed successfully with drained emulator")
	case <-time.After(5 * time.Second):
		t.Fatal("HANG: drained emulator still hangs on Claude output")
	}
}
