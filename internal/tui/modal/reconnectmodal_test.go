package modal

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestReconnectModal_Accessors(t *testing.T) {
	m := NewReconnectModal("Hera", "Reconnecting… (attempt 1)")
	testutil.Equal(t, m.Title(), "Hera")
	testutil.Equal(t, m.Message(), "Reconnecting… (attempt 1)")
	m.SetMessage("Still trying to reconnect… (attempt 9)")
	testutil.Equal(t, m.Message(), "Still trying to reconnect… (attempt 9)")
}

func TestReconnectModal_Draw(t *testing.T) {
	sim := drawAt(t, 100, 40)
	m := NewReconnectModal("Hera", "Reconnecting… (attempt 2)")
	m.SetRect(0, 0, 100, 40)
	m.Draw(sim)
	sim.Sync()

	body := screenString(sim)
	testutil.Contains(t, body, "Hera")
	testutil.Contains(t, body, "Reconnecting")
	testutil.Contains(t, body, "exit") // exit hint
}

func TestReconnectModal_DrawEmptyTitleFallsBack(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewReconnectModal("", "Reconnecting…")
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	testutil.Contains(t, screenString(sim), "Plugin")
}
