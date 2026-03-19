package skills

import "testing"

func TestFilterSkills(t *testing.T) {
	items := []SkillItem{
		{Name: "commit", Description: "Create a commit"},
		{Name: "review", Description: "Review PR"},
		{Name: "test", Description: "Run tests"},
	}

	// Empty prefix returns all
	got := FilterSkills(items, "")
	if len(got) != 3 {
		t.Errorf("empty prefix: got %d, want 3", len(got))
	}

	// Prefix "co" matches commit only
	got = FilterSkills(items, "co")
	if len(got) != 1 || got[0].Name != "commit" {
		t.Errorf("prefix 'co': got %v, want [commit]", got)
	}

	// Prefix "re" matches review only
	got = FilterSkills(items, "re")
	if len(got) != 1 || got[0].Name != "review" {
		t.Errorf("prefix 're': got %v, want [review]", got)
	}

	// No match
	got = FilterSkills(items, "xyz")
	if len(got) != 0 {
		t.Errorf("prefix 'xyz': got %d, want 0", len(got))
	}

	// Case sensitive
	got = FilterSkills(items, "CO")
	if len(got) != 0 {
		t.Errorf("prefix 'CO': got %d, want 0 (case sensitive)", len(got))
	}
}
