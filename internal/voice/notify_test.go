package voice

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestNotifyMac(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"failure is swallowed", errors.New("osascript missing")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			orig := runOsascript
			runOsascript = func(args ...string) error {
				got = args
				return tt.err
			}
			t.Cleanup(func() { runOsascript = orig })

			NotifyMac(`Argus "quoted"`, `body'; do shell script "x"`)

			testutil.DeepEqual(t, got, []string{
				"-e", "on run argv",
				"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
				"-e", "end run",
				`Argus "quoted"`, `body'; do shell script "x"`,
			})
		})
	}
}
