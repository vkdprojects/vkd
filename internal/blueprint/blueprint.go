// Package blueprint loads and validates blueprints: a directory holding one blueprint.yaml and a files/ tree.
package blueprint

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind classifies a blueprint.
type Kind string

const (
	KindBase  Kind = "base"
	KindStack Kind = "stack"
	KindAddon Kind = "addon"
)

// Question is an extra input a blueprint asks for. It becomes the flag --<blueprint>.<id>
// (and `--set <blueprint>.<id>=value`) and an entry in `vkd schema`.
type Question struct {
	ID       string   `yaml:"id" json:"id"`
	Prompt   string   `yaml:"prompt" json:"prompt"`
	Default  string   `yaml:"default,omitempty" json:"default,omitempty"`
	Options  []string `yaml:"options,omitempty" json:"options,omitempty"`
	Required bool     `yaml:"required,omitempty" json:"required,omitempty"`
}

// Contributes is what a blueprint adds to the files that are composed from many blueprints.
// Every string inside it is rendered as a template before it is used.
type Contributes struct {
	Atlas      Atlas                   `yaml:"atlas,omitempty" json:"atlas,omitempty"`
	CI         Jobs                    `yaml:"ci,omitempty" json:"ci,omitempty"`
	Release    Jobs                    `yaml:"release,omitempty" json:"release,omitempty"`
	Gitignore  []string                `yaml:"gitignore,omitempty" json:"gitignore,omitempty"`
	Workspaces []string                `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`
	Lefthook   map[string]LefthookHook `yaml:"lefthook,omitempty" json:"lefthook,omitempty"`
	Agents     string                  `yaml:"agents,omitempty" json:"agents,omitempty"`
	Env        []string                `yaml:"env,omitempty" json:"env,omitempty"`
}

// Composed declares a file that is built from the contributions of every blueprint in the plan instead of
// being copied. Only a base blueprint may declare them. Head is a template file under files/ holding what
// comes before the merged part (workflow header, AGENTS.md intro); it is not written to the project itself.
type Composed struct {
	Path string `yaml:"path" json:"path"`
	// Kind: atlas | ci | release | gitignore | agents | lefthook | env
	Kind string `yaml:"kind" json:"kind"`
	Head string `yaml:"head,omitempty" json:"head,omitempty"`
}

// ComposedKinds are the kinds compose knows.
var ComposedKinds = []string{"atlas", "ci", "release", "gitignore", "agents", "lefthook", "env"}

// FileRule adjusts one source file under files/ (matched by its path relative to files/, before templating).
//
//	as:   write it under this (templated) path instead of its own, e.g. LICENSE.apache -> LICENSE
//	when: template expression; the file is written only when it is truthy
type FileRule struct {
	Path string `yaml:"path" json:"path"`
	As   string `yaml:"as,omitempty" json:"as,omitempty"`
	When string `yaml:"when,omitempty" json:"when,omitempty"`
}

// Atlas is the part of atlas.yaml a blueprint owns.
type Atlas struct {
	// PreCommand is atlas.yaml's pre_command (e.g. "bun install"). At most one blueprint may set it.
	PreCommand string           `yaml:"pre_command,omitempty" json:"pre_command,omitempty"`
	Services   []map[string]any `yaml:"services,omitempty" json:"services,omitempty"`
	Actions    []map[string]any `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// Jobs holds GitHub Actions job bodies keyed by job id.
type Jobs struct {
	Jobs map[string]map[string]any `yaml:"jobs,omitempty" json:"jobs,omitempty"`
}

// LefthookHook is one lefthook hook (pre-push, ...).
type LefthookHook struct {
	Commands map[string]LefthookCommand `yaml:"commands,omitempty" json:"commands,omitempty"`
}

// LefthookCommand is one lefthook command.
type LefthookCommand struct {
	Run  string `yaml:"run" json:"run"`
	Glob string `yaml:"glob,omitempty" json:"glob,omitempty"`
}

// Blueprint is a parsed, validated blueprint.yaml plus where its files live.
type Blueprint struct {
	Name      string     `yaml:"name" json:"name"`
	Kind      Kind       `yaml:"kind" json:"kind"`
	Summary   string     `yaml:"summary" json:"summary"`
	Requires  []string   `yaml:"requires,omitempty" json:"requires,omitempty"`
	Conflicts []string   `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`
	When      string     `yaml:"when,omitempty" json:"when,omitempty"`
	Target    string     `yaml:"target,omitempty" json:"target,omitempty"`
	Questions []Question `yaml:"questions,omitempty" json:"questions,omitempty"`
	Composed  []Composed `yaml:"composed,omitempty" json:"composed,omitempty"`
	Rules     []FileRule `yaml:"files,omitempty" json:"files,omitempty"`
	// Contributes is the contributes block parsed as written. It is only meaningful when the block holds no
	// template syntax; use RenderContributes, which renders the text first, to get what a plan applies.
	Contributes Contributes `yaml:"-" json:"contributes,omitempty"`
	// ContributesRaw is the text of the contributes block, exactly as authored (template syntax included).
	ContributesRaw string `yaml:"-" json:"-"`

	// FS is rooted at the blueprint directory; files live under files/.
	FS fs.FS `yaml:"-" json:"-"`
	// Source says where it was loaded from (for errors and `vkd list`).
	Source string `yaml:"-" json:"-"`
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var idRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Parse decodes and validates one blueprint.yaml. Unknown keys are errors, so a typo never does nothing.
//
// The `contributes:` block is not decoded here when it holds template syntax: it is rendered as text first (see
// ParseContributes), so a template can add or drop keys, list items and steps. Everything else in the file is
// plain YAML.
func Parse(data []byte, source string) (*Blueprint, error) {
	rest, raw := splitContributes(string(data))
	var b Blueprint
	dec := yaml.NewDecoder(strings.NewReader(rest))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	b.Source = source
	b.ContributesRaw = raw
	if raw != "" && !strings.Contains(raw, "{{") {
		// No template syntax: decode now so authoring mistakes surface at load time, like every other field.
		c, err := ParseContributes(raw, source)
		if err != nil {
			return nil, err
		}
		b.Contributes = c
	}
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &b, nil
}

// splitContributes cuts the top-level `contributes:` block out of a blueprint.yaml. The block is the key line and
// every following line that is indented, blank or a comment, up to the next top-level key. rest is the document
// without it; block is the text under the key (the key line itself is not included), or "" when absent.
func splitContributes(doc string) (rest, block string) {
	lines := strings.SplitAfter(doc, "\n")
	var restB, blockB strings.Builder
	in := false
	for _, l := range lines {
		trimmed := strings.TrimRight(l, "\r\n")
		switch {
		case !in && (trimmed == "contributes:" || strings.HasPrefix(trimmed, "contributes: ")):
			in = true
			// `contributes: {}` or inline content on the key line is kept as the block's first line.
			if after := strings.TrimSpace(strings.TrimPrefix(trimmed, "contributes:")); after != "" {
				blockB.WriteString(after + "\n")
			}
		case in && (trimmed == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#'):
			blockB.WriteString(l)
		default:
			in = false
			restB.WriteString(l)
		}
	}
	return restB.String(), blockB.String()
}

// ParseContributes strictly decodes the (rendered) text of a contributes block and validates it. Unknown keys are
// errors, so a typo never does nothing.
func ParseContributes(text, source string) (Contributes, error) {
	var c Contributes
	if strings.TrimSpace(text) == "" {
		return c, nil
	}
	dec := yaml.NewDecoder(strings.NewReader("contributes:\n" + indentBlock(text)))
	dec.KnownFields(true)
	var doc struct {
		Contributes Contributes `yaml:"contributes"`
	}
	if err := dec.Decode(&doc); err != nil {
		return c, fmt.Errorf("%s: contributes: %w", source, err)
	}
	if err := validateContributes(doc.Contributes); err != nil {
		return c, fmt.Errorf("%s: %w", source, err)
	}
	return doc.Contributes, nil
}

// indentBlock makes text a mapping under `contributes:` whether it was written indented (a block) or as the inline
// value of the key (`contributes: {}`).
func indentBlock(text string) string {
	first := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(first, "{") || strings.HasPrefix(first, "null") || strings.HasPrefix(first, "~") {
		return "  " + first
	}
	return text
}

func validateContributes(c Contributes) error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	for job := range c.CI.Jobs {
		if job == "ci" {
			add("contributes.ci.jobs.ci is reserved: the aggregate CI job is generated")
		}
	}
	for i, s := range c.Atlas.Services {
		if n, _ := s["name"].(string); n == "" {
			add("contributes.atlas.services[%d]: name is required", i)
		}
	}
	for i, a := range c.Atlas.Actions {
		if k, _ := a["key"].(string); len([]rune(k)) != 1 {
			add("contributes.atlas.actions[%d]: key must be a single character", i)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Validate reports every problem at once, so an author fixes them in one pass.
func (b *Blueprint) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if !nameRE.MatchString(b.Name) {
		add("name %q must match %s", b.Name, nameRE)
	}
	switch b.Kind {
	case KindBase, KindStack, KindAddon:
	default:
		add("kind %q must be base, stack or addon", b.Kind)
	}
	if strings.TrimSpace(b.Summary) == "" {
		add("summary is required")
	}
	if b.Kind == KindBase && len(b.Requires) > 0 {
		add("a base blueprint cannot require others")
	}
	for _, r := range b.Requires {
		if r == b.Name {
			add("requires itself")
		}
	}
	for _, c := range b.Conflicts {
		if c == b.Name {
			add("conflicts with itself")
		}
		for _, r := range b.Requires {
			if r == c {
				add("%q is both required and conflicting", c)
			}
		}
	}
	if b.Target != "" {
		if strings.HasPrefix(b.Target, "/") || hasDotDot(b.Target) {
			add("target %q must be a relative path inside the project", b.Target)
		}
	}
	seen := map[string]bool{}
	for i, q := range b.Questions {
		switch {
		case !idRE.MatchString(q.ID):
			add("questions[%d].id %q must match %s", i, q.ID, idRE)
		case seen[q.ID]:
			add("questions[%d].id %q is duplicated", i, q.ID)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Prompt) == "" {
			add("questions[%d] (%s): prompt is required", i, q.ID)
		}
		if len(q.Options) > 0 && q.Default != "" && !contains(q.Options, q.Default) && !strings.Contains(q.Default, "[[") {
			add("questions[%d] (%s): default %q is not one of options", i, q.ID, q.Default)
		}
	}
	if len(b.Composed) > 0 && b.Kind != KindBase {
		add("only a base blueprint can declare composed files")
	}
	for i, c := range b.Composed {
		if c.Path == "" || strings.HasPrefix(c.Path, "/") || hasDotDot(c.Path) {
			add("composed[%d].path %q must be a relative path inside the project", i, c.Path)
		}
		if !contains(ComposedKinds, c.Kind) {
			add("composed[%d].kind %q must be one of %s", i, c.Kind, strings.Join(ComposedKinds, ", "))
		}
	}
	seenRule := map[string]bool{}
	for i, r := range b.Rules {
		switch {
		case r.Path == "":
			add("files[%d].path is required", i)
		case seenRule[r.Path]:
			add("files[%d].path %q is listed twice", i, r.Path)
		case r.As == "" && r.When == "":
			add("files[%d] (%s) sets neither as nor when", i, r.Path)
		}
		seenRule[r.Path] = true
		if r.As != "" && (strings.HasPrefix(r.As, "/") || hasDotDot(r.As)) {
			add("files[%d].as %q must be a relative path inside the project", i, r.As)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Files returns the paths under files/ in lexical order, relative to files/.
func (b *Blueprint) Files() ([]string, error) {
	var out []string
	sub, err := fs.Sub(b.FS, "files")
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == "." {
				return fs.SkipAll // no files/ directory is fine
			}
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ReadFile reads a path relative to files/.
func (b *Blueprint) ReadFile(p string) ([]byte, error) {
	return fs.ReadFile(b.FS, path.Join("files", p))
}

// Mode returns the file mode. embed.FS does not keep permission bits, so the rule is by name: files
// ending in .sh (after the .tmpl suffix is dropped) are executable, everything else is 0644.
func (b *Blueprint) Mode(p string) fs.FileMode {
	if strings.HasSuffix(strings.TrimSuffix(p, ".tmpl"), ".sh") {
		return 0o755
	}
	return 0o644
}

func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
