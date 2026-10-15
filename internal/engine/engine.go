// Package engine is the one code path behind the CLI and the MCP server: plan, create, add, update.
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/blueprint"
	"github.com/vkdprojects/vkd/internal/gitx"
	"github.com/vkdprojects/vkd/internal/plan"
	"github.com/vkdprojects/vkd/internal/update"
)

// StatePath is where answers are recorded, relative to the project root.
const StatePath = ".vkd/answers.yaml"

// State is .vkd/answers.yaml.
type State struct {
	VkdVersion string            `yaml:"vkd_version"`
	Answers    answers.Answers   `yaml:"answers"`
	Blueprints map[string]string `yaml:"blueprints"` // name -> content hash
}

// Engine holds what every operation needs.
type Engine struct {
	Registry *blueprint.Registry
	Versions map[string]any
	Version  string // vkd version, recorded in the state
}

func warnings(p *plan.Plan) []string {
	if p.Warnings == nil {
		return []string{}
	}
	return p.Warnings
}

// FileResult is one entry of the --json output.
type FileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// Result is the common --json output of create, add and update.
type Result struct {
	OK         bool         `json:"ok"`
	Files      []FileResult `json:"files"`
	Errors     []string     `json:"errors"`
	Blueprints []string     `json:"blueprints,omitempty"`
	Dir        string       `json:"dir,omitempty"`
	// Warnings are changes vkd made so the plan could compose (an action key moved off a clash). Never fatal.
	Warnings []string `json:"warnings"`
}

// Fail is the Result for an error that happened before there was anything to report.
func Fail(err error) *Result { return fail(err) }

func fail(err error) *Result {
	return &Result{OK: false, Files: []FileResult{}, Errors: []string{err.Error()}, Warnings: []string{}}
}

// Plan resolves and renders without writing anything.
func (e *Engine) Plan(a *answers.Answers) (*plan.Plan, error) {
	return plan.Build(e.Registry, a, e.Versions)
}

// PlanResult is Plan shaped as the --json output (every file is a "create").
func (e *Engine) PlanResult(a *answers.Answers) *Result {
	p, err := e.Plan(a)
	if err != nil {
		return fail(err)
	}
	r := &Result{OK: true, Files: []FileResult{}, Errors: []string{}, Blueprints: p.Blueprints, Warnings: warnings(p)}
	for _, f := range p.Files {
		r.Files = append(r.Files, FileResult{f.Path, string(update.Create)})
	}
	return r
}

// CreateOpts controls Create.
type CreateOpts struct {
	DryRun bool
	Git    bool // run `git init` when dir is not a repository (update needs one)
}

// Create writes a new project into dir. dir must be empty or absent: create never overwrites.
func (e *Engine) Create(dir string, a *answers.Answers, o CreateOpts) *Result {
	p, err := e.Plan(a)
	if err != nil {
		return fail(err)
	}
	if err := ensureEmpty(dir); err != nil {
		return fail(err)
	}
	res := &Result{OK: true, Files: []FileResult{}, Errors: []string{}, Blueprints: p.Blueprints, Dir: dir, Warnings: warnings(p)}
	for _, f := range p.Files {
		res.Files = append(res.Files, FileResult{f.Path, string(update.Create)})
	}
	state, err := e.state(p, a)
	if err != nil {
		return fail(err)
	}
	res.Files = append(res.Files, FileResult{StatePath, string(update.Create)})
	if o.DryRun {
		return res
	}
	if err := writePlan(dir, p); err != nil {
		return fail(err)
	}
	if err := writeState(dir, state); err != nil {
		return fail(err)
	}
	if o.Git {
		repo, err := openOrInit(dir)
		if err != nil {
			return fail(err)
		}
		if err := repo.WriteBase(update.Blobs(p), "vkd: create"); err != nil {
			return fail(err)
		}
	}
	return res
}

// Update re-renders the project in dir with the recorded answers and the current blueprints/versions and
// merges the result. add are extra components to include (used by Add).
func (e *Engine) Update(dir string, add []string, dryRun bool) *Result {
	st, err := ReadState(dir)
	if err != nil {
		return fail(err)
	}
	a := st.Answers
	a.Components = append(append([]string(nil), a.Components...), add...)
	p, err := e.Plan(&a)
	if err != nil {
		return fail(err)
	}
	repo, err := gitx.Open(dir)
	if err != nil {
		return fail(err)
	}
	if !sameDir(dir, repo.Dir) {
		// the project is a subdirectory of the repo: paths in the ref are relative to the project, so refuse
		// rather than guess.
		return fail(fmt.Errorf("%s is not the root of its git repository (%s); run vkd update from the repository root", dir, repo.Dir))
	}
	results, err := update.Apply(repo, dir, p, dryRun)
	if err != nil {
		return fail(err)
	}
	res := &Result{OK: true, Files: []FileResult{}, Errors: []string{}, Blueprints: p.Blueprints, Dir: dir, Warnings: warnings(p)}
	for _, r := range results {
		if r.Action == update.Conflict {
			res.OK = false
			res.Errors = append(res.Errors, r.Path+": merge conflict, resolve the <<<<<<< markers")
		}
		res.Files = append(res.Files, FileResult{r.Path, string(r.Action)})
	}
	if dryRun {
		return res
	}
	state, err := e.state(p, &a)
	if err != nil {
		return fail(err)
	}
	if err := writeState(dir, state); err != nil {
		return fail(err)
	}
	if err := repo.WriteBase(update.Blobs(p), "vkd: update"); err != nil {
		return fail(err)
	}
	return res
}

// ReadState loads .vkd/answers.yaml.
func ReadState(dir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(dir, StatePath))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s not found in %s: not a vkd project", StatePath, dir)
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", StatePath, err)
	}
	return &st, nil
}

func (e *Engine) state(p *plan.Plan, a *answers.Answers) (*State, error) {
	st := &State{VkdVersion: e.Version, Answers: *a, Blueprints: map[string]string{}}
	for _, n := range p.Blueprints {
		b, _ := e.Registry.Get(n)
		h, err := plan.Hash(b)
		if err != nil {
			return nil, err
		}
		st.Blueprints[n] = h
	}
	return st, nil
}

// WriteAnswers rewrites .vkd/answers.yaml with st (used by `vkd add --set`, before the update that applies it).
func WriteAnswers(dir string, st *State) error { return writeState(dir, st) }

func writeState(dir string, st *State) error {
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}
	header := "# Recorded by vkd. `vkd update` re-renders with these answers; change them with `vkd add` or by editing\n# this file and running `vkd update`.\n"
	abs := filepath.Join(dir, StatePath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, append([]byte(header), data...), 0o644)
}

func writePlan(dir string, p *plan.Plan) error {
	for _, f := range p.Files {
		abs := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, f.Content, 0o644); err != nil {
			return err
		}
		if err := os.Chmod(abs, f.Mode); err != nil {
			return err
		}
	}
	return nil
}

func ensureEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, en := range entries {
		if en.Name() != ".git" {
			names = append(names, en.Name())
		}
	}
	if len(names) > 0 {
		return fmt.Errorf("%s is not empty (%s): vkd create never overwrites; use `vkd add` or `vkd update` in an existing project", dir, strings.Join(names, ", "))
	}
	return nil
}

func openOrInit(dir string) (*gitx.Repo, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if r, err := gitx.Open(dir); err == nil {
		if sameDir(dir, r.Dir) {
			return r, nil
		}
	}
	return gitx.Init(dir)
}

// sameDir compares two directories after resolving symlinks (/var is /private/var on macOS).
func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	ra, err1 := filepath.EvalSymlinks(aa)
	rb, err2 := filepath.EvalSymlinks(bb)
	if err1 != nil || err2 != nil {
		return false
	}
	return ra == rb
}
