package daemon

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

func TestRunGlobalInjectors_CallsEachAndSurvivesFailures(t *testing.T) {
	var calls []int
	fams := []*agent.Family{
		{InjectGlobal: func(port int) error { calls = append(calls, port); return errors.New("boom") }},
		{},
		{InjectGlobal: func(port int) error { calls = append(calls, port*10); return nil }},
	}
	runGlobalInjectors(fams, 7)
	testutil.DeepEqual(t, calls, []int{7, 70})
}

func TestInjectGlobalBackendConfig_SkippedUnderGoTest(t *testing.T) {
	// The real families write to the developer's HOME; the .test guard must
	// stop that. HOME is pointed at a temp dir as a second safety net.
	t.Setenv("HOME", t.TempDir())
	injectGlobalBackendConfig(1)
}
