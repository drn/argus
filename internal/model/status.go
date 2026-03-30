package model

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drn/argus/internal/spinner"
)

// Status represents the workflow state of a task.
type Status int

const (
	StatusPending    Status = iota
	StatusInProgress
	StatusInReview
	StatusComplete
)

var statusNames = [...]string{
	"pending",
	"in_progress",
	"in_review",
	"complete",
}

var statusDisplayNames = [...]string{
	"Pending",
	"In Progress",
	"In Review",
	"Complete",
}

var statusDisplay = [...]string{
	"\uF10C",
	"\uEE06",
	"\uF06E",
	"\uF00C",
}

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

var statusBadges = [...]string{
	"○",
	"●",
	"●",
	"✓",
}

func (s Status) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return fmt.Sprintf("unknown(%d)", int(s))
}

// DisplayName returns a human-readable name like "In Progress".
func (s Status) DisplayName() string {
	if int(s) < len(statusDisplayNames) {
		return statusDisplayNames[s]
	}
	return s.String()
}

func (s Status) Display() string {
	if int(s) < len(statusDisplay) {
		return statusDisplay[s]
	}
	return s.String()
}

// DisplayForFrame returns the status icon for the given animation frame.
// Non-animated statuses ignore the frame parameter.
func (s Status) DisplayForFrame(frame int) string {
	if s == StatusInProgress {
		return string(SpinnerFrame(frame))
	}
	return s.Display()
}

func (s Status) Badge() string {
	if int(s) < len(statusBadges) {
		return statusBadges[s]
	}
	return "?"
}

// Next advances the status to the next state. Returns the same status if already complete.
func (s Status) Next() Status {
	if s < StatusComplete {
		return s + 1
	}
	return s
}

// Prev moves the status to the previous state. Returns the same status if already pending.
func (s Status) Prev() Status {
	if s > StatusPending {
		return s - 1
	}
	return s
}

// ParseStatus converts a string to a Status.
func ParseStatus(s string) (Status, error) {
	for i, name := range statusNames {
		if name == s {
			return Status(i), nil
		}
	}
	return StatusPending, fmt.Errorf("unknown status: %q", s)
}

func (s Status) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

func (s *Status) UnmarshalText(data []byte) error {
	parsed, err := ParseStatus(string(data))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
