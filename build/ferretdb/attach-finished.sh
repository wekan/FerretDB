#!/usr/bin/env bash
# attach-finished.sh [--check]
#
# Make sure every binary build.sh FINISHED in this run is on the GitHub Release
# FERRETDB_RELEASE_TAG together with its .sha256sum.
#
# Normally build.sh has already attached each binary the moment it was built
# (FERRETDB_DIST_PUBLISH), and this finds nothing to do. It exists for the run
# that is cancelled or fails part-way: the release workflows run it with
# always(), so a binary that finished but whose upload was cut short is still
# attached instead of being lost with the run.
#
# "Finished" means listed in build.sh's tmp/ferretdb-dist/built.list, which is
# written only after a binary compiled, passed its telemetry audit and was made
# executable. A file in dist/ that is not listed - a compile interrupted by the
# cancel, or one the audit was still checking - is never attached.
#
# --check   attach nothing; list what is missing and exit 1 if anything is.
#
# Environment: FERRETDB_RELEASE_TAG (required), GH_TOKEN (for gh).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tag="${FERRETDB_RELEASE_TAG:?set FERRETDB_RELEASE_TAG to the release tag}"
check=0
[ "${1:-}" != --check ] || check=1
built="$root/tmp/ferretdb-dist/built.list"
dist="$root/dist"

if [ ! -s "$built" ]; then
  echo "No binary finished building in this run; nothing to attach."
  exit 0
fi

attached="$(gh release view "$tag" --json assets --jq '.assets[].name')"
has() { printf '%s\n' "$attached" | grep -qxF "$1"; }

missing=()
n=0
while IFS= read -r name; do
  [ -n "$name" ] || continue
  n=$((n + 1))
  file="$dist/ferretdb-$name"
  [ -f "$file" ] || file="$file.exe"
  if [ ! -f "$file" ]; then
    echo "::error::build.sh listed $name as built but $dist has no binary for it." >&2
    exit 1
  fi
  base="$(basename "$file")"
  if has "$base" && has "$base.sha256sum"; then
    continue
  fi
  missing+=("$file")
done < "$built"

if [ "${#missing[@]}" -eq 0 ]; then
  echo "All $n finished binaries and their checksums are on release $tag."
  exit 0
fi

if [ "$check" -eq 1 ]; then
  echo "::error::Finished but not on release $tag: $(for f in "${missing[@]}"; do printf '%s ' "$(basename "$f")"; done)" >&2
  exit 1
fi

echo "Attaching ${#missing[@]} finished binaries that are not on $tag yet."
bash "$root/build/ferretdb/publish-release-asset.sh" "${missing[@]}"
