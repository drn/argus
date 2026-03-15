package model

import "fmt"

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
	"\uF10C",
	"\uF06E",
	"\uF00C",
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

// statusDisplayAlt holds the alternate animation frame for statuses that animate.
var statusDisplayAlt = [...]string{
	"\uF10C",
	"\uF192", // dot-circle-o: alternate frame for in_progress
	"\uF06E",
	"\uF00C",
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

// DisplayAlt returns the alternate animation frame for the status icon.
func (s Status) DisplayAlt() string {
	if int(s) < len(statusDisplayAlt) {
		return statusDisplayAlt[s]
	}
	return s.String()
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
