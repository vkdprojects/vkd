//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func svkReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func svkExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sveltekit-web pulls in bun-workspace through its `when`. Alone it is a static site (adapter-static, prerendered);
// with go-api it is a Node server that proxies /api and ships as a Docker image. Both must type-check, pass their
// own tests, build, and produce workflows that lint clean.
func TestSvelteKitWebPreset(t *testing.T) {
	variants := []struct {
		name   string
		stack  string
		server bool
	}{
		{"static", "sveltekit-web", false},
		{"with-go-api", "sveltekit-web,go-api", true},
	}
	for _, v := range variants {
		for _, oss := range []string{"--oss", "--no-oss"} {
			t.Run(v.name+"/"+oss, func(t *testing.T) {
				dir := create(t, "--stack", v.stack, oss)
				web := filepath.Join(dir, "apps", "web")

				t.Run("variant", func(t *testing.T) {
					vite := svkReadFile(t, filepath.Join(web, "vite.config.ts"))
					pkg := svkReadFile(t, filepath.Join(web, "package.json"))
					atlas := svkReadFile(t, filepath.Join(dir, "atlas.yaml"))
					release := svkReadFile(t, filepath.Join(dir, ".github", "workflows", "release.yml"))
					if v.server {
						if !strings.Contains(pkg, "adapter-node") || strings.Contains(pkg, "adapter-static") {
							t.Errorf("with go-api the app must use adapter-node only:\n%s", pkg)
						}
						if !strings.Contains(vite, `"/api": {`) {
							t.Errorf("vite.config.ts must proxy /api to go-api:\n%s", vite)
						}
						if !svkExists(filepath.Join(web, "Dockerfile")) || !svkExists(filepath.Join(web, "src", "routes", "+page.server.ts")) {
							t.Error("the server variant needs apps/web/Dockerfile and +page.server.ts")
						}
						if svkExists(filepath.Join(web, "src", "routes", "+layout.ts")) {
							t.Error("the server variant must not prerender (+layout.ts)")
						}
						if !strings.Contains(atlas, "depends_on:\n      - api") {
							t.Errorf("the web service must depend on api:\n%s", atlas)
						}
						if !strings.Contains(release, "docker build -f apps/web/Dockerfile") {
							t.Errorf("release must build the web image:\n%s", release)
						}
					} else {
						if !strings.Contains(pkg, "adapter-static") || strings.Contains(pkg, "adapter-node") {
							t.Errorf("alone the app must use adapter-static only:\n%s", pkg)
						}
						if strings.Contains(vite, "proxy") {
							t.Errorf("a static site has nothing to proxy:\n%s", vite)
						}
						if svkExists(filepath.Join(web, "Dockerfile")) || svkExists(filepath.Join(web, "src", "routes", "+page.server.ts")) {
							t.Error("the static variant must not have a Dockerfile or +page.server.ts")
						}
						if !svkExists(filepath.Join(web, "src", "routes", "+layout.ts")) {
							t.Error("the static variant must prerender (+layout.ts)")
						}
						if strings.Contains(atlas, "depends_on") {
							t.Errorf("the web service has nothing to depend on:\n%s", atlas)
						}
						if strings.Contains(release, "docker build") {
							t.Errorf("a static site releases a tarball, not an image:\n%s", release)
						}
					}
				})

				t.Run("bun", func(t *testing.T) {
					need(t, "bun")
					sh(t, dir, "bun", "install")
					sh(t, dir, "bun", "run", "check")
					sh(t, dir, "bun", "run", "test")
					sh(t, dir, "bun", "run", "build")

					if v.server {
						if !svkExists(filepath.Join(web, "build", "index.js")) {
							t.Error("apps/web/build/index.js (adapter-node entry) missing")
						}
					} else {
						for _, f := range []string{"index.html", "200.html"} {
							if !svkExists(filepath.Join(web, "build", f)) {
								t.Errorf("apps/web/build/%s missing", f)
							}
						}
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
			})
		}
	}
}
