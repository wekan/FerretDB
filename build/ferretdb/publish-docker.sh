#!/usr/bin/env bash
# Authenticate and publish independently; report partial failures after all hosts.
# This script publishes images and is for release runners / human maintainers.
#
# Two images per registry, from the same downloaded release binaries
# (Dockerfile.release picks one by FERRETDB_ASSET):
#   <registry>/ferretdb          ferretdb-<arch>: every handler
#   <registry>/ferretdb-sqlite   ferretdb-sqlite-<arch>: the sqlite handler only
#
# PLATFORMS is the ferretdb image's platform list and SQLITE_PLATFORMS the
# ferretdb-sqlite one's (build/ferretdb/release-platforms.sh dist ferretdb-sqlite).
# A release made before the SQLite-only build has no ferretdb-sqlite-<arch>, so
# an empty SQLITE_PLATFORMS publishes only the ferretdb image and says so.
#
# A repository that does not exist yet is created by its first push with the
# registry's default visibility - Quay and GHCR make it PRIVATE - so the first
# ferretdb-sqlite push has to be followed by making that package public.
set +x
set -euo pipefail
: "${VERSION:?release version required}"
: "${PLATFORMS:?runtime platforms required}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
bash "$root/build/ferretdb/validate-version.sh" "$VERSION"
cd "$root"
failed=0
published=0
total=0

# "<image name> <FERRETDB_ASSET> <platforms>"
images=("ferretdb ferretdb $PLATFORMS")
if [ -n "${SQLITE_PLATFORMS:-}" ]; then
  images+=("ferretdb-sqlite ferretdb-sqlite $SQLITE_PLATFORMS")
else
  echo "::warning::$VERSION has no ferretdb-sqlite-<arch> binaries; only the ferretdb image is published."
fi

publish() {
  local host="$1" namespace="$2" secret="$3" creds user token attempt logged_in=0
  local image name asset platforms repository
  total=$((total + ${#images[@]}))
  echo "::group::Publish to $host"
  if ! creds="$(printf '%s' "${!secret:-}" | base64 -d 2>/dev/null)"; then
    echo "::error::$host credentials are not valid base64"
    failed=1
  elif [[ "$creds" != *:* || -z "${creds%%:*}" || -z "${creds#*:}" ]]; then
    echo "::error::$host credentials are missing or malformed"
    failed=1
  else
    user="${creds%%:*}"
    token="${creds#*:}"
    for attempt in 1 2; do
      if printf '%s' "$token" | timeout 120 docker login "$host" -u "$user" --password-stdin; then
        logged_in=1
        break
      fi
      echo "::warning::$host login attempt $attempt failed; other registries will still be attempted"
      if [ "$attempt" -eq 1 ]; then sleep 5; fi
    done
    if [ "$logged_in" -eq 0 ]; then
      echo "::error::$host login unavailable; skipping only this registry"
      failed=1
    else
      for image in "${images[@]}"; do
        read -r name asset platforms <<< "$image"
        repository="$namespace/$name"
        if docker buildx build --file Dockerfile.release --platform "$platforms" \
            --build-arg "FERRETDB_ASSET=$asset" \
            -t "$repository:$VERSION" -t "$repository:latest" --push .; then
          echo "$repository: published $VERSION and latest"
          published=$((published + 1))
        else
          echo "::error::$repository build/push failed; continuing with the other images and registries"
          failed=1
        fi
      done
    fi
  fi
  echo '::endgroup::'
}

publish docker.io wekanteam DOCKERHUB_AUTH
publish quay.io quay.io/wekan QUAY_AUTH
publish ghcr.io ghcr.io/wekan GHCR_AUTH

echo "Published $published of $total images to 3 registries."
if [ "$failed" -ne 0 ]; then
  echo '::error::Some registries failed. Successful publications are retained; see the per-registry logs.'
  exit 1
fi
