#!/usr/bin/env bash
# publish-release-asset.sh <file>...
#
# Attach release files to the GitHub Release FERRETDB_RELEASE_TAG the moment
# they exist, instead of waiting for every platform to finish building.
#
# build.sh calls this once per binary, right after that binary compiled and
# passed its telemetry audit (FERRETDB_DIST_PUBLISH), so ferretdb-amd64 is
# downloadable while the other platforms are still compiling. The workflows
# call it directly for build.sh's README.md.
#
# For every ferretdb-<arch>[.exe] it first writes <file>.sha256sum beside it,
# in the "<sum>  <name>" format `sha256sum -c` reads, and uploads the binary
# and its checksum together. Other files (README.md) are uploaded as they are.
#
# Each upload is retried with --clobber, so a transient API error does not
# leave a platform missing and a rerun replaces a half-uploaded asset.
#
# Environment:
#   FERRETDB_RELEASE_TAG       release to attach to (required)
#   GH_TOKEN                   token gh uses (required by gh, not checked here)
#   FERRETDB_UPLOAD_ATTEMPTS   attempts per upload (default 5)
#   FERRETDB_UPLOAD_DELAY      seconds before the first retry, doubled after
#                              each failure (default 5)
set -euo pipefail

tag="${FERRETDB_RELEASE_TAG:?set FERRETDB_RELEASE_TAG to the release tag}"
attempts="${FERRETDB_UPLOAD_ATTEMPTS:-5}"
delay="${FERRETDB_UPLOAD_DELAY:-5}"

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <file>..." >&2
  exit 2
fi

upload() {
  local n=1 wait="$delay"
  while :; do
    if gh release upload "$tag" "$@" --clobber; then
      return 0
    fi
    if [ "$n" -ge "$attempts" ]; then
      echo "::error::Could not attach $* to release $tag after $attempts attempts." >&2
      return 1
    fi
    echo "::warning::Upload of $* to $tag failed (attempt $n of $attempts); retrying in ${wait}s." >&2
    sleep "$wait"
    n=$((n + 1))
    wait=$((wait * 2))
  done
}

for f in "$@"; do
  if [ ! -f "$f" ]; then
    echo "::error::$f does not exist; nothing to attach." >&2
    exit 1
  fi
  name="$(basename "$f")"
  case "$name" in
    *.sha256sum)
      echo "::error::$f is a checksum file; pass the binary and it is written." >&2
      exit 1
      ;;
    ferretdb-*)
      dir="$(dirname "$f")"
      (cd "$dir" && sha256sum "$name" > "$name.sha256sum")
      upload "$f" "$f.sha256sum"
      echo "Attached $name and $name.sha256sum to $tag."
      ;;
    *)
      upload "$f"
      echo "Attached $name to $tag."
      ;;
  esac
done
