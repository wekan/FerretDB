#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:?}/official-node-test.XXXXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/source/node-v26.9.0-linux-ppc64le/bin" "$work/dist"
printf '#!/bin/sh\necho v26.9.0\n' > "$work/source/node-v26.9.0-linux-ppc64le/bin/node"
printf 'license\n' > "$work/source/node-v26.9.0-linux-ppc64le/LICENSE"
tar -cJf "$work/source/node-v26.9.0-linux-ppc64le.tar.xz" -C "$work/source" node-v26.9.0-linux-ppc64le
(cd "$work/source" && sha256sum node-v26.9.0-linux-ppc64le.tar.xz > SHASUMS256.txt)
cat > "$work/bin/curl" <<'CURL'
#!/usr/bin/env bash
set -eu
while [[ $# -gt 0 ]]; do
 case "$1" in https:*) name="${1##*/}";; -o) shift; out="$1";; esac
 shift
done
cp "$FIXTURE_SOURCE/$name" "$out"
CURL
chmod +x "$work/bin/curl"
export PATH="$work/bin:$PATH" FIXTURE_SOURCE="$work/source"
helper="$root/build/ferretdb/official-ppc64le-node.sh"
bash "$helper" "$work/dist" v26.9.0
[[ ! -e "$work/dist/official-node-ppc64le" ]]
mkdir -p "$work/source/mongosh-ppc64le/bin"
cp "$work/source/node-v26.9.0-linux-ppc64le/bin/node" "$work/source/mongosh-ppc64le/bin/node"
tar -czf "$work/dist/mongosh-ppc64le.tgz" -C "$work/source" mongosh-ppc64le
cat > "$work/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -eu
[[ "$1 $2 $3 $4 $5" = 'run --rm --platform linux/ppc64le -v' ]]
[[ "$*" = *'/runtime/mongosh-ppc64le/bin/node --version'* ]]
printf 'v26.9.0\n'
DOCKER
chmod +x "$work/bin/docker"
bash "$helper" "$work/dist"
bash "$helper" "$work/dist" v26.9.0
[[ "$("$work/dist/official-node-ppc64le")" = v26.9.0 ]]
[[ "$(cat "$work/dist/official-node-ppc64le.version")" = v26.9.0 ]]
cmp "$work/dist/official-node-ppc64le.LICENSE" "$work/source/node-v26.9.0-linux-ppc64le/LICENSE"
if bash "$helper" "$work/dist" latest; then exit 1; fi
if bash "$helper" "$work/dist" v24.21.0; then exit 1; fi
cp "$work/source/SHASUMS256.txt" "$work/source/original-checksum"
cat "$work/source/original-checksum" >> "$work/source/SHASUMS256.txt"
if bash "$helper" "$work/dist" v26.9.0; then exit 1; fi
cp "$work/source/original-checksum" "$work/source/SHASUMS256.txt"
printf 'corrupt' >> "$work/source/node-v26.9.0-linux-ppc64le.tar.xz"
if bash "$helper" "$work/dist" v26.9.0; then exit 1; fi
printf '' > "$work/source/SHASUMS256.txt"
if bash "$helper" "$work/dist" v26.9.0; then exit 1; fi
printf 'official PPC runtime: version, checksum, missing entry and absent target checks passed\n'
