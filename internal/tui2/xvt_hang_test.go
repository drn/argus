package tui2

import (
	"testing"
	"time"
)

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
