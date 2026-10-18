#!/usr/bin/env bash
# Usage: check-release-version.sh TAG [VERSION]
#
# Fails unless TAG is "v" followed by the version flake.nix stamps into the binary and image.
# VERSION defaults to `nix eval --raw .#default.version`; on success the version is printed.
set -euo pipefail

tag=${1:?usage: check-release-version.sh TAG [VERSION]}
version=${2:-$(nix eval --raw .#default.version)}

if [[ $tag != "v$version" ]]; then
  echo "tag $tag does not match the flake version $version (expected v$version)" >&2
  exit 1
fi
echo "$version"
