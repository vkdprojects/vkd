# Contributing to vkd

Thanks for wanting to help. By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md). vkd is
[MIT](LICENSE) licensed, and so are your contributions.

## Report a bug or ask for a feature

Open an [issue](https://github.com/vkdprojects/vkd/issues/new/choose) with `vkd version`, your OS, what you ran, what you
expected and what happened. For a generated-project problem include `.vkd/answers.yaml`. **Security problems do not go
in public issues**: see [SECURITY.md](SECURITY.md).

## Send a change

For anything bigger than a small fix, open an issue first.

```bash
git clone https://github.com/vkdprojects/vkd.git && cd vkd
go build ./... && go vet ./... && go test ./...
go test -tags e2e ./e2e      # needs go, bun, actionlint, zizmor, atlas, shellcheck; missing tools skip
```

1. Branch from `main`: `type/short-desc` (`fix/update-relative-dir`).
2. **Tests come first.** For a bug, write the regression test, watch it fail for the right reason, then fix it. Prefer
   behavior tests (boundaries, failures, state transitions) over getters, and prove a new test can fail by breaking the
   code once.
3. Run the gate above and `golangci-lint run`. Fix warnings; do not add `//nolint` without a reason.
4. Open a pull request (a draft until ready) and fill in the template.

Rules of thumb:

- A new blueprint or a blueprint change needs `go test -tags e2e ./e2e` green: generated output must pass its own tools.
  See [docs/blueprints.md](docs/blueprints.md).
- No new dependency without saying why. Versions are pinned in `versions.yaml`; no `latest`.
- Commit subjects are short and imperative with a conventional prefix (`feat`, `fix`, `perf`, `refactor`, `docs`, `test`,
  `build`, `ci`, `chore`, `revert`, `style`), scoped when useful (`fix(update): ...`), `!` for a breaking change. The pull
  request **title** has the same form: it becomes the commit subject on squash, and the `Conventional title` check fails
  otherwise. Install [lefthook](https://lefthook.dev) (`lefthook install`) to check commit messages locally.

### Pull request flow and who merges

Open a pull request against `main`. Required checks: `CI`, `Signed-off-by` and `Conventional title`. A maintainer
squash-merges once checks are green and a maintainer approved. AI agents follow the same flow and never push to `main`.

## Developer Certificate of Origin (DCO)

Every commit must carry a `Signed-off-by` trailer: your statement, under the [Developer Certificate of Origin
1.1](https://developercertificate.org/), that you wrote the change or have the right to submit it under this license.
There is no CLA and you keep your copyright.

```bash
git commit -s -m "fix: keep the user's edit on update"   # adds: Signed-off-by: Your Name <you@example.com>
git rebase --signoff main                                  # sign every commit of a branch you already made
git commit --amend -s --no-edit                            # sign the last one
```

## AI-assisted contributions

AI tools are fine; the rules protect reviewers' time. A human is accountable for every line and has run it. Pull requests
opened by an agent with nobody driving are closed. Disclose substantial AI assistance in the description. Do not paste
unverified output or put secrets in a prompt. See [AGENTS.md](AGENTS.md) for what coding agents need in this repository.
