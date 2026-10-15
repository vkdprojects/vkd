# Writing a blueprint

A blueprint is a directory of plain files plus one `blueprint.yaml`. Adding one needs no Go code. This is the whole
contract: if something is not here, it does not exist.

Contents: [Layout](#layout) · [blueprint.yaml](#blueprintyaml) · [Templates](#templates) · [Template functions](#template-functions) ·
[Template data](#template-data) · [Questions](#questions) · [Contributes](#contributes) · [Composed files](#composed-files-base-only) ·
[File rules](#file-rules) · [Resolution](#how-blueprints-are-selected-and-ordered) · [Add a blueprint](#add-a-blueprint-step-by-step) ·
[Test it](#test-it) · [Conventions](#conventions-for-a-stack) · [Pitfalls](#pitfalls)

## Layout

```text
blueprints/<name>/
  blueprint.yaml
  files/                  # copied into the project; `*.tmpl` are rendered and lose the suffix
    README.md.tmpl
    cmd/{{.Name}}/main.go.tmpl      # path segments may be templates
    static.png                      # no .tmpl: copied byte for byte
```

- The directory name must equal `name` in `blueprint.yaml`.
- Files land under `target` (default: the project root). A file in `files/cmd/x.go.tmpl` of a blueprint with
  `target: apps/api` is written to `apps/api/cmd/x.go`.
- Files ending in `.sh` (before `.tmpl`) are written executable; everything else is `0644`.
- Dotfiles and dot directories (`.github/`, `.gitignore`) are fine; they are embedded with `all:`.
- Two blueprints writing the same output path is an error that names both. Namespace shared directories, for example
  put a stack's docs in `docs/<name>.md`, never `README.md`.

## blueprint.yaml

```yaml
name: go-api                  # ^[a-z][a-z0-9-]*$, equal to the directory name
kind: stack                   # base | stack | addon
summary: Go HTTP API (net/http, slog), tested, with CI and a Docker image
requires: [base]              # blueprints that must be in the plan; added automatically
conflicts: []                 # blueprints that cannot be in the plan together with this one
when: ""                      # addons: a template expression; truthy = included automatically
target: apps/api              # where files/ lands; a template; default "."
questions: []                 # see Questions
files: []                     # see File rules
composed: []                  # base only, see Composed files
contributes: {}               # see Contributes
```

| Field | Required | Notes |
|---|---|---|
| `name` | yes | unique; a later source (see [Resolution](#how-blueprints-are-selected-and-ordered)) overrides the same name |
| `kind` | yes | `base` always applies and cannot `requires`; `stack` is chosen with `--stack`; `addon` is usually chosen by `when` |
| `summary` | yes | one line; shown by `vkd list` and the MCP `vkd_list` tool |
| `requires` | stacks, addons | every stack and addon should list `base` |
| `conflicts` | no | an error when both are selected, directly or through `requires` |
| `when` | no | see below |
| `target` | no | relative, no `..`; rendered with the template data |

Unknown keys anywhere in `blueprint.yaml` are errors, so a typo never silently does nothing. Validation reports every
problem at once, prefixed with the file path.

### `when`

The body of a template `if`, evaluated with the [template data](#template-data) and `has` for what is selected so far.
It is re-evaluated until the selection stops changing, so one addon can pull in another.

```yaml
when: .OpenSource                                           # oss
when: or (has "sveltekit-web") (has "astro-site")           # bun-workspace
when: and .OpenSource (has "go-api")
```

An expression that does not parse, or names a field that does not exist, is an error naming the blueprint.

## Templates

`text/template` with **`{{ }}` delimiters**, `missingkey=error`, and the [functions](#template-functions) below.

- **GitHub Actions expressions pass through.** `${{ github.sha }}` is left exactly as written, anywhere in a template,
  so workflows need no escaping. This is the only `{{` that is left alone.
- A literal `{{` that is not a GitHub expression is written `{{"{{"}}`.
- A field that does not exist is an error naming the template (`{{.Nmae}}` fails); it never renders as empty.
- Rendering is **deterministic**: no clock, no randomness. `{{.Year}}` is recorded at create time. `vkd update` right
  after `vkd create` must change nothing, and an end-to-end test checks it.
- Whitespace: use `{{-` and `-}}` with care. `{{- if .X}}` eats the newline before; `{{end -}}` eats **the indentation
  of the next line too**, which breaks YAML. Put block tags on their own line without a trailing `-` when the next line
  is indented.

`*.tmpl` files are rendered. Any other file is copied unchanged, so a binary or a file full of `{{` needs no
treatment: just do not name it `.tmpl`.

**Template Go source is named `*.go.tmpl`** (and `go.mod.tmpl`). A `.go` file or a nested `go.mod` inside `blueprints/`
would be compiled, vetted and linted as part of vkd itself. A test fails the build if one slips in.

## Template functions

| Function | Result |
|---|---|
| `snake s` `kebab s` `camel s` `pascal s` | case conversion; splits on `-`, `_`, spaces and camelCase (`myHTTPServer` is `my-http-server`) |
| `upper s` `lower s` | case |
| `indent n s` | indent every non-empty line by `n` spaces |
| `toYaml v` | YAML of a value, without the trailing newline |
| `has "name"` | is that blueprint in the plan |
| `version "key"` | pinned value from `versions.yaml`; dotted for nested (`version "actions.checkout"`); an unknown key is an error |
| `port "name"` | a port from the project's block of ten, allocated per NAME in plan order, stable: same name, same port, never colliding across blueprints. `port n` (0..9) is the old fixed offset |
| `default d v` | `v`, or `d` when `v` is empty |
| `join sep list` | strings joined |
| `quote s` | a Go-quoted string, valid JSON for plain text |
| `trimPrefix p s` `replace old new s` | string helpers |

Plus every `text/template` builtin: `if`, `range`, `eq`, `ne`, `and`, `or`, `not`, `index`, `len`, `printf`.

## Template data

| Field | Type | What |
|---|---|---|
| `.Name` | string | project name |
| `.Owner` | string | GitHub owner |
| `.Description` | string | one-line description (defaults to the name) |
| `.OpenSource` | bool | `--oss` |
| `.License` | string | `MIT`, `Apache-2.0`, `AGPL-3.0` or `proprietary` |
| `.PortBase` | int | first port of the project's block of ten |
| `.Year` | int | copyright year, recorded at create time |
| `.Components` | []string | what the user selected with `--stack` (not the automatic ones) |
| `.Answers` | map | per-blueprint answers: `{{index .Answers "go-api" "module"}}` |
| `.Workspaces` | []string | bun workspace globs contributed by every blueprint in the plan |
| `.Blueprint` | string | the blueprint being rendered |
| `.Target` | string | its rendered target directory |

Always read a blueprint's own answers with `index`: `{{index .Answers "go-api" "module"}}`. A blueprint outside the plan
has no answers and `index` yields an empty string without an error.

## Questions

```yaml
questions:
  - id: module                  # ^[a-z][a-z0-9_]*$
    prompt: Go module path
    default: "github.com/{{.Owner}}/{{.Name}}"     # a template
    options: [free, pro]        # optional: the answer must be one of these
    required: true              # optional: no default, must be answered
```

Each question becomes the flag `--<blueprint>.<id>` (and `--set <blueprint>.<id>=value`), an entry under `per.<blueprint>`
in `vkd schema`, and a prompt on a terminal. Answers are recorded in `.vkd/answers.yaml`.

- Without `--yes`, a terminal prompts only for what no flag answered. With `--yes`, or without a terminal, vkd never
  prompts: an unanswered `required` question fails with `missing required answers: --<blueprint>.<id>`.
- A default is rendered with the global answers. A question with neither default nor `required` is empty.
- An answer outside `options`, or for a question that does not exist, is an error naming the flag.

## Contributes

What a blueprint adds to files that exist once per project.

**The whole `contributes` block is rendered as TEXT first and parsed as YAML after.** So a template can change the
structure, not just fill in values: add or drop a key, a list item or a step.

```yaml
contributes:
  atlas:
    services:
      - name: web
        command: bun run dev
        port: {{ port "web" }}
        {{- if has "go-api" }}
        depends_on: [api]
        {{- end }}
```

Rules for that text render:

- `${{ ... }}` GitHub expressions pass through untouched, as everywhere.
- Inside a template action you may write the action in a double-quoted YAML scalar with escaped quotes
  (`uses: "{{ version \"actions.checkout\" }}"`) or plainly (`uses: {{ version "actions.checkout" }}`); both work.
- Put `{{- if }}` / `{{- end }}` on their own lines and keep the indentation of the line the block belongs to. The
  rendered text must be valid YAML; a mistake is an error naming the blueprint, with the line of the rendered text.
- The rendered result is checked like an authored one: unknown keys, a reserved `ci` job id and an action `key` that is
  not one character are errors. A block with no `{{` is checked when the blueprint loads (`vkd blueprint lint`).
- Top-level `target` and `when` are plain strings rendered as templates when used, not part of this text render.

Two blueprints contributing the same service name, port, job id or lefthook command is an error naming both. Action
keys are the exception: see [atlas.yaml](#atlasyaml).

```yaml
contributes:
  atlas:
    pre_command: bun install          # atlas.yaml pre_command; two different values are an error
    services:
      - name: api                     # becomes the key under services:
        command: go run ./cmd/api
        dir: apps/api
        port: {{ port "api" }}        # a NAME: collision-free across blueprints, same name = same port everywhere
        health: "http://localhost:{{ port "api" }}/healthz"      # health is a full URL
        env: { PORT: "{{ port "api" }}" }
        open_key: a
    actions:
      - { key: c, label: Check api, command: "cd apps/api && go test ./..." }
  ci:
    jobs:
      go-api: { name: Go (api), runs-on: ubuntu-latest, steps: [...] }
  release:
    jobs:
      go-api-image: { name: Docker image, needs: gate, runs-on: ubuntu-latest, steps: [...] }
  gitignore: [apps/api/bin/]
  workspaces: [apps/web]              # bun workspace globs
  env: ["API_LOG_LEVEL=debug"]        # lines of .env.example
  lefthook:
    pre-push:
      commands:
        go-api-test: { run: "cd apps/api && go test ./..." }
  agents: |                           # appended to AGENTS.md under "## <blueprint name>"
    How an agent runs, checks and changes this component.
```

| Key | Lands in | Rule |
|---|---|---|
| `atlas.services`, `atlas.actions`, `atlas.pre_command` | `atlas.yaml` | see below |
| `ci.jobs` | `.github/workflows/ci.yml` | the aggregate `CI` job is generated and `needs` every job; `ci` is a reserved id |
| `release.jobs` | `.github/workflows/release.yml` | declare `needs: gate`; the base `publish` job waits for every job |
| `gitignore` | `.gitignore` | merged, deduplicated, grouped under `# <blueprint>` |
| `workspaces` | root `package.json` (via `.Workspaces`) | sorted, deduplicated |
| `env` | `.env.example` | deduplicated |
| `lefthook` | `lefthook.yml` | `pre-push` and `pre-commit` run in parallel |
| `agents` | `AGENTS.md` | one section per blueprint |

### atlas.yaml

The schema is [atlas's](https://atlas.codeatlas.com.br/docs/configuration). Run `atlas config check` on the output; the
end-to-end test does. Things that bite:

- **Ports: ask by name.** `{{port "api"}}` gives the service its port from the project's block of ten (`PortBase`..
  `PortBase+9`). Names are allocated in the order they are first asked for, blueprints are rendered in plan order, so
  the result is deterministic. The same name returns the same port in `contributes` and in `files/`, so another
  blueprint can reach it: sveltekit-web's dev proxy uses `{{port "api"}}` for go-api's service. Two blueprints can
  never collide. More than ten names is an error naming the ports taken.
  The old numeric `{{port 3}}` (a fixed offset) still works, and a name never takes an offset a number already holds,
  but a number asked after a name took that offset is an error: use the name.
- `health` is a **full URL** (`http://localhost:{{port "api"}}/healthz`), not a path.
- **Action keys are suggestions.** Atlas allows a small set of single characters and rejects built-in dashboard keys
  (`f g i j k m n q r s t v z`, capitals `D E G J L M N P S T V W`, and digits). In plan order an action keeps its `key`
  unless that is a built-in key, a service's `open_key`, or taken by an earlier action; then it gets the first free key
  from `c y x b l p a e w d h o u A B C F H I K O Q R U X Y Z` and the plan carries a warning
  (`--json` has `warnings`, text output prints `warning:`). Nothing fails, nothing is random. Out of keys is an error.
  Give an action a `key` you would like and expect it to move when stacks are combined; never reference a key in a
  doc or another file.
- Services are keyed by `name`; `command`, `dir`, `port`, `health`, `depends_on`, `env`, `open_key` are written in reading order.

### CI jobs

Standard GitHub Actions job bodies. Use `{{ version "actions.checkout" }}` for every action: pins are full commit
SHAs with a `# vX` comment, and vkd writes the comment as a real YAML comment, which is what zizmor needs.

```yaml
steps:
  - uses: "{{ version \"actions.checkout\" }}"
    with: { persist-credentials: false }
```

Rules the generated workflows must keep (zizmor and actionlint run in CI and in the end-to-end test):

- `persist-credentials: false` on every checkout; default `permissions: contents: read`, widened per job.
- Never put `${{ }}` of untrusted input (a PR title, a branch name) inside a `run:`; pass it through `env:`.
- **A release job never restores a cache.** `actions/setup-go` in a release workflow needs `cache: false`; otherwise
  zizmor reports `cache-poisoning`. Cache freely in CI.
- Add the version of a tool to `versions.yaml`, not to the blueprint.

## Composed files (base only)

The `base` blueprint declares the files built from every blueprint's contributions:

```yaml
composed:
  - { path: .github/workflows/ci.yml, kind: ci, head: _compose/ci-head.yml.tmpl }
```

`kind` is one of `atlas`, `ci`, `release`, `gitignore`, `agents`, `lefthook`, `env`. `head` is a template file under
`files/` with what comes before the merged part (a workflow's `name`/`on`/`concurrency`, AGENTS.md's intro); it is not
copied to the project. Only `base` declares composed files; you should not need to.

## File rules

```yaml
files:
  - { path: LICENSE.mit.tmpl, as: LICENSE, when: 'eq .License "MIT"' }
  - { path: LICENSE.apache-2.0, as: LICENSE, when: 'eq .License "Apache-2.0"' }
```

`path` is relative to `files/`, before templating. `as` writes the file under another (templated) path; `when` writes it
only when the expression is truthy. A rule must set at least one. Use it to choose between variants of a file.

## How blueprints are selected and ordered

1. `base` blueprints (always).
2. The user's `--stack` components. An unknown name fails, listing the known ones.
3. Everything they `requires`, then every blueprint whose `when` is truthy, repeated until nothing changes.
4. A selected blueprint that `conflicts` with another selected one is an error.
5. Order: base first, then each blueprint after everything it `requires`, ties by name. A `requires` cycle is an error.

Sources, later wins by name: built-in, then `$XDG_CONFIG_HOME/vkd/blueprints/`, then each `--blueprints DIR`
(repeatable). Each directory under one of these is a blueprint.

## Add a blueprint step by step

```bash
vkd blueprint new rust-cli --kind stack --dir blueprints     # scaffolds blueprints/rust-cli
$EDITOR blueprints/rust-cli/blueprint.yaml
vkd blueprint lint blueprints/rust-cli
vkd --blueprints blueprints create demo --stack rust-cli --owner you --yes --dry-run --json
```

For a built-in blueprint, work in `blueprints/<name>/` of this repository; it is embedded at build time.

A stack should provide: its `atlas` service(s) with a `port` from `{{port "name"}}`, a `ci` job that runs the stack's own
lint and tests, `release` artifacts (a binary matrix for a CLI, a Docker image to GHCR for a service, a static build for
a site), a `lefthook` pre-push command, an `agents` section with how to run, check and change it, and a test file so the
generated project's own tests have something to run.

## Test it

1. `vkd blueprint lint <dir>`: validates `blueprint.yaml` and renders the blueprint over the built-ins with sample
   answers, so a template that does not parse or reads an unknown field fails with its file name.
2. Dry run: `vkd --blueprints <dir> create demo --stack <name> --owner you --yes --dry-run --json`.
3. Create and run the stack's own tools on the result. Add a case to [`e2e/e2e_test.go`](../e2e/e2e_test.go):

```go
func TestRustCLIPreset(t *testing.T) {
	dir := create(t, "--stack", "rust-cli", "--oss")
	t.Run("cargo", func(t *testing.T) {
		need(t, "cargo")                       // skipped, never faked, when the tool is missing
		sh(t, dir, "cargo", "clippy", "--all-targets", "--", "-D", "warnings")
		sh(t, dir, "cargo", "test")
	})
	t.Run("actionlint", func(t *testing.T) { need(t, "actionlint"); sh(t, dir, "actionlint") })
	t.Run("zizmor", func(t *testing.T) { need(t, "zizmor"); sh(t, dir, "zizmor", "--offline", "--quiet", ".github") })
	t.Run("atlas config", func(t *testing.T) { need(t, "atlas"); sh(t, dir, "atlas", "config", "check") })
}
```

   Run it with `go test -tags e2e ./e2e -run TestRustCLIPreset -v`. A stack that cannot be checked here (a tool that
   is not installed) must say so in its PR.
4. Write the failing test first when you fix a bug in a blueprint, and break the template once to see the test go red.
5. `go build ./... && go vet ./... && go test ./...`: includes `TestNoGoSourceInsideBlueprints` and the versions test.

A blueprint that needs JavaScript tooling selects `bun-workspace` automatically through its `when`; add your stack's
name to that `when` and contribute `workspaces: [apps/<name>]`. The root `package.json`, `biome.json` and `turbo.json`
must pass `bun run check`; the `probe-web` stack in `e2e/testdata` proves it with two workspaces.

## Conventions for a stack

- Pin every tool version in `versions.yaml` and read it with `version`. Never `latest`, `^`, `~` or a floating action tag.
- Every frontend uses `@codeatlasdev/ui` (`version "codeatlasdev_ui"`).
- Formatting is the stack's own formatter's output: Biome for JS/TS/JSON (tabs, double quotes; `package.json` arrays are
  expanded one item per line), `gofmt` for Go (template Go files end in `.go.tmpl`), `rustfmt` for Rust.
- Config from the environment only; structured logs on stdout; no secrets in the repository (`.env` is ignored,
  `.env.example` documents the keys).
- Ports: one block of ten per project, allocated by `{{port "name"}}`. Do not hard-code a port or use a number.

## Pitfalls

- **`{{end -}}` followed by an indented line** removes the indent. Test YAML templates with `actionlint`.
- **`--blueprints DIR` works before or after the subcommand** (`vkd --blueprints DIR create ...` or
  `vkd create ... --blueprints DIR`). The `--<blueprint>.<id>` flags of a blueprint that lives in `DIR` exist only if
  `DIR` is on the same command line.
- **A quoted port is rejected by atlas**: write `port: {{ port "api" }}` unquoted. (A quoted `"{{ port "api" }}"` in
  `contributes` is converted to an integer, but unquoted is the form to use.)
- **A YAML value containing ` #`** starts a comment in a plain scalar; use a block scalar (`run: |`) or quote it.
- **`uses:` pins**: write `uses: {{ version "actions.checkout" }}` unquoted, in `contributes` and in `*.tmpl` workflows
  alike, so the `# vX` comment becomes a real YAML comment. The escaped-quote form also works.
- **Never commit what update must regenerate**: lockfiles and generated output belong in `.gitignore` or in the user's
  repository, not in `files/`.
