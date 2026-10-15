// Package compose merges what blueprints contribute into the files that exist once per project:
// atlas.yaml, ci.yml, release.yml, .gitignore, package.json workspaces, AGENTS.md sections, lefthook.yml.
//
// Everything here works on already-rendered fragments, so it is plain data in, bytes out.
package compose

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Fragment is one blueprint's rendered contribution. Order of fragments is plan order.
type Fragment struct {
	Blueprint  string
	PreCommand string
	Services   []map[string]any
	Actions    []map[string]any
	CIJobs     map[string]map[string]any
	RelJobs    map[string]map[string]any
	Gitignore  []string
	Workspaces []string
	Lefthook   map[string]map[string]map[string]any // hook -> commands -> name -> command
	Agents     string
	Env        []string
}

// Header is the project-level data around the merged parts.
type Header struct {
	Name       string
	PreCommand string // e.g. "bun install"; empty = omit
	EnvFiles   []string
}

func marshal(v any) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return nil, err
	}
	liftComments(&node)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// liftComments turns every `uses` value of the form "owner/repo@sha # v1.2.3" into the plain scalar
// "owner/repo@sha" with a real trailing YAML comment "# v1.2.3". Written as one quoted string, the comment would be
// part of the value and the action would no longer be pinned to a hash in the eyes of zizmor.
func liftComments(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "uses" && v.Kind == yaml.ScalarNode {
				if ref, note, ok := strings.Cut(v.Value, " # "); ok {
					v.Value = ref
					v.Style = 0
					v.LineComment = "# " + note
				}
			}
		}
	}
	for _, c := range n.Content {
		liftComments(c)
	}
}

// orderedMap keeps keys in insertion order when encoded to YAML.
type orderedMap struct {
	keys []string
	vals map[string]any
}

func newOrdered() *orderedMap { return &orderedMap{vals: map[string]any{}} }

func (o *orderedMap) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedMap) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range o.keys {
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		vn := &yaml.Node{}
		if err := vn.Encode(o.vals[k]); err != nil {
			return nil, err
		}
		n.Content = append(n.Content, kn, vn)
	}
	return n, nil
}

// serviceKeyOrder is the order keys are written in a service: what a human reads first.
var serviceKeyOrder = []string{"command", "dir", "port", "health", "depends_on", "env", "restart", "max_restarts",
	"oneshot", "start_timeout", "autostart", "critical", "critical_help", "enabled", "open_key", "open_url",
	"hide_logs", "ready_log", "lock_file", "max_memory"}

var actionKeyOrder = []string{"key", "label", "command", "confirm", "pause_services", "session", "group"}

func orderedFrom(m map[string]any, order []string) *orderedMap {
	o := newOrdered()
	for _, k := range order {
		if v, ok := m[k]; ok {
			o.set(k, v)
		}
	}
	rest := make([]string, 0)
	for k := range m {
		if !contains(order, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		o.set(k, m[k])
	}
	return o
}

// reservedKeys are the single characters atlas uses as built-in dashboard keys: an action key from this set is
// rejected by `atlas config check`. Found by asking atlas itself, one character at a time (docs/tui.md lists them).
const reservedKeys = "fgijkmnqrstvzDEGJLMNPSTVW0123456789"

// freeActionKeys is what an action may use, in the order a reassigned action is offered them: the letters blueprints
// suggest most (check, types, test-ish, build, lint, play) first, then the rest of what atlas leaves free.
const freeActionKeys = "cyxblpaewdhouABCFHIKOQRUXYZ"

// Atlas builds atlas.yaml. It returns warnings for what it had to change.
//
// An action `key` is a suggestion. In plan order, an action keeps its key unless that key is a built-in atlas key, a
// service's open_key, or already taken by an earlier action; then it gets the first free key from freeActionKeys and
// a warning says so. Service names and ports stay hard errors: they name a real clash that no rename can fix silently.
func Atlas(h Header, frags []Fragment) ([]byte, []string, error) {
	services := newOrdered()
	owner := map[string]string{}
	ports := map[int]string{}
	var actions []any
	var warnings []string

	// Service open_keys are explicit UX choices: collect them all first so an action never takes one.
	taken := map[string]string{}
	for _, f := range frags {
		for _, s := range f.Services {
			if k, _ := s["open_key"].(string); k != "" {
				taken[k] = "service " + fmt.Sprint(s["name"])
			}
		}
	}
	next := func(label string) (string, error) {
		for _, r := range freeActionKeys {
			if k := string(r); taken[k] == "" {
				return k, nil
			}
		}
		return "", fmt.Errorf("no free action key left for %q: atlas has a limited set, give some actions to the command palette instead", label)
	}
	for _, f := range frags {
		if f.PreCommand != "" {
			if h.PreCommand != "" && h.PreCommand != f.PreCommand {
				return nil, nil, fmt.Errorf("atlas pre_command is set by two blueprints: %q and %q", h.PreCommand, f.PreCommand)
			}
			h.PreCommand = f.PreCommand
		}
		for _, s := range f.Services {
			name, _ := s["name"].(string)
			if prev, dup := owner[name]; dup {
				return nil, nil, fmt.Errorf("atlas service %q is contributed by both %q and %q", name, prev, f.Blueprint)
			}
			owner[name] = f.Blueprint
			body := map[string]any{}
			for k, v := range s {
				if k != "name" {
					body[k] = v
				}
			}
			if p, ok := toInt(body["port"]); ok {
				if prev, dup := ports[p]; dup {
					return nil, nil, fmt.Errorf("atlas port %d is used by service %q and %q", p, prev, name)
				}
				ports[p] = name
				body["port"] = p
			}
			services.set(name, orderedFrom(body, serviceKeyOrder))
		}
		for _, a := range f.Actions {
			k, _ := a["key"].(string)
			label, _ := a["label"].(string)
			if why := taken[k]; why != "" || strings.Contains(reservedKeys, k) {
				if why == "" {
					why = "a built-in atlas key"
				}
				nk, err := next(label)
				if err != nil {
					return nil, nil, err
				}
				warnings = append(warnings, fmt.Sprintf("blueprint %q: action %q wanted key %q (%s), got %q", f.Blueprint, label, k, why, nk))
				a = copyMap(a)
				a["key"], k = nk, nk
			}
			taken[k] = "action " + label + " of " + f.Blueprint
			actions = append(actions, orderedFrom(a, actionKeyOrder))
		}
	}
	doc := newOrdered()
	doc.set("name", h.Name)
	if h.PreCommand != "" {
		doc.set("pre_command", h.PreCommand)
	}
	if len(h.EnvFiles) > 0 {
		doc.set("env_files", h.EnvFiles)
	}
	if len(services.keys) == 0 {
		doc.set("services", map[string]any{})
	} else {
		doc.set("services", services)
	}
	if len(actions) > 0 {
		doc.set("actions", actions)
	}
	out, err := marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	return append([]byte("# atlas.yaml: dev environment for `atlas dev` (https://atlas.codeatlas.com.br/docs/configuration).\n"+
		"# Composed by vkd from the blueprints in .vkd/answers.yaml; edits are kept by `vkd update`.\n"), out...), warnings, nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// jobsDoc orders jobs: ids in `order` first, then the rest by name.
func jobsDoc(jobs map[string]map[string]any, order []string) *orderedMap {
	o := newOrdered()
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range order {
		if _, ok := jobs[id]; ok {
			o.set(id, orderedFrom(jobs[id], jobKeyOrder))
		}
	}
	for _, id := range ids {
		if !contains(order, id) {
			o.set(id, orderedFrom(jobs[id], jobKeyOrder))
		}
	}
	return o
}

var jobKeyOrder = []string{"name", "needs", "if", "runs-on", "timeout-minutes", "permissions", "environment",
	"strategy", "outputs", "env", "steps"}

// CI builds .github/workflows/ci.yml: the contributed jobs plus the aggregate `ci` job (name `CI`) whose
// `needs` lists every job, so one required check covers the whole workflow and adding a blueprint adds a gate.
// head is the already-rendered workflow header (name, on, permissions, concurrency).
func CI(head string, frags []Fragment) ([]byte, error) {
	jobs := map[string]map[string]any{}
	owner := map[string]string{}
	for _, f := range frags {
		for id, j := range f.CIJobs {
			if prev, dup := owner[id]; dup {
				return nil, fmt.Errorf("ci job %q is contributed by both %q and %q", id, prev, f.Blueprint)
			}
			owner[id] = f.Blueprint
			jobs[id] = j
		}
	}
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	jobs["ci"] = aggregate(ids)
	return workflow(head, jobs, append(ids, "ci"))
}

// aggregate is the `CI` job. `!cancelled()` so it runs (and fails) when a needed job failed; every result must
// be success. Same pattern as codeatlasdev/atlas.
func aggregate(needs []string) map[string]any {
	return map[string]any{
		"name":            "CI",
		"needs":           needs,
		"if":              "${{ !cancelled() }}",
		"runs-on":         "ubuntu-latest",
		"timeout-minutes": 5,
		"permissions":     map[string]any{},
		"steps": []any{map[string]any{
			"name": "Every job passed",
			"env":  map[string]any{"NEEDS": "${{ toJson(needs) }}"},
			"run": "jq -r 'to_entries[] | \"\\(.key): \\(.value.result)\"' <<<\"$NEEDS\"\n" +
				"jq -e 'to_entries | all(.value.result == \"success\")' <<<\"$NEEDS\" >/dev/null\n",
		}},
	}
}

// Release builds release.yml from the contributed release jobs. The `gate` and `publish` jobs come from the
// base blueprint; stack jobs declare `needs: gate` themselves, and `publish` declares `needs: ["*"]`,
// which expands to every other job.
func Release(head string, frags []Fragment) ([]byte, error) {
	jobs := map[string]map[string]any{}
	owner := map[string]string{}
	for _, f := range frags {
		for id, j := range f.RelJobs {
			if prev, dup := owner[id]; dup {
				return nil, fmt.Errorf("release job %q is contributed by both %q and %q", id, prev, f.Blueprint)
			}
			owner[id] = f.Blueprint
			jobs[id] = j
		}
	}
	expandAllNeeds(jobs)
	order := make([]string, 0, len(jobs))
	for id := range jobs {
		order = append(order, id)
	}
	sort.Strings(order)
	// gate first, publish last, the rest between.
	rank := func(id string) int {
		switch id {
		case "gate":
			return 0
		case "publish":
			return 2
		}
		return 1
	}
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i]) < rank(order[j]) })
	return workflow(head, jobs, order)
}

// expandAllNeeds replaces `needs: ["*"]` with every other job id (sorted), so a job such as `publish`
// can wait for all the jobs that other blueprints contribute without knowing their names.
func expandAllNeeds(jobs map[string]map[string]any) {
	for id, j := range jobs {
		needs := stringList(j["needs"])
		if len(needs) != 1 || needs[0] != "*" {
			continue
		}
		var all []string
		for other := range jobs {
			if other != id {
				all = append(all, other)
			}
		}
		sort.Strings(all)
		j["needs"] = all
	}
}

func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}

func workflow(head string, jobs map[string]map[string]any, order []string) ([]byte, error) {
	body, err := marshal(map[string]any{"jobs": jobsDoc(jobs, order)})
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(head, "\n") + "\n\n" + string(body)), nil
}

// Gitignore merges lines in plan order, dropping duplicates and keeping comments that are first seen.
func Gitignore(frags []Fragment) []byte {
	seen := map[string]bool{}
	var b strings.Builder
	for _, f := range frags {
		var group []string
		for _, l := range f.Gitignore {
			if !seen[l] {
				seen[l] = true
				group = append(group, l)
			}
		}
		if len(group) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# %s\n", f.Blueprint)
		for _, l := range group {
			b.WriteString(l + "\n")
		}
	}
	return []byte(b.String())
}

// Workspaces returns the merged bun workspace globs, sorted, without duplicates.
func Workspaces(frags []Fragment) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range frags {
		for _, w := range f.Workspaces {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Agents builds AGENTS.md: the intro followed by one "## <blueprint>" section per blueprint that has one.
func Agents(intro string, frags []Fragment) []byte {
	var b strings.Builder
	b.WriteString(strings.TrimRight(intro, "\n") + "\n")
	for _, f := range frags {
		if strings.TrimSpace(f.Agents) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", f.Blueprint, strings.TrimSpace(f.Agents))
	}
	return []byte(b.String())
}

// Lefthook builds lefthook.yml from the contributed hooks. A command name defined twice is an error.
func Lefthook(frags []Fragment) ([]byte, error) {
	hooks := map[string]map[string]any{}
	owner := map[string]string{}
	for _, f := range frags {
		for hook, cmds := range f.Lefthook {
			if hooks[hook] == nil {
				hooks[hook] = map[string]any{}
			}
			for name, c := range cmds["commands"] {
				k := hook + "/" + name
				if prev, dup := owner[k]; dup {
					return nil, fmt.Errorf("lefthook %s command %q is contributed by both %q and %q", hook, name, prev, f.Blueprint)
				}
				owner[k] = f.Blueprint
				hooks[hook][name] = c
			}
		}
	}
	names := make([]string, 0, len(hooks))
	for h := range hooks {
		names = append(names, h)
	}
	sort.Strings(names)
	doc := newOrdered()
	for _, h := range names {
		cmds := newOrdered()
		cn := make([]string, 0, len(hooks[h]))
		for n := range hooks[h] {
			cn = append(cn, n)
		}
		sort.Strings(cn)
		for _, n := range cn {
			cmds.set(n, hooks[h][n])
		}
		hook := newOrdered()
		if h == "pre-push" || h == "pre-commit" {
			hook.set("parallel", true)
		}
		hook.set("commands", cmds)
		doc.set(h, hook)
	}
	out, err := marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte("# lefthook: https://lefthook.dev. Composed by vkd from the blueprints in .vkd/answers.yaml.\n"), out...), nil
}

// Env merges .env.example lines, dropping exact duplicates.
func Env(frags []Fragment) []byte {
	seen := map[string]bool{}
	var b strings.Builder
	for _, f := range frags {
		for _, l := range f.Env {
			if !seen[l] {
				seen[l] = true
				b.WriteString(l + "\n")
			}
		}
	}
	return []byte(b.String())
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &n); err == nil && fmt.Sprint(n) == strings.TrimSpace(x) {
			return n, true
		}
	}
	return 0, false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
