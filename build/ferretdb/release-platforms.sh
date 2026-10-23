#!/usr/bin/env bash
# Debian trixie-slim runtime platforms, intersected with release assets.
# ARMv6 and Loong64 have standalone binaries but no matching official base.
#
#   release-platforms.sh <dist> [asset]
#
# asset is the binary name before -<arch>: ferretdb (default, every handler) or
# ferretdb-sqlite (the SQLite-only build), so each image gets the platforms its
# own binaries exist for.
set -euo pipefail
dist="${1:?release asset directory required}"
asset="${2:-ferretdb}"
case "$asset" in
  ferretdb|ferretdb-sqlite) ;;
  *) echo "Unknown FerretDB asset name: $asset" >&2; exit 2 ;;
esac
platforms=""
for binary in amd64 arm64 armhf i386 ppc64le s390x riscv64; do
  [ -f "$dist/$asset-$binary" ] || continue
  shell="$binary"
  case "$binary" in
    armhf) platform=linux/arm/v7; shell=armv7 ;;
    i386) platform=linux/386 ;;
    *) platform="linux/$binary" ;;
  esac
  if [ ! -s "$dist/mongosh-$shell.tgz" ]; then
    echo "Missing mongosh-$shell.tgz for $asset-$binary" >&2
    exit 1
  fi
  platforms="${platforms:+$platforms,}$platform"
done
for binary in armv6 armel loong64; do
  if [ -f "$dist/$asset-$binary" ]; then
    echo "Omitting $binary container: no supported Debian runtime/mongosh pair; standalone binary remains available" >&2
  fi
done
if [ -z "$platforms" ]; then
  echo "No supported FerretDB runtime platforms with matching mongosh packages for $asset" >&2
  exit 1
fi
printf '%s\n' "$platforms"
