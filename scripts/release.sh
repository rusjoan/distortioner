#!/usr/bin/env bash
# Tags a release from an up-to-date master after running the checks.
# Usage: scripts/release.sh 1.2.3   (DRY_RUN=1 to stop before tagging)
set -euo pipefail

dry=${DRY_RUN:-0}
branch_main=master
latest=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || echo v0.0.0)

fail() { echo "release: $*" >&2; exit 1; }
# In a dry run, repository state problems are reported but do not stop it.
check() { if [[ $dry == 1 ]]; then echo "release: warning: $*" >&2; else fail "$*"; fi; }

next_patch() {
  local v=${latest#v} major minor patch
  IFS=. read -r major minor patch <<<"${v%%-*}"
  echo "$major.$minor.$((patch + 1))"
}

version=${1:-}
if [[ -z $version ]]; then
  fail "VERSION is required, e.g. make release VERSION=$(next_patch) (latest tag: $latest)"
fi
version=${version#v}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || fail "'$version' is not semver (x.y.z[-pre])"
tag=v$version

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  fail "tag $tag already exists"
fi
if [[ $(printf '%s\n%s\n' "${latest#v}" "$version" | sort -V | tail -1) != "$version" ]]; then
  fail "$tag is not newer than $latest"
fi

branch=$(git rev-parse --abbrev-ref HEAD)
[[ $branch == "$branch_main" ]] || check "on branch '$branch', releases are cut from $branch_main"
[[ -z $(git status --porcelain) ]] || check "working tree is not clean"

git fetch --quiet origin "$branch_main" --tags
[[ $(git rev-parse HEAD) == $(git rev-parse "origin/$branch_main") ]] ||
  check "HEAD is not in sync with origin/$branch_main"

echo "release: running checks for $tag"
make --no-print-directory fmt-check vet lint test

if [[ $dry == 1 ]]; then
  echo "release: dry run passed, would tag $tag (latest: $latest) and push it"
  exit 0
fi

git tag -a "$tag" -m "Release $tag"
git push origin "$tag"
echo "release: pushed $tag; GitHub Actions will build and publish it"
