#!/usr/bin/env bash
# Static release guard for docker.yml and Dockerfile.release: the image is the
# static ferretdb binary on distroless static, with no mongosh, and the target
# platforms are exactly the ones the release has binaries for.
set -euo pipefail
# Every check below is a quiet grep; say which one failed, or a release job
# stops with only "exit code 1" in its log (as the v1.90.0 Docker run did).
trap 'echo "docker_release_test.sh: check at line $LINENO failed" >&2' ERR
root="$(cd "$(dirname "$0")/.." && pwd)"
workflow="$root/.github/workflows/docker.yml"
dockerfile="$root/Dockerfile.release"
grep -q 'FROM runtime-${TARGETARCH} AS final' "$dockerfile"
grep -q 'FROM gcr.io/distroless/static-debian13:nonroot AS runtime-amd64' "$dockerfile"
grep -q 'RUN \["/ferretdb", "--version"\]' "$dockerfile"
# Negative: no mongosh, no Node.js and no C++ runtime in the image or its build.
# (Its comments may say why they are gone; its instructions name none of them.)
! grep -vE '^[[:space:]]*#' "$dockerfile" | grep -qiE 'mongosh|/opt/node|libstdc\+\+|libatomic|libgcc|official-node'
! grep -qE 'mongosh-patches|mongosh_version|official-ppc64le-node' "$workflow"
! grep -q 'mongosh_version' "$root/.github/workflows/release-all.yml"
# The target runtimes are emulated through binfmt, registered from the image
# the job pulls through build/ferretdb/pull-image.sh (Docker Hub with retries,
# then its mirrors) - what docker/setup-qemu-action did, without its single
# unretried Docker Hub pull (tests/pull-image.py checks the step order).
grep -q 'pull-image.sh tonistiigi/binfmt:latest' "$workflow" || { echo 'docker.yml must pull binfmt through pull-image.sh' >&2; exit 1; }
grep -q 'tonistiigi/binfmt:latest --install arm,arm64,ppc64le,s390x,riscv64' "$workflow" || { echo 'docker.yml must register qemu for every target' >&2; exit 1; }
grep -q 'bash build/ferretdb/release-platforms.sh dist' "$workflow"
# Exercise actual selection with complete, partial and empty release assets.
# The unsupported platforms previously made every registry build fail.
: "${TMPDIR:?set TMPDIR to a repository-local temporary directory}"
tmp="$(mktemp -d "$TMPDIR/ferretdb-platforms.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
for arch in amd64 arm64 armhf i386 ppc64le s390x riscv64 armv6 armel loong64; do
  printf 'binary' > "$tmp/ferretdb-$arch"
done
select_platforms() { bash "$root/build/ferretdb/release-platforms.sh" "$tmp"; }
[ "$(select_platforms 2> "$tmp/warnings")" = 'linux/amd64,linux/arm64,linux/arm/v7,linux/386,linux/ppc64le,linux/s390x,linux/riscv64' ]
for arch in armv6 armel loong64; do
  grep -q "Omitting $arch container" "$tmp/warnings"
done
# A platform needs only its ferretdb binary now: no mongosh package beside it.
! ls "$tmp"/mongosh-* >/dev/null 2>&1
rm "$tmp/ferretdb-armhf"
select_platforms > "$tmp/result" 2> "$tmp/warnings"
! grep -q 'linux/arm/v7' "$tmp/result"
rm "$tmp"/ferretdb-*
if select_platforms > "$tmp/result" 2> "$tmp/error"; then
  echo 'Empty release must fail selection' >&2; exit 1
fi
grep -q 'No supported FerretDB' "$tmp/error"
echo 'docker_release_test.sh: ok'
