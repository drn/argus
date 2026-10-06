package model

import "testing"

func TestValidateArtifactRelPath(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"a.png", true},
		{"shots/01.png", true},
		{"a/b/c.txt", true},
		{".hidden.txt", true},
		{"", false},
		{"/abs/x", false},
		{"a//b", false},
		{"a/../b", false},
		{"..", false},
		{"./a", false},
		{"a/", false},
		{"a\\b", false},
		{"a\x00b", false},
		{".thumbs/x.jpg", false},
		{"folder/.thumbs/x.jpg", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			_, err := ValidateArtifactRelPath(tc.in)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateArtifactRelPath(%q) err=%v, want ok=%v", tc.in, err, tc.ok)
			}
		})
	}
}
