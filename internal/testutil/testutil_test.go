package testutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// mockTB captures t.Errorf calls without failing the real test.
type mockTB struct {
	testing.TB
	failed  bool
	message string
}

func (m *mockTB) Helper() {}

func (m *mockTB) Errorf(format string, args ...any) {
	m.failed = true
	m.message = fmt.Sprintf(format, args...)
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name    string
		got     int
		want    int
		wantErr bool
	}{
		{"match", 42, 42, false},
		{"mismatch", 1, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &mockTB{}
			Equal(m, tt.got, tt.want)
			if m.failed != tt.wantErr {
				t.Errorf("Equal(%v, %v): failed=%v, want %v", tt.got, tt.want, m.failed, tt.wantErr)
			}
		})
	}
}

func TestEqual_Strings(t *testing.T) {
	m := &mockTB{}
	Equal(m, "hello", "hello")
	if m.failed {
		t.Error("Equal should pass for matching strings")
	}

	m = &mockTB{}
	Equal(m, "hello", "world")
	if !m.failed {
		t.Error("Equal should fail for mismatching strings")
	}
}

func TestDeepEqual(t *testing.T) {
	type S struct {
		A int
		B string
	}

	t.Run("match", func(t *testing.T) {
		m := &mockTB{}
		DeepEqual(m, S{1, "x"}, S{1, "x"})
		if m.failed {
			t.Error("DeepEqual should pass for equal structs")
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		m := &mockTB{}
		DeepEqual(m, S{1, "x"}, S{2, "y"})
		if !m.failed {
			t.Error("DeepEqual should fail for different structs")
		}
	})

	t.Run("with_options", func(t *testing.T) {
		type F struct {
			X float64
		}
		m := &mockTB{}
		approx := cmp.Comparer(func(a, b float64) bool {
			return (a-b) < 0.01 && (b-a) < 0.01
		})
		DeepEqual(m, F{1.001}, F{1.002}, approx)
		if m.failed {
			t.Error("DeepEqual should pass with custom comparer")
		}
	})
}

func TestNotEqual(t *testing.T) {
	m := &mockTB{}
	NotEqual(m, 1, 2)
	if m.failed {
		t.Error("NotEqual should pass for different values")
	}

	m = &mockTB{}
	NotEqual(m, 1, 1)
	if !m.failed {
		t.Error("NotEqual should fail for same values")
	}
}

func TestNil(t *testing.T) {
	t.Run("untyped_nil", func(t *testing.T) {
		m := &mockTB{}
		Nil(m, nil)
		if m.failed {
			t.Error("Nil should pass for untyped nil")
		}
	})

	t.Run("nil_pointer", func(t *testing.T) {
		m := &mockTB{}
		var p *int
		Nil(m, p)
		if m.failed {
			t.Error("Nil should pass for nil pointer")
		}
	})

	t.Run("nil_interface_trap", func(t *testing.T) {
		// A nil *error assigned to an interface{} is non-nil at the interface level
		// but our Nil helper should detect it as nil via reflection.
		m := &mockTB{}
		var err *testError
		Nil(m, err)
		if m.failed {
			t.Error("Nil should pass for nil concrete type in interface (nil-interface trap)")
		}
	})

	t.Run("non_nil", func(t *testing.T) {
		m := &mockTB{}
		x := 42
		Nil(m, &x)
		if !m.failed {
			t.Error("Nil should fail for non-nil pointer")
		}
	})
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestNotNil(t *testing.T) {
	t.Run("non_nil", func(t *testing.T) {
		m := &mockTB{}
		x := 42
		NotNil(m, &x)
		if m.failed {
			t.Error("NotNil should pass for non-nil pointer")
		}
	})

	t.Run("untyped_nil", func(t *testing.T) {
		m := &mockTB{}
		NotNil(m, nil)
		if !m.failed {
			t.Error("NotNil should fail for untyped nil")
		}
	})

	t.Run("nil_interface_trap", func(t *testing.T) {
		m := &mockTB{}
		var err *testError
		NotNil(m, err)
		if !m.failed {
			t.Error("NotNil should fail for nil concrete type in interface")
		}
	})
}

func TestNoError(t *testing.T) {
	m := &mockTB{}
	NoError(m, nil)
	if m.failed {
		t.Error("NoError should pass for nil error")
	}

	m = &mockTB{}
	NoError(m, errors.New("boom"))
	if !m.failed {
		t.Error("NoError should fail for non-nil error")
	}
}

func TestError(t *testing.T) {
	m := &mockTB{}
	Error(m, errors.New("boom"))
	if m.failed {
		t.Error("Error should pass for non-nil error")
	}

	m = &mockTB{}
	Error(m, nil)
	if !m.failed {
		t.Error("Error should fail for nil error")
	}
}

func TestErrorIs(t *testing.T) {
	sentinel := errors.New("sentinel")

	m := &mockTB{}
	ErrorIs(m, fmt.Errorf("wrapped: %w", sentinel), sentinel)
	if m.failed {
		t.Error("ErrorIs should pass for wrapped sentinel")
	}

	m = &mockTB{}
	ErrorIs(m, errors.New("other"), sentinel)
	if !m.failed {
		t.Error("ErrorIs should fail for unrelated error")
	}
}

func TestTrue(t *testing.T) {
	m := &mockTB{}
	True(m, true)
	if m.failed {
		t.Error("True should pass for true")
	}

	m = &mockTB{}
	True(m, false)
	if !m.failed {
		t.Error("True should fail for false")
	}
}

func TestFalse(t *testing.T) {
	m := &mockTB{}
	False(m, false)
	if m.failed {
		t.Error("False should pass for false")
	}

	m = &mockTB{}
	False(m, true)
	if !m.failed {
		t.Error("False should fail for true")
	}
}

func TestContains(t *testing.T) {
	m := &mockTB{}
	Contains(m, "hello world", "world")
	if m.failed {
		t.Error("Contains should pass when substring present")
	}

	m = &mockTB{}
	Contains(m, "hello", "world")
	if !m.failed {
		t.Error("Contains should fail when substring absent")
	}
}
