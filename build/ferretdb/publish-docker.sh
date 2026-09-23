#!/usr/bin/env bash
# Authenticate and publish independently; report partial failures after all hosts.
# This script publishes images and is for release runners / human maintainers.
set +x
set -euo pipefail
: "${VERSION:?release version required}"
: "${PLATFORMS:?runtime platforms required}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
bash "$root/build/ferretdb/validate-version.sh" "$VERSION"
cd "$root"
failed=0
published=0

publish() {
  local host="$1" repository="$2" secret="$3" creds user token attempt logged_in=0
  echo "::group::Publish $repository"
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
    elif docker buildx build --file Dockerfile.release --platform "$PLATFORMS" \
        -t "$repository:$VERSION" -t "$repository:latest" --push .; then
      echo "$repository: published $VERSION and latest"
      published=$((published + 1))
    else
      echo "::error::$repository build/push failed; continuing with other registries"
      failed=1
    fi
  fi
  echo '::endgroup::'
}

publish docker.io wekanteam/ferretdb DOCKERHUB_AUTH
publish quay.io quay.io/wekan/ferretdb QUAY_AUTH
publish ghcr.io ghcr.io/wekan/ferretdb GHCR_AUTH

echo "Published to $published of 3 registries."
if [ "$failed" -ne 0 ]; then
  echo '::error::Some registries failed. Successful publications are retained; see the per-registry logs.'
  exit 1
fi
