package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func heraToolSchema(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	for _, tool := range heraToolDefs {
		if tool.Name == name {
			return tool.InputSchema.(map[string]interface{})
		}
	}
	t.Fatalf("tool %s not advertised", name)
	return nil
}

func schemaProps(m map[string]interface{}) map[string]interface{} {
	p, _ := m["properties"].(map[string]interface{})
	return p
}

// jsonTags lists the json field names of a decode struct.
func jsonTags(v interface{}) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		if tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
}

func TestHeraPlanSchemas_AdvertiseKindAndGoal(t *testing.T) {
	node := heraToolSchema(t, "hera_plan_node")
	planNodes := schemaProps(heraToolSchema(t, "hera_plan"))["nodes"].(map[string]interface{})["items"].(map[string]interface{})

	for name, schema := range map[string]map[string]interface{}{"hera_plan_node": node, "hera_plan nodes[]": planNodes} {
		t.Run(name, func(t *testing.T) {
			props := schemaProps(schema)
			kind, ok := props["kind"].(map[string]interface{})
			testutil.Equal(t, ok, true)
			testutil.DeepEqual(t, kind["enum"].([]string), []string{"worker", "subcoord"})
			testutil.Equal(t, kind["default"], "worker")
			_, ok = props["goal"].(map[string]interface{})
			testutil.Equal(t, ok, true)
			for _, r := range schema["required"].([]string) {
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
// every JSON param the handlers decode must be advertised.
func TestHeraPlanSchemas_MatchHandlerParams(t *testing.T) {
	props := schemaProps(heraToolSchema(t, "hera_plan_node"))
	for _, tag := range append(jsonTags(heraPlanNodeSpec{}), jsonTags(struct {
		Cwd          string `json:"cwd"`
		Orchestrator string `json:"orchestrator"`
	}{})...) {
		if _, ok := props[tag]; !ok {
			t.Errorf("hera_plan_node handler decodes %q but the schema does not advertise it", tag)
		}
	}
	planNode := schemaProps(schemaProps(heraToolSchema(t, "hera_plan"))["nodes"].(map[string]interface{})["items"].(map[string]interface{}))
	for _, tag := range jsonTags(heraPlanNodeSpec{}) {
		if _, ok := planNode[tag]; !ok {
			t.Errorf("hera_plan node handler decodes %q but the schema does not advertise it", tag)
		}
	}
}

func TestHera_NewOrchestrator_RejectsNonClaudeCaller(t *testing.T) {
	s, d := testHeraServer(t)
	task := &model.Task{Name: "codex-worker", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/cx", Backend: "codex"}
	testutil.NoError(t, d.Add(task))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"name":"sub","coordinator_role_name":"coord"}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, `hera_plan_node(kind="subcoord"`)
	testutil.Contains(t, cr.Content[0].Text, `backend="claude"`)
	if _, err := d.HeraOrchestratorByName("sub"); err == nil {
		t.Error("rejected call must not create an orchestrator")
	}
}

func TestHera_NewOrchestrator_AllowsClaudeAndUnknownBackend(t *testing.T) {
	for name, backend := range map[string]string{"claude": "claude", "empty": "", "unresolvable": "no-such-backend"} {
		t.Run(name, func(t *testing.T) {
			s, d := testHeraServer(t)
			task := &model.Task{Name: "w", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/" + name, Backend: backend}
			testutil.NoError(t, d.Add(task))
			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "hera_new_orchestrator",
				Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"name":"o","coordinator_role_name":"coord"}`, task.Worktree)),
			})
			testutil.NoError(t, respErr(resp))
			testutil.Equal(t, callResult(t, resp).IsError, false)
		})
	}
}
