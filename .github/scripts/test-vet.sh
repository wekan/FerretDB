#!/usr/bin/env bash
# Exercise the actual lint action without invoking Go or repository checks.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
lint_source="$(sed -n '/^act_lint() {/,/^}/p' "$ROOT/build.sh")"
for scenario in success main-vet integration-vet list empty; do
  output="$(SCENARIO="$scenario" bash -c '
    act_no_lfs() { return 0; }
    go_env() { return 0; }
    info() { printf "%s\n" "$*"; }
    err() { printf "%s\n" "$*" >&2; }
    go() {
      if [ "$1" = list ]; then
        [ "$SCENARIO" != list ] || return 1
        [ "$SCENARIO" != empty ] || return 0
        printf "%s\n" example.org/package example.org/tmp/scratch
        return 0
      fi
      printf "vet:%s:%s\n" "${PWD##*/}" "$*"
      case "$SCENARIO:${PWD##*/}" in main-vet:FerretDB|integration-vet:integration) return 1;; esac
      return 0
    }
    eval "$1"
    cd "$2"
    act_lint
  ' _ "$lint_source" "$ROOT" 2>&1)" && status=0 || status=$?
  if [ "$scenario" = success ]; then
    [ "$status" -eq 0 ]
    [[ "$output" = *'vet:FerretDB:'* && "$output" = *'vet:integration:'* ]]
    [[ "$output" != *'example.org/tmp/scratch'* ]]
  else
    [ "$status" -ne 0 ]
    [[ "$output" != *'vet done'* ]]
  fi
  printf 'PASS vet exit status: %s\n' "$scenario"
done
