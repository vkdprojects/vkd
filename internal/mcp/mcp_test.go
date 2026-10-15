package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vkd "github.com/vkdprojects/vkd"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/engine"
)

func testEngine(t *testing.T) *engine.Engine {
	t.Helper()
	reg := blueprint.NewRegistry()
	if err := reg.LoadFS(vkd.Blueprints, "blueprints", "builtin"); err != nil {
		t.Fatal(err)
	}
	v, err := vkd.Versions()
	if err != nil {
		t.Fatal(err)
	}
	return &engine.Engine{Registry: reg, Versions: v, Version: "test"}
}

// exchange sends each request line to a real Serve loop and returns the responses by id ("" for no id).
func exchange(t *testing.T, eng *engine.Engine, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, eng, "test"); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, l)
		}
		res = append(res, m)
	}
	return res
}

func byID(t *testing.T, rs []map[string]any, id float64) map[string]any {
	t.Helper()
	for _, r := range rs {
		if r["id"] == id {
			return r
		}
	}
	t.Fatalf("no response with id %v in %v", id, rs)
	return nil
}

func toolText(t *testing.T, r map[string]any) (text string, isErr bool) {
	t.Helper()
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", r)
	}
	c := res["content"].([]any)[0].(map[string]any)
	return c["text"].(string), res["isError"].(bool)
}

func TestInitializeToolsListAndPlan(t *testing.T) {
	rs := exchange(t, testEngine(t),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vkd_plan","arguments":{"name":"demo","owner":"acme","open_source":true,"components":["go-api"]}}}`,
	)
	if len(rs) != 3 {
		t.Fatalf("want 3 responses (the notification gets none), got %d: %v", len(rs), rs)
	}

	init := byID(t, rs, 1)["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" || init["serverInfo"].(map[string]any)["name"] != "vkd" {
		t.Fatalf("initialize: %v", init)
	}
	if _, ok := init["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("the server must advertise the tools capability")
	}

	var names []string
	for _, tl := range byID(t, rs, 2)["result"].(map[string]any)["tools"].([]any) {
		m := tl.(map[string]any)
		names = append(names, m["name"].(string))
		if m["description"] == "" || m["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool %v needs a description and an object inputSchema", m["name"])
		}
	}
	if strings.Join(names, ",") != "vkd_list,vkd_schema,vkd_plan,vkd_create,vkd_update" {
		t.Fatalf("tools = %v", names)
	}

	text, isErr := toolText(t, byID(t, rs, 3))
	if isErr {
		t.Fatalf("vkd_plan failed: %s", text)
	}
	var plan struct {
		OK         bool
		Blueprints []string
		Files      []struct{ Path, Action string }
	}
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.OK || strings.Join(plan.Blueprints, ",") != "base,go-api,oss" {
		t.Fatalf("plan = %+v", plan)
	}
	seen := map[string]bool{}
	for _, f := range plan.Files {
		seen[f.Path] = true
		if f.Action != "create" {
			t.Errorf("a plan only creates: %+v", f)
		}
	}
	for _, p := range []string{"atlas.yaml", ".github/workflows/ci.yml", "apps/api/go.mod", "CONTRIBUTING.md"} {
		if !seen[p] {
			t.Errorf("plan lacks %s", p)
		}
	}
}

func TestPlanWritesNothingAndCreateWritesTheProject(t *testing.T) {
	eng := testEngine(t)
	dir := filepath.Join(t.TempDir(), "out")
	args := `{"name":"demo","owner":"acme","components":["go-api"],"dir":` + quote(dir) + `}`

	planArgs := `{"name":"demo","owner":"acme","components":["go-api"]}`
	rs := exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_plan","arguments":`+planArgs+`}}`)
	if text, isErr := toolText(t, byID(t, rs, 1)); isErr {
		t.Fatalf("plan failed: %s", text)
	}
	if _, err := os.Stat("demo"); !os.IsNotExist(err) {
		t.Fatal("vkd_plan wrote to disk")
	}

	rs = exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_create","arguments":`+args+`}}`)
	text, isErr := toolText(t, byID(t, rs, 1))
	if isErr {
		t.Fatalf("vkd_create failed: %s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps/api/go.mod")); err != nil {
		t.Fatalf("vkd_create did not write the project: %v", err)
	}

	// a second create into the same directory is refused, as a tool error, not a protocol error.
	rs = exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_create","arguments":`+args+`}}`)
	text, isErr = toolText(t, byID(t, rs, 1))
	if !isErr || !strings.Contains(text, "not empty") {
		t.Fatalf("want a tool error about the non-empty directory, got %v %s", isErr, text)
	}

	// vkd_update on the created project is a clean no-op.
	rs = exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_update","arguments":{"dir":`+quote(dir)+`}}}`)
	text, isErr = toolText(t, byID(t, rs, 1))
	if isErr || !strings.Contains(text, `"ok": true`) {
		t.Fatalf("update: %v %s", isErr, text)
	}
}

func TestErrorsAreToolErrorsOrProperRPCErrors(t *testing.T) {
	eng := testEngine(t)
	rs := exchange(t, eng,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_plan","arguments":{"owner":"acme"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vkd_plan","arguments":{"name":"demo","owner":"acme","oops":1}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}`,
		`{"jsonrpc":"2.0","id":5,"method":"does/not/exist"}`,
		`not json at all`,
		`{"id":6,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`,
	)
	if text, isErr := toolText(t, byID(t, rs, 1)); !isErr || !strings.Contains(text, "NAME") {
		t.Errorf("a missing name is a tool error naming NAME: %v %s", isErr, text)
	}
	if text, isErr := toolText(t, byID(t, rs, 2)); !isErr || !strings.Contains(text, "oops") {
		t.Errorf("an unknown argument must not be silently dropped: %v %s", isErr, text)
	}
	codes := map[float64]float64{3: -32602, 4: -32602, 5: -32601, 6: -32600}
	for id, code := range codes {
		e, ok := byID(t, rs, id)["error"].(map[string]any)
		if !ok || e["code"] != code {
			t.Errorf("id %v: want rpc error %v, got %v", id, code, byID(t, rs, id))
		}
	}
	var parseErr bool
	for _, r := range rs {
		if e, ok := r["error"].(map[string]any); ok && e["code"] == float64(-32700) && r["id"] == nil {
			parseErr = true
		}
	}
	if !parseErr {
		t.Error("invalid JSON must produce a -32700 parse error with a null id")
	}
	if _, ok := byID(t, rs, 7)["result"]; !ok {
		t.Error("ping must succeed")
	}
}

// The schema a tool advertises is the contract: vkd_plan has no `dir` (it writes nothing) and must reject it, so a
// client validating against inputSchema and the server agree. vkd_create advertises and accepts it.
func TestAdvertisedSchemaMatchesWhatTheServerAccepts(t *testing.T) {
	eng := testEngine(t)
	rs := exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	props := map[string]map[string]any{}
	for _, tl := range byID(t, rs, 1)["result"].(map[string]any)["tools"].([]any) {
		m := tl.(map[string]any)
		props[m["name"].(string)] = m["inputSchema"].(map[string]any)["properties"].(map[string]any)
	}
	if _, ok := props["vkd_plan"]["dir"]; ok {
		t.Error("vkd_plan must not advertise dir")
	}
	if _, ok := props["vkd_create"]["dir"]; !ok {
		t.Error("vkd_create must advertise dir")
	}
	if _, ok := props["vkd_update"]["add"]; !ok {
		t.Error("vkd_update must advertise add")
	}
	rs = exchange(t, eng, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_plan","arguments":{"name":"demo","owner":"acme","dir":"/tmp/x"}}}`)
	if text, isErr := toolText(t, byID(t, rs, 1)); !isErr || !strings.Contains(text, "dir") {
		t.Errorf("vkd_plan must reject dir: %v %s", isErr, text)
	}
}

func TestListAndSchemaTools(t *testing.T) {
	rs := exchange(t, testEngine(t),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vkd_list"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vkd_schema"}}`,
	)
	text, _ := toolText(t, byID(t, rs, 1))
	if !strings.Contains(text, `"go-api"`) || !strings.Contains(text, `"module"`) {
		t.Errorf("vkd_list lacks go-api and its question: %s", text)
	}
	text, _ = toolText(t, byID(t, rs, 2))
	var s map[string]any
	if err := json.Unmarshal([]byte(text), &s); err != nil || s["$schema"] == nil {
		t.Errorf("vkd_schema is not a JSON Schema: %v %s", err, text)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
