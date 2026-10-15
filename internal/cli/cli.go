// Package cli is the vkd command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	vkd "github.com/vkdprojects/vkd"
	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/engine"
)

// Version is set by the linker (-X) on release builds.
var Version = "dev"

// App holds streams and where blueprint directories come from, so tests can drive the CLI in-process.
type App struct {
	Out, Err io.Writer
	In       io.Reader
	// IsTTY says whether prompting is possible. Tests set it; main derives it from the terminal.
	IsTTY func() bool
	// ConfigHome overrides $XDG_CONFIG_HOME (tests).
	ConfigHome string

	// args are the arguments of the current Run. Flags that depend on the loaded blueprints (--<blueprint>.<id>)
	// are declared before urfave parses anything, so --blueprints is read from here, never from os.Args.
	args []string
}

// New returns an App wired to the process streams.
func New() *App {
	return &App{Out: os.Stdout, Err: os.Stderr, In: os.Stdin, IsTTY: stdinIsTTY}
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Run executes args (without the program name) and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	a.args = args
	root := &cli.Command{
		Name:                  "vkd",
		Usage:                 "AI-first scaffolding that generates and updates projects",
		Version:               Version,
		Writer:                a.Out,
		ErrWriter:             a.Err,
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "blueprints", Usage: "extra blueprint directory (repeatable); later ones override earlier ones by name"},
		},
		Commands: []*cli.Command{
			a.createCmd(), a.addCmd(), a.updateCmd(), a.listCmd(), a.schemaCmd(), a.blueprintCmd(), a.mcpCmd(), a.versionCmd(),
		},
	}
	if err := root.Run(ctx, append([]string{"vkd"}, args...)); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		a.errln("vkd:", err)
		return 1
	}
	return 0
}

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// outf, outln and errln write to the CLI's streams. A failed write to a closed pipe has nowhere useful to be
// reported, so it is dropped here, once, instead of at every call site.
func (a *App) outf(format string, args ...any) { _, _ = fmt.Fprintf(a.Out, format, args...) }
func (a *App) outln(args ...any)               { _, _ = fmt.Fprintln(a.Out, args...) }
func (a *App) errln(args ...any)               { _, _ = fmt.Fprintln(a.Err, args...) }

// engine builds the engine: embedded blueprints, then $XDG_CONFIG_HOME/vkd/blueprints, then --blueprints dirs.
func (a *App) engine(cmd *cli.Command) (*engine.Engine, error) {
	return a.engineFor(cmd.Root().StringSlice("blueprints"))
}

// engineFor is engine with the --blueprints directories given directly (flags are declared before parsing).
func (a *App) engineFor(extra []string) (*engine.Engine, error) {
	reg := blueprint.NewRegistry()
	if err := reg.LoadFS(vkd.Blueprints, "blueprints", "builtin"); err != nil {
		return nil, err
	}
	if err := reg.LoadDir(filepath.Join(a.configHome(), "vkd", "blueprints")); err != nil {
		return nil, err
	}
	for _, d := range extra {
		if err := reg.LoadDir(d); err != nil {
			return nil, err
		}
	}
	versions, err := vkd.Versions()
	if err != nil {
		return nil, err
	}
	return &engine.Engine{Registry: reg, Versions: versions, Version: Version}, nil
}

func (a *App) configHome() string {
	if a.ConfigHome != "" {
		return a.ConfigHome
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// report prints a Result as JSON or text and returns the exit error for a failed one.
func (a *App) report(r *engine.Result, asJSON bool) error {
	if asJSON {
		if err := a.printJSON(r); err != nil {
			return err
		}
	} else {
		for _, f := range r.Files {
			a.outf("%-9s %s\n", f.Action, f.Path)
		}
		for _, w := range r.Warnings {
			a.errln("warning:", w)
		}
		for _, e := range r.Errors {
			a.errln("error:", e)
		}
	}
	if !r.OK {
		return &exitError{code: 1, msg: strings.Join(r.Errors, "; ")}
	}
	return nil
}

func (a *App) versionCmd() *cli.Command {
	return &cli.Command{
		Name: "version", Usage: "print the vkd version",
		Action: func(_ context.Context, _ *cli.Command) error {
			a.outln("vkd", Version)
			return nil
		},
	}
}

func (a *App) listCmd() *cli.Command {
	return &cli.Command{
		Name: "list", Usage: "list blueprints",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "machine-readable output"}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			eng, err := a.engine(cmd)
			if err != nil {
				return err
			}
			items := List(eng.Registry)
			if cmd.Bool("json") {
				return a.printJSON(map[string]any{"ok": true, "blueprints": items})
			}
			for _, it := range items {
				a.outf("%-14s %-6s %s\n", it.Name, it.Kind, it.Summary)
				for _, q := range it.Questions {
					a.outf("%-14s        --%s.%s  %s\n", "", it.Name, q.ID, q.Prompt)
				}
			}
			return nil
		},
	}
}

// ListItem is one entry of `vkd list --json` and the MCP vkd_list tool.
type ListItem struct {
	Name      string               `json:"name"`
	Kind      blueprint.Kind       `json:"kind"`
	Summary   string               `json:"summary"`
	Requires  []string             `json:"requires,omitempty"`
	Conflicts []string             `json:"conflicts,omitempty"`
	Auto      string               `json:"auto_when,omitempty"`
	Questions []blueprint.Question `json:"questions,omitempty"`
	Source    string               `json:"source"`
}

// List describes every blueprint in reg.
func List(reg *blueprint.Registry) []ListItem {
	var out []ListItem
	for _, b := range reg.All() {
		out = append(out, ListItem{Name: b.Name, Kind: b.Kind, Summary: b.Summary, Requires: b.Requires,
			Conflicts: b.Conflicts, Auto: b.When, Questions: b.Questions, Source: b.Source})
	}
	return out
}

// Licenses is re-exported for the interactive form.
var Licenses = answers.Licenses
