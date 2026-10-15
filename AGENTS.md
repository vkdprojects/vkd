# AGENTS.md

Guidance for AI coding agents (and humans) working in **vkd**. Read it first. User-facing docs: [README.md](README.md).
Writing a blueprint: [docs/blueprints.md](docs/blueprints.md).

## What vkd is

A Go CLI that generates and updates projects from blueprints (plain files + `blueprint.yaml`). `vkd create` renders and
records `.vkd/answers.yaml` plus the pristine render in `refs/vkd/base`; `vkd update` three-way merges a new render
into the user's edits with `git merge-file`. The same engine backs the CLI and the MCP server.

## Ground rules

- **Do not push to `main`.** Branch `type/short-desc`, open a pull request. The PR title is a Conventional Commits
  subject; every commit is signed off (`git commit -s`), see [CONTRIBUTING.md](CONTRIBUTING.md).
- **Tests come first.** For a bug: write the regression test, watch it fail for the right reason, then fix it. A bug
  in the Go/JSON contract (what the CLI, `--json` or MCP emits) needs a test that exercises the real serialized output,
  not a hand-written mock.
- **Prove a test can fail.** After a green first run, break the code on purpose and watch the test go red.
- No new dependency without a reason. Today: `urfave/cli/v3`, `charm.land/huh/v2`, `gopkg.in/yaml.v3`.
- No telemetry, no network calls. Versions are pinned in `versions.yaml`; never `latest`, never a floating tag for an action.
- Generated output must pass its own tools. A blueprint change is not done until `go test -tags e2e ./e2e` is green.

## Commands

```bash
go build ./... && go vet ./... && go test ./...      # the gate; CI also runs -race
go test -tags e2e ./e2e                              # creates each preset and runs the stack's own checks
go run ./cmd/vkd create demo --stack go-api --oss --owner acme --yes --dry-run --json
scripts/lint-workflows.sh                            # actionlint + zizmor over .github/
golangci-lint run                                    # config: .golangci.yml
```

E2E steps need `go`, `bun`, `actionlint`, `zizmor`, `atlas`, `shellcheck`; a missing tool skips its step, never fakes it.

## Layout

| Path | Role |
|---|---|
| `cmd/vkd` | `main` |
| `internal/cli` | commands, flags (including one `--<blueprint>.<id>` per question), huh prompts |
| `internal/engine` | create / update / add: the one code path under the CLI and MCP |
| `internal/blueprint` | load and validate `blueprint.yaml`; registry; later sources override by name |
| `internal/plan` | resolve answers to an ordered blueprint set; render every file |
| `internal/render` | `text/template` with delimiters `{{ }}`, `${{ }}` passthrough, funcs |
| `internal/compose` | merge contributions: atlas.yaml, ci.yml (aggregate `CI`), release.yml, .gitignore, AGENTS.md, lefthook |
| `internal/update` | the 3-way decision table and merge |
| `internal/gitx` | the few git plumbing calls (`refs/vkd/base`, `merge-file`) |
| `internal/mcp` | stdio JSON-RPC server, protocol 2025-06-18 |
| `internal/schema` | JSON Schema of the create input, derived from the loaded blueprints |
| `blueprints/` | shipped blueprints, embedded (`embed.go`) |
| `versions.yaml` | every pinned version and action SHA |

## Invariants (each has a test)

- Rendering is deterministic: no clock, no randomness at render time (`Year` is recorded at create). `vkd update`
  right after `vkd create` changes nothing.
- A `.go` or `go.mod` file inside `blueprints/` would be compiled as vkd: name template Go `*.go.tmpl`.
- Action pins are full commit SHAs with a trailing `# vX` comment, and the comment is a real YAML comment.
- The aggregate `CI` job needs every other job and accepts only `success`.
- A tool's advertised MCP `inputSchema` equals what it accepts; unknown arguments are errors, not ignored.
- `update` never commits the worktree or touches the index, never overwrites an edited file silently, and keeps
  reporting a conflict until its markers are gone.
- Release workflows never restore a build cache (`cache: false`): zizmor's `cache-poisoning`.
- Failures name the flag that fixes them (`missing required answers: --owner`).
