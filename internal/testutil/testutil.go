// Package testutil provides thin assertion helpers for TDD workflows.
// All assertions use t.Errorf (not t.Fatalf) so multiple failures surface in one run.
package testutil

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Equal asserts got == want for comparable types.
func Equal[T comparable](t testing.TB, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// DeepEqual asserts structural equality using go-cmp.
func DeepEqual[T any](t testing.TB, got, want T, opts ...cmp.Option) {
	t.Helper()
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

// NotEqual asserts got != unwanted for comparable types.
func NotEqual[T comparable](t testing.TB, got, unwanted T) {
	t.Helper()
	if got == unwanted {
		t.Errorf("got %v, want anything else", got)
	}
}

// Nil asserts that got is nil, handling the nil-interface trap via reflection.
func Nil(t testing.TB, got any) {
	t.Helper()
	if got != nil && !reflect.ValueOf(got).IsNil() {
		t.Errorf("got %v, want nil", got)
	}
}

// NotNil asserts that got is not nil, handling the nil-interface trap via reflection.
func NotNil(t testing.TB, got any) {
	t.Helper()
	if got == nil || reflect.ValueOf(got).IsNil() {
		t.Errorf("got nil, want non-nil")
	}
}

// NoError asserts err is nil.
func NoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Error asserts err is non-nil.
func Error(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

// ErrorIs asserts errors.Is(err, target).
func ErrorIs(t testing.TB, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("got error %v, want errors.Is(%v)", err, target)
	}
}

// True asserts got is true.
func True(t testing.TB, got bool) {
	t.Helper()
	if !got {
		t.Errorf("got false, want true")
	}
}

// False asserts got is false.
func False(t testing.TB, got bool) {
	t.Helper()
	if got {
		t.Errorf("got true, want false")
	}
}

// Contains asserts that s contains substr.
func Contains(t testing.TB, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("string %q does not contain %q", s, substr)
	}
}
