package ui

import "testing"

func TestParseRuntime_DefaultsToBubbleTea(t *testing.T) {
	got, err := ParseRuntime("")
	if err != nil {
		t.Fatalf("ParseRuntime returned error: %v", err)
	}
	if got != RuntimeBubbleTea {
		t.Fatalf("ParseRuntime(\"\") = %q, want %q", got, RuntimeBubbleTea)
	}
}

func TestParseRuntime_AcceptsKnownValues(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Runtime
	}{
		{name: "bubbletea", in: "bubbletea", want: RuntimeBubbleTea},
		{name: "tcell", in: "tcell", want: RuntimeTCell},
		{name: "mixed case with spaces", in: "  TCELL ", want: RuntimeTCell},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRuntime(tt.in)
			if err != nil {
				t.Fatalf("ParseRuntime(%q) returned error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseRuntime(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRuntime_RejectsUnknownValues(t *testing.T) {
	if _, err := ParseRuntime("warpdrive"); err == nil {
		t.Fatal("expected invalid runtime to return an error")
	}
}
