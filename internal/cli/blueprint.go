package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/schema"
)

func (a *App) schemaCmd() *cli.Command {
	return &cli.Command{
		Name: "schema", Usage: "print the JSON Schema of the create input",
		Action: func(_ context.Context, cmd *cli.Command) error {
			eng, err := a.engine(cmd)
			if err != nil {
				return err
			}
			return a.printJSON(schema.Create(eng.Registry))
		},
	}
}

func (a *App) blueprintCmd() *cli.Command {
	return &cli.Command{
		Name: "blueprint", Usage: "author blueprints",
		Commands: []*cli.Command{
			{
				Name: "new", Usage: "scaffold a blueprint directory", ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dir", Value: ".", Usage: "directory the blueprint directory is created in"},
					&cli.StringFlag{Name: "kind", Value: "addon", Usage: "stack or addon"},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return fmt.Errorf("usage: vkd blueprint new <name> [--dir DIR]")
					}
					return a.scaffold(cmd.String("dir"), name, blueprint.Kind(cmd.String("kind")))
				},
			},
			{
				Name: "lint", Usage: "validate a blueprint directory by rendering it", ArgsUsage: "<dir>",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "machine-readable output"}},
				Action: func(_ context.Context, cmd *cli.Command) error {
					dir := cmd.Args().First()
					if dir == "" {
						return fmt.Errorf("usage: vkd blueprint lint <dir>")
					}
					return a.lint(cmd, dir)
				},
			},
		},
	}
}

func (a *App) scaffold(parent, name string, kind blueprint.Kind) error {
	if kind != blueprint.KindStack && kind != blueprint.KindAddon {
		return fmt.Errorf("--kind must be stack or addon, got %q", kind)
	}
	dir := filepath.Join(parent, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return err
	}
	yaml := fmt.Sprintf(`name: %[1]s
kind: %[2]s
summary: "One line saying what %[1]s adds to a project"
requires: [base]
# when: .OpenSource        # addons only: include automatically when this expression is truthy
# target: apps/%[1]s       # where files/ lands (a template); default is the project root
questions: []
#  - id: module
#    prompt: Go module path
#    default: "github.com/{{.Owner}}/{{.Name}}"
contributes:
  gitignore: []
  agents: |
    How an agent runs, tests and changes %[1]s.
`, name, kind)
	if err := os.WriteFile(filepath.Join(dir, "blueprint.yaml"), []byte(yaml), 0o644); err != nil {
		return err
	}
	// Files must not collide with another blueprint's: namespace the example under docs/<blueprint>.md.
	if err := os.MkdirAll(filepath.Join(dir, "files", "docs"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "docs", name+".md.tmpl"), []byte("# "+name+" in {{.Name}}\n"), 0o644); err != nil {
		return err
	}
	a.outf("created %s\nnext: edit it, then `vkd blueprint lint %s` and `vkd --blueprints %s create demo --stack %s --owner you --yes --dry-run`\n",
		dir, dir, parent, name)
	return nil
}

// lint loads dir (schema validation), then builds a plan with it selected over the built-in blueprints and
// sample answers, so a template that does not parse or reads an unknown field fails here and not at create time.
func (a *App) lint(cmd *cli.Command, dir string) error {
	asJSON := cmd.Bool("json")
	b, err := blueprint.LoadOne(dir)
	if err != nil {
		return a.fatal(err, asJSON)
	}
	eng, err := a.engineFor(append(scanBlueprintDirs(a.args), filepath.Dir(filepath.Clean(dir))))
	if err != nil {
		return a.fatal(err, asJSON)
	}
	// The directory under lint wins over a same-named blueprint loaded from its parent.
	eng.Registry.Add(b)
	ans := lintAnswers(b)
	res := eng.PlanResult(ans)
	if res.OK && !asJSON {
		a.outf("ok: %s (%s) renders %d files with sample answers\n", b.Name, b.Kind, len(res.Files))
		return nil
	}
	return a.report(res, asJSON)
}

// lintAnswers are sample answers that select b: open source (so addons guarded by .OpenSource render too).
func lintAnswers(b *blueprint.Blueprint) *answers.Answers {
	ans := &answers.Answers{Name: "sample", Owner: "acme", Description: "Sample project", OpenSource: true, License: "MIT"}
	if b.Kind != blueprint.KindBase {
		ans.Components = []string{b.Name}
	}
	for _, q := range b.Questions {
		if q.Default == "" {
			v := "sample"
			if len(q.Options) > 0 {
				v = q.Options[0]
			}
			ans.Set(b.Name, q.ID, v)
		}
	}
	return ans
}
