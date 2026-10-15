package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/engine"
)

// blueprintFlags declares one --<blueprint>.<id> flag per question of every known blueprint, so
// `vkd create --go-api.module github.com/acme/x` works and shows up in --help. The set is read from the same
// directories the command will use, found by scanning the raw arguments for --blueprints (urfave builds flags
// before it parses them).
func (a *App) blueprintFlags(args []string) []cli.Flag {
	eng, err := a.engineFor(scanBlueprintDirs(args))
	if err != nil {
		return nil
	}
	var flags []cli.Flag
	for _, b := range eng.Registry.All() {
		for _, q := range b.Questions {
			usage := q.Prompt
			if q.Default != "" {
				usage += " (default " + q.Default + ")"
			}
			flags = append(flags, &cli.StringFlag{Name: b.Name + "." + q.ID, Usage: usage, Category: "blueprint " + b.Name})
		}
	}
	return flags
}

func (a *App) createFlags(args []string) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "stack", Usage: "comma-separated blueprints to include (see `vkd list`)"},
		&cli.BoolFlag{Name: "oss", Usage: "open source project: adds CONTRIBUTING, DCO check, issue and PR templates"},
		&cli.BoolFlag{Name: "no-oss", Usage: "closed source project (default)"},
		&cli.StringFlag{Name: "license", Usage: "MIT, Apache-2.0, AGPL-3.0 or proprietary (default MIT with --oss, else proprietary)"},
		&cli.StringFlag{Name: "owner", Usage: "GitHub owner (user or organization) of the repository"},
		&cli.StringFlag{Name: "description", Usage: "one-line project description"},
		&cli.IntFlag{Name: "port-base", Usage: "first port of the project's block of 10 (default derived from the name)"},
		&cli.StringSliceFlag{Name: "set", Usage: "answer a blueprint question: blueprint.id=value (repeatable)"},
		&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "never prompt: use flags and defaults, fail naming the missing flag"},
		&cli.BoolFlag{Name: "dry-run", Usage: "show what would be written, write nothing"},
		&cli.BoolFlag{Name: "json", Usage: "machine-readable output"},
		&cli.BoolFlag{Name: "git", Usage: "git init and record refs/vkd/base so `vkd update` works (default on; --git=false to skip)", Value: true},
		&cli.BoolFlag{Name: "install", Usage: "run the install step of the stacks (bun install, go mod download) after creating"},
	}
	return append(flags, a.blueprintFlags(args)...)
}

func (a *App) createCmd() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "create a project",
		ArgsUsage: "[name]",
		Flags:     a.createFlags(a.args),
		Action:    a.create,
	}
}

// collect builds Answers from flags. It reports every missing required global answer at once, by flag.
func collect(cmd *cli.Command, name string) (*answers.Answers, error) {
	ans := &answers.Answers{
		Name: name, Owner: cmd.String("owner"), Description: cmd.String("description"),
		License: cmd.String("license"), PortBase: cmd.Int("port-base"),
		OpenSource: cmd.Bool("oss") && !cmd.Bool("no-oss"),
	}
	if cmd.Bool("oss") && cmd.Bool("no-oss") {
		return nil, errors.New("--oss and --no-oss are mutually exclusive")
	}
	if s := cmd.String("stack"); s != "" {
		for _, c := range strings.Split(s, ",") {
			if c = strings.TrimSpace(c); c != "" {
				ans.Components = append(ans.Components, c)
			}
		}
	}
	for _, f := range cmd.Flags {
		n := f.Names()[0]
		if strings.Contains(n, ".") && cmd.IsSet(n) {
			bp, id, _ := strings.Cut(n, ".")
			ans.Set(bp, id, cmd.String(n))
		}
	}
	for _, s := range cmd.StringSlice("set") {
		bp, id, v, err := answers.ParseSet(s)
		if err != nil {
			return nil, err
		}
		ans.Set(bp, id, v)
	}
	return ans, nil
}

func missingGlobals(ans *answers.Answers, nonInteractive bool) []string {
	var m []string
	if ans.Name == "" {
		m = append(m, "NAME (first argument)")
	}
	if ans.Owner == "" {
		m = append(m, "--owner")
	}
	return m
}

func (a *App) create(ctx context.Context, cmd *cli.Command) error {
	asJSON := cmd.Bool("json")
	eng, err := a.engine(cmd)
	if err != nil {
		return a.fatal(err, asJSON)
	}
	ans, err := collect(cmd, cmd.Args().First())
	if err != nil {
		return a.fatal(err, asJSON)
	}

	interactive := !cmd.Bool("yes") && !asJSON && a.IsTTY != nil && a.IsTTY()
	if interactive {
		if err := promptMissing(a, eng, ans, cmd); err != nil {
			return a.fatal(err, asJSON)
		}
	}
	if missing := missingGlobals(ans, !interactive); len(missing) > 0 {
		return a.fatal(fmt.Errorf("missing required answers: %s", strings.Join(missing, ", ")), asJSON)
	}

	dir := ans.Name
	res := eng.Create(dir, ans, engine.CreateOpts{DryRun: cmd.Bool("dry-run"), Git: cmd.Bool("git")})
	if res.OK && cmd.Bool("install") && !cmd.Bool("dry-run") {
		if err := install(a, dir, res.Blueprints); err != nil {
			res.OK = false
			res.Errors = append(res.Errors, err.Error())
		}
	}
	return a.report(res, asJSON)
}

// fatal reports a failure that happened before there was a Result.
func (a *App) fatal(err error, asJSON bool) error {
	return a.report(engine.Fail(err), asJSON)
}

func (a *App) addCmd() *cli.Command {
	return &cli.Command{
		Name: "add", Usage: "add a component to an existing vkd project", ArgsUsage: "<blueprint>...",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Value: ".", Usage: "project directory"},
			&cli.BoolFlag{Name: "dry-run", Usage: "show what would change, write nothing"},
			&cli.BoolFlag{Name: "json", Usage: "machine-readable output"},
			&cli.StringSliceFlag{Name: "set", Usage: "answer a blueprint question: blueprint.id=value (repeatable)"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			asJSON := cmd.Bool("json")
			eng, err := a.engine(cmd)
			if err != nil {
				return a.fatal(err, asJSON)
			}
			if cmd.Args().Len() == 0 {
				return a.fatal(errors.New("usage: vkd add <blueprint> [blueprint...]"), asJSON)
			}
			dir := cmd.String("dir")
			st, err := engine.ReadState(dir)
			if err != nil {
				return a.fatal(err, asJSON)
			}
			for _, s := range cmd.StringSlice("set") {
				bp, id, v, err := answers.ParseSet(s)
				if err != nil {
					return a.fatal(err, asJSON)
				}
				st.Answers.Set(bp, id, v)
			}
			if err := engine.WriteAnswers(dir, st); err != nil {
				return a.fatal(err, asJSON)
			}
			return a.report(eng.Update(dir, cmd.Args().Slice(), cmd.Bool("dry-run")), asJSON)
		},
	}
}

func (a *App) updateCmd() *cli.Command {
	return &cli.Command{
		Name: "update", Usage: "re-render with current blueprints and versions and merge into the project",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Value: ".", Usage: "project directory"},
			&cli.BoolFlag{Name: "dry-run", Usage: "show what would change, write nothing"},
			&cli.BoolFlag{Name: "json", Usage: "machine-readable output"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			asJSON := cmd.Bool("json")
			eng, err := a.engine(cmd)
			if err != nil {
				return a.fatal(err, asJSON)
			}
			return a.report(eng.Update(cmd.String("dir"), nil, cmd.Bool("dry-run")), asJSON)
		},
	}
}

// install runs the install step the blueprints in the plan need. It only runs tools that are present.
func install(a *App, dir string, blueprints []string) error {
	has := map[string]bool{}
	for _, b := range blueprints {
		has[b] = true
	}
	type step struct {
		when bool
		dir  string
		cmd  []string
	}
	steps := []step{
		{has["bun-workspace"], ".", []string{"bun", "install"}},
		{has["go-api"], "apps/api", []string{"go", "mod", "tidy"}},
	}
	for _, s := range steps {
		if !s.when {
			continue
		}
		if err := runIn(a, filepath.Join(dir, s.dir), s.cmd); err != nil {
			return err
		}
	}
	return nil
}

// scanBlueprintDirs finds --blueprints values in raw arguments: `--blueprints DIR` and `--blueprints=DIR`.
func scanBlueprintDirs(args []string) []string {
	var dirs []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--blueprints" && i+1 < len(args):
			dirs = append(dirs, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--blueprints="):
			dirs = append(dirs, strings.TrimPrefix(args[i], "--blueprints="))
		}
	}
	return dirs
}
