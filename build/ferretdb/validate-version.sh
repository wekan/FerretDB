#!/usr/bin/env bash
# Release artifacts from this SQLite fork must never use FerretDB v2 tags.
set -euo pipefail
if [[ ! "${1:-}" =~ ^v1\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a FerretDB v1 release tag (v1.MINOR.PATCH), got: ${1:-}" >&2
  # An empty version is resolved with `git describe`, which only names a tag
  # when the branch is exactly on it. After commits land on top of a release,
  # it says how far past the tag the branch is; say what that means.
  if [[ "${1:-}" =~ ^(v1\.[0-9]+\.[0-9]+)-([0-9]+)-g[0-9a-f]+$ ]]; then
    echo "The branch is ${BASH_REMATCH[2]} commit(s) past ${BASH_REMATCH[1]}, so there is no tag to infer." >&2
    echo "Release the new commits as the next version (./build.sh release-ferretdb)," >&2
    echo "or pass the version explicitly (-f version=v1.MINOR.PATCH)." >&2
  fi
  exit 1
fi
