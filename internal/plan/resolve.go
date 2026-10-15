// Package plan turns answers into an ordered blueprint set and then into a list of files.
package plan

import (
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/render"
)

// Resolve returns the blueprints to apply, in application order:
//   - base blueprints first, then the rest in dependency order (a blueprint after everything it requires),
//     ties broken by name so the output is deterministic;
//   - the user's components plus everything they require, plus every blueprint whose `when` is truthy
//     (evaluated against the answers and the set selected so far, until it stops growing).
//
// It fails on unknown names, missing requirements, conflicts and require cycles.
func Resolve(reg *blueprint.Registry, a *answers.Answers, env *render.Env) ([]*blueprint.Blueprint, error) {
	sel := map[string]bool{}
	var queue []string
	push := func(n string) {
		if !sel[n] {
			sel[n] = true
			queue = append(queue, n)
		}
	}
	for _, b := range reg.All() {
		if b.Kind == blueprint.KindBase {
			push(b.Name)
		}
	}
	for _, c := range a.UniqueComponents() {
		b, ok := reg.Get(c)
		if !ok {
			return nil, fmt.Errorf("unknown blueprint %q (known: %s)", c, strings.Join(reg.Names(), ", "))
		}
		if b.Kind == blueprint.KindBase {
			continue // always on
		}
		push(c)
	}

	// Close over requires and `when`, repeating until nothing is added.
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(queue); i++ {
			b, _ := reg.Get(queue[i])
			for _, r := range b.Requires {
				if _, ok := reg.Get(r); !ok {
					return nil, fmt.Errorf("blueprint %q requires unknown blueprint %q", b.Name, r)
				}
				if !sel[r] {
					push(r)
					changed = true
				}
			}
		}
		for _, b := range reg.All() {
			if sel[b.Name] || b.When == "" {
				continue
			}
			ok, err := evalWhen(b, a, selectedNames(sel))
			if err != nil {
				return nil, err
			}
			if ok {
				push(b.Name)
				changed = true
			}
		}
	}

	for name := range sel {
		b, _ := reg.Get(name)
		for _, c := range b.Conflicts {
			if sel[c] {
				return nil, fmt.Errorf("blueprint %q conflicts with %q", b.Name, c)
			}
		}
	}
	return order(reg, sel)
}

func selectedNames(sel map[string]bool) []string {
	out := make([]string, 0, len(sel))
	for n := range sel {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// evalWhen evaluates a blueprint's `when` expression. It is the body of a template action, so
// `when: .OpenSource` and `when: or (has "sveltekit-web") (has "astro-site")` both work. The result is
// truthy unless it renders to "", "false", "0" or "<no value>".
func evalWhen(b *blueprint.Blueprint, a *answers.Answers, selected []string) (bool, error) {
	env := render.NewEnv(nil, selected, a.PortBase)
	funcs := env.Funcs()
	ctx := render.NewContext(a)
	text := "{{ if " + b.When + " }}true{{ end }}"
	t, err := template.New("when:" + b.Name).Funcs(funcs).Option("missingkey=error").Parse(text)
	if err != nil {
		return false, fmt.Errorf("blueprint %q: when %q: %w", b.Name, b.When, err)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, ctx); err != nil {
		return false, fmt.Errorf("blueprint %q: when %q: %w", b.Name, b.When, err)
	}
	return sb.String() == "true", nil
}

// order sorts the selected blueprints: base first, then requires-before-dependents, ties by name.
func order(reg *blueprint.Registry, sel map[string]bool) ([]*blueprint.Blueprint, error) {
	names := selectedNames(sel)
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var out []*blueprint.Blueprint
	var visit func(n string, path []string) error
	visit = func(n string, path []string) error {
		switch state[n] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("requires cycle: %s", strings.Join(append(path, n), " -> "))
		}
		state[n] = 1
		b, _ := reg.Get(n)
		reqs := append([]string(nil), b.Requires...)
		sort.Strings(reqs)
		for _, r := range reqs {
			if err := visit(r, append(path, n)); err != nil {
				return err
			}
		}
		state[n] = 2
		out = append(out, b)
		return nil
	}
	// base kind first (stable by name), then everything else.
	for _, n := range names {
		if b, _ := reg.Get(n); b.Kind == blueprint.KindBase {
			if err := visit(n, nil); err != nil {
				return nil, err
			}
		}
	}
	for _, n := range names {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}
