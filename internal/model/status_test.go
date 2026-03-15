package model

import (
	"encoding/json"
	"testing"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{StatusPending, "pending"},
		{StatusInProgress, "in_progress"},
		{StatusInReview, "in_review"},
		{StatusComplete, "complete"},
		{Status(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestStatus_DisplayName(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{StatusPending, "Pending"},
		{StatusInProgress, "In Progress"},
		{StatusInReview, "In Review"},
		{StatusComplete, "Complete"},
		{Status(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.s.DisplayName(); got != tt.want {
			t.Errorf("Status(%d).DisplayName() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestStatus_Display(t *testing.T) {
	if got := StatusPending.Display(); got != "\uF10C" {
		t.Errorf("Pending.Display() = %q", got)
	}
	if got := StatusComplete.Display(); got != "\uF00C" {
		t.Errorf("Complete.Display() = %q", got)
	}
	if got := Status(99).Display(); got != "unknown(99)" {
		t.Errorf("out-of-range Display() = %q", got)
	}
}

func TestStatus_DisplayAlt(t *testing.T) {
	if got := StatusInProgress.DisplayAlt(); got != "\uF192" {
		t.Errorf("InProgress.DisplayAlt() = %q, want dot-circle-o", got)
	}
	// Non-animated statuses return same as Display
	if got := StatusPending.DisplayAlt(); got != "\uF10C" {
		t.Errorf("Pending.DisplayAlt() = %q", got)
	}
	if got := Status(99).DisplayAlt(); got != "unknown(99)" {
		t.Errorf("out-of-range DisplayAlt() = %q", got)
	}
}

func TestStatus_Badge(t *testing.T) {
	if got := StatusPending.Badge(); got != "○" {
		t.Errorf("Pending.Badge() = %q", got)
	}
	if got := StatusComplete.Badge(); got != "✓" {
		t.Errorf("Complete.Badge() = %q", got)
	}
	if got := Status(99).Badge(); got != "?" {
		t.Errorf("out-of-range Badge() = %q", got)
	}
}

func TestStatus_Next(t *testing.T) {
	if StatusPending.Next() != StatusInProgress {
		t.Error("Pending.Next() should be InProgress")
	}
	if StatusInProgress.Next() != StatusInReview {
		t.Error("InProgress.Next() should be InReview")
	}
	if StatusComplete.Next() != StatusComplete {
		t.Error("Complete.Next() should stay Complete")
	}
}

func TestStatus_Prev(t *testing.T) {
	if StatusComplete.Prev() != StatusInReview {
		t.Error("Complete.Prev() should be InReview")
	}
	if StatusPending.Prev() != StatusPending {
		t.Error("Pending.Prev() should stay Pending")
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		input string
		want  Status
		err   bool
	}{
		{"pending", StatusPending, false},
		{"in_progress", StatusInProgress, false},
		{"in_review", StatusInReview, false},
		{"complete", StatusComplete, false},
		{"bogus", StatusPending, true},
		{"", StatusPending, true},
	}
	for _, tt := range tests {
		got, err := ParseStatus(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("ParseStatus(%q) error = %v, wantErr %v", tt.input, err, tt.err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseStatus(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestStatus_MarshalUnmarshal(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusInProgress, StatusInReview, StatusComplete} {
		data, err := s.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%v): %v", s, err)
		}
		var got Status
		if err := got.UnmarshalText(data); err != nil {
			t.Fatalf("UnmarshalText(%q): %v", data, err)
		}
		if got != s {
			t.Errorf("roundtrip: got %v, want %v", got, s)
		}
	}
}

func TestStatus_UnmarshalText_Invalid(t *testing.T) {
	var s Status
	if err := s.UnmarshalText([]byte("nope")); err == nil {
		t.Error("expected error for invalid status text")
	}
}

func TestStatus_JSONRoundtrip(t *testing.T) {
	type wrapper struct {
		S Status `json:"status"`
	}
	w := wrapper{S: StatusInReview}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	// Should serialize as string, not integer
	if !contains(string(data), `"in_review"`) {
		t.Errorf("expected string serialization, got %s", data)
	}
	var got wrapper
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.S != StatusInReview {
		t.Errorf("got %v, want InReview", got.S)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
