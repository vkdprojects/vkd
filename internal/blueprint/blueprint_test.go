package blueprint

import (
	"strings"
	"testing"
	"testing/fstest"
)

const valid = `name: go-api
kind: stack
summary: Go HTTP API
requires: [base]
target: apps/api
questions:
  - id: module
    prompt: Go module path
contributes:
  atlas:
    services:
      - name: api
        command: go run ./cmd/api
    actions:
      - key: c
        label: Check
        command: go test ./...
`

func TestParseValid(t *testing.T) {
	b, err := Parse([]byte(valid), "x/blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "go-api" || b.Kind != KindStack || b.Target != "apps/api" || len(b.Questions) != 1 {
		t.Fatalf("parsed wrong: %+v", b)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{"bad name", "name: Go_API\nkind: stack\nsummary: s\n", "name"},
		{"bad kind", "name: a\nkind: widget\nsummary: s\n", `kind "widget"`},
		{"no summary", "name: a\nkind: stack\n", "summary is required"},
		{"base requires", "name: a\nkind: base\nsummary: s\nrequires: [b]\n", "base blueprint cannot require"},
		{"requires itself", "name: a\nkind: stack\nsummary: s\nrequires: [a]\n", "requires itself"},
		{"required and conflicting", "name: a\nkind: stack\nsummary: s\nrequires: [b]\nconflicts: [b]\n", "both required and conflicting"},
		{"absolute target", "name: a\nkind: stack\nsummary: s\ntarget: /etc\n", "relative path"},
		{"dotdot target", "name: a\nkind: stack\nsummary: s\ntarget: ../x\n", "relative path"},
		{"duplicate question", "name: a\nkind: stack\nsummary: s\nquestions:\n  - {id: x, prompt: p}\n  - {id: x, prompt: p}\n", "duplicated"},
		{"question id", "name: a\nkind: stack\nsummary: s\nquestions:\n  - {id: Bad-Id, prompt: p}\n", "must match"},
		{"question prompt", "name: a\nkind: stack\nsummary: s\nquestions:\n  - {id: x}\n", "prompt is required"},
		{"default not in options", "name: a\nkind: stack\nsummary: s\nquestions:\n  - {id: x, prompt: p, options: [a, b], default: c}\n", "not one of options"},
		{"reserved ci job", "name: a\nkind: stack\nsummary: s\ncontributes:\n  ci:\n    jobs:\n      ci: {name: x}\n", "reserved"},
		{"service without name", "name: a\nkind: stack\nsummary: s\ncontributes:\n  atlas:\n    services:\n      - {command: x}\n", "name is required"},
		{"action key two chars", "name: a\nkind: stack\nsummary: s\ncontributes:\n  atlas:\n    actions:\n      - {key: ab, label: x, command: y}\n", "single character"},
		{"composed in a stack", "name: a\nkind: stack\nsummary: s\ncomposed:\n  - {path: x, kind: atlas}\n", "only a base"},
		{"composed unknown kind", "name: base\nkind: base\nsummary: s\ncomposed:\n  - {path: x, kind: nope}\n", "composed[0].kind"},
		{"files rule does nothing", "name: a\nkind: stack\nsummary: s\nfiles:\n  - {path: x}\n", "neither as nor when"},
		{"unknown key", "name: a\nkind: stack\nsummary: s\nsummry: typo\n", "summry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.yaml), "bp.yaml")
			if err == nil {
				t.Fatalf("want an error containing %q, got none", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
			if !strings.Contains(err.Error(), "bp.yaml") {
				t.Errorf("error %q does not name the source file", err)
			}
		})
	}
}

func TestParseReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Parse([]byte("name: BAD\nkind: nope\n"), "bp.yaml")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"name", "kind", "summary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

func TestLoadFSLaterSourceOverridesAndDirNameMustMatch(t *testing.T) {
	builtin := fstest.MapFS{
		"blueprints/a/blueprint.yaml": {Data: []byte("name: a\nkind: stack\nsummary: builtin\n")},
		"blueprints/a/files/x.txt":    {Data: []byte("1")},
	}
	user := fstest.MapFS{
		"a/blueprint.yaml": {Data: []byte("name: a\nkind: stack\nsummary: user override\n")},
	}
	r := NewRegistry()
	if err := r.LoadFS(builtin, "blueprints", "builtin"); err != nil {
		t.Fatal(err)
	}
	if err := r.LoadFS(user, ".", "user"); err != nil {
		t.Fatal(err)
	}
	b, _ := r.Get("a")
	if b.Summary != "user override" {
		t.Fatalf("later source did not override: %q", b.Summary)
	}

	bad := fstest.MapFS{"dir/blueprint.yaml": {Data: []byte("name: other\nkind: stack\nsummary: s\n")}}
	err := NewRegistry().LoadFS(bad, ".", "src")
	if err == nil || !strings.Contains(err.Error(), "must equal its directory") {
		t.Fatalf("want a directory-name mismatch error, got %v", err)
	}

	missing := fstest.MapFS{"dir/files/x": {Data: []byte("1")}}
	err = NewRegistry().LoadFS(missing, ".", "src")
	if err == nil || !strings.Contains(err.Error(), "blueprint.yaml") {
		t.Fatalf("a directory without blueprint.yaml must be an error, got %v", err)
	}
}

func TestFilesAndMode(t *testing.T) {
	b := &Blueprint{FS: fstest.MapFS{
		"files/b.txt":          {Data: []byte("b")},
		"files/a/run.sh.tmpl":  {Data: []byte("x")},
		"files/a/deep/c.go":    {Data: []byte("c")},
		"blueprint.yaml":       {Data: []byte("ignored")},
		"files/.github/ci.yml": {Data: []byte("y")},
	}}
	got, err := b.Files()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".github/ci.yml", "a/deep/c.go", "a/run.sh.tmpl", "b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Files() = %v, want %v", got, want)
	}
	if b.Mode("a/run.sh.tmpl") != 0o755 || b.Mode("b.txt") != 0o644 {
		t.Errorf("modes: sh=%v txt=%v", b.Mode("a/run.sh.tmpl"), b.Mode("b.txt"))
	}

	empty := &Blueprint{FS: fstest.MapFS{"blueprint.yaml": {Data: []byte("x")}}}
	if files, err := empty.Files(); err != nil || len(files) != 0 {
		t.Fatalf("a blueprint without files/ has no files, got %v, %v", files, err)
	}
}
