//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// astro-site pulls in bun-workspace through its `when`, so the stack alone must yield a root that passes its own
// `bun run check`, a site that type-checks, builds and passes its own tests, and workflows that lint clean.
func TestAstroSitePreset(t *testing.T) {
	for _, oss := range []string{"--oss", "--no-oss"} {
		t.Run(oss, func(t *testing.T) {
			dir := create(t, "--stack", "astro-site", oss)

			t.Run("bun", func(t *testing.T) {
				need(t, "bun")
				sh(t, dir, "bun", "install")
				sh(t, dir, "bun", "run", "check")
				sh(t, dir, "bun", "run", "build")
				sh(t, dir, "bun", "run", "test")

				dist := filepath.Join(dir, "apps", "site", "dist")
				for _, f := range []string{"index.html", "404.html", "robots.txt", "sitemap-index.xml", "docs/index.html"} {
					if _, err := os.Stat(filepath.Join(dist, f)); err != nil {
						t.Errorf("dist/%s missing: %v", f, err)
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
