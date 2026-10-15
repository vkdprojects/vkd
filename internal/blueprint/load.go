package blueprint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Registry is the set of known blueprints by name.
type Registry struct {
	byName map[string]*Blueprint
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byName: map[string]*Blueprint{}} }

// Get returns a blueprint by name.
func (r *Registry) Get(name string) (*Blueprint, bool) {
	b, ok := r.byName[name]
	return b, ok
}

// Names returns the blueprint names sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for n := range r.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns every blueprint sorted by name.
func (r *Registry) All() []*Blueprint {
	names := r.Names()
	out := make([]*Blueprint, len(names))
	for i, n := range names {
		out[i] = r.byName[n]
	}
	return out
}

// Add registers b, replacing any blueprint with the same name (later sources override earlier ones).
func (r *Registry) Add(b *Blueprint) { r.byName[b.Name] = b }

// LoadFS adds every <root>/<name>/blueprint.yaml found in fsys. root "." means the top of fsys.
// A directory without blueprint.yaml is an error, so a half-copied blueprint is never silently skipped.
func (r *Registry) LoadFS(fsys fs.FS, root, source string) error {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(fsys, joinSlash(root, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		data, err := fs.ReadFile(sub, "blueprint.yaml")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", source, e.Name(), err))
			continue
		}
		b, err := Parse(data, source+"/"+e.Name()+"/blueprint.yaml")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if b.Name != e.Name() {
			errs = append(errs, fmt.Errorf("%s: name %q must equal its directory %q", b.Source, b.Name, e.Name()))
			continue
		}
		b.FS = sub
		r.Add(b)
	}
	return errors.Join(errs...)
}

// LoadDir adds blueprints from a directory on disk. A missing directory is not an error (optional config dir).
func (r *Registry) LoadDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return r.LoadFS(os.DirFS(dir), ".", dir)
}

// LoadOne loads a single blueprint directory (used by `vkd blueprint lint <dir>`).
func LoadOne(dir string) (*Blueprint, error) {
	data, err := os.ReadFile(filepath.Join(dir, "blueprint.yaml"))
	if err != nil {
		return nil, err
	}
	b, err := Parse(data, filepath.Join(dir, "blueprint.yaml"))
	if err != nil {
		return nil, err
	}
	if filepath.Base(filepath.Clean(dir)) != b.Name {
		return nil, fmt.Errorf("%s: name %q must equal its directory %q", b.Source, b.Name, filepath.Base(filepath.Clean(dir)))
	}
	b.FS = os.DirFS(dir)
	return b, nil
}

func joinSlash(a, b string) string {
	if a == "." || a == "" {
		return b
	}
	return a + "/" + b
}
