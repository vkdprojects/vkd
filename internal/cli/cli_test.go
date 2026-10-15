package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI in-process with the real embedded blueprints, from a fresh temp working directory.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &App{Out: &out, Err: &errb, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: t.TempDir()}
	code = a.Run(context.Background(), args)
	return code, out.String(), errb.String()
}

func inTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

type result struct {
	OK     bool `json:"ok"`
	Files  []struct{ Path, Action string }
	Errors []string
}

func decodeResult(t *testing.T, s string) result {
	t.Helper()
	var r result
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return r
}

func TestCreateYesWithoutOwnerFailsNamingTheFlag(t *testing.T) {
	dir := inTemp(t)
	code, out, errs := run(t, "create", "demo", "--stack", "go-api", "--yes")
	if code == 0 {
		t.Fatal("want a non-zero exit")
	}
	if !strings.Contains(errs, "--owner") {
		t.Errorf("stderr %q does not name --owner", errs)
	}
	if out != "" {
		t.Errorf("stdout must stay empty on failure in text mode, got %q", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a failed create left %d entries behind", len(entries))
	}

	code, out, _ = run(t, "create", "--owner", "acme", "--yes", "--json")
	r := decodeResult(t, out)
	if code == 0 || r.OK || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "NAME") {
		t.Fatalf("a missing name must be reported in JSON naming NAME: code=%d %+v", code, r)
	}
	if string(mustMarshal(t, r.Files)) == "null" {
		t.Error(`files must be [] and not null in the --json output`)
	}
}

func TestCreateMissingRequiredBlueprintAnswerNamesTheFlag(t *testing.T) {
	inTemp(t)
	bp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bp, "needy", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bp, "needy", "blueprint.yaml"), []byte(
		"name: needy\nkind: stack\nsummary: s\nrequires: [base]\nquestions:\n  - {id: region, prompt: Region, required: true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := run(t, "--blueprints", bp, "create", "demo", "--stack", "needy", "--owner", "acme", "--yes")
	if code == 0 || !strings.Contains(errs, "--needy.region") {
		t.Fatalf("want a failure naming --needy.region, got code=%d %q", code, errs)
	}
	// the dotted flag is generated from the blueprint's question and satisfies it. Nothing here touches os.Args:
	// the flags must come from the arguments given to Run.
	code, out, errs := run(t, "--blueprints", bp, "create", "demo", "--stack", "needy", "--owner", "acme", "--yes", "--needy.region", "br", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
}

func TestCreateGoAPIPreset(t *testing.T) {
	dir := inTemp(t)
	code, out, errs := run(t, "create", "demo", "--stack", "go-api", "--oss", "--owner", "acme", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code=%d err=%s out=%s", code, errs, out)
	}
	r := decodeResult(t, out)
	if !r.OK {
		t.Fatalf("not ok: %+v", r)
	}
	for _, p := range []string{"atlas.yaml", ".github/workflows/ci.yml", ".github/workflows/pr.yml", ".github/workflows/release.yml",
		"AGENTS.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md", "LICENSE", "llms.txt", "lefthook.yml",
		".agents/skills/demo/SKILL.md", "apps/api/go.mod", "apps/api/cmd/api/main.go", "scripts/check-dco.sh", ".vkd/answers.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "demo", filepath.FromSlash(p))); err != nil {
			t.Errorf("%s was not generated: %v", p, err)
		}
	}
	ci := readFile(t, filepath.Join(dir, "demo", ".github/workflows/ci.yml"))
	if !strings.Contains(ci, "name: CI") || !strings.Contains(ci, "- go-api") || !strings.Contains(ci, "- workflows") {
		t.Errorf("ci.yml: the aggregate CI job must need every job:\n%s", ci)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "demo", ".github/workflows/pr.yml")), "scripts/check-dco.sh") {
		t.Error("pr.yml must run the DCO check for an open source project")
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", ".git")); err != nil {
		t.Errorf("--git is on by default: %v", err)
	}
}

func TestCreateNoOSSOmitsGovernanceAndDCO(t *testing.T) {
	dir := inTemp(t)
	code, _, errs := run(t, "create", "demo", "--stack", "go-api", "--owner", "acme", "--yes")
	if code != 0 {
		t.Fatal(errs)
	}
	for _, p := range []string{"CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md", "scripts/check-dco.sh", ".github/CODEOWNERS"} {
		if _, err := os.Stat(filepath.Join(dir, "demo", filepath.FromSlash(p))); err == nil {
			t.Errorf("%s must only exist for --oss", p)
		}
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "demo", ".github/workflows/pr.yml")), "check-dco") {
		t.Error("a closed source project has no DCO job")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "demo", "LICENSE")), "All rights reserved") {
		t.Error("default license without --oss is proprietary")
	}
}

func TestLicenseChoice(t *testing.T) {
	for license, marker := range map[string]string{"MIT": "Permission is hereby granted", "Apache-2.0": "Apache License", "AGPL-3.0": "GNU AFFERO GENERAL PUBLIC LICENSE"} {
		t.Run(license, func(t *testing.T) {
			dir := inTemp(t)
			if code, _, errs := run(t, "create", "demo", "--oss", "--license", license, "--owner", "acme", "--yes"); code != 0 {
				t.Fatal(errs)
			}
			if got := readFile(t, filepath.Join(dir, "demo", "LICENSE")); !strings.Contains(got, marker) {
				t.Errorf("LICENSE for %s lacks %q", license, marker)
			}
		})
	}
	inTemp(t)
	if code, _, errs := run(t, "create", "demo", "--oss", "--license", "proprietary", "--owner", "acme", "--yes"); code == 0 || !strings.Contains(errs, "--oss") {
		t.Errorf("--oss with a proprietary license must fail naming --oss, got %d %q", code, errs)
	}
}

func TestCreateRefusesNonEmptyDirectory(t *testing.T) {
	dir := inTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run(t, "create", "demo", "--owner", "acme", "--yes"); code == 0 || !strings.Contains(errs, "keep.txt") {
		t.Fatalf("want a refusal naming keep.txt, got %d %q", code, errs)
	}
	if readFile(t, filepath.Join(dir, "demo", "keep.txt")) != "x" {
		t.Fatal("existing file was modified")
	}
}

func TestCreateDryRunWritesNothing(t *testing.T) {
	dir := inTemp(t)
	code, out, _ := run(t, "create", "demo", "--stack", "go-api", "--owner", "acme", "--yes", "--dry-run", "--json")
	if code != 0 || !decodeResult(t, out).OK {
		t.Fatalf("dry run failed: %s", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dry run wrote %d entries", len(entries))
	}
}

func TestBadFlagsAreRejected(t *testing.T) {
	inTemp(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown stack", []string{"create", "demo", "--stack", "nope", "--owner", "a", "--yes"}, `unknown blueprint "nope"`},
		{"oss and no-oss", []string{"create", "demo", "--oss", "--no-oss", "--owner", "a", "--yes"}, "mutually exclusive"},
		{"bad set", []string{"create", "demo", "--owner", "a", "--set", "novalue", "--yes"}, "blueprint.id=value"},
		{"set unknown question", []string{"create", "demo", "--stack", "go-api", "--owner", "a", "--set", "go-api.nope=1", "--yes"}, `no question "nope"`},
		{"bad project name", []string{"create", "My App", "--owner", "a", "--yes"}, "NAME"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, errs := run(t, c.args...)
			if code == 0 || !strings.Contains(errs, c.want) {
				t.Fatalf("got code=%d %q, want a failure containing %q", code, errs, c.want)
			}
		})
	}
}

func TestSetAnswersARealBlueprintQuestion(t *testing.T) {
	dir := inTemp(t)
	if code, _, errs := run(t, "create", "demo", "--stack", "go-api", "--owner", "acme", "--yes", "--set", "go-api.module=example.com/x/y"); code != 0 {
		t.Fatal(errs)
	}
	if got := readFile(t, filepath.Join(dir, "demo", "apps/api/go.mod")); !strings.HasPrefix(got, "module example.com/x/y\n") {
		t.Errorf("go.mod = %q", got)
	}
}

func TestListAndSchema(t *testing.T) {
	code, out, _ := run(t, "list", "--json")
	if code != 0 {
		t.Fatal(out)
	}
	var l struct {
		Blueprints []struct {
			Name, Kind string
			Questions  []struct{ ID string }
		}
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, b := range l.Blueprints {
		kinds[b.Name] = b.Kind
	}
	if kinds["base"] != "base" || kinds["go-api"] != "stack" || kinds["oss"] != "addon" || kinds["bun-workspace"] != "addon" {
		t.Fatalf("kinds = %v", kinds)
	}

	code, out, _ = run(t, "schema")
	if code != 0 {
		t.Fatal(out)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" || s["type"] != "object" {
		t.Fatalf("schema header wrong: %v", s)
	}
	req := s["required"].([]any)
	if len(req) != 2 || req[0] != "name" || req[1] != "owner" {
		t.Errorf("required = %v", req)
	}
	per := s["properties"].(map[string]any)["per"].(map[string]any)["properties"].(map[string]any)
	if _, ok := per["go-api"].(map[string]any)["properties"].(map[string]any)["module"]; !ok {
		t.Errorf("the go-api module question is missing from the schema: %v", per)
	}
}

func TestBlueprintNewThenLint(t *testing.T) {
	dir := inTemp(t)
	if code, _, errs := run(t, "blueprint", "new", "my-addon", "--dir", dir); code != 0 {
		t.Fatal(errs)
	}
	code, out, errs := run(t, "blueprint", "lint", filepath.Join(dir, "my-addon"))
	if code != 0 || !strings.Contains(out, "ok: my-addon") {
		t.Fatalf("a freshly scaffolded blueprint must lint clean: code=%d out=%q err=%q", code, out, errs)
	}
	if code, _, errs := run(t, "blueprint", "new", "my-addon", "--dir", dir); code == 0 || !strings.Contains(errs, "already exists") {
		t.Errorf("new must not overwrite: %d %q", code, errs)
	}

	// a template that reads an unknown field fails lint, naming the file.
	bad := filepath.Join(dir, "my-addon", "files", "bad.txt.tmpl")
	if err := os.WriteFile(bad, []byte("{{.NoSuchField}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs = run(t, "blueprint", "lint", filepath.Join(dir, "my-addon"))
	if code == 0 || !strings.Contains(errs, "NoSuchField") || !strings.Contains(errs, "bad.txt.tmpl") {
		t.Fatalf("lint must catch a broken template, got %d %q", code, errs)
	}

	// a YAML-level mistake fails lint too.
	if err := os.WriteFile(filepath.Join(dir, "my-addon", "blueprint.yaml"), []byte("name: my-addon\nkind: nope\nsummary: s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run(t, "blueprint", "lint", filepath.Join(dir, "my-addon")); code == 0 || !strings.Contains(errs, "kind") {
		t.Fatalf("got %d %q", code, errs)
	}
}

func TestUserBlueprintDirOverridesBuiltin(t *testing.T) {
	inTemp(t)
	cfg := t.TempDir()
	override := filepath.Join(cfg, "vkd", "blueprints", "go-api")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(override, "blueprint.yaml"), []byte("name: go-api\nkind: stack\nsummary: my own go-api\nrequires: [base]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	a := &App{Out: &out, Err: &bytes.Buffer{}, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: cfg}
	if code := a.Run(context.Background(), []string{"list"}); code != 0 || !strings.Contains(out.String(), "my own go-api") {
		t.Fatalf("$XDG_CONFIG_HOME/vkd/blueprints must override the built-in: %q", out.String())
	}
}

func TestVersion(t *testing.T) {
	if code, out, _ := run(t, "version"); code != 0 || !strings.HasPrefix(out, "vkd ") {
		t.Fatalf("got %d %q", code, out)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// docs/blueprints.md promises --blueprints works before or after the subcommand, and that the dotted flags of a
// blueprint in that directory are available either way.
func TestBlueprintsFlagBeforeOrAfterTheSubcommand(t *testing.T) {
	bp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bp, "needy", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bp, "needy", "blueprint.yaml"), []byte(
		"name: needy\nkind: stack\nsummary: s\nrequires: [base]\nquestions:\n  - {id: region, prompt: Region, required: true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"before": {"--blueprints", bp, "create", "demo", "--stack", "needy", "--owner", "a", "--needy.region", "br", "--yes", "--dry-run", "--json"},
		"after":  {"create", "demo", "--stack", "needy", "--owner", "a", "--needy.region", "br", "--blueprints", bp, "--yes", "--dry-run", "--json"},
		"equals": {"create", "demo", "--stack", "needy", "--owner", "a", "--needy.region=br", "--blueprints=" + bp, "--yes", "--dry-run", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			inTemp(t)
			if code, out, errs := run(t, args...); code != 0 {
				t.Fatalf("code=%d out=%s err=%s", code, out, errs)
			}
		})
	}
}
