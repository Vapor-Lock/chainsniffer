#!/usr/bin/env bash
# Usage: release-notes.sh VERSION [CHANGELOG]
#
# Prints the CHANGELOG.md entry for VERSION: the lines between its "## [VERSION]" heading and the
# next "## [" heading or the link references at the bottom. Fails if the entry is missing or empty.
set -euo pipefail

version=${1:?usage: release-notes.sh VERSION [CHANGELOG]}
changelog=${2:-CHANGELOG.md}

notes=$(awk -v v="$version" '
  index($0, "## [" v "]") == 1 { on = 1; next }
  on && (/^## \[/ || /^\[[^]]+\]: /) { exit }
  on { print }
' "$changelog")

if ! grep -q '[^[:space:]]' <<<"$notes"; then
  echo "no $changelog entry for $version" >&2
  exit 1
fi
printf '%s\n' "$notes"
