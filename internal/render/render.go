// Package render renders blueprint templates.
//
// Delimiters are {{ and }} as in the blueprint spec. GitHub Actions expressions are written ${{ ... }}:
// those are passed through untouched (see protect), so a workflow needs no escaping. Any other literal
// {{ in a template is written {{ "{{" }}.
package render

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/vkdprojects/vkd/internal/answers"
)

// Context is the template data root.
type Context struct {
	Name        string
	Owner       string
	Description string
	OpenSource  bool
	License     string
	PortBase    int
	Year        int
	Components  []string
	// Answers.<blueprint>.<id>
	Answers map[string]map[string]string
	// Workspaces is the merged bun workspace globs contributed by every blueprint in the plan.
	Workspaces []string
	// Blueprint is the name of the blueprint being rendered ("" for composed files).
	Blueprint string
	// Target is the rendered target directory of that blueprint ("." by default).
	Target string
}

// Env carries what funcs need besides the data: pinned versions and the selected blueprint set.
type Env struct {
	Versions map[string]any
	Selected map[string]bool
	ports    *portAllocator
}

// NewEnv builds an Env. versions is the parsed versions.yaml; selected is every blueprint in the plan.
func NewEnv(versions map[string]any, selected []string, portBase int) *Env {
	sel := map[string]bool{}
	for _, s := range selected {
		sel[s] = true
	}
	return &Env{Versions: versions, Selected: sel, ports: &portAllocator{base: portBase}}
}

// NewContext derives the template data from answers.
func NewContext(a *answers.Answers) Context {
	return Context{
		Name: a.Name, Owner: a.Owner, Description: a.Description, OpenSource: a.OpenSource,
		License: a.License, PortBase: a.PortBase, Year: a.Year, Components: a.Components, Answers: a.Per, Target: ".",
	}
}

// portBlock is how many consecutive ports a project owns, starting at PortBase.
const portBlock = 10

// portAllocator hands out the project's block of ports, two ways that never collide:
//
//	{{port "api"}}  a NAME: the first name asked for gets PortBase, the next PortBase+1, ... skipping any port a
//	                number holds. Asking again returns the same port. This is the form to use: two blueprints
//	                cannot clash, whatever order they are applied in.
//	{{port 3}}      an OFFSET: PortBase+3, fixed. Kept for existing blueprints; it fails if a name already took it.
//
// Names are allocated in the order they are first requested, and blueprints are rendered in plan order, so the
// result is deterministic for a given plan.
type portAllocator struct {
	base   int
	names  map[string]int // name -> offset
	owners map[int]string // offset -> name ("" for a numeric offset)
	fixed  map[int]bool   // offsets asked for as numbers
}

func (p *portAllocator) port(arg any) (int, error) {
	if p.names == nil {
		p.names, p.owners, p.fixed = map[string]int{}, map[int]string{}, map[int]bool{}
	}
	switch v := arg.(type) {
	case string:
		return p.named(v)
	case int:
		return p.offset(v)
	}
	return 0, fmt.Errorf("port: want a service name (string) or an offset (integer), got %v", arg)
}

func (p *portAllocator) named(name string) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("port: the service name is empty")
	}
	if off, ok := p.names[name]; ok {
		return p.base + off, nil
	}
	for off := 0; off < portBlock; off++ {
		if _, taken := p.owners[off]; taken || p.fixed[off] {
			continue
		}
		p.names[name], p.owners[off] = off, name
		return p.base + off, nil
	}
	return 0, fmt.Errorf("port %q: the project's block of %d ports (%d..%d) is full; taken by %s",
		name, portBlock, p.base, p.base+portBlock-1, p.takenBy())
}

func (p *portAllocator) offset(n int) (int, error) {
	if n < 0 || n >= portBlock {
		return 0, fmt.Errorf("port %d is outside the project block 0..%d", n, portBlock-1)
	}
	if owner := p.owners[n]; owner != "" {
		return 0, fmt.Errorf("port offset %d is already given to the service name %q: use {{port %q}} instead of a number", n, owner, owner)
	}
	p.fixed[n] = true
	return p.base + n, nil
}

func (p *portAllocator) takenBy() string {
	var out []string
	for name := range p.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// Funcs returns the template functions for env.
func (e *Env) Funcs() template.FuncMap {
	return template.FuncMap{
		"snake":  func(s string) string { return joinWords(s, "_", strings.ToLower) },
		"kebab":  func(s string) string { return joinWords(s, "-", strings.ToLower) },
		"camel":  func(s string) string { return camel(s, false) },
		"pascal": func(s string) string { return camel(s, true) },
		"upper":  strings.ToUpper,
		"lower":  strings.ToLower,
		"indent": func(n int, s string) string {
			pad := strings.Repeat(" ", n)
			lines := strings.Split(s, "\n")
			for i, l := range lines {
				if l != "" {
					lines[i] = pad + l
				}
			}
			return strings.Join(lines, "\n")
		},
		"toYaml": func(v any) (string, error) {
			b, err := yaml.Marshal(v)
			return strings.TrimRight(string(b), "\n"), err
		},
		// has reports whether a blueprint is in the plan: {{ if has "go-api" }}.
		"has": func(name string) bool { return e.Selected[name] },
		// version looks up versions.yaml: {{ version "bun" }}, {{ version "actions.checkout" }}.
		"version": func(key string) (string, error) { return e.lookup(key) },
		// port allocates from the project's port block: {{ port "api" }} (a name; use this) or {{ port 0 }}.
		"port": e.ports.port,
		// default returns d when v is empty.
		"default": func(d, v string) string {
			if v == "" {
				return d
			}
			return v
		},
		"join":  func(sep string, xs []string) string { return strings.Join(xs, sep) },
		"quote": func(s string) string { return fmt.Sprintf("%q", s) },
		// trimPrefix, replace: the small string helpers templates actually need.
		"trimPrefix": func(p, s string) string { return strings.TrimPrefix(s, p) },
		"replace":    func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
	}
}

func (e *Env) lookup(key string) (string, error) {
	var cur any = e.Versions
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("version %q: %q is not a table", key, part)
		}
		cur, ok = m[part]
		if !ok {
			return "", fmt.Errorf("version %q is not pinned in versions.yaml", key)
		}
	}
	s, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("version %q is a table, not a value", key)
	}
	return s, nil
}

// Contributes renders the text of a blueprint's contributes block. It is String, plus one courtesy: authors write
// template actions inside double-quoted YAML scalars (`uses: "{{ version \"actions.checkout\" }}"`), where the
// quotes of the action are escaped for YAML. Inside an action only, `\"` is turned back into `"` before the
// template is parsed, so both that style and the plain `uses: {{ version "actions.checkout" }}` work.
func (e *Env) Contributes(name, text string, ctx Context) (string, error) {
	return e.String(name, unescapeActionQuotes(text), ctx)
}

var action = regexp.MustCompile(`\{\{.*?\}\}`)

func unescapeActionQuotes(text string) string {
	return action.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "{{") && !strings.Contains(m, `\"`) {
			return m
		}
		return strings.ReplaceAll(m, `\"`, `"`)
	})
}

// String renders text with ctx. name only labels errors.
func (e *Env) String(name, text string, ctx Context) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	text, restore := protect(text)
	t, err := template.New(name).Funcs(e.Funcs()).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return restore(buf.String()), nil
}

// ghExpr matches a GitHub Actions expression, ${{ ... }}.
var ghExpr = regexp.MustCompile(`\$\{\{.*?\}\}`)

// protect swaps every ${{ ... }} for an opaque token before parsing and returns the function that puts
// them back. A token has no template syntax, so text/template never sees the expression.
func protect(text string) (string, func(string) string) {
	var saved []string
	out := ghExpr.ReplaceAllStringFunc(text, func(m string) string {
		saved = append(saved, m)
		return fmt.Sprintf("\x00GH%d\x00", len(saved)-1)
	})
	return out, func(s string) string {
		for i, m := range saved {
			s = strings.ReplaceAll(s, fmt.Sprintf("\x00GH%d\x00", i), m)
		}
		return s
	}
}

// Path renders every segment of a relative path and rejects results that escape the project.
// A segment that renders empty drops the whole file's path (error), never silently collapses.
func (e *Env) Path(p string, ctx Context) (string, error) {
	out, err := e.String("path "+p, p, ctx)
	if err != nil {
		return "", err
	}
	for _, seg := range strings.Split(out, "/") {
		if seg == "" || seg == ".." {
			return "", fmt.Errorf("path %q rendered to %q, which has an empty or .. segment", p, out)
		}
	}
	return out, nil
}

// Value renders every string inside a decoded YAML/JSON value, in place, recursively.
func (e *Env) Value(name string, v any, ctx Context) (any, error) {
	switch x := v.(type) {
	case string:
		return e.String(name, x, ctx)
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			r, err := e.Value(fmt.Sprintf("%s[%d]", name, i), el, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, el := range x {
			r, err := e.Value(name+"."+k, el, ctx)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case unicode.IsUpper(r):
			// start a word at lower→Upper, or at the last capital of an acronym followed by a lowercase.
			if i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
				(unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
				flush()
			}
			cur = append(cur, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func joinWords(s, sep string, f func(string) string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = f(w)
	}
	return strings.Join(ws, sep)
}

func camel(s string, upperFirst bool) string {
	ws := words(s)
	for i, w := range ws {
		w = strings.ToLower(w)
		if i > 0 || upperFirst {
			rs := []rune(w)
			rs[0] = unicode.ToUpper(rs[0])
			w = string(rs)
		}
		ws[i] = w
	}
	return strings.Join(ws, "")
}
