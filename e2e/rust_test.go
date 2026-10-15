//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// shEnv runs a command with extra environment, failing the test with its output.
func shEnv(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s (in %s): %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
}

// rustChecks runs the stack's own tools on a generated project. The target dir is shared across subtests of one
// project so the dependency graph compiles once.
func rustChecks(t *testing.T, dir string, workspaceCrates ...string) {
	t.Helper()
	// A project is a git repository; cargo-deny and the lockfile need the generated tree only.
	for _, c := range workspaceCrates {
		if _, err := os.Stat(filepath.Join(dir, "crates", c, "Cargo.toml")); err != nil {
			t.Fatalf("crate %s missing: %v", c, err)
		}
	}
	t.Run("cargo", func(t *testing.T) {
		need(t, "cargo")
		sh(t, dir, "cargo", "fmt", "--all", "--check")
		sh(t, dir, "cargo", "generate-lockfile")
		sh(t, dir, "cargo", "clippy", "--workspace", "--all-targets", "--locked", "--", "-D", "warnings")
		sh(t, dir, "cargo", "test", "--workspace", "--locked")
	})
	t.Run("nextest", func(t *testing.T) {
		need(t, "cargo-nextest")
		sh(t, dir, "cargo", "nextest", "run", "--workspace", "--locked")
	})
	t.Run("deny", func(t *testing.T) {
		need(t, "cargo-deny")
		sh(t, dir, "cargo", "deny", "check")
	})
	t.Run("actionlint", func(t *testing.T) { need(t, "actionlint"); sh(t, dir, "actionlint") })
	t.Run("zizmor", func(t *testing.T) { need(t, "zizmor"); sh(t, dir, "zizmor", "--offline", "--quiet", ".github") })
	t.Run("atlas config", func(t *testing.T) { need(t, "atlas"); sh(t, dir, "atlas", "config", "check") })
}

func TestRustCLIPreset(t *testing.T) {
	dir := create(t, "--stack", "rust-cli", "--oss")
	rustChecks(t, dir, "demo")
	t.Run("binary", func(t *testing.T) {
		need(t, "cargo")
		cmd := exec.Command("cargo", "run", "-q", "--locked", "-p", "demo", "--", "greet", "--name", "Ada")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "Hello, Ada!\n" {
			t.Fatalf("greet: %v %q", err, out)
		}
	})
}

func TestRustServicePreset(t *testing.T) {
	dir := create(t, "--stack", "rust-service", "--oss")
	rustChecks(t, dir, "demo-api")
	t.Run("dockerfile", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, "crates", "demo-api", "Dockerfile")); err != nil {
			t.Fatal(err)
		}
	})
	// The real binary: bind a free port, answer /healthz, stop cleanly on SIGTERM.
	t.Run("serves and shuts down", func(t *testing.T) {
		need(t, "cargo")
		need(t, "curl")
		sh(t, dir, "cargo", "build", "-q", "--locked", "-p", "demo-api")
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
		_ = l.Close()
		cmd := exec.Command(filepath.Join(dir, "target", "debug", "demo-api"))
		cmd.Env = append(os.Environ(), "PORT="+port)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		defer func() { _ = cmd.Process.Kill() }()
		var body []byte
		for i := 0; i < 50; i++ {
			body, err = exec.Command("curl", "-fsS", "http://127.0.0.1:"+port+"/healthz").Output()
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
			t.Fatalf("healthz: %v %q", err, body)
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("service must exit 0 on SIGTERM: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("service did not stop on SIGTERM")
		}
	})
}

// Both stacks together share one root: the three root files must exist once and both crates must build.
func TestRustCLIAndServiceTogether(t *testing.T) {
	dir := create(t, "--stack", "rust-cli,rust-service", "--no-oss")
	rustChecks(t, dir, "demo", "demo-api")
	for _, f := range []string{"Cargo.toml", "rust-toolchain.toml", "deny.toml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
