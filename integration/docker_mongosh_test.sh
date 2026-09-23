#!/usr/bin/env bash
# Static release guard plus the command the published image must pass on its
# native architecture after it starts.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
workflow="$root/.github/workflows/docker.yml"
dockerfile="$root/Dockerfile.release"
grep -q 'MONGOSH_REPO: wekan/mongosh-patches' "$workflow"
grep -q 'gh release download.*--repo "$MONGOSH_REPO"' "$workflow"
grep -q 'sha256sum -c.*mongosh-' "$workflow"
grep -q 'mongosh-${m}.tgz' "$dockerfile"
grep -q 'arm/v7).*m=armv7' "$dockerfile"
grep -q 'ENV PATH=/opt/mongosh:' "$dockerfile"
grep -q 'FROM debian:trixie-slim AS final' "$dockerfile"
grep -q 'ca-certificates libstdc++6 libatomic1' "$dockerfile"
grep -q '/opt/mongosh/mongosh --version' "$dockerfile"
grep -q 'docker/setup-qemu-action@' "$workflow"
grep -q 'bash build/ferretdb/release-platforms.sh dist' "$workflow"

# Exercise actual selection with complete, partial and broken release assets.
# The unsupported platforms previously made every registry build fail.
: "${TMPDIR:?set TMPDIR to a repository-local temporary directory}"
bash "$root/tests/official-ppc64le-node.sh"
tmp="$(mktemp -d "$TMPDIR/ferretdb-platforms.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
for arch in amd64 arm64 armhf i386 ppc64le s390x riscv64 armv6 armel loong64; do
  printf 'binary' > "$tmp/ferretdb-$arch"
  shell="$arch"
  [ "$arch" != armhf ] || shell=armv7
  printf 'archive' > "$tmp/mongosh-$shell.tgz"
done
select_platforms() { bash "$root/build/ferretdb/release-platforms.sh" "$tmp"; }
[ "$(select_platforms 2> "$tmp/warnings")" = 'linux/amd64,linux/arm64,linux/arm/v7,linux/386,linux/ppc64le,linux/s390x,linux/riscv64' ]
for arch in armv6 armel loong64; do
  grep -q "Omitting $arch container" "$tmp/warnings"
done
rm "$tmp/mongosh-armv7.tgz"
if select_platforms > "$tmp/result" 2> "$tmp/error"; then
  echo 'Missing supported shell must fail selection' >&2; exit 1
fi
grep -q 'Missing mongosh-armv7.tgz' "$tmp/error"
rm "$tmp/ferretdb-armhf"
select_platforms > "$tmp/result" 2> "$tmp/warnings"
! grep -q 'linux/arm/v7' "$tmp/result"
rm "$tmp"/ferretdb-*
if select_platforms > "$tmp/result" 2> "$tmp/error"; then
  echo 'Empty release must fail selection' >&2; exit 1
fi
grep -q 'No supported FerretDB' "$tmp/error"
if [ "${FERRETDB_MONGOSH_CONTAINER:-}" ]; then
  docker exec "$FERRETDB_MONGOSH_CONTAINER" mongosh \
    'mongodb://127.0.0.1:27017/?directConnection=true' \
    --quiet --eval 'if (db.runCommand({ping:1}).ok !== 1) quit(2)'
fi
echo 'FerretDB Docker mongosh wiring passed'
