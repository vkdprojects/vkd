#!/usr/bin/env bash
# sync-labels.sh — make the repository's labels match .github/labels.yml, from your machine (no Actions
# minutes). Idempotent: `gh label create --force` creates a missing label and updates color and description
# of an existing one. It never deletes anything.
#
# Usage: scripts/sync-labels.sh [--dry-run] [--repo OWNER/REPO]
#   --dry-run  print the `gh label create` commands, run nothing, need no login
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="$root/.github/labels.yml"
repo=""
dry=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry=1 ;;
    --repo) repo="${2:?--repo needs OWNER/REPO}"; shift ;;
    -h|--help) sed -n '2,8p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

[[ -f "$file" ]] || { echo "labels file not found: $file" >&2; exit 2; }
if [[ -z $repo ]]; then
  repo="$(git -C "$root" remote get-url origin 2>/dev/null | sed -E 's#^(git@github.com:|https://github.com/)##; s#\.git$##')"
  [[ -n $repo ]] || { echo "pass --repo OWNER/REPO (no origin remote)" >&2; exit 2; }
fi
[[ $dry == 1 ]] || command -v gh >/dev/null || { echo "gh is required (https://cli.github.com)" >&2; exit 2; }

# labels.yml is a flat list of three-line entries (- name / color / description); read it without a YAML parser.
count=0
name="" color="" desc=""
emit() {
  [[ -n $name ]] || return 0
  if [[ $dry == 1 ]]; then
    printf 'gh label create --repo %s "%s" --color %s --description "%s" --force\n' "$repo" "$name" "$color" "$desc"
  else
    gh label create --repo "$repo" "$name" --color "$color" --description "$desc" --force
  fi
  count=$((count + 1))
}
while IFS= read -r line; do
  case "$line" in
    "- name: "*) emit; name="${line#- name: }"; name="${name//\"/}"; color=""; desc="" ;;
    "  color: "*) color="${line#  color: }" ;;
    "  description: "*) desc="${line#  description: }" ;;
  esac
done < "$file"
emit
echo "$([[ $dry == 1 ]] && echo 'dry run: ')$count labels from .github/labels.yml" >&2
