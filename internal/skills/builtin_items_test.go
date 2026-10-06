package skills

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestWithBuiltins(t *testing.T) {
	got := WithBuiltins([]SkillItem{{Name: "zeta"}, {Name: "argus-archive", Description: "user override"}})
	byName := map[string]string{}
	for _, it := range got {
		byName[it.Name] = it.Description
	}
	_, ok := byName["hera"]
	testutil.True(t, ok)
	_, ok = byName["zeta"]
	testutil.True(t, ok)
	testutil.Equal(t, byName["argus-archive"], "user override")
	for i := 1; i < len(got); i++ {
		testutil.True(t, got[i-1].Name <= got[i].Name)
	}
}
