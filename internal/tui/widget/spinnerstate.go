package widget

import (
	"sync/atomic"
	"time"

	"github.com/drn/argus/internal/spinner"
)

// activeSpinner holds the currently active spinner. Accessed from multiple
// goroutines (spinnerLoop reader, tview main goroutine writer) so uses atomic.
var activeSpinner atomic.Pointer[spinner.Spinner]

func init() {
	activeSpinner.Store(spinner.Get(spinner.StyleProgress))
}

// SetActiveSpinner changes the active spinner style.
func SetActiveSpinner(style string) {
	activeSpinner.Store(spinner.Get(spinner.Style(style)))
}

// SpinnerFrame returns the spinner rune for the given animation frame.
func SpinnerFrame(frame int) rune {
	return activeSpinner.Load().Frame(frame)
}

// SpinnerFrameCount returns the number of frames in the active spinner.
func SpinnerFrameCount() int {
	return activeSpinner.Load().FrameCount()
}

// SpinnerTickInterval returns the tick interval of the active spinner.
func SpinnerTickInterval() time.Duration {
	return activeSpinner.Load().TickInterval
}
