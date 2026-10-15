#!/usr/bin/env bash
# lint-workflows.sh — actionlint (syntax, expressions, shellcheck of `run:` blocks) and zizmor (security
# audit; config .github/zizmor.yml) over .github/.
# Install: brew install actionlint zizmor   (or: go install github.com/rhysd/actionlint/cmd/actionlint@latest)
set -euo pipefail
cd "$(dirname "$0")/.."

for tool in actionlint zizmor; do
  command -v "$tool" >/dev/null || { echo "lint-workflows: $tool is missing" >&2; exit 1; }
done

actionlint
# --offline: the audits that need the GitHub API are covered by dependabot and the SHA pins; offline keeps the
# gate deterministic and free of token/rate limits.
zizmor --offline --quiet .github
