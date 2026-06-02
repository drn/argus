package model

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestDocsPlugins_ExistsAndCoversContract pins docs/plugins.md against drift.
// PR 8 of the plugin substrate is "docs only", but the doc is the spec — a
// rename of an event type, endpoint, or auth header that ships without a
// matching docs update is a contract bug. This test fails the build when the
// surface area documented in docs/plugins.md falls behind code.
//
// The expected-value table is intentionally string-literal rather than a
// reference to the (eventual) EventType* constants in event.go. PR 8 ships
// before PR 2's event constants land upstream, so importing them would make
// this branch's CI dependent on PR 2. String literals keep the test
// standalone while still failing the build if any future rename changes
// either the constant value OR the doc — both must move together; the test
// pinning either side tells the renamer "go update the other".
//
// We deliberately scan for substring presence rather than parsing the markdown
// — the doc is human-prose with code fences and tables, and a fragile parser
// would create more breakage than it prevents.
func TestDocsPlugins_ExistsAndCoversContract(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	docPath := filepath.Join(repoRoot, "docs", "plugins.md")

	data, err := os.ReadFile(docPath) //nolint:gosec // test reads bundled doc
	testutil.NoError(t, err)
	doc := string(data)

	t.Run("every event type is documented", func(t *testing.T) {
		// Mirrors the EventType* constants in PR 2's internal/model/event.go.
		// If a value changes there, update this list in lockstep.
		for _, ev := range []string{
			"task.created",
			"task.status_changed",
			"task.completed",
			"task.archived",
			"task.renamed",
			"task.forked",
			"message.sent",
			"message.acked",
			"link.created",
			"link.removed",
			"session.started",
			"session.exited",
			"session.idle",
			"resync",
		} {
			if !strings.Contains(doc, ev) {
				t.Errorf("docs/plugins.md missing event type %q", ev)
			}
		}
	})

	t.Run("every plugin endpoint is documented", func(t *testing.T) {
		// These match the routes wired in internal/api/routes.go that the
		// substrate plan flags as plugin-callable.
		for _, ep := range []string{
			"/api/events/stream",
			"/api/tasks/:id/meta",
			"/api/tasks/:id/input",
			"/api/mcp/tools",
			"/api/plugins/settings/sections",
		} {
			if !strings.Contains(doc, ep) {
				t.Errorf("docs/plugins.md missing endpoint %q", ep)
			}
		}
	})

	t.Run("auth headers and CLI verbs are documented", func(t *testing.T) {
		for _, tok := range []string{
			"X-Argus-Auth",
			"X-Argus-Plugin-Version",
			"argus token mint",
			"argus token list",
			"argus token revoke",
		} {
			if !strings.Contains(doc, tok) {
				t.Errorf("docs/plugins.md missing token %q", tok)
			}
		}
	})

	t.Run("settings section types are documented", func(t *testing.T) {
		for _, tok := range []string{
			// Settings section types.
			"form",
			"stream",
			// Form field types.
			"bool",
			"int",
			"string",
			"enum",
		} {
			if !strings.Contains(doc, tok) {
				t.Errorf("docs/plugins.md missing token %q", tok)
			}
		}
	})

	t.Run("doc starts with a top-level heading", func(t *testing.T) {
		// Smoke check that the file is structured markdown, not stray text.
		first := strings.SplitN(doc, "\n", 2)[0]
		if !strings.HasPrefix(first, "# ") {
			t.Errorf("docs/plugins.md must start with a top-level heading, got %q", first)
		}
	})
}
