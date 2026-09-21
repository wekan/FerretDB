#!/usr/bin/env bash
# The cross-built PPC runtime has an invalid V8 snapshot. Use the same Node
# version's official native build until the producer ships a working snapshot.
set -euo pipefail
out="${1:?usage: official-ppc64le-node.sh DIST VERSION}"
version="${2:-}"
[[ -f "$out/mongosh-ppc64le.tgz" ]] || { echo 'No PPC mongosh package; no replacement required'; exit 0; }
mkdir -p "${TMPDIR:?TMPDIR must be set}"
stage="$(mktemp -d "$TMPDIR/ferretdb-node.XXXXXXXX")"
trap 'rm -rf "$stage"' EXIT
if [[ -z "$version" ]]; then
  tar -xzf "$out/mongosh-ppc64le.tgz" -C "$stage"
  version="$(docker run --rm --platform linux/ppc64le \
    -v "$stage:/runtime:ro" debian:trixie-slim sh -ec '
      apt-get update >&2
      apt-get install -y --no-install-recommends libstdc++6 >&2
      /runtime/mongosh-ppc64le/bin/node --version
    ')"
fi
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid Node version' >&2; exit 1; }
[[ "$version" == v26.* ]] || { echo 'mongosh requires Node.js 26' >&2; exit 1; }
archive="node-$version-linux-ppc64le.tar.xz"
base="https://nodejs.org/dist/$version"
curl --fail --location --retry 3 "$base/SHASUMS256.txt" -o "$stage/SHASUMS256.txt"
curl --fail --location --retry 3 "$base/$archive" -o "$stage/$archive"
awk -v name="$archive" '$2 == name {print}' "$stage/SHASUMS256.txt" > "$stage/checksum"
[[ "$(wc -l < "$stage/checksum")" -eq 1 ]] || { echo 'Missing or duplicate official Node checksum' >&2; exit 1; }
(cd "$stage" && sha256sum --check checksum)
tar -xJf "$stage/$archive" -C "$stage" "node-$version-linux-ppc64le/bin/node" "node-$version-linux-ppc64le/LICENSE"
install -m 0755 "$stage/node-$version-linux-ppc64le/bin/node" "$out/official-node-ppc64le"
install -m 0644 "$stage/node-$version-linux-ppc64le/LICENSE" "$out/official-node-ppc64le.LICENSE"
printf '%s\n' "$version" > "$out/official-node-ppc64le.version"
