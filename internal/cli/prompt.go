package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/urfave/cli/v3"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/engine"
	"github.com/vkdprojects/vkd/internal/render"
)

// promptMissing asks, with huh, only for what flags did not already answer. It runs only on a TTY and never
// with --yes or --json (the caller checks).
func promptMissing(a *App, eng *engine.Engine, ans *answers.Answers, cmd *cli.Command) error {
	var fields []huh.Field
	if ans.Name == "" {
		fields = append(fields, huh.NewInput().Title("Project name").Description("lowercase letters, digits, dashes").
			Value(&ans.Name).Validate(func(s string) error {
			probe := answers.Answers{Name: s, Owner: "x", License: "MIT", PortBase: 3000}
			if err := probe.Validate(); err != nil && strings.Contains(err.Error(), "name") {
				return fmt.Errorf("use lowercase letters, digits and dashes, starting with a letter")
			}
			return nil
		}))
	}
	if ans.Owner == "" {
		fields = append(fields, huh.NewInput().Title("GitHub owner").Description("user or organization").Value(&ans.Owner))
	}
	if !cmd.IsSet("oss") && !cmd.IsSet("no-oss") {
		fields = append(fields, huh.NewConfirm().Title("Open source?").
			Description("adds CONTRIBUTING, DCO check, Code of Conduct, issue and PR templates").Value(&ans.OpenSource))
	}
	var stacks []string
	if len(ans.Components) == 0 {
		var opts []huh.Option[string]
		for _, b := range eng.Registry.All() {
			if b.Kind == blueprint.KindStack {
				opts = append(opts, huh.NewOption(b.Name+" - "+b.Summary, b.Name))
			}
		}
		if len(opts) > 0 {
			fields = append(fields, huh.NewMultiSelect[string]().Title("Stacks").Options(opts...).Value(&stacks))
		}
	}
	if len(fields) == 0 {
		return nil
	}
	form := huh.NewForm(huh.NewGroup(fields...))
	if os.Getenv("ACCESSIBLE") != "" {
		form = form.WithAccessible(true)
	}
	if err := form.Run(); err != nil {
		return err
	}
	ans.Components = append(ans.Components, stacks...)

	// Required blueprint questions that still have no answer and no default.
	ans.Defaults()
	probe := render.NewEnv(nil, nil, ans.PortBase)
	var qfields []huh.Field
	for _, name := range ans.Components {
		b, ok := eng.Registry.Get(name)
		if !ok {
			continue
		}
		for _, q := range b.Questions {
			if ans.Get(b.Name, q.ID) != "" || q.Default != "" && !q.Required {
				continue
			}
			v := new(string)
			if q.Default != "" {
				d, err := probe.String(q.ID, q.Default, render.NewContext(ans))
				if err == nil {
					*v = d
				}
			}
			bp, id := b.Name, q.ID
			qfields = append(qfields, huh.NewInput().Title(q.Prompt).Value(v).Validate(func(s string) error {
				ans.Set(bp, id, s)
				return nil
			}))
		}
	}
	if len(qfields) > 0 {
		return huh.NewForm(huh.NewGroup(qfields...)).Run()
	}
	return nil
}

// runIn runs a command in dir, streaming its output to the app's streams. A missing tool is an error that
// names it: --install was asked for explicitly.
func runIn(a *App, dir string, argv []string) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("--install: %s is not installed", argv[0])
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Stdout, c.Stderr = a.Err, a.Err
	if err := c.Run(); err != nil {
		return fmt.Errorf("--install: %s in %s: %w", strings.Join(argv, " "), dir, err)
	}
	return nil
}
