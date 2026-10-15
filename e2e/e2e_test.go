//go:build e2e

// Package e2e creates each preset into a temp directory with the real vkd CLI and runs the stack's own checks on
// the result. A check is skipped, never faked, when its tool is not installed.
//
//	go test -tags e2e ./e2e
package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vkdprojects/vkd/internal/cli"
)

func create(t *testing.T, args ...string) string {
	t.Helper()
	work := t.TempDir()
	t.Chdir(work)
	var out, errb bytes.Buffer
	app := &cli.App{Out: &out, Err: &errb, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: t.TempDir()}
	full := append([]string{"create", "demo", "--owner", "acme", "--yes", "--json"}, args...)
	if code := app.Run(context.Background(), full); code != 0 {
		t.Fatalf("vkd %v exited %d\nstdout: %s\nstderr: %s", full, code, out.String(), errb.String())
	}
	return filepath.Join(work, "demo")
}

func need(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s is not installed", tool)
	}
}

func sh(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s (in %s): %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
}

func TestGoAPIPreset(t *testing.T) {
	for _, oss := range []string{"--oss", "--no-oss"} {
		t.Run(oss, func(t *testing.T) {
			dir := create(t, "--stack", "go-api", oss)

			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				t.Fatalf("create must leave a git repository: %v", err)
			}
			if out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "refs/vkd/base").CombinedOutput(); err != nil {
				t.Fatalf("refs/vkd/base missing: %v %s", err, out)
			}

			t.Run("go", func(t *testing.T) {
				need(t, "go")
				api := filepath.Join(dir, "apps", "api")
				if out, _ := exec.Command("gofmt", "-l", api).Output(); len(out) != 0 {
					t.Fatalf("generated Go is not gofmt-clean:\n%s", out)
				}
				sh(t, api, "go", "vet", "./...")
				sh(t, api, "go", "test", "./...")
			})

			t.Run("actionlint", func(t *testing.T) {
				need(t, "actionlint")
				sh(t, dir, "actionlint")
			})

			t.Run("zizmor", func(t *testing.T) {
				need(t, "zizmor")
				sh(t, dir, "zizmor", "--offline", "--quiet", ".github")
			})

			t.Run("atlas config", func(t *testing.T) {
				need(t, "atlas")
				sh(t, dir, "atlas", "config", "check")
			})

			t.Run("update is a clean no-op", func(t *testing.T) {
				var out, errb bytes.Buffer
				app := &cli.App{Out: &out, Err: &errb, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: t.TempDir()}
				if code := app.Run(context.Background(), []string{"update", "--dir", dir, "--json"}); code != 0 {
					t.Fatalf("update exited %d: %s %s", code, out.String(), errb.String())
				}
				if strings.Contains(out.String(), `"action": "update"`) || strings.Contains(out.String(), `"action": "conflict"`) {
					t.Fatalf("an update right after create must change nothing:\n%s", out.String())
				}
			})

			t.Run("scripts", func(t *testing.T) {
				need(t, "shellcheck")
				for _, s := range []string{"check-commit-msg.sh", "check-pr-title.sh", "ci-passed.sh", "lint-workflows.sh"} {
					sh(t, dir, "shellcheck", filepath.Join("scripts", s))
				}
			})
		})
	}
}

// bun-workspace is exercised through a test-only stack (testdata/blueprints/probe-web) so the root package.json,
// biome.json and turbo.json it generates are proven to pass `bun run check` with several workspaces.
func TestBunWorkspaceRootPassesItsOwnCheck(t *testing.T) {
	need(t, "bun")
	abs, err := filepath.Abs(filepath.Join("testdata", "blueprints"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Chdir(work)
	var out, errb bytes.Buffer
	app := &cli.App{Out: &out, Err: &errb, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: t.TempDir()}
	args := []string{"--blueprints", abs, "create", "demo", "--stack", "probe-web", "--owner", "acme", "--yes", "--json"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("create exited %d\n%s\n%s", code, out.String(), errb.String())
	}
	dir := filepath.Join(work, "demo")
	sh(t, dir, "bun", "install")
	sh(t, dir, "bun", "run", "check")

	t.Run("actionlint", func(t *testing.T) {
		need(t, "actionlint")
		sh(t, dir, "actionlint")
	})
}
