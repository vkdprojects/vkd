package compose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, data)
	}
	return m
}

func TestAtlasMergesServicesActionsAndHeader(t *testing.T) {
	out, _, err := Atlas(Header{Name: "demo", EnvFiles: []string{".env"}}, []Fragment{
		{Blueprint: "go-api", PreCommand: "bun install",
			Services: []map[string]any{{"name": "api", "command": "go run ./cmd/api", "dir": "apps/api", "port": "4060", "health": "http://localhost:4060/healthz"}},
			Actions:  []map[string]any{{"key": "c", "label": "Check api", "command": "go test ./..."}}},
		{Blueprint: "web",
			Services: []map[string]any{{"name": "web", "command": "bun run dev", "port": 4061, "depends_on": []any{"api"}}},
			Actions:  []map[string]any{{"key": "w", "label": "Check web", "command": "bun run check"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parse(t, out)
	if m["name"] != "demo" || m["pre_command"] != "bun install" {
		t.Fatalf("header wrong: %v", m)
	}
	svcs := m["services"].(map[string]any)
	if len(svcs) != 2 {
		t.Fatalf("services = %v", svcs)
	}
	api := svcs["api"].(map[string]any)
	if api["port"] != 4060 {
		t.Errorf("a templated string port must become a YAML integer (atlas rejects strings), got %#v", api["port"])
	}
	if _, has := api["name"]; has {
		t.Error("the service name is the map key, not a field")
	}
	if len(m["actions"].([]any)) != 2 {
		t.Errorf("actions = %v", m["actions"])
	}
	// keys are written in the order a human reads them: command first, then dir, port, health.
	text := string(out)
	cmd, dir, port := strings.Index(text, "command:"), strings.Index(text, "dir:"), strings.Index(text, "port:")
	if cmd >= dir || dir >= port {
		t.Errorf("service keys are not in reading order:\n%s", text)
	}
}

func TestAtlasRejectsClashes(t *testing.T) {
	svc := func(bp, name string, port any) Fragment {
		return Fragment{Blueprint: bp, Services: []map[string]any{{"name": name, "command": "x", "port": port}}}
	}
	cases := []struct {
		name  string
		frags []Fragment
		want  []string
	}{
		{"same service name", []Fragment{svc("a", "api", 1), svc("b", "api", 2)}, []string{`"api"`, `"a"`, `"b"`}},
		{"same port", []Fragment{svc("a", "one", "4060"), svc("b", "two", 4060)}, []string{"4060", `"one"`, `"two"`}},
		{"two pre_commands", []Fragment{{Blueprint: "a", PreCommand: "bun install"}, {Blueprint: "b", PreCommand: "npm ci"}}, []string{"pre_command"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Atlas(Header{Name: "d"}, c.frags)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %s", err, w)
				}
			}
		})
	}
	if _, _, err := Atlas(Header{Name: "d"}, []Fragment{{Blueprint: "a", PreCommand: "bun install"}, {Blueprint: "b", PreCommand: "bun install"}}); err != nil {
		t.Errorf("the same pre_command from two blueprints is not a clash: %v", err)
	}
}

func TestAtlasWithNoServicesIsStillValidYAML(t *testing.T) {
	out, _, err := Atlas(Header{Name: "empty"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := parse(t, out); m["name"] != "empty" || m["services"] == nil {
		t.Fatalf("got %v", m)
	}
}

func jobs(ids ...string) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, id := range ids {
		m[id] = map[string]any{"name": id, "runs-on": "ubuntu-latest", "steps": []any{map[string]any{"run": "true"}}}
	}
	return m
}

func TestCIAggregateNeedsEveryJob(t *testing.T) {
	out, err := CI("name: CI\non: push", []Fragment{
		{Blueprint: "go-api", CIJobs: jobs("go-api")},
		{Blueprint: "web", CIJobs: jobs("web", "web-e2e")},
		{Blueprint: "base", CIJobs: jobs("workflows")},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parse(t, out)
	all := m["jobs"].(map[string]any)
	ci := all["ci"].(map[string]any)
	if ci["name"] != "CI" || ci["if"] != "${{ !cancelled() }}" {
		t.Fatalf("aggregate job wrong: %v", ci)
	}
	var needs []string
	for _, n := range ci["needs"].([]any) {
		needs = append(needs, n.(string))
	}
	if strings.Join(needs, ",") != "go-api,web,web-e2e,workflows" {
		t.Fatalf("needs = %v, want every other job, sorted, and not itself", needs)
	}
	for id := range all {
		if id == "ci" {
			continue
		}
		found := false
		for _, n := range needs {
			found = found || n == id
		}
		if !found {
			t.Errorf("job %q is not gated by the aggregate CI job", id)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(string(out)), "<<<\"$NEEDS\" >/dev/null") {
		t.Errorf("the aggregate job must be last and must check every result:\n%s", out)
	}
	if !strings.Contains(string(out), `all(.value.result == "success")`) {
		t.Errorf("a skipped or failed job must fail the aggregate:\n%s", out)
	}
}

func TestCIRejectsDuplicateJobAndKeepsHead(t *testing.T) {
	_, err := CI("name: CI", []Fragment{{Blueprint: "a", CIJobs: jobs("x")}, {Blueprint: "b", CIJobs: jobs("x")}})
	if err == nil || !strings.Contains(err.Error(), `"x"`) || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("want a clash naming the job and both blueprints, got %v", err)
	}
	out, _ := CI("name: CI\n# a comment in the head\non: push\n", []Fragment{{Blueprint: "a", CIJobs: jobs("x")}})
	if !strings.HasPrefix(string(out), "name: CI\n# a comment in the head\non: push\n\njobs:") {
		t.Fatalf("the head must be kept verbatim before the jobs:\n%s", out)
	}
}

// A `uses` value "owner/repo@sha # v1" must come out with a real YAML comment, not as part of a quoted string:
// zizmor only accepts a hash pin when the ref is a plain value.
func TestUsesCommentIsARealComment(t *testing.T) {
	j := map[string]map[string]any{"a": {"runs-on": "x", "steps": []any{
		map[string]any{"uses": "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"},
		map[string]any{"run": "echo 'a # b'"},
	}}}
	out, err := CI("name: CI", []Fragment{{Blueprint: "a", CIJobs: j}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n") {
		t.Fatalf("want an unquoted uses with a trailing comment:\n%s", out)
	}
	steps := parse(t, out)["jobs"].(map[string]any)["a"].(map[string]any)["steps"].([]any)
	if got := steps[0].(map[string]any)["uses"]; got != "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1" {
		t.Errorf("parsed uses = %q, the comment leaked into the value", got)
	}
	if got := steps[1].(map[string]any)["run"]; got != "echo 'a # b'" {
		t.Errorf("a ' # ' in a run script must survive untouched, got %q", got)
	}
}

func TestReleaseExpandsStarNeedsAndOrdersGateFirstPublishLast(t *testing.T) {
	pub := map[string]any{"name": "Publish", "needs": []any{"*"}, "runs-on": "x", "steps": []any{}}
	out, err := Release("name: Release", []Fragment{
		{Blueprint: "base", RelJobs: map[string]map[string]any{"publish": pub, "gate": jobs("gate")["gate"]}},
		{Blueprint: "go-api", RelJobs: map[string]map[string]any{"go-api-image": {"name": "img", "needs": "gate", "runs-on": "x", "steps": []any{}}}},
		{Blueprint: "cli", RelJobs: jobs("cli-build")},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	g, c, p := strings.Index(text, "\n  gate:"), strings.Index(text, "\n  go-api-image:"), strings.Index(text, "\n  publish:")
	if g < 0 || g >= c || c >= p {
		t.Fatalf("order must be gate, then the rest, then publish (gate=%d image=%d publish=%d):\n%s", g, c, p, text)
	}
	needs := parse(t, out)["jobs"].(map[string]any)["publish"].(map[string]any)["needs"].([]any)
	var got []string
	for _, n := range needs {
		got = append(got, n.(string))
	}
	if strings.Join(got, ",") != "cli-build,gate,go-api-image" {
		t.Fatalf("publish needs = %v, want every other job and not itself", got)
	}
}

func TestGitignoreDedupesAndGroupsByBlueprint(t *testing.T) {
	out := string(Gitignore([]Fragment{
		{Blueprint: "base", Gitignore: []string{".env", ".DS_Store"}},
		{Blueprint: "web", Gitignore: []string{"node_modules/", ".env"}},
		{Blueprint: "empty"},
		{Blueprint: "dup-only", Gitignore: []string{"node_modules/"}},
	}))
	want := "# base\n.env\n.DS_Store\n\n# web\nnode_modules/\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestWorkspacesSortedUnique(t *testing.T) {
	got := Workspaces([]Fragment{{Workspaces: []string{"apps/web", "packages/*"}}, {Workspaces: []string{"apps/docs", "apps/web"}}})
	if strings.Join(got, ",") != "apps/docs,apps/web,packages/*" {
		t.Fatalf("got %v", got)
	}
}

func TestAgentsSectionsPerBlueprint(t *testing.T) {
	out := string(Agents("# AGENTS.md\n\nintro\n", []Fragment{
		{Blueprint: "base", Agents: ""},
		{Blueprint: "go-api", Agents: "\n  Run it.\n\n"},
	}))
	if out != "# AGENTS.md\n\nintro\n\n## go-api\n\nRun it.\n" {
		t.Fatalf("got %q", out)
	}
}

func TestLefthookMergesHooksAndRejectsDuplicates(t *testing.T) {
	lh := func(hook, name, run string) map[string]map[string]map[string]any {
		return map[string]map[string]map[string]any{hook: {"commands": {name: map[string]any{"run": run}}}}
	}
	out, err := Lefthook([]Fragment{
		{Blueprint: "base", Lefthook: lh("commit-msg", "conventional-commit", "scripts/check-commit-msg.sh {1}")},
		{Blueprint: "go-api", Lefthook: lh("pre-push", "go-test", "go test ./...")},
		{Blueprint: "web", Lefthook: lh("pre-push", "js-check", "bun run check")},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parse(t, out)
	pp := m["pre-push"].(map[string]any)
	if pp["parallel"] != true || len(pp["commands"].(map[string]any)) != 2 {
		t.Fatalf("pre-push = %v", pp)
	}
	if _, ok := m["commit-msg"].(map[string]any)["commands"].(map[string]any)["conventional-commit"]; !ok {
		t.Fatalf("commit-msg lost: %v", m)
	}
	_, err = Lefthook([]Fragment{{Blueprint: "a", Lefthook: lh("pre-push", "t", "x")}, {Blueprint: "b", Lefthook: lh("pre-push", "t", "y")}})
	if err == nil || !strings.Contains(err.Error(), `"t"`) {
		t.Fatalf("want a duplicate command error, got %v", err)
	}
}

func TestEnvDedupes(t *testing.T) {
	out := string(Env([]Fragment{{Env: []string{"A=1", "# c"}}, {Env: []string{"A=1", "B=2"}}}))
	if out != "A=1\n# c\nB=2\n" {
		t.Fatalf("got %q", out)
	}
}

func actionKeys(t *testing.T, out []byte) map[string]string {
	t.Helper()
	got := map[string]string{} // label -> key
	for _, a := range parse(t, out)["actions"].([]any) {
		m := a.(map[string]any)
		got[m["label"].(string)] = m["key"].(string)
	}
	return got
}

func keyed(bp, key, label string) Fragment {
	return Fragment{Blueprint: bp, Actions: []map[string]any{{"key": key, "label": label, "command": "c"}}}
}

// An action key is a suggestion. On a clash the later blueprint (plan order) gets the next free key, the first keeps
// its own, and a warning says what moved, so three stacks that all want `c` still compose.
func TestAtlasActionKeyClashIsResolvedDeterministically(t *testing.T) {
	frags := []Fragment{keyed("go-api", "c", "Check api"), keyed("rust-workspace", "c", "Check rust"), keyed("tauri", "c", "Check desktop")}
	out, warnings, err := Atlas(Header{Name: "d"}, frags)
	if err != nil {
		t.Fatalf("a key clash must not be fatal: %v", err)
	}
	keys := actionKeys(t, out)
	if keys["Check api"] != "c" {
		t.Errorf("the first blueprint keeps its key, got %q", keys["Check api"])
	}
	seen := map[string]bool{}
	for label, k := range keys {
		if len([]rune(k)) != 1 || seen[k] {
			t.Errorf("%s: key %q is not a unique single character (%v)", label, k, keys)
		}
		seen[k] = true
	}
	if len(warnings) != 2 {
		t.Fatalf("want one warning per moved action, got %v", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, `"c"`) || !strings.Contains(w, "Check") {
			t.Errorf("warning %q must name the requested key and the action", w)
		}
	}
	again, againWarn, _ := Atlas(Header{Name: "d"}, frags)
	if string(again) != string(out) || strings.Join(againWarn, "|") != strings.Join(warnings, "|") {
		t.Error("resolution must be deterministic: same input, same keys and warnings")
	}
}

func TestAtlasActionKeysNeverTakeAReservedOrServiceKey(t *testing.T) {
	svc := Fragment{Blueprint: "web", Services: []map[string]any{{"name": "web", "command": "x", "port": 4001, "open_key": "c"}}}
	out, warnings, err := Atlas(Header{Name: "d"}, []Fragment{svc, keyed("a", "c", "Check a"), keyed("b", "t", "Test b")})
	if err != nil {
		t.Fatal(err)
	}
	keys := actionKeys(t, out)
	if keys["Check a"] == "c" {
		t.Errorf("an action must not take a service's open_key: %v", keys)
	}
	if keys["Test b"] == "t" {
		t.Errorf("t is a built-in atlas dashboard key and must be reassigned: %v", keys)
	}
	for _, k := range keys {
		if strings.Contains(string(reservedKeys), k) {
			t.Errorf("key %q is reserved by atlas", k)
		}
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestAtlasActionKeysRunOutOfKeysIsAnError(t *testing.T) {
	var frags []Fragment
	for i := 0; i < 100; i++ {
		frags = append(frags, keyed("bp"+strings.Repeat("x", i), "c", "A"+strings.Repeat("y", i)))
	}
	if _, _, err := Atlas(Header{Name: "d"}, frags); err == nil || !strings.Contains(err.Error(), "free action key") {
		t.Fatalf("want an error when no free key is left, got %v", err)
	}
}
