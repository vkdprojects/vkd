# vkd

An AI-first scaffolding CLI that **generates and updates** projects.

```bash
vkd create demo --stack go-api --oss --owner acme --yes
cd demo && atlas dev
# months later, after vkd itself improved:
vkd update          # re-renders and three-way merges; your edits are kept
```

Every project gets the same professional skeleton: one `atlas.yaml` for the dev environment, one `ci.yml` with a
single required `CI` check, `pr.yml` (Conventional Commits title, DCO for open source), `release.yml` on a `v*` tag
gated on a green CI, `AGENTS.md`, agent skills, `llms.txt`, lefthook and pinned tool versions.

## Install

```bash
go install github.com/vkdprojects/vkd/cmd/vkd@latest
```

Or download a binary (darwin and linux, amd64 and arm64) from the [releases](https://github.com/vkdprojects/vkd/releases)
and verify it against `SHA256SUMS`. vkd needs `git` for `create` and `update`. It has no telemetry and makes no network calls.

## Usage

```bash
vkd create [name] [--stack go-api,...] [--oss|--no-oss] [--license MIT|Apache-2.0|AGPL-3.0|proprietary]
           [--owner X] [--description X] [--set blueprint.id=value]... [--yes] [--dry-run] [--json] [--install]
vkd add <blueprint>...       # add a component to an existing project
vkd update [--dry-run]       # re-render with current blueprints and versions, merge into the project
vkd list [--json]            # blueprints, what they do and the questions they ask
vkd schema                   # JSON Schema of the create input
vkd blueprint new <name>     # scaffold a blueprint; `vkd blueprint lint <dir>` validates it
vkd mcp                      # MCP server on stdio
```

On a terminal, `vkd create` asks only what you did not pass as a flag. With `--yes`, or without a terminal, it never
prompts: a missing required answer is an error that names the flag (`missing required answers: --owner`).

Each blueprint question is also a flag: `--go-api.module github.com/acme/demo`, same as `--set go-api.module=...`.

### How `update` works

`vkd create` records your answers in `.vkd/answers.yaml` and the pristine render in the git ref `refs/vkd/base`.
`vkd update` renders the new pristine output and compares three versions of every file: the previous pristine (base),
yours, and the new one.

| You | Template | Result |
|---|---|---|
| unchanged | changed | overwritten (`update`) |
| changed | unchanged | kept |
| changed | changed | merged with `git merge-file` (`merge`, or `conflict` with markers, exit 1) |
| unchanged | dropped the file | deleted |
| deleted it | changed | stays deleted |

It never commits your worktree and never touches your index. Review with `git diff`.

## AI usage

Every command has a non-interactive form and `--json` output: `{"ok": bool, "files": [{"path", "action"}], "errors": []}`.

- `vkd schema` is the machine-readable input contract; `vkd list --json` says what each blueprint adds.
- `vkd create --dry-run --json` plans without writing.
- `vkd mcp` serves five tools over stdio: `vkd_list`, `vkd_schema`, `vkd_plan`, `vkd_create`, `vkd_update`.
  Add it to an MCP client as `{"command": "vkd", "args": ["mcp"]}`. The tools take the same answers as the schema.
  Suggested flow: `vkd_list`, `vkd_schema`, `vkd_plan`, then `vkd_create`.
- Generated projects ship `AGENTS.md`, `.agents/skills/<project>/SKILL.md` and `llms.txt`.

## Blueprints

A blueprint is a directory of plain files plus one `blueprint.yaml`. Adding one needs no Go code. Built in:

| Blueprint | Kind | |
|---|---|---|
| `base` | base | always: README, license, AGENTS.md, atlas.yaml, CI, PR checks, release, lefthook |
| `oss` | addon | automatic with `--oss`: CONTRIBUTING (DCO), Code of Conduct, SECURITY, issue and PR templates, labels |
| `bun-workspace` | addon | automatic with any JavaScript stack: bun workspaces, turbo, biome |
| `go-api` | stack | Go HTTP API: net/http, slog, graceful shutdown, tests, Docker image to GHCR on release |

Write your own: [docs/blueprints.md](docs/blueprints.md). Personal blueprints live in
`$XDG_CONFIG_HOME/vkd/blueprints/`; `--blueprints DIR` adds more. A later source overrides an earlier one by name.

## Philosophy

- **Low bloat.** Few dependencies in vkd, pinned versions in what it generates (`versions.yaml`), nothing set to
  `latest`, no telemetry. Each blueprint adds what the stack needs and nothing else.
- **Generate and update.** A scaffold you cannot update is a fork you maintain forever. vkd records how a project was
  made, so improvements to a blueprint reach existing projects as a reviewable merge.
- **Reviewable output.** Blueprints are plain files in the repository, not a packed blob. What you will get is what you
  can read in `blueprints/`, and `--dry-run` shows it before anything is written.
- **A small, curated surface.** Compared with [create-better-t-stack](https://github.com/AmanVarshney01/create-better-t-stack),
  which composes a large option matrix of the newest dependencies, vkd ships few opinionated stacks, pins every version
  in one file, validates combinations as data (`requires`, `conflicts`) and has no bypass flag.
- **Agents are first-class.** The CLI, the JSON output, the schema, the MCP server and the generated `AGENTS.md` are one
  contract, not separate features.

## License

MIT, see [LICENSE](LICENSE). Contributing: [CONTRIBUTING.md](CONTRIBUTING.md).
