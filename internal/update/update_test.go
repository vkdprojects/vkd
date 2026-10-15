package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vkdprojects/vkd/internal/gitx"
	"github.com/vkdprojects/vkd/internal/plan"
)

func v(s string) Version { return Version{Data: []byte(s), Exists: true} }

var none = Version{}

func TestDecide(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs Version
		want               Action
	}{
		{"new in template", none, none, v("T"), Create},
		{"template unchanged, user unchanged", v("B"), v("B"), v("B"), Skip},
		{"template changed, user untouched: overwrite", v("B"), v("B"), v("T"), Update},
		{"user changed, template unchanged: keep", v("B"), v("O"), v("B"), Skip},
		{"both changed to the same thing", v("B"), v("X"), v("X"), Skip},
		{"both changed differently: merge", v("B"), v("O"), v("T"), Merge},
		{"template dropped it, user untouched: delete", v("B"), v("B"), none, Delete},
		{"template dropped it, user edited it: keep", v("B"), v("O"), none, Skip},
		{"user deleted it: stays deleted", v("B"), none, v("T"), Skip},
		{"user deleted it and template dropped it", v("B"), none, none, Skip},
		{"both added the same file with the same content", none, v("X"), v("X"), Skip},
		{"both added the same file differently", none, v("O"), v("T"), Merge},
		{"nowhere", none, none, none, Skip},
		{"an empty file is still a file", v(""), v(""), v("T"), Update},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.base, c.ours, c.theirs); got != c.want {
				t.Fatalf("Decide = %s, want %s", got, c.want)
			}
		})
	}
}

// project is a real git repository with a recorded refs/vkd/base, like `vkd create` leaves behind.
type project struct {
	t    *testing.T
	dir  string
	repo *gitx.Repo
}

func newProject(t *testing.T, files map[string]string) *project {
	t.Helper()
	dir := t.TempDir()
	repo, err := gitx.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &project{t: t, dir: dir, repo: repo}
	pl := toPlan(files)
	for _, f := range pl.Files {
		abs := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, f.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.WriteBase(Blobs(pl), "create"); err != nil {
		t.Fatal(err)
	}
	return p
}

func toPlan(files map[string]string) *plan.Plan {
	pl := &plan.Plan{}
	for path, c := range files {
		pl.Files = append(pl.Files, plan.File{Path: path, Content: []byte(c), Mode: 0o644})
	}
	return pl
}

func (p *project) read(path string) string {
	p.t.Helper()
	b, err := os.ReadFile(filepath.Join(p.dir, path))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(b)
}

func (p *project) write(path, content string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.dir, path), []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *project) apply(files map[string]string, dry bool) map[string]Action {
	p.t.Helper()
	res, err := Apply(p.repo, p.dir, toPlan(files), dry)
	if err != nil {
		p.t.Fatal(err)
	}
	out := map[string]Action{}
	for _, r := range res {
		out[r.Path] = r.Action
	}
	return out
}

const readmeV1 = "# demo\n\nline one\nline two\nline three\nline four\nline five\nline six\n"

func TestApplyCases(t *testing.T) {
	t.Run("user-only change is kept", func(t *testing.T) {
		p := newProject(t, map[string]string{"README.md": readmeV1, "a.txt": "A\n"})
		p.write("README.md", readmeV1+"my note\n")
		got := p.apply(map[string]string{"README.md": readmeV1, "a.txt": "A\n"}, false)
		if got["README.md"] != Skip || p.read("README.md") != readmeV1+"my note\n" {
			t.Fatalf("user edit lost or mislabeled: %v %q", got, p.read("README.md"))
		}
	})

	t.Run("template-only change overwrites an untouched file", func(t *testing.T) {
		p := newProject(t, map[string]string{"a.txt": "A\n"})
		got := p.apply(map[string]string{"a.txt": "A2\n"}, false)
		if got["a.txt"] != Update || p.read("a.txt") != "A2\n" {
			t.Fatalf("got %v %q", got, p.read("a.txt"))
		}
	})

	t.Run("both changed in different places: clean merge", func(t *testing.T) {
		p := newProject(t, map[string]string{"README.md": readmeV1})
		p.write("README.md", readmeV1+"my note\n")
		tmpl := strings.Replace(readmeV1, "line one", "line ONE", 1)
		got := p.apply(map[string]string{"README.md": tmpl}, false)
		want := strings.Replace(readmeV1, "line one", "line ONE", 1) + "my note\n"
		if got["README.md"] != Merge || p.read("README.md") != want {
			t.Fatalf("got %v\n%q\nwant\n%q", got, p.read("README.md"), want)
		}
	})

	t.Run("both changed the same line: conflict with markers", func(t *testing.T) {
		p := newProject(t, map[string]string{"README.md": readmeV1})
		p.write("README.md", strings.Replace(readmeV1, "line one", "line MINE", 1))
		got := p.apply(map[string]string{"README.md": strings.Replace(readmeV1, "line one", "line THEIRS", 1)}, false)
		out := p.read("README.md")
		if got["README.md"] != Conflict {
			t.Fatalf("got %v", got)
		}
		for _, w := range []string{"<<<<<<< README.md (yours)", "line MINE", "||||||| README.md (vkd previous)", "line one", "line THEIRS", ">>>>>>> README.md (vkd new)"} {
			if !strings.Contains(out, w) {
				t.Errorf("conflict output is missing %q:\n%s", w, out)
			}
		}
	})

	t.Run("a file the template dropped is deleted when untouched, kept when edited", func(t *testing.T) {
		p := newProject(t, map[string]string{"keep.txt": "K\n", "gone.txt": "G\n", "edited.txt": "E\n"})
		p.write("edited.txt", "E mine\n")
		got := p.apply(map[string]string{"keep.txt": "K\n"}, false)
		if got["gone.txt"] != Delete || got["edited.txt"] != Skip {
			t.Fatalf("got %v", got)
		}
		if _, err := os.Stat(filepath.Join(p.dir, "gone.txt")); !os.IsNotExist(err) {
			t.Error("gone.txt should have been deleted")
		}
		if p.read("edited.txt") != "E mine\n" {
			t.Error("an edited file must survive the template dropping it")
		}
	})

	t.Run("a file the user deleted stays deleted", func(t *testing.T) {
		p := newProject(t, map[string]string{"a.txt": "A\n"})
		if err := os.Remove(filepath.Join(p.dir, "a.txt")); err != nil {
			t.Fatal(err)
		}
		got := p.apply(map[string]string{"a.txt": "A2\n"}, false)
		if _, err := os.Stat(filepath.Join(p.dir, "a.txt")); got["a.txt"] != Skip || !os.IsNotExist(err) {
			t.Fatalf("got %v, err %v", got, err)
		}
	})

	t.Run("new template file in a new directory is created", func(t *testing.T) {
		p := newProject(t, map[string]string{"a.txt": "A\n"})
		got := p.apply(map[string]string{"a.txt": "A\n", "deep/dir/new.txt": "N\n"}, false)
		if got["deep/dir/new.txt"] != Create || p.read("deep/dir/new.txt") != "N\n" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dry run reports and writes nothing", func(t *testing.T) {
		p := newProject(t, map[string]string{"a.txt": "A\n", "gone.txt": "G\n"})
		got := p.apply(map[string]string{"a.txt": "A2\n", "new.txt": "N\n"}, true)
		if got["a.txt"] != Update || got["new.txt"] != Create || got["gone.txt"] != Delete {
			t.Fatalf("got %v", got)
		}
		if p.read("a.txt") != "A\n" || p.read("gone.txt") != "G\n" {
			t.Error("dry run changed the worktree")
		}
		if _, err := os.Stat(filepath.Join(p.dir, "new.txt")); !os.IsNotExist(err) {
			t.Error("dry run created a file")
		}
	})

	t.Run("an unresolved conflict keeps failing on the next update", func(t *testing.T) {
		p := newProject(t, map[string]string{"README.md": readmeV1})
		p.write("README.md", strings.Replace(readmeV1, "line one", "line MINE", 1))
		theirs := map[string]string{"README.md": strings.Replace(readmeV1, "line one", "line THEIRS", 1)}
		if got := p.apply(theirs, false); got["README.md"] != Conflict {
			t.Fatalf("first: %v", got)
		}
		if err := p.repo.WriteBase(Blobs(toPlan(theirs)), "update"); err != nil {
			t.Fatal(err)
		}
		before := p.read("README.md")
		if got := p.apply(theirs, false); got["README.md"] != Conflict {
			t.Fatalf("an unresolved conflict must stay a conflict, got %v", got)
		}
		if p.read("README.md") != before {
			t.Error("the second update must not touch the file again")
		}
		p.write("README.md", "resolved\n")
		if got := p.apply(theirs, false); got["README.md"] != Skip {
			t.Fatalf("after resolving it must be clean, got %v", got)
		}
	})

	t.Run("executable mode of a created file follows the plan", func(t *testing.T) {
		p := newProject(t, map[string]string{"a.txt": "A\n"})
		pl := toPlan(map[string]string{"a.txt": "A\n"})
		pl.Files = append(pl.Files, plan.File{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Mode: 0o755})
		if _, err := Apply(p.repo, p.dir, pl, false); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(p.dir, "run.sh"))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("run.sh must be executable: %v %v", info, err)
		}
	})
}

func TestApplyWithoutBaseRefExplains(t *testing.T) {
	dir := t.TempDir()
	repo, err := gitx.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(repo, dir, toPlan(nil), false)
	if err == nil || !strings.Contains(err.Error(), gitx.BaseRef) || !strings.Contains(err.Error(), "vkd create") {
		t.Fatalf("want an error naming the ref and the fix, got %v", err)
	}
}

func TestBaseRefChainsCommitsAndLeavesWorktreeAndIndexAlone(t *testing.T) {
	p := newProject(t, map[string]string{"a.txt": "A\n"})
	if err := p.repo.WriteBase(Blobs(toPlan(map[string]string{"a.txt": "A2\n"})), "update"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", p.dir, "rev-list", "--count", gitx.BaseRef).Output()
	if err != nil || strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("want 2 chained commits on %s, got %q, %v", gitx.BaseRef, out, err)
	}
	if got, _, _ := p.repo.ReadBase("a.txt"); string(got) != "A2\n" {
		t.Errorf("tip content = %q", got)
	}
	// the user's index must not have been touched: nothing is staged.
	if st, _ := exec.Command("git", "-C", p.dir, "diff", "--cached", "--name-only").Output(); len(st) != 0 {
		t.Errorf("WriteBase staged files in the user's index: %s", st)
	}
	if p.read("a.txt") != "A\n" {
		t.Error("WriteBase changed the worktree")
	}
}
