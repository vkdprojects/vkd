//go:build e2e

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

func TestGoCLIPreset(t *testing.T) {
	dir := create(t, "--stack", "go-cli", "--oss", "--set", "go-cli.module=example.com/acme/demo")
	cliDir := filepath.Join(dir, "apps", "cli")

	t.Run("layout", func(t *testing.T) {
		for _, p := range []string{
			"apps/cli/go.mod", "apps/cli/cmd/demo/main.go", "apps/cli/internal/app/app.go", "apps/cli/internal/app/app_test.go",
		} {
			if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
				t.Errorf("missing %s: %v", p, err)
			}
		}
		if _, err := os.Stat(filepath.Join(cliDir, "go.sum")); err == nil {
			t.Errorf("go.sum is a lockfile; vkd must not generate it")
		}
	})

	t.Run("go", func(t *testing.T) {
		need(t, "go")
		// go.sum is the user's (`go mod tidy`, committed); the generated CI, atlas action and lefthook run it first.
		sh(t, cliDir, "go", "mod", "tidy")
		if out, _ := exec.Command("gofmt", "-l", cliDir).Output(); len(out) != 0 {
			t.Fatalf("generated Go is not gofmt-clean:\n%s", out)
		}
		sh(t, cliDir, "go", "vet", "./...")
		sh(t, cliDir, "go", "test", "-race", "./...")

		// The release build: version through ldflags into the module the user chose.
		bin := filepath.Join(t.TempDir(), "demo")
		sh(t, cliDir, "go", "build", "-trimpath", "-ldflags", "-s -w -X example.com/acme/demo/internal/app.Version=1.2.3", "-o", bin, "./cmd/demo")
		out, err := exec.Command(bin, "--version").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "1.2.3") {
			t.Fatalf("--version = %q, %v; want the ldflags version", out, err)
		}
		out, err = exec.Command(bin, "hello", "--name", "atlas").CombinedOutput()
		if err != nil || string(out) != "hello, atlas\n" {
			t.Fatalf("hello = %q, %v", out, err)
		}
	})

	t.Run("release matrix", func(t *testing.T) {
		rel, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "release.yml"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(rel)
		// vkd writes mapping keys sorted, so a matrix entry is `- goarch: X` then `goos: Y`.
		for _, p := range [][2]string{{"amd64", "linux"}, {"arm64", "linux"}, {"amd64", "darwin"}, {"arm64", "darwin"}} {
			if !strings.Contains(text, "- goarch: "+p[0]+"\n            goos: "+p[1]+"\n") {
				t.Errorf("release.yml lacks the %s/%s matrix entry", p[1], p[0])
			}
		}
		for _, want := range []string{"cache: false", "internal/app.Version=$VERSION", "needs: gate", "- go-cli-binaries", "SHA256SUMS", "CGO_ENABLED: \"0\""} {
			if !strings.Contains(text, want) {
				t.Errorf("release.yml lacks %q", want)
			}
		}
	})

	t.Run("update is a clean no-op", func(t *testing.T) {
		var out, errb bytes.Buffer
		app := &cli.App{Out: &out, Err: &errb, In: strings.NewReader(""), IsTTY: func() bool { return false }, ConfigHome: t.TempDir()}
		if code := app.Run(context.Background(), []string{"update", "--dir", dir, "--json"}); code != 0 {
			t.Fatalf("update exited %d: %s %s", code, out.String(), errb.String())
		}
		if strings.Contains(out.String(), `"action": "update"`) || strings.Contains(out.String(), `"action": "conflict"`) {
			t.Fatalf("an update right after create must change nothing (even after go mod tidy wrote go.sum):\n%s", out.String())
		}
	})

	t.Run("actionlint", func(t *testing.T) {
		need(t, "actionlint")
		sh(t, dir, "actionlint")
	})

	t.Run("zizmor", func(t *testing.T) {
		need(t, "zizmor")
		sh(t, dir, "zizmor", "--offline", "--quiet", ".github")
	})

	t.Run("lint-workflows script", func(t *testing.T) {
		need(t, "actionlint")
		need(t, "zizmor")
		sh(t, dir, "scripts/lint-workflows.sh")
	})

	t.Run("atlas config", func(t *testing.T) {
		need(t, "atlas")
		sh(t, dir, "atlas", "config", "check")
	})
}
