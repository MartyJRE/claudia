#!/usr/bin/env bash
# next-version.sh: prints the tag the next release gets, or nothing when HEAD
# is already tagged. It bumps the patch of the latest vX.Y.Z tag, unless a commit
# since then has a "Release: minor" or "Release: major" trailer. The first is v0.1.0.
set -euo pipefail

if [ -n "$(git tag --points-at HEAD --list 'v*')" ]; then
  exit 0
fi
last=$(git tag --merged HEAD --list 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
if [ -z "$last" ]; then
  echo v0.1.0
  exit 0
fi

bump=patch
for t in $(git log --format='%(trailers:key=Release,valueonly)' "$last..HEAD" | tr 'A-Z' 'a-z'); do
  case $t in
    major) bump=major ;;
    minor) [ $bump = major ] || bump=minor ;;
  esac
done

IFS=. read -r major minor patch <<<"${last#v}"
case $bump in
  major) echo "v$((major + 1)).0.0" ;;
  minor) echo "v$major.$((minor + 1)).0" ;;
  patch) echo "v$major.$minor.$((patch + 1))" ;;
esac
