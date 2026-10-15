package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/compose"
	"github.com/vkdprojects/vkd/internal/render"
)

// File is one rendered output file.
type File struct {
	Path    string      `json:"path"`
	Content []byte      `json:"-"`
	Mode    fs.FileMode `json:"-"`
	// From is the blueprint that produced it ("composed" files name the base blueprint).
	From string `json:"from"`
}

// Plan is everything vkd would write, in path order.
type Plan struct {
	Blueprints []string `json:"blueprints"`
	Files      []File   `json:"files"`
	// Warnings are things vkd changed so the plan could compose (an action key moved off a clash).
	Warnings []string `json:"warnings"`
}

// Paths returns the file paths.
func (p *Plan) Paths() []string {
	out := make([]string, len(p.Files))
	for i, f := range p.Files {
		out[i] = f.Path
	}
	return out
}

// Find returns a file by path.
func (p *Plan) Find(path string) (File, bool) {
	for _, f := range p.Files {
		if f.Path == path {
			return f, true
		}
	}
	return File{}, false
}

// Hash is a stable hash of one blueprint's source files, recorded in .vkd/answers.yaml so `vkd update` can say
// which blueprints changed.
func Hash(b *blueprint.Blueprint) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(b.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(b.FS, p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", p, len(data)) // hash.Hash.Write never returns an error
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Build resolves the blueprints and renders every file.
func Build(reg *blueprint.Registry, a *answers.Answers, versions map[string]any) (*Plan, error) {
	a.Defaults()
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := fillQuestionDefaults(reg, a, versions); err != nil {
		return nil, err
	}
	probe := render.NewEnv(versions, nil, a.PortBase)
	bps, err := Resolve(reg, a, probe)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(bps))
	for i, b := range bps {
		names[i] = b.Name
	}
	env := render.NewEnv(versions, names, a.PortBase)

	files := map[string]File{}
	put := func(f File) error {
		if prev, dup := files[f.Path]; dup {
			return fmt.Errorf("file %q is produced by both %q and %q", f.Path, prev.From, f.From)
		}
		files[f.Path] = f
		return nil
	}

	// Pass 1: targets and contributions of every blueprint, so pass 2 can render files that depend on the
	// whole plan (root package.json lists every workspace).
	type item struct {
		b      *blueprint.Blueprint
		ctx    render.Context
		target string
	}
	var items []item
	var frags []compose.Fragment
	var composed []blueprint.Composed
	var composedFrom string
	var warnings []string
	heads := map[string]bool{} // files/ paths used as composed heads: not copied to the project
	for _, b := range bps {
		ctx := render.NewContext(a)
		ctx.Blueprint = b.Name
		target, err := env.Path(orDot(b.Target), ctx)
		if err != nil && b.Target != "" {
			return nil, fmt.Errorf("blueprint %q target: %w", b.Name, err)
		}
		if b.Target == "" {
			target = "."
		}
		ctx.Target = target
		for _, c := range b.Composed {
			composed = append(composed, c)
			composedFrom = b.Name
			if c.Head != "" {
				heads[b.Name+"/"+c.Head] = true
			}
		}
		fr, err := fragment(env, b, ctx)
		if err != nil {
			return nil, err
		}
		frags = append(frags, fr)
		items = append(items, item{b, ctx, target})
	}
	workspaces := compose.Workspaces(frags)

	// Pass 2: files.
	for _, it := range items {
		it.ctx.Workspaces = workspaces
		paths, err := it.b.Files()
		if err != nil {
			return nil, fmt.Errorf("blueprint %q: %w", it.b.Name, err)
		}
		rules := map[string]blueprint.FileRule{}
		for _, r := range it.b.Rules {
			rules[r.Path] = r
		}
		for _, p := range paths {
			if heads[it.b.Name+"/"+p] {
				continue
			}
			src, as := p, ""
			if r, ok := rules[p]; ok {
				if r.When != "" {
					ok, err := evalExpr(env, it.b.Name+" files "+p, r.When, it.ctx)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
				}
				as = r.As
			}
			f, err := renderFile(env, it.b, it.ctx, it.target, src, as)
			if err != nil {
				return nil, err
			}
			if err := put(f); err != nil {
				return nil, err
			}
		}
	}

	// Composed files. Heads live in the declaring base blueprint.
	for _, c := range composed {
		base, _ := reg.Get(composedFrom)
		ctx := render.NewContext(a)
		ctx.Blueprint = composedFrom
		ctx.Workspaces = workspaces
		head := ""
		if c.Head != "" {
			data, err := base.ReadFile(c.Head)
			if err != nil {
				return nil, fmt.Errorf("composed %s: head %s: %w", c.Path, c.Head, err)
			}
			head, err = env.String(composedFrom+"/"+c.Head, string(data), ctx)
			if err != nil {
				return nil, err
			}
		}
		content, warns, err := composeOne(c, head, a, frags, env.Versions)
		warnings = append(warnings, warns...)
		if err != nil {
			return nil, fmt.Errorf("composed %s: %w", c.Path, err)
		}
		mode := fs.FileMode(0o644)
		if err := put(File{Path: c.Path, Content: content, Mode: mode, From: composedFrom}); err != nil {
			return nil, err
		}
	}

	out := &Plan{Blueprints: names, Warnings: warnings}
	for _, f := range files {
		out.Files = append(out.Files, f)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// fillQuestionDefaults gives every question of the selected blueprints its default (rendered) when the user did
// not answer, and errors naming the flag for a required question without an answer.
func fillQuestionDefaults(reg *blueprint.Registry, a *answers.Answers, versions map[string]any) error {
	probe := render.NewEnv(versions, nil, a.PortBase)
	bps, err := Resolve(reg, a, probe)
	if err != nil {
		return err
	}
	var missing []string
	for _, b := range bps {
		for _, q := range b.Questions {
			if a.Get(b.Name, q.ID) != "" {
				continue
			}
			if q.Default != "" {
				v, err := probe.String(b.Name+".questions."+q.ID, q.Default, render.NewContext(a))
				if err != nil {
					return err
				}
				a.Set(b.Name, q.ID, v)
				continue
			}
			if q.Required {
				missing = append(missing, "--"+b.Name+"."+q.ID)
			}
		}
	}
	if len(missing) > 0 {
		return &MissingError{Flags: missing}
	}
	for bp, m := range a.Per {
		b, ok := reg.Get(bp)
		if !ok {
			return fmt.Errorf("answer for unknown blueprint %q", bp)
		}
		for id, v := range m {
			var q *blueprint.Question
			for i := range b.Questions {
				if b.Questions[i].ID == id {
					q = &b.Questions[i]
				}
			}
			if q == nil {
				return fmt.Errorf("blueprint %q has no question %q (known: %s)", bp, id, questionIDs(b))
			}
			if len(q.Options) > 0 && !contains(q.Options, v) {
				return fmt.Errorf("--%s.%s: %q must be one of %s", bp, id, v, strings.Join(q.Options, ", "))
			}
		}
	}
	return nil
}

// MissingError reports required answers that were not given. Flags names every one.
type MissingError struct{ Flags []string }

func (e *MissingError) Error() string {
	return "missing required answers: " + strings.Join(e.Flags, ", ")
}

func questionIDs(b *blueprint.Blueprint) string {
	var ids []string
	for _, q := range b.Questions {
		ids = append(ids, q.ID)
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// renderFile renders one file of a blueprint: the path (every segment may be a template), the content when it
// ends in .tmpl, and lands it under the blueprint's target.
func renderFile(env *render.Env, b *blueprint.Blueprint, ctx render.Context, target, src, as string) (File, error) {
	dest := src
	if as != "" {
		dest = as
	}
	rel, err := env.Path(dest, ctx)
	if err != nil {
		return File{}, fmt.Errorf("blueprint %q: %w", b.Name, err)
	}
	data, err := b.ReadFile(src)
	if err != nil {
		return File{}, err
	}
	if strings.HasSuffix(src, ".tmpl") {
		rel = strings.TrimSuffix(rel, ".tmpl")
		s, err := env.String(b.Name+"/"+src, string(data), ctx)
		if err != nil {
			return File{}, err
		}
		data = []byte(s)
	}
	if target != "." {
		rel = path.Join(target, rel)
	}
	return File{Path: rel, Content: data, Mode: b.Mode(src), From: b.Name}, nil
}

// fragment renders a blueprint's contributions. The contributes block is rendered as TEXT first and parsed after,
// so a template can add or drop keys, list items and steps; then every nested string is rendered once more only
// for what the text render cannot reach (nothing: the whole block has already been through the template).
func fragment(env *render.Env, b *blueprint.Blueprint, ctx render.Context) (compose.Fragment, error) {
	f := compose.Fragment{Blueprint: b.Name}
	text, err := env.Contributes(b.Name+".contributes", b.ContributesRaw, ctx)
	if err != nil {
		return f, fmt.Errorf("blueprint %q: %w", b.Name, err)
	}
	c, err := blueprint.ParseContributes(text, b.Source)
	if err != nil {
		return f, fmt.Errorf("blueprint %q: %w", b.Name, err)
	}

	f.Services = c.Atlas.Services
	f.Actions = c.Atlas.Actions
	f.PreCommand = c.Atlas.PreCommand
	f.CIJobs = c.CI.Jobs
	f.RelJobs = c.Release.Jobs
	f.Gitignore = c.Gitignore
	f.Workspaces = c.Workspaces
	f.Env = c.Env
	f.Agents = c.Agents
	if len(c.Lefthook) > 0 {
		f.Lefthook = map[string]map[string]map[string]any{}
		for hook, h := range c.Lefthook {
			f.Lefthook[hook] = map[string]map[string]any{"commands": {}}
			for name, cmd := range h.Commands {
				entry := map[string]any{"run": cmd.Run}
				if cmd.Glob != "" {
					entry["glob"] = cmd.Glob
				}
				f.Lefthook[hook]["commands"][name] = entry
			}
		}
	}
	return f, nil
}

func composeOne(c blueprint.Composed, head string, a *answers.Answers, frags []compose.Fragment, versions map[string]any) ([]byte, []string, error) {
	switch c.Kind {
	case "atlas":
		return compose.Atlas(compose.Header{Name: a.Name, EnvFiles: []string{".env"}}, frags)
	case "ci":
		out, err := compose.CI(head, frags)
		return out, nil, err
	case "release":
		out, err := compose.Release(head, frags)
		return out, nil, err
	case "gitignore":
		return compose.Gitignore(frags), nil, nil
	case "agents":
		return compose.Agents(head, frags), nil, nil
	case "lefthook":
		out, err := compose.Lefthook(frags)
		return out, nil, err
	case "env":
		return compose.Env(frags), nil, nil
	}
	return nil, nil, fmt.Errorf("unknown composed kind %q", c.Kind)
}

// evalExpr evaluates a `when` expression: the body of a template `if`.
func evalExpr(env *render.Env, name, expr string, ctx render.Context) (bool, error) {
	out, err := env.String(name, "{{ if "+expr+" }}true{{ end }}", ctx)
	if err != nil {
		return false, fmt.Errorf("when %q: %w", expr, err)
	}
	return out == "true", nil
}
