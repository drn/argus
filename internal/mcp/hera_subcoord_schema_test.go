package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func heraToolSchema(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	for _, tool := range heraToolDefs {
		if tool.Name == name {
			m, ok := tool.InputSchema.(map[string]interface{})
			if !ok {
				t.Fatalf("tool %s InputSchema is not an object", name)
			}
			return m
		}
	}
	t.Fatalf("tool %s not advertised", name)
	return nil
}

// obj asserts v is a JSON-schema object (map) and returns it.
func obj(t *testing.T, v interface{}, what string) map[string]interface{} {
	t.Helper()
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("%s: expected an object in the schema, got %T", what, v)
	}
	return m
}

func schemaProps(t *testing.T, m map[string]interface{}) map[string]interface{} {
	t.Helper()
	return obj(t, m["properties"], "properties")
}

// nodesItems returns the per-node item schema of hera_plan.
func nodesItems(t *testing.T) map[string]interface{} {
	t.Helper()
	nodes := obj(t, schemaProps(t, heraToolSchema(t, "hera_plan"))["nodes"], "hera_plan.nodes")
	return obj(t, nodes["items"], "hera_plan.nodes.items")
}

// jsonTags lists the json field names of a decode struct, recursing into
// embedded (anonymous) fields so a param added to an embedded type is seen.
func jsonTags(v interface{}) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous {
			out = append(out, jsonTags(reflect.New(f.Type).Elem().Interface())...)
			continue
		}
		if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
}

func requireAdvertised(t *testing.T, what string, props map[string]interface{}, decode interface{}) {
	t.Helper()
	tags := jsonTags(decode)
	if len(tags) == 0 {
		t.Fatalf("%s: decode struct has no json tags", what)
	}
	for _, tag := range tags {
		if _, ok := props[tag]; !ok {
			t.Errorf("%s handler decodes %q but the schema does not advertise it", what, tag)
		}
	}
}

func TestHeraPlanSchemas_AdvertiseKindAndGoal(t *testing.T) {
	schemas := map[string]map[string]interface{}{
		"hera_plan_node":    heraToolSchema(t, "hera_plan_node"),
		"hera_plan nodes[]": nodesItems(t),
	}
	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			props := schemaProps(t, schema)
			kind := obj(t, props["kind"], "kind")
			enum, ok := kind["enum"].([]string)
			if !ok {
				t.Fatalf("kind.enum is %T, want []string", kind["enum"])
			}
			testutil.DeepEqual(t, enum, []string{"worker", "subcoord"})
			testutil.Equal(t, kind["default"], "worker")
			obj(t, props["goal"], "goal")
			required, _ := schema["required"].([]string)
			for _, r := range required {
				if r == "prompt" {
					t.Errorf("prompt must not be schema-required (subcoord nodes use goal)")
				}
			}
		})
	}
}

func TestHeraPlanSchemas_DescribeSubcoordRoute(t *testing.T) {
	for _, tool := range heraToolDefs {
		if tool.Name == "hera_plan_node" || tool.Name == "hera_plan" {
			testutil.Contains(t, tool.Description, "kind=subcoord")
			testutil.Contains(t, tool.Description, "Claude")
		}
	}
}

// TestHeraPlanSchemas_MatchHandlerParams guards the drift that hid kind/goal:
// every JSON param the handlers decode must be advertised, at every level.
func TestHeraPlanSchemas_MatchHandlerParams(t *testing.T) {
	requireAdvertised(t, "hera_plan_node", schemaProps(t, heraToolSchema(t, "hera_plan_node")), heraPlanNodeParams{})
	requireAdvertised(t, "hera_plan", schemaProps(t, heraToolSchema(t, "hera_plan")), heraPlanParams{})
	requireAdvertised(t, "hera_plan nodes[]", schemaProps(t, nodesItems(t)), heraPlanNodeSpec{})
	edges := obj(t, schemaProps(t, heraToolSchema(t, "hera_plan"))["edges"], "hera_plan.edges")
	requireAdvertised(t, "hera_plan edges[]", schemaProps(t, obj(t, edges["items"], "edges.items")), heraPlanEdgeSpec{})
}

func TestJSONTags_RecursesIntoEmbedded(t *testing.T) {
	testutil.Contains(t, strings.Join(jsonTags(heraPlanNodeParams{}), ","), "goal")
}

// hideConfig wraps a store so it does NOT satisfy accountConfigSource.
type hideConfigTaskStore struct{ TaskStore }
type hideConfigKB struct{ KBQuerier }

func TestHera_NewOrchestrator_BackendGuard(t *testing.T) {
	cases := []struct {
		name    string
		backend config.Backend // registered under task backend name when Command != ""
		taskBe  string
		project string // project whose Backend is "codex" (for unstamped-task case)
		noCfg   bool
		reject  bool
	}{
		{name: "codex-reject", taskBe: "codex", reject: true},
		{name: "name-mismatched-kind", taskBe: "fast", backend: config.Backend{Command: "codex --yolo"}, reject: true},
		{name: "claude-accept", taskBe: "claude"},
		{name: "full-path-claude-with-flags", taskBe: "fancy", backend: config.Backend{Command: "/opt/bin/claude --verbose"}},
		{name: "custom-wrapper-rejected", taskBe: "wrap", backend: config.Backend{Command: "claude-wrapper --x"}, reject: true},
		{name: "unresolvable-backend-fail-open", taskBe: "no-such-backend"},
		{name: "unstamped-fail-open-despite-codex-project", taskBe: "", project: "cxproj"},
		{name: "no-config-source-fail-open", taskBe: "codex", noCfg: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, d := testHeraServer(t)
			if c.backend.Command != "" {
				testutil.NoError(t, d.SetBackend(c.taskBe, c.backend))
			}
			if c.project != "" {
				testutil.NoError(t, d.SetProject(c.project, config.Project{Path: "/tmp/x", Backend: "codex"}))
			}
			if c.noCfg {
				s.taskDB = hideConfigTaskStore{s.taskDB}
				s.db = hideConfigKB{s.db}
			}
			project := "p"
			if c.project != "" {
				project = c.project
			}
			task := &model.Task{Name: "w", Status: model.StatusInProgress, Project: project, Worktree: "/wt/" + c.name, Backend: c.taskBe}
			testutil.NoError(t, d.Add(task))

			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "hera_new_orchestrator",
				Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"name":"sub","coordinator_role_name":"coord"}`, task.Worktree)),
			})
			testutil.NoError(t, respErr(resp))
			cr := callResult(t, resp)
			testutil.Equal(t, cr.IsError, c.reject)
			_, err := d.HeraOrchestratorByName("sub")
			if c.reject {
				testutil.Contains(t, cr.Content[0].Text, "hera_send")
				testutil.Contains(t, cr.Content[0].Text, `kind="subcoord"`)
				testutil.Contains(t, cr.Content[0].Text, `backend="claude"`)
				testutil.Contains(t, cr.Content[0].Text, "TUI New")
				if err == nil {
					t.Error("rejected call must not create an orchestrator")
				}
			} else if err != nil {
				t.Errorf("accepted call should create the orchestrator: %v", err)
			}
		})
	}
}

// A Codex-backed task that already coordinates (operator-created via the TUI
// form) must keep the idempotent re-call guidance, not the Codex redirect.
func TestHera_NewOrchestrator_ExistingCoordinatorSkipsBackendGuard(t *testing.T) {
	s, d := testHeraServer(t)
	task := &model.Task{Name: "cx-coord", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/cxc", Backend: "claude"}
	testutil.NoError(t, d.Add(task))
	call := func(name string) ToolCallResult {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "hera_new_orchestrator",
			Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"name":%q,"coordinator_role_name":"coord"}`, task.Worktree, name)),
		})
		testutil.NoError(t, respErr(resp))
		return callResult(t, resp)
	}
	testutil.Equal(t, call("o1").IsError, false)

	// The task is now switched to a non-Claude backend (as a TUI-created Codex coordinator would be).
	task.Backend = "codex"
	testutil.NoError(t, d.Update(task))

	same := call("o1")
	testutil.Equal(t, same.IsError, true)
	testutil.Contains(t, same.Content[0].Text, "hera_join")
	other := call("o2")
	testutil.Equal(t, other.IsError, true)
	testutil.Contains(t, other.Content[0].Text, "already the coordinator")
}
