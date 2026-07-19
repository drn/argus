package skills

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBuiltinItems_IncludesAllExpectedSkills(t *testing.T) {
	items := BuiltinItems()
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	testutil.DeepEqual(t, names, []string{
		"archive",
		"argus-complete",
		"argus-schedule",
		"hera",
		"hera-plan",
		"hera-review",
		"hera-review-test-adversary",
	})
}

func TestBuiltinItems_ReviewSkillsHaveDescriptions(t *testing.T) {
	items := BuiltinItems()
	byName := make(map[string]string, len(items))
	for _, it := range items {
		byName[it.Name] = it.Description
	}

	reviewDesc, ok := byName["hera-review"]
	if !ok || reviewDesc == "" {
		t.Fatalf("expected non-empty description for hera-review, got %q (present: %v)", reviewDesc, ok)
	}
	adversaryDesc, ok := byName["hera-review-test-adversary"]
	if !ok || adversaryDesc == "" {
		t.Fatalf("expected non-empty description for hera-review-test-adversary, got %q (present: %v)", adversaryDesc, ok)
	}
}
