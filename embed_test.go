package vkd

import (
	"io/fs"
	"strings"
	"testing"
)

// A .go file inside blueprints/ would be compiled, vetted and linted as part of vkd itself, tying vkd's build to
// the Go version and imports of a generated project. Template Go source is named *.go.tmpl.
func TestNoGoSourceInsideBlueprints(t *testing.T) {
	err := fs.WalkDir(Blueprints, "blueprints", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			t.Errorf("%s: name template Go source *.go.tmpl so vkd's own toolchain ignores it", p)
		}
		if !d.IsDir() && strings.HasSuffix(p, "go.mod") {
			t.Errorf("%s: name it go.mod.tmpl: a nested go.mod would make Go treat the directory as another module", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVersionsPinEverythingTemplatesUse(t *testing.T) {
	v, err := Versions()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"go", "bun", "rust", "node", "biome", "svelte", "sveltekit", "astro", "tauri", "codeatlasdev_ui", "golangci_lint"} {
		s, ok := v[key].(string)
		if !ok || s == "" || s == "latest" || strings.ContainsAny(s, "^~*") {
			t.Errorf("versions.yaml %q = %v: must be an exact pinned version", key, v[key])
		}
	}
	actions := v["actions"].(map[string]any)
	for _, key := range []string{"checkout", "setup_go", "setup_bun", "upload_artifact", "download_artifact", "action_gh_release", "golangci_lint"} {
		s, _ := actions[key].(string)
		ref, _, _ := strings.Cut(s, " # ")
		at := strings.LastIndex(ref, "@")
		if at < 0 || len(ref[at+1:]) != 40 {
			t.Errorf("actions.%s = %q: must be pinned to a full 40-character commit SHA with a `# vX` comment", key, s)
		}
		if !strings.Contains(s, " # v") {
			t.Errorf("actions.%s = %q: missing the `# vX` comment", key, s)
		}
	}
}
