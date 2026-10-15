package render

import (
	"strings"
	"testing"

	"github.com/vkdprojects/vkd/internal/answers"
)

func testEnv() *Env {
	return NewEnv(map[string]any{
		"bun":     "1.4.3",
		"actions": map[string]any{"checkout": "actions/checkout@abc # v7"},
	}, []string{"base", "go-api"}, 4060)
}

func testCtx() Context {
	a := &answers.Answers{Name: "demo-app", Owner: "acme", Description: "A demo", OpenSource: true, License: "MIT",
		PortBase: 4060, Year: 2026, Components: []string{"go-api"},
		Per: map[string]map[string]string{"go-api": {"module": "github.com/acme/demo-app"}}}
	return NewContext(a)
}

func TestStringTable(t *testing.T) {
	e, ctx := testEnv(), testCtx()
	cases := []struct{ name, in, want string }{
		{"plain text is untouched", "no templates here", "no templates here"},
		{"field", "{{.Name}} by {{.Owner}}", "demo-app by acme"},
		{"per-blueprint answer", `{{index .Answers "go-api" "module"}}`, "github.com/acme/demo-app"},
		{"snake", `{{snake .Name}}`, "demo_app"},
		{"kebab of camel", `{{kebab "myHTTPServer"}}`, "my-http-server"},
		{"camel", `{{camel "demo-app"}}`, "demoApp"},
		{"pascal", `{{pascal "demo_app_x"}}`, "DemoAppX"},
		{"upper lower", `{{upper "a"}}{{lower "B"}}`, "Ab"},
		{"indent keeps blank lines empty", "{{indent 2 \"a\\n\\nb\"}}", "  a\n\n  b"},
		{"has selected", `{{if has "go-api"}}y{{else}}n{{end}}`, "y"},
		{"has not selected", `{{if has "rust-cli"}}y{{else}}n{{end}}`, "n"},
		{"version", `{{version "bun"}}`, "1.4.3"},
		{"nested version", `{{version "actions.checkout"}}`, "actions/checkout@abc # v7"},
		{"port 0 is the base", `{{port 0}}`, "4060"},
		{"port 3", `{{port 3}}`, "4063"},
		{"toYaml", `{{toYaml .Components}}`, "- go-api"},
		{"default", `{{default "x" ""}}-{{default "x" "y"}}`, "x-y"},
		{"join", `{{join ", " .Components}}`, "go-api"},
		{"GitHub expression passes through", `run: ${{ github.event.pull_request.number }} for {{.Name}}`, "run: ${{ github.event.pull_request.number }} for demo-app"},
		{"two GitHub expressions", `${{ a }}|{{.Name}}|${{ b || c }}`, "${{ a }}|demo-app|${{ b || c }}"},
		{"literal braces via the template", `{{"{{"}} {{.Name}}`, "{{ demo-app"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.String("t", c.in, ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestStringErrors(t *testing.T) {
	e, ctx := testEnv(), testCtx()
	cases := []struct{ name, in, want string }{
		{"unknown field is an error, never empty", `{{.NoSuchField}}`, "NoSuchField"},
		{"unpinned version", `{{version "nope"}}`, "not pinned"},
		{"version is a table", `{{version "actions"}}`, "table"},
		{"port outside the block", `{{port 10}}`, "outside the project block"},
		{"syntax error", `{{if}}`, "t:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.String("t", c.in, ctx)
			if err == nil {
				t.Fatalf("want error containing %q, got %q", c.want, got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

// A blueprint that is not in the plan has no answers; index on the missing key renders "" without an error.
// This is why Build fills every selected question's default before rendering (see plan tests).
func TestMissingAnswerRendersEmptyNotError(t *testing.T) {
	got, err := testEnv().String("t", `[{{index .Answers "rust-cli" "x"}}]`, testCtx())
	if err != nil || got != "[]" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPath(t *testing.T) {
	e, ctx := testEnv(), testCtx()
	got, err := e.Path("apps/{{.Name}}/{{snake .Name}}.go", ctx)
	if err != nil || got != "apps/demo-app/demo_app.go" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"{{.Description}}/../x", "a//b", "../x", "{{default \"\" \"\"}}/x"} {
		if out, err := e.Path(bad, ctx); err == nil {
			t.Errorf("Path(%q) = %q, want an error for an empty or .. segment", bad, out)
		}
	}
}

func TestValueRendersNestedStrings(t *testing.T) {
	e, ctx := testEnv(), testCtx()
	in := map[string]any{
		"name":  "{{.Name}}-api",
		"port":  "{{port 0}}",
		"steps": []any{map[string]any{"uses": `{{version "actions.checkout"}}`, "run": "echo ${{ github.sha }}"}},
		"n":     5,
		"ok":    true,
	}
	out, err := e.Value("v", in, ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	step := m["steps"].([]any)[0].(map[string]any)
	if m["name"] != "demo-app-api" || m["port"] != "4060" || m["n"] != 5 || m["ok"] != true ||
		step["uses"] != "actions/checkout@abc # v7" || step["run"] != "echo ${{ github.sha }}" {
		t.Fatalf("rendered wrong: %#v", out)
	}
	if _, err := e.Value("v", map[string]any{"a": []any{"{{.Nope}}"}}, ctx); err == nil || !strings.Contains(err.Error(), "v.a[0]") {
		t.Fatalf("want an error naming the path v.a[0], got %v", err)
	}
}

func portEnv() *Env { return NewEnv(nil, nil, 4060) }

func portOf(t *testing.T, e *Env, arg string) string {
	t.Helper()
	got, err := e.String("t", "{{port "+arg+"}}", testCtx())
	if err != nil {
		t.Fatalf("port %s: %v", arg, err)
	}
	return got
}

// A name gets a port in the order names are first asked for, from the project's base. Asking again returns the
// same port, so a blueprint can use it in `port`, `health` and `env` and another file can refer to it.
func TestNamedPortsAreAllocatedInOrderAndStable(t *testing.T) {
	e := portEnv()
	if got := portOf(t, e, `"api"`); got != "4060" {
		t.Fatalf("first name gets the base, got %s", got)
	}
	if got := portOf(t, e, `"desktop"`); got != "4061" {
		t.Fatalf("second name gets base+1, got %s", got)
	}
	if got := portOf(t, e, `"api"`); got != "4060" {
		t.Fatalf("the same name must give the same port, got %s", got)
	}
	if got := portOf(t, e, `"web"`); got != "4062" {
		t.Fatalf("third name, got %s", got)
	}
}

// The reported bug: two blueprints both asking for "the first port" collided. With names they never can.
func TestNamedPortsNeverCollideAcrossBlueprints(t *testing.T) {
	e := portEnv()
	seen := map[string]string{}
	for _, name := range []string{"api", "desktop", "web", "site", "worker", "api", "desktop"} {
		p := portOf(t, e, `"`+name+`"`)
		if other, dup := seen[p]; dup && other != name {
			t.Fatalf("%s and %s both got %s", name, other, p)
		}
		seen[p] = name
	}
	if len(seen) != 5 {
		t.Fatalf("five distinct names must give five distinct ports, got %v", seen)
	}
}

// Numeric ports (the old form) keep working as fixed offsets, and a name never takes a port a number already holds,
// whichever is asked first.
func TestNamedPortsSkipNumericOffsets(t *testing.T) {
	e := portEnv()
	if got := portOf(t, e, "0"); got != "4060" {
		t.Fatalf("numeric 0 is the base, got %s", got)
	}
	if got := portOf(t, e, `"api"`); got != "4061" {
		t.Fatalf("a name must skip the port numeric 0 holds, got %s", got)
	}

	e = portEnv()
	if got := portOf(t, e, `"api"`); got != "4060" {
		t.Fatal(got)
	}
	_, err := e.String("t", "{{port 0}}", testCtx())
	if err == nil || !strings.Contains(err.Error(), `"api"`) {
		t.Fatalf("numeric 0 after the name api took it must be an error naming api, got %v", err)
	}
}

func TestNamedPortsStayInsideTheBlock(t *testing.T) {
	e := portEnv()
	for i := 0; i < 10; i++ {
		portOf(t, e, `"svc`+strings.Repeat("x", i)+`"`)
	}
	_, err := e.String("t", `{{port "one-too-many"}}`, testCtx())
	if err == nil || !strings.Contains(err.Error(), "block") {
		t.Fatalf("an eleventh name must be an error about the 10-port block, got %v", err)
	}
	if _, err := e.String("t", `{{port ""}}`, testCtx()); err == nil {
		t.Error("an empty name is an error")
	}
	if _, err := e.String("t", `{{port 1.5}}`, testCtx()); err == nil {
		t.Error("a non-integer, non-string argument is an error")
	}
}
