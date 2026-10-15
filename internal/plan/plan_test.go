package plan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
)

// reg builds a registry from name -> blueprint.yaml (+ optional files). Each blueprint must be valid.
func reg(t *testing.T, bps map[string]string, files map[string]map[string]string) *blueprint.Registry {
	t.Helper()
	r := blueprint.NewRegistry()
	for name, y := range bps {
		fsys := fstest.MapFS{"blueprint.yaml": {Data: []byte(y)}}
		for p, c := range files[name] {
			fsys["files/"+p] = &fstest.MapFile{Data: []byte(c)}
		}
		b, err := blueprint.Parse([]byte(y), name)
		if err != nil {
			t.Fatalf("blueprint %s: %v", name, err)
		}
		b.FS = fsys
		r.Add(b)
	}
	return r
}

func ans(components ...string) *answers.Answers {
	return &answers.Answers{Name: "demo", Owner: "acme", Components: components}
}

func names(bps []*blueprint.Blueprint) string {
	var out []string
	for _, b := range bps {
		out = append(out, b.Name)
	}
	return strings.Join(out, ",")
}

func resolve(t *testing.T, r *blueprint.Registry, a *answers.Answers) ([]*blueprint.Blueprint, error) {
	t.Helper()
	a.Defaults()
	return Resolve(r, a, nil)
}

const base = "name: base\nkind: base\nsummary: s\n"

func TestResolveOrderRequiresAndBaseFirst(t *testing.T) {
	r := reg(t, map[string]string{
		"base": base,
		"web":  "name: web\nkind: stack\nsummary: s\nrequires: [api]\n",
		"api":  "name: api\nkind: stack\nsummary: s\nrequires: [base]\n",
		"zed":  "name: zed\nkind: stack\nsummary: s\nrequires: [base]\n",
	}, nil)
	got, err := resolve(t, r, ans("web", "zed"))
	if err != nil {
		t.Fatal(err)
	}
	// base first; api pulled in by web's requires and placed before web; zed (selected) sorted by name.
	if names(got) != "base,api,web,zed" {
		t.Fatalf("order = %s", names(got))
	}
	again, _ := resolve(t, r, ans("zed", "web"))
	if names(again) != names(got) {
		t.Fatalf("order depends on input order: %s vs %s", names(again), names(got))
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		name string
		bps  map[string]string
		in   []string
		want string
	}{
		{"unknown component", map[string]string{"base": base}, []string{"nope"}, `unknown blueprint "nope" (known: base)`},
		{"requires unknown", map[string]string{"base": base,
			"a": "name: a\nkind: stack\nsummary: s\nrequires: [ghost]\n"}, []string{"a"}, `requires unknown blueprint "ghost"`},
		{"conflict", map[string]string{"base": base,
			"a": "name: a\nkind: stack\nsummary: s\nconflicts: [b]\n",
			"b": "name: b\nkind: stack\nsummary: s\n"}, []string{"a", "b"}, `"a" conflicts with "b"`},
		{"conflict through requires", map[string]string{"base": base,
			"a": "name: a\nkind: stack\nsummary: s\nconflicts: [c]\n",
			"b": "name: b\nkind: stack\nsummary: s\nrequires: [c]\n",
			"c": "name: c\nkind: stack\nsummary: s\n"}, []string{"a", "b"}, "conflicts"},
		{"cycle", map[string]string{"base": base,
			"a": "name: a\nkind: stack\nsummary: s\nrequires: [b]\n",
			"b": "name: b\nkind: stack\nsummary: s\nrequires: [a]\n"}, []string{"a"}, "requires cycle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolve(t, reg(t, c.bps, nil), ans(c.in...))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestResolveWhen(t *testing.T) {
	bps := map[string]string{
		"base":  base,
		"oss":   "name: oss\nkind: addon\nsummary: s\nrequires: [base]\nwhen: .OpenSource\n",
		"web":   "name: web\nkind: stack\nsummary: s\nrequires: [base]\n",
		"bunws": "name: bunws\nkind: addon\nsummary: s\nrequires: [base]\nwhen: or (has \"web\") (has \"site\")\n",
		"site":  "name: site\nkind: stack\nsummary: s\nrequires: [base]\n",
	}
	r := reg(t, bps, nil)

	a := ans()
	got, err := resolve(t, r, a)
	if err != nil || names(got) != "base" {
		t.Fatalf("no answers: %s, %v", names(got), err)
	}

	a = ans()
	a.OpenSource = true
	got, _ = resolve(t, r, a)
	if names(got) != "base,oss" {
		t.Fatalf("open source: %s", names(got))
	}

	// bunws depends on a stack selected by the user: `when` sees the selection.
	got, _ = resolve(t, r, ans("site"))
	if names(got) != "base,bunws,site" {
		t.Fatalf("has: %s", names(got))
	}

	// a `when` that becomes true only because another `when` added something (fixpoint).
	chain := reg(t, map[string]string{
		"base": base,
		// "a-late" sorts BEFORE "z-first", so one pass over the registry in name order cannot see z-first yet.
		"z-first": "name: z-first\nkind: addon\nsummary: s\nrequires: [base]\nwhen: .OpenSource\n",
		"a-late":  "name: a-late\nkind: addon\nsummary: s\nrequires: [base]\nwhen: has \"z-first\"\n",
	}, nil)
	a = ans()
	a.OpenSource = true
	got, _ = resolve(t, chain, a)
	if names(got) != "base,a-late,z-first" {
		t.Fatalf("fixpoint: %s", names(got))
	}

	bad := reg(t, map[string]string{"base": base,
		"z": "name: z\nkind: addon\nsummary: s\nrequires: [base]\nwhen: .NoSuchField\n"}, nil)
	if _, err := resolve(t, bad, ans()); err == nil || !strings.Contains(err.Error(), "NoSuchField") {
		t.Fatalf("a broken when must be an error naming it, got %v", err)
	}
}

func TestBuildFillsDefaultsAndNamesMissingRequiredFlag(t *testing.T) {
	bps := map[string]string{
		"base": base,
		"api": `name: api
kind: stack
summary: s
requires: [base]
questions:
  - {id: module, prompt: Module, default: "github.com/{{.Owner}}/{{.Name}}"}
  - {id: region, prompt: Region, required: true}
  - {id: tier, prompt: Tier, options: [free, pro], default: free}
`}
	files := map[string]map[string]string{"api": {"m.txt.tmpl": `{{index .Answers "api" "module"}} {{index .Answers "api" "tier"}}`}}
	r := reg(t, bps, files)

	_, err := Build(r, ans("api"), nil)
	var me *MissingError
	if !errors.As(err, &me) || len(me.Flags) != 1 || me.Flags[0] != "--api.region" {
		t.Fatalf("want MissingError naming --api.region, got %v", err)
	}

	a := ans("api")
	a.Set("api", "region", "br")
	p, err := Build(r, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := p.Find("m.txt")
	if !ok || string(f.Content) != "github.com/acme/demo free" {
		t.Fatalf("defaults were not applied: %q, %v", f.Content, ok)
	}

	a = ans("api")
	a.Set("api", "region", "br")
	a.Set("api", "tier", "gold")
	if _, err := Build(r, a, nil); err == nil || !strings.Contains(err.Error(), "--api.tier") || !strings.Contains(err.Error(), "free, pro") {
		t.Fatalf("an answer outside options must name the flag and the options, got %v", err)
	}

	a = ans("api")
	a.Set("api", "region", "br")
	a.Set("api", "typo", "x")
	if _, err := Build(r, a, nil); err == nil || !strings.Contains(err.Error(), `no question "typo"`) {
		t.Fatalf("an unknown answer must be rejected, got %v", err)
	}
}

func TestBuildPathTemplatingTargetAndFileRules(t *testing.T) {
	bps := map[string]string{
		"base": base,
		"api": `name: api
kind: stack
summary: s
requires: [base]
target: apps/{{.Name}}
files:
  - {path: LIC.mit, as: LICENSE, when: 'eq .License "MIT"'}
  - {path: LIC.apache, as: LICENSE, when: 'eq .License "Apache-2.0"'}
`}
	files := map[string]map[string]string{"api": {
		"cmd/{{.Name}}/main.go.tmpl": "package main // {{.Name}}",
		"static.txt":                 "{{.Name}} stays literal",
		"LIC.mit":                    "MIT",
		"LIC.apache":                 "APACHE",
	}}
	r := reg(t, bps, files)

	a := ans("api")
	a.License = "Apache-2.0"
	p, err := Build(r, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := p.Find("apps/demo/cmd/demo/main.go"); !ok || string(f.Content) != "package main // demo" {
		t.Fatalf("templated path/content/target wrong: %v", p.Paths())
	}
	if f, ok := p.Find("apps/demo/static.txt"); !ok || string(f.Content) != "{{.Name}} stays literal" {
		t.Fatalf("a file without .tmpl must be copied verbatim: %q", f.Content)
	}
	if f, ok := p.Find("apps/demo/LICENSE"); !ok || string(f.Content) != "APACHE" {
		t.Fatalf("the license rule picked the wrong file: %q", f.Content)
	}
	if _, ok := p.Find("apps/demo/LIC.mit"); ok {
		t.Error("a file with an `as` rule must not also be written under its own name")
	}
}

func TestBuildRejectsTwoBlueprintsWritingTheSameFile(t *testing.T) {
	r := reg(t, map[string]string{
		"base": base,
		"a":    "name: a\nkind: stack\nsummary: s\nrequires: [base]\n",
		"b":    "name: b\nkind: stack\nsummary: s\nrequires: [base]\n",
	}, map[string]map[string]string{"a": {"x.txt": "1"}, "b": {"x.txt": "2"}})
	_, err := Build(r, ans("a", "b"), nil)
	if err == nil || !strings.Contains(err.Error(), `"x.txt"`) || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("want a clash naming the file and both blueprints, got %v", err)
	}
}

func TestBuildValidatesGlobalAnswers(t *testing.T) {
	r := reg(t, map[string]string{"base": base}, nil)
	cases := []struct {
		name string
		a    *answers.Answers
		want string
	}{
		{"bad name", &answers.Answers{Name: "Bad Name", Owner: "acme"}, "name"},
		{"bad owner", &answers.Answers{Name: "demo", Owner: "-x-"}, "--owner"},
		{"bad license", &answers.Answers{Name: "demo", Owner: "acme", License: "WTFPL"}, "--license"},
		{"oss proprietary", &answers.Answers{Name: "demo", Owner: "acme", OpenSource: true, License: "proprietary"}, "--oss"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Build(r, c.a, nil); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

// Every `contributes` key a blueprint can set must reach the generated files. A key that parses but is never read
// would let an author think something is configured when nothing is.
func TestEveryContributesKeyReachesAFile(t *testing.T) {
	r := reg(t, map[string]string{
		"base": `name: base
kind: base
summary: s
composed:
  - {path: atlas.yaml, kind: atlas}
  - {path: .gitignore, kind: gitignore}
  - {path: .env.example, kind: env}
  - {path: lefthook.yml, kind: lefthook}
  - {path: AGENTS.md, kind: agents, head: _head/agents.tmpl}
  - {path: .github/workflows/ci.yml, kind: ci, head: _head/ci.tmpl}
  - {path: .github/workflows/release.yml, kind: release, head: _head/release.tmpl}
`,
		"probe": `name: probe
kind: stack
summary: s
requires: [base]
contributes:
  atlas:
    pre_command: probe-pre-command
    services: [{name: probe-svc, command: probe-run, port: 4001}]
    actions: [{key: p, label: probe-action, command: probe-act}]
  ci:
    jobs:
      probe-ci: {name: probe-ci-name, runs-on: ubuntu-latest, steps: [{run: "true"}]}
  release:
    jobs:
      probe-rel: {name: probe-rel-name, needs: gate, runs-on: ubuntu-latest, steps: [{run: "true"}]}
  gitignore: [probe-ignored/]
  workspaces: [probe-ws]
  env: [PROBE_ENV=1]
  lefthook:
    pre-push:
      commands:
        probe-hook: {run: probe-hook-cmd}
  agents: probe-agents-text
`,
	}, map[string]map[string]string{"base": {
		"_head/agents.tmpl":  "# AGENTS\n",
		"_head/ci.tmpl":      "name: CI\non: push\n",
		"_head/release.tmpl": "name: Release\non: push\n",
		"ws.txt.tmpl":        "{{range .Workspaces}}[{{.}}]{{end}}",
	}})
	p, err := Build(r, ans("probe"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"atlas.yaml":                    "probe-pre-command",
		".gitignore":                    "probe-ignored/",
		".env.example":                  "PROBE_ENV=1",
		"lefthook.yml":                  "probe-hook-cmd",
		"AGENTS.md":                     "probe-agents-text",
		".github/workflows/ci.yml":      "probe-ci-name",
		".github/workflows/release.yml": "probe-rel-name",
		"ws.txt":                        "[probe-ws]",
	}
	for path, needle := range want {
		f, ok := p.Find(path)
		if !ok || !strings.Contains(string(f.Content), needle) {
			t.Errorf("%s does not contain %q (a contributes key is parsed but not applied):\n%s", path, needle, f.Content)
		}
	}
	atlas, _ := p.Find("atlas.yaml")
	for _, n := range []string{"probe-svc", "probe-run", "probe-action"} {
		if !strings.Contains(string(atlas.Content), n) {
			t.Errorf("atlas.yaml lacks %q", n)
		}
	}
}

const structBase = `name: base
kind: base
summary: s
composed:
  - {path: atlas.yaml, kind: atlas}
`

// contributes is rendered as TEXT before it is parsed, so a template can change the structure of the YAML.
func TestContributesTemplatesCanChangeStructure(t *testing.T) {
	bps := map[string]string{
		"base": structBase,
		"x":    "name: x\nkind: stack\nsummary: s\nrequires: [base]\n",
		"api":  "name: api\nkind: stack\nsummary: s\nrequires: [base]\ncontributes:\n  atlas:\n    services:\n      - {name: api, command: run-api, port: 4001}\n",
		"web": `name: web
kind: stack
summary: s
requires: [base]
contributes:
  atlas:
    services:
      - name: web
        command: run-web
        port: {{ port 1 }}
        {{- if has "x" }}
        depends_on: [api]
        {{- end }}
        {{ if has "x" }}open_key: w{{ end }}
`,
	}
	r := reg(t, bps, nil)
	with, err := Build(r, ans("web", "x", "api"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := with.Find("atlas.yaml")
	if !strings.Contains(string(f.Content), "depends_on:") || !strings.Contains(string(f.Content), "open_key: w") {
		t.Fatalf("x is selected: want depends_on and open_key:\n%s", f.Content)
	}
	without, err := Build(r, ans("web", "api"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _ = without.Find("atlas.yaml")
	if strings.Contains(string(f.Content), "depends_on") || strings.Contains(string(f.Content), "open_key") {
		t.Fatalf("x is not selected: the keys must be absent:\n%s", f.Content)
	}
	if !strings.Contains(string(f.Content), "port: 4003") && !strings.Contains(string(f.Content), "port: 4") {
		t.Fatalf("port func must still work inside structural templates:\n%s", f.Content)
	}
}

// `${{ }}` inside contributes still passes through the text render untouched.
func TestContributesTextRenderKeepsGitHubExpressions(t *testing.T) {
	r := reg(t, map[string]string{
		"base": "name: base\nkind: base\nsummary: s\ncomposed:\n  - {path: .github/workflows/ci.yml, kind: ci, head: _h/ci.tmpl}\n",
		"job": `name: job
kind: stack
summary: s
requires: [base]
contributes:
  ci:
    jobs:
      j:
        runs-on: ubuntu-latest
        steps:
          - run: echo ${{ github.sha }} {{ .Name }}
`,
	}, map[string]map[string]string{"base": {"_h/ci.tmpl": "name: CI\non: push\n"}})
	p, err := Build(r, ans("job"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := p.Find(".github/workflows/ci.yml")
	if !strings.Contains(string(f.Content), "echo ${{ github.sha }} demo") {
		t.Fatalf("got:\n%s", f.Content)
	}
}

// Rendered contributes are validated like authored ones: the error names the blueprint.
func TestRenderedContributesAreValidated(t *testing.T) {
	r := reg(t, map[string]string{
		"base": structBase,
		"bad": "name: bad\nkind: stack\nsummary: s\nrequires: [base]\ncontributes:\n  ci:\n    jobs:\n      " +
			"{{ if has \"bad\" }}ci{{ else }}ok{{ end }}: {name: x}\n",
	}, nil)
	if _, err := Build(r, ans("bad"), nil); err == nil || !strings.Contains(err.Error(), "bad") || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("want a reserved-job error naming the blueprint, got %v", err)
	}
	// A template-free block is validated when the blueprint loads (earlier than a plan); a templated one only once
	// rendered. Both name the typo.
	if _, err := blueprint.Parse([]byte("name: typo\nkind: stack\nsummary: s\ncontributes:\n  gitignor: [x]\n"), "typo.yaml"); err == nil || !strings.Contains(err.Error(), "gitignor") {
		t.Fatalf("an unknown contributes key must fail at load, got %v", err)
	}
	r = reg(t, map[string]string{
		"base": structBase,
		"typo": "name: typo\nkind: stack\nsummary: s\nrequires: [base]\ncontributes:\n  {{ if has \"typo\" }}gitignor{{ end }}: [x]\n",
	}, nil)
	if _, err := Build(r, ans("typo"), nil); err == nil || !strings.Contains(err.Error(), "gitignor") {
		t.Fatalf("an unknown rendered contributes key must be an error, got %v", err)
	}
}

// Three stacks that all suggest `c` for their check action still compose: no error, unique valid keys, and a
// warning in the plan for each action that moved.
func TestPlanResolvesActionKeyClashesWithWarnings(t *testing.T) {
	stack := func(n string) string {
		return "name: " + n + "\nkind: stack\nsummary: s\nrequires: [base]\ncontributes:\n  atlas:\n    actions:\n      - {key: c, label: Check " + n + ", command: x}\n"
	}
	r := reg(t, map[string]string{"base": structBase, "go-api": stack("go-api"), "rust-workspace": stack("rust-workspace"), "tauri-desktop": stack("tauri-desktop")}, nil)
	p, err := Build(r, ans("tauri-desktop", "go-api", "rust-workspace"), nil)
	if err != nil {
		t.Fatalf("a key clash must not fail the plan: %v", err)
	}
	if len(p.Warnings) != 2 {
		t.Fatalf("want 2 warnings (two actions moved), got %v", p.Warnings)
	}
	f, _ := p.Find("atlas.yaml")
	text := string(f.Content)
	// plan order is by name here: go-api, rust-workspace, tauri-desktop: the first keeps `c`.
	if !strings.Contains(text, "key: c\n    label: Check go-api") {
		t.Errorf("the first blueprint in plan order must keep c:\n%s", text)
	}
}

// Two blueprints that each ask for a port by service name never collide, in either plan order, and a file template
// that names the same service gets the same port the atlas service got.
func TestPlanPortsNeverCollideAndFilesAgreeWithAtlas(t *testing.T) {
	svc := func(n, service string) string {
		return "name: " + n + "\nkind: stack\nsummary: s\nrequires: [base]\ncontributes:\n  atlas:\n    services:\n" +
			"      - {name: " + service + ", command: run, port: '{{ port \"" + service + "\" }}'}\n"
	}
	r := reg(t, map[string]string{"base": structBase, "a-first": svc("a-first", "api"), "z-last": svc("z-last", "desktop")},
		map[string]map[string]string{"z-last": {"ports.txt.tmpl": `desktop={{port "desktop"}} api={{port "api"}}`}})
	p, err := Build(r, ans("a-first", "z-last"), nil)
	if err != nil {
		t.Fatalf("two blueprints asking for ports must compose: %v", err)
	}
	atlas, _ := p.Find("atlas.yaml")
	ports, _ := p.Find("ports.txt")
	var api, desktop int
	for _, line := range strings.Split(string(atlas.Content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "port:") {
			var n int
			_, _ = fmtSscan(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "port:")), &n)
			if api == 0 {
				api = n
			} else {
				desktop = n
			}
		}
	}
	if api == 0 || desktop == 0 || api == desktop {
		t.Fatalf("services must have distinct ports, got api=%d desktop=%d:\n%s", api, desktop, atlas.Content)
	}
	want := "desktop=" + itoa(desktop) + " api=" + itoa(api)
	if string(ports.Content) != want {
		t.Fatalf("a file must see the same ports as atlas.yaml: got %q want %q", ports.Content, want)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func fmtSscan(s string, n *int) (int, error) { return fmt.Sscanf(s, "%d", n) }
