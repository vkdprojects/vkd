package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
)

func newEngine(t *testing.T, files map[string]string) *Engine {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	r := blueprint.NewRegistry()
	if err := r.LoadFS(fsys, ".", "test"); err != nil {
		t.Fatal(err)
	}
	return &Engine{Registry: r, Versions: map[string]any{"go": "1.25"}, Version: "test"}
}

var demoFiles = map[string]string{
	"base/blueprint.yaml":       "name: base\nkind: base\nsummary: s\ncomposed:\n  - {path: .gitignore, kind: gitignore}\ncontributes:\n  gitignore: [.env]\n",
	"base/files/README.md.tmpl": "# {{.Name}}\n\nline one\nline two\nline three\nline four\nline five\nline six\n",
	"base/files/run.sh":         "#!/bin/sh\n",
}

func demoAnswers() *answers.Answers {
	return &answers.Answers{Name: "demo", Owner: "acme", Description: "d"}
}

func TestCreateWritesFilesStateAndBaseRef(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := filepath.Join(t.TempDir(), "demo")
	res := e.Create(dir, demoAnswers(), CreateOpts{Git: true})
	if !res.OK {
		t.Fatalf("create failed: %v", res.Errors)
	}
	got := map[string]string{}
	for _, f := range res.Files {
		got[f.Path] = f.Action
	}
	for _, p := range []string{"README.md", "run.sh", ".gitignore", StatePath} {
		if got[p] != "create" {
			t.Errorf("%s not reported as created: %v", p, got)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("run.sh must be executable: %v %v", info, err)
	}
	st, err := ReadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Answers.Name != "demo" || st.VkdVersion != "test" || st.Blueprints["base"] == "" || st.Answers.PortBase == 0 || st.Answers.Year == 0 {
		t.Fatalf("state wrong: %+v", st)
	}
}

func TestCreateNeverOverwritesAndDryRunWritesNothing(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.Create(dir, demoAnswers(), CreateOpts{})
	if res.OK || !strings.Contains(res.Errors[0], "not empty") || !strings.Contains(res.Errors[0], "mine.txt") {
		t.Fatalf("want a refusal naming the existing file, got %+v", res)
	}

	fresh := filepath.Join(t.TempDir(), "p")
	res = e.Create(fresh, demoAnswers(), CreateOpts{DryRun: true, Git: true})
	if !res.OK || len(res.Files) == 0 {
		t.Fatalf("dry run should succeed and list files: %+v", res)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", fresh)
	}
}

func TestCreateFailureWritesNothing(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := filepath.Join(t.TempDir(), "p")
	a := demoAnswers()
	a.Components = []string{"nope"}
	if res := e.Create(dir, a, CreateOpts{}); res.OK {
		t.Fatal("unknown component must fail")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a failed create must not leave a directory behind")
	}
}

// Regression: `vkd update` run from inside the project (--dir defaults to ".") failed with "not the root of its
// git repository" because a relative path was compared with the absolute repository root.
func TestUpdateWithRelativeDirDot(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := filepath.Join(t.TempDir(), "demo")
	if res := e.Create(dir, demoAnswers(), CreateOpts{Git: true}); !res.OK {
		t.Fatal(res.Errors)
	}
	t.Chdir(dir)
	res := e.Update(".", nil, false)
	if !res.OK {
		t.Fatalf("update from inside the project failed: %v", res.Errors)
	}
}

func TestUpdateKeepsUserEditsAndAppliesTemplateChange(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := filepath.Join(t.TempDir(), "demo")
	if res := e.Create(dir, demoAnswers(), CreateOpts{Git: true}); !res.OK {
		t.Fatal(res.Errors)
	}
	readme := filepath.Join(dir, "README.md")
	body, _ := os.ReadFile(readme)
	if err := os.WriteFile(readme, append(body, []byte("my note\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	// no template change: the user's edit survives and nothing is reported as changed.
	res := e.Update(dir, nil, false)
	if !res.OK {
		t.Fatal(res.Errors)
	}
	for _, f := range res.Files {
		if f.Action != "skip" {
			t.Errorf("no-op update reported %s %s", f.Action, f.Path)
		}
	}

	// the template changes far from the user's edit: clean merge keeps both.
	e2 := newEngine(t, withFile(demoFiles, "base/files/README.md.tmpl",
		"# {{.Name}}\n\nline ONE\nline two\nline three\nline four\nline five\nline six\n"))
	res = e2.Update(dir, nil, false)
	if !res.OK {
		t.Fatal(res.Errors)
	}
	final, _ := os.ReadFile(readme)
	if !strings.Contains(string(final), "line ONE") || !strings.HasSuffix(string(final), "my note\n") {
		t.Fatalf("merge lost something:\n%s", final)
	}
}

func TestUpdateConflictIsNotOK(t *testing.T) {
	e := newEngine(t, demoFiles)
	dir := filepath.Join(t.TempDir(), "demo")
	if res := e.Create(dir, demoAnswers(), CreateOpts{Git: true}); !res.OK {
		t.Fatal(res.Errors)
	}
	readme := filepath.Join(dir, "README.md")
	body, _ := os.ReadFile(readme)
	if err := os.WriteFile(readme, []byte(strings.Replace(string(body), "line one", "line MINE", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	e2 := newEngine(t, withFile(demoFiles, "base/files/README.md.tmpl",
		"# {{.Name}}\n\nline THEIRS\nline two\nline three\nline four\nline five\nline six\n"))
	res := e2.Update(dir, nil, false)
	if res.OK || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "README.md") {
		t.Fatalf("a conflict must make the result not ok and name the file: %+v", res)
	}
}

func TestUpdateOutsideAVkdProject(t *testing.T) {
	e := newEngine(t, demoFiles)
	res := e.Update(t.TempDir(), nil, false)
	if res.OK || !strings.Contains(res.Errors[0], "not a vkd project") {
		t.Fatalf("got %+v", res)
	}
}

func TestUpdateAddComponent(t *testing.T) {
	files := withFile(demoFiles, "extra/blueprint.yaml", "name: extra\nkind: stack\nsummary: s\nrequires: [base]\n")
	files["extra/files/extra.txt"] = "hi\n"
	e := newEngine(t, files)
	dir := filepath.Join(t.TempDir(), "demo")
	if res := e.Create(dir, demoAnswers(), CreateOpts{Git: true}); !res.OK {
		t.Fatal(res.Errors)
	}
	res := e.Update(dir, []string{"extra"}, false)
	if !res.OK {
		t.Fatal(res.Errors)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "extra.txt")); err != nil || string(b) != "hi\n" {
		t.Fatalf("added component's file missing: %q %v", b, err)
	}
}

func withFile(base map[string]string, path, content string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	out[path] = content
	return out
}

// Regression: `vkd add` must record the component in .vkd/answers.yaml. Otherwise the next plain `vkd update`
// re-renders without it and deletes the files `add` just wrote.
func TestAddIsRememberedByTheNextUpdate(t *testing.T) {
	files := withFile(demoFiles, "extra/blueprint.yaml", "name: extra\nkind: stack\nsummary: s\nrequires: [base]\n")
	files["extra/files/extra.txt"] = "hi\n"
	e := newEngine(t, files)
	dir := filepath.Join(t.TempDir(), "demo")
	if res := e.Create(dir, demoAnswers(), CreateOpts{Git: true}); !res.OK {
		t.Fatal(res.Errors)
	}
	if res := e.Update(dir, []string{"extra"}, false); !res.OK {
		t.Fatal(res.Errors)
	}
	st, err := ReadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.Answers.Components, ",") != "extra" {
		t.Fatalf("components after add = %v, want [extra]", st.Answers.Components)
	}
	res := e.Update(dir, nil, false)
	if !res.OK {
		t.Fatal(res.Errors)
	}
	for _, f := range res.Files {
		if f.Path == "extra.txt" && f.Action != "skip" {
			t.Fatalf("a plain update after add changed extra.txt: %s", f.Action)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.txt")); err != nil {
		t.Fatalf("extra.txt vanished after the next update: %v", err)
	}
}
