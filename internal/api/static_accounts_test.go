package api

import (
	"os"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestStaticIndex_AccountSelector guards the SPA's account semantics, which
// Playwright (not in CI) would otherwise be the only check for: options carry
// the account name so an explicit default pick sends "default", the project's
// default is preselected via /api/accounts?project=, the select re-filters on
// backend change, and skills are fetched per account / task.
func TestStaticIndex_AccountSelector(t *testing.T) {
	data, err := os.ReadFile("static/index.html")
	testutil.NoError(t, err)
	js := extractInlineScript(string(data))

	for _, want := range []string{
		"opt.value = a.name;",
		"'/api/accounts' + (project ? '?project=' + encodeURIComponent(project) : '')",
		"function pickCreateAccount(usable, picked)",
		"return d ? d.name : 'default';",
		"sel.onchange = () => { rebuildCreateModelOptions(); updateCreateAccountVisibility(); };",
		"grp.style.display = usable.length > 1 ? '' : 'none';",
		"if (ctx.account) q.set('account', ctx.account);",
		"if (ctx.task) q.set('task', ctx.task);",
		"getContext: composeSkillsCtx,",
		"getContext: createSkillsCtx,",
	} {
		t.Run(want, func(t *testing.T) { testutil.Contains(t, js, want) })
	}
	t.Run("default option is not blanked", func(t *testing.T) {
		testutil.False(t, strings.Contains(js, "a.is_default ? '' : a.name"))
	})
}
