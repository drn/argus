package kb

import "testing"

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"query:field", "query field"},
		{"(hello)", "hello"},
		{`"quoted"`, "quoted"},
		{"star*match", "star match"},
		{"hello^world", "hello world"},
		{"  whitespace  ", "whitespace"},
		{"", ""},
	}
	for _, tc := range tests {
		got := SanitizeQuery(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeQuery(%q): got %q, want %q", tc.input, got, tc.want)
		}
	}
}
