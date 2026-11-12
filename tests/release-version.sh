#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
helper="$root/build/ferretdb/next-version.sh"
[[ "$(bash "$helper" v1.99.0)" = v1.100.0 ]]
[[ "$(bash "$helper" v1.100.0)" = v1.101.0 ]]
[[ "$(bash "$helper" v1.72.0)" = v1.73.0 ]]
[[ "$(bash "$helper" v1.72.9)" = v1.73.0 ]]
[[ "$(bash "$helper" v1.0.0)" = v1.1.0 ]]
[[ "$(bash "$helper" v1.999.0)" = v1.1000.0 ]]
for invalid in v2.0.0 v0.99.0 v1.99 v1.x.0 v1.99.0-extra ''; do
  if bash "$helper" "$invalid" >/dev/null 2>&1; then
    echo "Invalid version accepted: $invalid" >&2; exit 1
  fi
done
# Both menu aliases now use the shared audited release launcher. Version
# arithmetic still comes from the same pure helper; CI owns tag publication.
grep -q 'build/ferretdb/next-version.sh' "$root/releases/remote-release.py"
grep -q 'release-ferretdb|release-all)' "$root/build.sh"
! grep -q 'nmin - smin' "$root/build.sh"
echo 'release-version: v1 minor increments without rollover; patch resets to zero'
validator="$root/build/ferretdb/validate-version.sh"
for valid in v1.99.0 v1.100.0 v1.101.0 v1.72.9; do bash "$validator" "$valid"; done
for invalid in v2.0.0 v1.99.0-1-gabcdef latest deadbeef ''; do
  if bash "$validator" "$invalid" >/dev/null 2>&1; then
    echo "Invalid release tag accepted: $invalid" >&2; exit 1
  fi
done
# Exercise the actual resolve-version run blocks with stubbed read-only inputs.
# Cut at the output boundary: no build, download, tag or release operation runs.
: "${TMPDIR:?set TMPDIR to a repository-local temporary directory}"
work="$(mktemp -d "$TMPDIR/ferretdb-version-test.XXXXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
printf '#!/bin/sh\nprintf "%%s\\n" "$TEST_RESOLVED_TAG"\n' > "$work/bin/git"
cp "$work/bin/git" "$work/bin/gh"
chmod +x "$work/bin/git" "$work/bin/gh"
export PATH="$work/bin:$PATH"
for workflow in release-all release-all-missing docker; do
  awk '
    /^      - name: Resolve release (version|tag)/ {selected=1; next}
    selected && /^        run: \|/ {body=1; next}
    body && /^      - / {exit}
    body {sub(/^          /, ""); if ($0 ~ /bash tests\/release-version.sh/) next; print; if ($0 ~ /echo "(version=|VERSION=)/) exit}
  ' "$root/.github/workflows/$workflow.yml" > "$work/resolve.sh"
  grep -q 'bash build/ferretdb/validate-version.sh' "$work/resolve.sh"
  for version in v1.99.0 v1.100.0 v1.101.0 v2.0.0; do
    modes="explicit inferred"
    [[ "$workflow" != docker ]] || modes="$modes release-event"
    for mode in $modes; do
      input="$version"; event=''
      [[ "$mode" != inferred ]] || input=''
      if [[ "$mode" = release-event ]]; then input=''; event="$version"; fi
      : > "$work/output"
      if (cd "$root" && INPUT_VERSION="$input" RELEASE_TAG="$event" TEST_RESOLVED_TAG="$version" GITHUB_OUTPUT="$work/output" GITHUB_ENV="$work/output" bash "$work/resolve.sh") > "$work/log" 2>&1; then
        [[ "$version" != v2.0.0 ]] || { echo "$workflow accepted v2" >&2; exit 1; }
        grep -q "=$version$" "$work/output"
      else
        [[ "$version" = v2.0.0 ]] || { cat "$work/log"; exit 1; }
        [[ ! -s "$work/output" ]]
      fi
    done
  done
done
echo 'release-version: all three workflow resolvers preserve v1 tags and reject v2'
# An inferred version on a branch that moved past its tag (the v1.108.0 retry:
# v1.108.0-2-g0c99dae2) is refused, and the refusal says why and what to run.
if bash "$validator" v1.108.0-2-g0c99dae2 2> "$work/log"; then
  echo "A describe string past a tag was accepted" >&2; exit 1
fi
grep -q '2 commit(s) past v1.108.0' "$work/log"
grep -q 'release-ferretdb' "$work/log"
# Negative: an unrelated invalid version gets no misleading past-a-tag hint.
bash "$validator" latest 2> "$work/log" && exit 1
! grep -q 'past' "$work/log"
echo 'release-version: a branch past its tag is refused with the next step to take'
# The source checks run before anything is published. v1.108.0 was tagged and
# given an empty GitHub Release on a commit tests/integration-module-sync.py
# rejects, because the checks only ran inside the build step. In both release
# workflows, every source check must come before the first step that can push a
# tag or create a release, and none may remain after it.
checks=('check-telemetry.py --source .' tests/release-telemetry.py tests/telemetry-build.py
        tests/release-upload.py tests/integration-module-sync.py tests/dependency-floors.py
        tests/go-telemetry-off.py tests/mptcp-off.py)
for workflow in release-all release-all-missing; do
  file="$root/.github/workflows/$workflow.yml"
  publish="$(grep -n -m1 -E 'git push origin "refs/tags|gh release create' "$file" | cut -d: -f1)"
  resolve="$(grep -n -m1 'name: Resolve release version' "$file" | cut -d: -f1)"
  [[ -n "$publish" && -n "$resolve" ]] || { echo "$workflow: publish/resolve step not found" >&2; exit 1; }
  for check in "${checks[@]}"; do
    lines="$(grep -n -F -e "$check" "$file" | grep -v -E '^[0-9]+:\s*#' | cut -d: -f1)"
    count="$(printf '%s\n' "$lines" | grep -c .)"
    [[ "$count" = 1 ]] || { echo "$workflow: $check runs $count times" >&2; exit 1; }
    (( lines < resolve && lines < publish )) || { echo "$workflow: $check runs after the tag/release step" >&2; exit 1; }
  done
done
echo 'release-version: source checks run before any tag or release is published'
