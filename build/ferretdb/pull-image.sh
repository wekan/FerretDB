#!/usr/bin/env bash
# Pull a container image, and keep pulling it when Docker Hub does not answer.
#
#   pull-image.sh <image> [<platform>]      e.g. pull-image.sh debian:trixie-slim linux/ppc64le
#
# The Docker image job pulls from Docker Hub before it builds anything - the
# qemu registration (tonistiigi/binfmt), the BuildKit builder (moby/buildkit)
# and the ppc64le runtime check (debian:trixie-slim) - and a run of v1.89.0
# died on the last of those with Docker Hub's "toomanyrequests", the
# unauthenticated pull limit a shared runner address runs into. `docker run`
# pulls once and gives up.
#
# So the image is pulled here first, and from the first of these that answers:
#
#   1. Docker Hub itself, as named - twice, unless it says the pull limit is
#      reached, when asking again only makes it worse;
#   2. mirror.gcr.io - Google's pull-through cache of Docker Hub, the same
#      images by the same names;
#   3. public.ecr.aws/docker/library - the Docker Official Images as Docker
#      publishes them on Amazon ECR Public, for official images such as debian.
#
# A mirror's copy is tagged with the name it was asked for, so a following
# `docker run <image>` finds it locally and pulls nothing. Which source served
# it is printed, so the log says where the image came from.
#
# PULL_IMAGE_SLEEP sets the seconds between attempts (tests set it to 0).
set -euo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <image> [<platform>]" >&2
  exit 2
fi
image="$1"
platform="${2:-}"
pause="${PULL_IMAGE_SLEEP:-15}"

# Only Docker Hub names: no registry host before the first "/" (a host has a
# "." or ":" in it, or is localhost).
first="${image%%/*}"
if [ "$first" != "$image" ] && { [[ "$first" == *.* ]] || [[ "$first" == *:* ]] || [ "$first" = localhost; }; then
  echo "pull-image.sh: $image is not a Docker Hub image; pulling it as named." >&2
  hub_only=1
else
  hub_only=0
fi

name="${image%%:*}"
tag="${image#"$name"}"; tag="${tag#:}"; tag="${tag:-latest}"
case "$name" in
  */*) repo="$name" ;;
  *)   repo="library/$name" ;;
esac

pull() {
  # $1 reference, $2 platform (may be empty); output kept for the caller.
  if [ -n "$2" ]; then
    docker pull --platform "$2" "$1" 2>&1
  else
    docker pull "$1" 2>&1
  fi
}

try() {
  # $1 reference, $2 platform, $3 attempts
  local ref="$1" plat="$2" attempts="$3" n out
  for n in $(seq 1 "$attempts"); do
    if out="$(pull "$ref" "$plat")"; then
      echo "Pulled $ref${plat:+ ($plat)}."
      return 0
    fi
    printf '%s\n' "$out" | tail -n 2 >&2
    if printf '%s' "$out" | grep -q 'toomanyrequests'; then
      echo "$ref: pull limit reached; not asking again." >&2
      return 1
    fi
    if [ "$n" -lt "$attempts" ]; then
      echo "$ref: attempt $n of $attempts failed; trying again in ${pause}s." >&2
      sleep "$pause"
    fi
  done
  return 1
}

use() {
  # Name a mirror's copy as the workflow names the image.
  if [ "$1" != "$image" ]; then
    docker tag "$1" "$image"
    echo "Tagged $1 as $image."
  fi
  echo "Build image $image came from $1."
  exit 0
}

if try "$image" "$platform" 2; then use "$image"; fi
if [ "$hub_only" = 1 ]; then
  echo "::error::Could not pull $image." >&2
  exit 1
fi

if try "mirror.gcr.io/$repo:$tag" "$platform" 3; then use "mirror.gcr.io/$repo:$tag"; fi

ecr=""
case "$repo" in
  library/*) ecr="public.ecr.aws/docker/$repo:$tag" ;;
  i386/*)
    # The official image's linux/386 variant; only for that platform.
    if [ -z "$platform" ] || [ "$platform" = linux/386 ]; then
      ecr="public.ecr.aws/docker/library/${repo#i386/}:$tag"
      platform=linux/386
    fi
    ;;
esac
if [ -n "$ecr" ] && try "$ecr" "$platform" 3; then use "$ecr"; fi

echo "::error::Could not pull $image from Docker Hub or its mirrors (mirror.gcr.io${ecr:+, public.ecr.aws})." >&2
exit 1
