#!/usr/bin/env bash
# check-commit-msg.sh — commit-msg hook (lefthook): the first line must be a Conventional Commits subject
# (https://www.conventionalcommits.org/en/v1.0.0/). Merge, fixup! and squash! commits are accepted.
# Usage: scripts/check-commit-msg.sh <commit-message-file>
set -euo pipefail

file="${1:?usage: check-commit-msg.sh <commit-message-file>}"
subject="$(head -n1 "$file")"
types='feat|fix|perf|refactor|docs|test|build|ci|chore|revert|style'
re="^($types)(\([a-z0-9][a-z0-9._/-]*\))?!?: [^[:space:]].*$"

if [[ $subject =~ $re || $subject =~ ^(Merge|Revert|fixup!|squash!) ]]; then
  exit 0
fi
cat >&2 <<MSG
Commit subject is not a Conventional Commits subject: $subject
Use "<type>(<optional scope>): <imperative summary>", type one of: ${types//|/, }.
Example: "fix(api): return 404 for an unknown id"
MSG
exit 1
