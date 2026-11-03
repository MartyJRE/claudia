#!/usr/bin/env bash
# publish-badge.sh FILE LABEL PERCENT: writes a shields.io endpoint JSON to the
# badges branch, keeping the other badges there. Needs GITHUB_TOKEN and GITHUB_REPOSITORY.
set -euo pipefail

file=$1 label=$2 pct=$3
color=$(awk -v p="$pct" 'BEGIN { print (p >= 80 ? "brightgreen" : p >= 60 ? "green" : p >= 40 ? "yellow" : p >= 20 ? "orange" : "red") }')
remote="https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.git"

cd "$(mktemp -d)"
git init -q -b badges
for attempt in 1 2 3; do
  if git fetch -q --depth=1 "$remote" badges; then
    git reset -q --hard FETCH_HEAD
  fi
  printf '{"schemaVersion":1,"label":"%s","message":"%s%%","color":"%s"}\n' "$label" "$pct" "$color" > "$file"
  git add "$file"
  git -c user.name=github-actions -c user.email=github-actions@github.com commit -q -m "$label $pct%" || exit 0
  git push -q "$remote" HEAD:badges && exit 0
  sleep $((attempt * 5))
done
exit 1
