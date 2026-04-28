package model

import (
	"strings"
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	cases := []struct {
		name string
		expr string
		ok   bool
	}{
		{"daily", "@daily", true},
		{"every-30m", "@every 30m", true},
		{"five-fields", "0 9 * * 1-5", true},
		{"empty", "", false},
		{"garbage", "not a cron", false},
		{"six-fields-rejected", "0 0 9 * * 1", false}, // 6-field with seconds is not allowed
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSchedule(tc.expr)
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestScheduledTaskValidate(t *testing.T) {
	good := &ScheduledTask{Name: "x", Project: "p", Prompt: "go", Schedule: "@daily"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good schedule rejected: %v", err)
	}

	cases := []struct {
		name string
		s    *ScheduledTask
		want string
	}{
		{"missing-name", &ScheduledTask{Project: "p", Prompt: "go", Schedule: "@daily"}, "name"},
		{"missing-project", &ScheduledTask{Name: "x", Prompt: "go", Schedule: "@daily"}, "project"},
		{"missing-prompt", &ScheduledTask{Name: "x", Project: "p", Schedule: "@daily"}, "prompt"},
		{"bad-schedule", &ScheduledTask{Name: "x", Project: "p", Prompt: "go", Schedule: "bogus"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error to mention %q, got %v", tc.want, err)
			}
		})
	}
}

func TestNextFire(t *testing.T) {
	s := &ScheduledTask{Schedule: "@every 1h"}
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	next := s.NextFire(now)
	if !next.Equal(now.Add(time.Hour)) {
		t.Fatalf("expected %v, got %v", now.Add(time.Hour), next)
	}

	bad := &ScheduledTask{Schedule: "garbage"}
	if !bad.NextFire(now).IsZero() {
		t.Fatal("expected zero time for bad schedule")
	}
}
