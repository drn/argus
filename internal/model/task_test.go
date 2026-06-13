package model

import (
	"regexp"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// uuidV4Re matches the canonical 8-4-4-4-12 hex form with the version nibble
// pinned to 4 and the variant nibble pinned to RFC 4122 (8/9/a/b).
var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestGenerateSessionID_WellFormedUUIDv4(t *testing.T) {
	id := GenerateSessionID()

	t.Run("length", func(t *testing.T) {
		testutil.Equal(t, len(id), 36)
	})
	t.Run("rfc4122 version and variant", func(t *testing.T) {
		if !uuidV4Re.MatchString(id) {
			t.Errorf("GenerateSessionID() = %q, not a well-formed UUID v4", id)
		}
	})
	t.Run("version nibble is 4", func(t *testing.T) {
		// Group 3 starts at index 14 (8 hex + hyphen + 4 hex + hyphen).
		testutil.Equal(t, string(id[14]), "4")
	})
	t.Run("variant nibble is 8/9/a/b", func(t *testing.T) {
		// Group 4 starts at index 19.
		testutil.Contains(t, "89ab", string(id[19]))
	})
}

func TestGenerateSessionID_Unique(t *testing.T) {
	// Two consecutive calls must differ — IDs pin a Claude conversation, so a
	// collision would cross-wire sessions.
	a := GenerateSessionID()
	b := GenerateSessionID()
	if a == b {
		t.Errorf("GenerateSessionID() returned identical IDs on consecutive calls: %q", a)
	}
}

func TestTask_SetPinned(t *testing.T) {
	tests := []struct {
		name         string
		initial      Task
		set          bool
		wantPinned   bool
		wantArchived bool
	}{
		{"pin-on clears archived", Task{Archived: true}, true, true, false},
		{"pin-on from clean", Task{}, true, true, false},
		{"pin-off leaves archived untouched", Task{Archived: true, Pinned: false}, false, false, true},
		{"pin-off from pinned, archived stays false", Task{Pinned: true}, false, false, false},
		{"idempotent re-pin keeps archived clear", Task{Pinned: true}, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := tt.initial
			task.SetPinned(tt.set)
			testutil.Equal(t, task.Pinned, tt.wantPinned)
			testutil.Equal(t, task.Archived, tt.wantArchived)
		})
	}
}

func TestTask_SetArchived(t *testing.T) {
	tests := []struct {
		name         string
		initial      Task
		set          bool
		wantPinned   bool
		wantArchived bool
	}{
		{"archive-on clears pinned", Task{Pinned: true}, true, false, true},
		{"archive-on from clean", Task{}, true, false, true},
		{"archive-off leaves pinned untouched", Task{Pinned: true, Archived: false}, false, true, false},
		{"archive-off from archived, pinned stays false", Task{Archived: true}, false, false, false},
		{"idempotent re-archive keeps pinned clear", Task{Archived: true}, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := tt.initial
			task.SetArchived(tt.set)
			testutil.Equal(t, task.Pinned, tt.wantPinned)
			testutil.Equal(t, task.Archived, tt.wantArchived)
		})
	}
}

func TestTask_PinArchive_MutualExclusion(t *testing.T) {
	// The two setters must never leave both flags true simultaneously, no
	// matter the order of operations.
	t.Run("pin then archive", func(t *testing.T) {
		task := &Task{}
		task.SetPinned(true)
		task.SetArchived(true)
		testutil.Equal(t, task.Pinned, false)
		testutil.Equal(t, task.Archived, true)
	})
	t.Run("archive then pin", func(t *testing.T) {
		task := &Task{}
		task.SetArchived(true)
		task.SetPinned(true)
		testutil.Equal(t, task.Pinned, true)
		testutil.Equal(t, task.Archived, false)
	})
}

func TestTask_Elapsed_NotStarted(t *testing.T) {
	task := &Task{}
	if d := task.Elapsed(); d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestTask_Elapsed_Running(t *testing.T) {
	task := &Task{StartedAt: time.Now().Add(-5 * time.Second)}
	d := task.Elapsed()
	if d < 4*time.Second || d > 6*time.Second {
		t.Errorf("expected ~5s, got %v", d)
	}
}

func TestTask_Elapsed_Completed(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	task := &Task{StartedAt: start, EndedAt: end}
	if d := task.Elapsed(); d != 10*time.Minute {
		t.Errorf("expected 10m, got %v", d)
	}
}

func TestTask_Elapsed_InProgress_ReturnsPositive(t *testing.T) {
	// Running task (StartedAt set, EndedAt zero) must report live positive
	// elapsed via time.Since — the clamp must NOT swallow the normal path.
	task := &Task{StartedAt: time.Now().Add(-10 * time.Minute)}
	d := task.Elapsed()
	if d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("expected ~10m for in-progress task, got %v", d)
	}
}

func TestTask_Elapsed_ZeroDuration_StartEqualsEnd(t *testing.T) {
	// A task that started and ended at the same instant is a legitimate zero,
	// not a skew artifact; Elapsed returns 0 (boundary: not negative).
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{StartedAt: start, EndedAt: start}
	if d := task.Elapsed(); d != 0 {
		t.Errorf("expected 0 for equal start/end, got %v", d)
	}
}

func TestTask_Elapsed_FutureStartedAt_ClampsToZero(t *testing.T) {
	// A backward wall-clock correction can leave StartedAt in the future on a
	// running task; Elapsed must floor at 0 rather than report negative time.
	task := &Task{StartedAt: time.Now().Add(42 * time.Minute)}
	if d := task.Elapsed(); d != 0 {
		t.Errorf("expected 0 for future StartedAt, got %v", d)
	}
}

func TestTask_Elapsed_EndedBeforeStarted_ClampsToZero(t *testing.T) {
	// Both timestamps stamped under clock skew can leave EndedAt < StartedAt.
	start := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	end := start.Add(-10 * time.Minute)
	task := &Task{StartedAt: start, EndedAt: end}
	if d := task.Elapsed(); d != 0 {
		t.Errorf("expected 0 when EndedAt precedes StartedAt, got %v", d)
	}
}

func TestTask_ElapsedString_NegativeDuration_RendersEmpty(t *testing.T) {
	// The "-2503s" / "-41h" display regression: a future StartedAt must not
	// render as a negative string. Elapsed clamps to 0, so the string is empty.
	task := &Task{StartedAt: time.Now().Add(2 * time.Hour)}
	if got := task.ElapsedString(); got != "" {
		t.Errorf("ElapsedString() = %q, want \"\"", got)
	}
}

func TestTask_ElapsedString(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want string
	}{
		{"not started", Task{}, ""},
		{"seconds", Task{StartedAt: time.Now().Add(-30 * time.Second)}, "30s"},
		{"minutes", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 1, 0, 5, 0, 0, time.UTC),
		}, "5m"},
		{"hours", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC),
		}, "2h"},
		{"days", Task{
			StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
		}, "2d"},
		{"ended before started", Task{
			StartedAt: time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.ElapsedString(); got != tt.want {
				t.Errorf("ElapsedString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTask_SetStatus_InProgress(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusInProgress)

	if task.Status != StatusInProgress {
		t.Error("status not set")
	}
	if task.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if !task.EndedAt.IsZero() {
		t.Error("EndedAt should be zero")
	}
}

func TestTask_SetStatus_InProgress_PreservesStartedAt(t *testing.T) {
	original := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{StartedAt: original}
	task.SetStatus(StatusInProgress)

	if !task.StartedAt.Equal(original) {
		t.Error("StartedAt should not be overwritten")
	}
}

func TestTask_SetStatus_Complete(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusComplete)

	if task.Status != StatusComplete {
		t.Error("status not set")
	}
	if task.EndedAt.IsZero() {
		t.Error("EndedAt should be set")
	}
}

func TestTask_SetStatus_Pending_NoTimestamps(t *testing.T) {
	task := &Task{}
	task.SetStatus(StatusPending)

	if !task.StartedAt.IsZero() {
		t.Error("StartedAt should remain zero for pending")
	}
	if !task.EndedAt.IsZero() {
		t.Error("EndedAt should remain zero for pending")
	}
}
