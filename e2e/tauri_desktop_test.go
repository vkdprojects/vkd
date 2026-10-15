//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// tauri-desktop requires rust-workspace and bun-workspace, so the stack alone must yield a Cargo workspace whose
// crate builds without a frontend, a SvelteKit app that checks, tests and builds, and workflows that lint clean.
func TestTauriDesktopPreset(t *testing.T) {
	for _, oss := range []string{"--oss", "--no-oss"} {
		t.Run(oss, func(t *testing.T) {
			dir := create(t, "--stack", "tauri-desktop", oss)

			for _, f := range []string{
				"Cargo.toml", "rust-toolchain.toml", "crates/desktop/Cargo.toml", "crates/desktop/tauri.conf.json",
				"crates/desktop/capabilities/default.json", "apps/desktop/package.json", "package.json",
			} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Fatalf("%s missing: %v", f, err)
				}
			}

			// Run before the frontend is built: without tauri's `custom-protocol` feature the crate is in dev mode and
			// embeds no assets, so a fresh clone (and the rust CI job) can `cargo clippy --workspace` without bun.
			t.Run("cargo without a frontend build", func(t *testing.T) {
				need(t, "cargo")
				if _, err := os.Stat(filepath.Join(dir, "apps", "desktop", "build")); err == nil {
					t.Fatal("apps/desktop/build must not exist before the first cargo run: cargo alone must not need it")
				}
				sh(t, dir, "cargo", "fmt", "--all", "--check")
				sh(t, dir, "cargo", "generate-lockfile")
				sh(t, dir, "cargo", "clippy", "--workspace", "--all-targets", "--locked", "--", "-D", "warnings")
				sh(t, dir, "cargo", "test", "--workspace", "--locked")
			})

			t.Run("bun", func(t *testing.T) {
				need(t, "bun")
				sh(t, dir, "bun", "install")
				sh(t, dir, "bun", "run", "check")
				sh(t, dir, "bun", "run", "test")
				sh(t, dir, "bun", "run", "build")
				if _, err := os.Stat(filepath.Join(dir, "apps", "desktop", "build", "index.html")); err != nil {
					t.Errorf("the static build must write apps/desktop/build/index.html: %v", err)
				}
			})

			// The real Tauri CLI (the bun-pinned @tauri-apps/cli) validates tauri.conf.json, the capabilities and
			// the icons and links the binary. Only the installer bundle is skipped (slow, needs platform tools).
			t.Run("tauri build without bundling", func(t *testing.T) {
				need(t, "bun")
				need(t, "cargo")
				sh(t, dir, "bun", "run", "--cwd", "apps/desktop", "tauri", "build", "--debug", "--no-bundle", "--ci")
			})

			t.Run("deny", func(t *testing.T) {
				need(t, "cargo-deny")
				sh(t, dir, "cargo", "deny", "check")
			})

			t.Run("actionlint", func(t *testing.T) { need(t, "actionlint"); sh(t, dir, "actionlint") })
			t.Run("zizmor", func(t *testing.T) {
				need(t, "zizmor")
				sh(t, dir, "zizmor", "--offline", "--quiet", ".github")
			})
			t.Run("atlas config", func(t *testing.T) { need(t, "atlas"); sh(t, dir, "atlas", "config", "check") })
		})
	}
}
