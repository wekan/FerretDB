#!/usr/bin/env bash
#
# build.sh - interactive helper for FerretDB v1 (SQLite).
#
# Usage:
#   ./build.sh              # interactive menu
#   ./build.sh <command>    # run one action non-interactively, e.g.:
#   ./build.sh deps | build | run | goenv | unit | lint | docker | clean
#   ./build.sh no-lfs                  # fail if anything is stored in Git LFS (also part of lint)
#   ./build.sh dist                    # build all per-arch binaries, sequential (default)
#   ./build.sh dist-seq                # build all per-arch binaries, one platform at a time
#   ./build.sh dist-par                # build all per-arch binaries, all platforms in parallel
#   ./build.sh release [version]       # trigger release-all.yml (per-arch binaries + GitHub Release)
#   ./build.sh release-ferretdb        # full release: rename Upcoming, tag + push, then run release-all.yml (-> docker.yml)
#   ./build.sh docker-release [version]# trigger docker.yml (multi-arch image to registries)
#   ./build.sh test                    # integration tests, parallel (default)
#   ./build.sh test-seq                # integration tests, sequential (one at a time)
#   ./build.sh test-par [N]            # integration tests, parallel with N workers
#   ./build.sh test-one <Test> [seq|par]   # one integration test (default parallel)
#
# Test parallelism can also be set with TEST_PARALLEL=<N> (defaults to CPU count).
# It self-installs a local Go toolchain under ./.goroot if `go` is not found.

set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

GO_VERSION="1.25.11"
LOCAL_GOROOT="$ROOT/.goroot"
STATE_DIR="$ROOT/state"
SQLITE_TEST_DIR="$ROOT/tmp/sqlite-tests"
LISTEN_ADDR="${FERRETDB_LISTEN_ADDR:-127.0.0.1:27017}"

# ---- colours -------------------------------------------------------------
if [ -t 1 ]; then
  B="\033[1m"; G="\033[32m"; Y="\033[33m"; R="\033[31m"; N="\033[0m"
else
  B=""; G=""; Y=""; R=""; N=""
fi
info() { printf "${G}==>${N} %s\n" "$*"; }
warn() { printf "${Y}==>${N} %s\n" "$*"; }
err()  { printf "${R}==>${N} %s\n" "$*" >&2; }

# ---- Go toolchain --------------------------------------------------------
detect_go() {
  if [ -x "$LOCAL_GOROOT/bin/go" ]; then
    export GOROOT="$LOCAL_GOROOT"
    export PATH="$LOCAL_GOROOT/bin:$PATH"
  fi
  command -v go >/dev/null 2>&1
}

install_go() {
  local os arch
  case "$(uname -s)" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *) err "Unsupported OS $(uname -s); install Go $GO_VERSION manually."; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) err "Unsupported arch $(uname -m); install Go $GO_VERSION manually."; return 1 ;;
  esac
  local tarball="go${GO_VERSION}.${os}-${arch}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  info "Downloading $url ..."
  rm -rf "$LOCAL_GOROOT" "$ROOT/.go-dl.tgz"
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --retry 3 -o "$ROOT/.go-dl.tgz" "$url" || { err "download failed"; return 1; }
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$ROOT/.go-dl.tgz" "$url" || { err "download failed"; return 1; }
  else
    err "need curl or wget to download Go"; return 1
  fi
  mkdir -p "$LOCAL_GOROOT"
  tar -C "$ROOT" -xzf "$ROOT/.go-dl.tgz" || { err "extract failed"; return 1; }
  # the tarball extracts to ./go ; move it to ./.goroot
  rm -rf "$LOCAL_GOROOT"; mv "$ROOT/go" "$LOCAL_GOROOT"
  rm -f "$ROOT/.go-dl.tgz"
  export GOROOT="$LOCAL_GOROOT"
  export PATH="$LOCAL_GOROOT/bin:$PATH"
  info "Installed $(go version)"
}

ensure_go() {
  if detect_go; then
    return 0
  fi
  warn "Go not found on PATH."
  printf "Install a local Go %s toolchain under ./.goroot ? [Y/n] " "$GO_VERSION"
  read -r ans
  case "${ans:-Y}" in
    [nN]*) err "Go is required. Aborting."; exit 1 ;;
    *) install_go || exit 1 ;;
  esac
}

go_env() {
  ensure_go
  # keep module graph writable so first build can resolve deps
  export GOFLAGS="${GOFLAGS:-} -mod=mod"
  export FERRETDB_TELEMETRY=disable
  # Put Go's build scratch dir ($WORK) on the same (large) disk as the repo
  # instead of the default $TMPDIR — /tmp is often a small tmpfs, and building
  # many targets in parallel (option 12) can otherwise fail with
  # "no space left on device". Kept under tmp/ (gitignored).
  export GOTMPDIR="${GOTMPDIR:-$ROOT/tmp/go}"
  mkdir -p "$GOTMPDIR"
}

# ---- actions -------------------------------------------------------------
act_goenv() {
  go_env
  info "GOROOT=$GOROOT"
  info "$(go version)"
  info "GOFLAGS=$GOFLAGS"
}

act_deps() {
  go_env
  info "Downloading root module dependencies ..."
  go mod download all || return 1
  if [ -f integration/go.mod ]; then
    info "Downloading integration module dependencies ..."
    ( cd integration && go mod download all )
  fi
  if [ -f tools/go.mod ]; then
    info "Downloading tools module dependencies ..."
    ( cd tools && go mod download all )
  fi
  info "Dependencies ready."
}

act_build() {
  go_env
  # Regenerate build/version FIRST, exactly as act_dist does. Go stamps the VCS
  # revision into the binary, and build/version/version.go panics at STARTUP when
  # that revision disagrees with the committed build/version/commit.txt:
  #
  #   panic: commit.txt value "e7820f36..." != vcs.revision value "cd795fa7..."
  #
  # Those files are only refreshed by this generator, so every commit made after
  # the last refresh produced a binary that could not start at all - which is what
  # `./build.sh build` did until now, while the release build was fine because it
  # already regenerated them.
  info "Generating version info (build/version) ..."
  ( cd build/version && go run generate.go ) || { err "gen-version failed"; return 1; }
  info "Building FerretDB (sqlite, postgresql, mysql, hana handlers) -> bin/ferretdb ..."
  mkdir -p bin
  # Same build tag as the release build below, so a local binary answers the same
  # --handler values the released ones do.
  go build -tags ferretdb_hana -o bin/ferretdb ./cmd/ferretdb || return 1
  info "Built bin/ferretdb"
}

act_run() {
  act_build || return 1
  mkdir -p "$STATE_DIR"
  info "Running FerretDB v1 with the SQLite backend."
  info "  listen: $LISTEN_ADDR   data: $STATE_DIR"
  info "  connect: mongodb://$LISTEN_ADDR/   (Ctrl-C to stop)"
  FERRETDB_HANDLER=sqlite \
  FERRETDB_SQLITE_URL="file:$STATE_DIR/" \
  FERRETDB_LISTEN_ADDR="$LISTEN_ADDR" \
  FERRETDB_TELEMETRY=disable \
    exec ./bin/ferretdb
}

# ---- per-arch binaries (cross-compile for every platform) ----------------
# Release arch name + GOOS + GOARCH + GOARM. .exe suffix is added only for
# Windows; every other platform's binary has no extension. Each target compiles
# to a single self-contained binary named ferretdb-<arch>[.exe]; these are the
# individual assets attached to the GitHub Release (no ferretdb.zip). Targets a
# platform can't compile (e.g. an arch modernc.org/sqlite has no port for) are
# skipped.
FERRETDB_DIST_TARGETS=(
  "amd64 linux amd64 "
  "arm64 linux arm64 "
  "armhf linux arm 7"
  "armel linux arm 5"
  "i386 linux 386 "
  "ppc64le linux ppc64le "
  "s390x linux s390x "
  "riscv64 linux riscv64 "
  "loong64 linux loong64 "
  "win64 windows amd64 "
  "win-arm64 windows arm64 "
  "win32 windows 386 "
  "mac-amd64 darwin amd64 "
  "mac-arm64 darwin arm64 "
  "freebsd-amd64 freebsd amd64 "
  "freebsd-arm64 freebsd arm64 "
)

# build_ferretdb_target <name> <goos> <goarch> <goarm> <out-dir> <report-dir>
# Builds one target; on success writes the (executable) binary as
# <out-dir>/ferretdb-<name>[.exe] and records <name> in <report-dir>/built.list,
# else skips and records it in <report-dir>/failed.list. Safe to run concurrently.
build_ferretdb_target() {
  local name="$1" goos="$2" goarch="$3" goarm="$4" out="$5" rep="$6"
  local ext=""; [ "$goos" = windows ] && ext=".exe"
  mkdir -p "$out"
  # FERRETDB_DIST_SKIP_LIST names a file of asset names already on the release
  # (one per line). A target listed there is not rebuilt, which is how
  # release-all-missing.yml builds only the gap instead of all sixteen
  # platforms. Absent or empty => build everything, which is what an ordinary
  # release does.
  #
  # BOTH the binary and its .sha256sum have to be there. A binary whose checksum
  # upload failed is exactly the half-published state that workflow exists to
  # repair, and treating the binary alone as present would leave it that way.
  if [ -n "${FERRETDB_DIST_SKIP_LIST:-}" ] && [ -s "${FERRETDB_DIST_SKIP_LIST}" ] \
     && grep -qxF "ferretdb-$name$ext" "$FERRETDB_DIST_SKIP_LIST" \
     && grep -qxF "ferretdb-$name$ext.sha256sum" "$FERRETDB_DIST_SKIP_LIST"; then
    printf '%s\n' "$name" >> "$rep/have.list"
    info "  have    $name (already on the release)"
    return 0
  fi
  # -tags ferretdb_hana: the "hana" handler is behind that build tag, so without it
  # the released binaries answer `--handler=hana` with "unknown handler" - which is
  # what a client compose file for SAP HANA would hit. go-hdb is pure Go, so it
  # cross-compiles with CGO_ENABLED=0 like the rest.
  if CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
       go build -trimpath -tags ferretdb_hana -o "$out/ferretdb-$name$ext" ./cmd/ferretdb 2>"$rep/$name.log"; then
    chmod +x "$out/ferretdb-$name$ext"
    printf '%s\n' "$name" >> "$rep/built.list"
    info "  built   $name"
  else
    printf '%s\n' "$name" >> "$rep/failed.list"
    warn "  skipped $name (does not compile) — see $rep/$name.log"
    tail -3 "$rep/$name.log" | sed 's/^/        /' >&2 || true
    rm -f "$out/ferretdb-$name$ext"
  fi
}

# act_dist <seq|par> — cross-compile FerretDB (SQLite) for all platforms into
# ./dist/ as individual, self-contained binaries named ferretdb-<arch>[.exe]
# (plus a README.md). No ferretdb.zip is produced: the GitHub Release attaches
# every binary separately, so each consumer downloads only the one binary for the
# platform it targets.
act_dist() {
  local mode="${1:-seq}"
  go_env

  info "Generating version info (build/version) ..."
  ( cd build/version && go run generate.go ) || { err "gen-version failed"; return 1; }
  local fver; fver="$(cat build/version/version.txt 2>/dev/null || echo unknown)"
  info "FerretDB version: $fver"

  local out="$ROOT/dist"
  local rep="$ROOT/tmp/ferretdb-dist"
  rm -rf "$out" "$rep"; mkdir -p "$out" "$rep"
  : > "$rep/built.list"; : > "$rep/failed.list"; : > "$rep/have.list"

  # Prime the module + build cache once (resolve deps, compile shared std/deps)
  # so the parallel builds below don't all race downloading/compiling at once.
  info "Priming build cache (this resolves modules on first run) ..."
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/ferretdb 2>/dev/null || true

  if [ "$mode" = par ]; then
    info "Cross-compiling FerretDB for all platforms — PARALLEL ..."
    local pids=()
    for t in "${FERRETDB_DIST_TARGETS[@]}"; do
      # shellcheck disable=SC2086
      set -- $t
      build_ferretdb_target "$1" "$2" "$3" "${4:-}" "$out" "$rep" &
      pids+=($!)
    done
    wait "${pids[@]}" 2>/dev/null || true
  else
    info "Cross-compiling FerretDB for all platforms — SEQUENTIAL ..."
    for t in "${FERRETDB_DIST_TARGETS[@]}"; do
      # shellcheck disable=SC2086
      set -- $t
      build_ferretdb_target "$1" "$2" "$3" "${4:-}" "$out" "$rep"
    done
  fi

  {
    echo "# FerretDB v1 (SQLite)"
    echo
    echo "FerretDB v1 binaries compiled from this fork:"
    echo "https://github.com/wekan/FerretDB"
    echo
    echo "FerretDB is an open-source, MongoDB-compatible database. These builds use"
    echo "the embedded pure-Go SQLite backend, so each binary is a single"
    echo "self-contained MongoDB-compatible server that needs no external database."
    echo
    echo "Version: $fver"
    echo
    echo "## Assets"
    echo
    echo "Each platform is a separate release asset (no ferretdb.zip):"
    echo
    echo "    ferretdb-<arch>        (Linux/macOS/BSD)"
    echo "    ferretdb-<arch>.exe    (Windows)"
    echo
    echo "Download only the one you need, e.g.:"
    echo
    echo "    curl -fSLO https://github.com/wekan/FerretDB/releases/latest/download/ferretdb-amd64"
    echo
    echo "Included in this build: $(tr '\n' ' ' < "$rep/built.list")"
    echo
    echo "## Run"
    echo
    echo "    ferretdb-<arch> --handler=sqlite --sqlite-url=file:./ferretdb-sqlite/ \\"
    echo "      --listen-addr=127.0.0.1:27017 --telemetry=disable"
    echo
    echo "Then point your MongoDB client at it: MONGO_URL=mongodb://127.0.0.1:27017/mydb"
    echo
    echo "Telemetry is disabled by the flag above (FerretDB also honors DO_NOT_TRACK=1)."
  } > "$out/README.md"

  info "Created per-arch binaries under $out/"
  info "Built:   $(tr '\n' ' ' < "$rep/built.list")"
  if [ -s "$rep/have.list" ]; then
    info "Have:    $(tr '\n' ' ' < "$rep/have.list") (already on the release, not rebuilt)"
  fi
  if [ -s "$rep/failed.list" ]; then
    warn "Skipped: $(tr '\n' ' ' < "$rep/failed.list")"
  fi
  echo
  ls -la "$out"
}

# number of parallel test workers; override with TEST_PARALLEL.
cpu_count() { nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4; }

# Integration tests run in-process against the SQLite backend.
# -target-tls is REQUIRED (otherwise the client hits SCRAM auth with no
# credentials and the run hangs). Certs live in build/certs/.
#
# run_integration <runexpr> <mode>
#   mode: seq -> one test at a time  (-p 1 -parallel 1)
#         par -> concurrent          (-parallel N; N = TEST_PARALLEL or CPU count)
run_integration() {
  go_env
  local runexpr="${1:-}"
  local mode="${2:-par}"
  rm -rf "$SQLITE_TEST_DIR"; mkdir -p "$SQLITE_TEST_DIR"
  local args=(test -count=1 -timeout=600s
    -target-backend=ferretdb-sqlite
    -sqlite-url="file:../tmp/sqlite-tests/"
    -target-tls)
  local desc
  case "$mode" in
    seq)
      args+=(-p 1 -parallel 1)
      desc="sequential"
      ;;
    *)
      local n="${TEST_PARALLEL:-$(cpu_count)}"
      args+=(-parallel "$n")
      desc="parallel (${n} workers)"
      ;;
  esac
  if [ -n "$runexpr" ]; then
    args+=(-run "$runexpr" -v)
  fi
  info "Running integration tests (SQLite, in-process, TLS, ${desc})${runexpr:+ matching '$runexpr'} ..."
  ( cd integration && go "${args[@]}" . )
}

act_test()     { run_integration "" "${1:-par}"; }
act_test_one() {
  if [ -z "${1:-}" ]; then err "usage: $0 test-one <TestNameRegex> [seq|par]"; return 2; fi
  run_integration "$1" "${2:-par}"
}

act_unit() {
  go_env
  # The ferretdb_debug build tag enables the debug-only assertions that some unit
  # tests require (e.g. TestCheckError asserts debugbuild.Enabled); it is the tag
  # FerretDB's own `task test-unit` builds with.
  info "Running unit tests (./internal/... ./cmd/..., -tags ferretdb_debug) ..."
  go test -count=1 -short -tags=ferretdb_debug ./internal/... ./cmd/...
}

# act_test_all — every FerretDB test there is, SEQUENTIALLY, in one go.
#
# Unit tests, then vet, then the integration suite one test at a time. Sequential
# because that is what makes a failure readable: a parallel integration run
# interleaves the output of a dozen tests, and the point of "run everything" is to
# find out what is broken, not to find out quickly.
#
# WeKan's own build.sh calls this (FerretDB is a subdirectory of the wekan repo),
# so it must run without a menu and return a meaningful exit code: 0 only when
# every stage passed.
act_test_all() {
  go_env

  # ../log/<datetime>/ - the same place every WeKan test run writes, because
  # FerretDB is a subdirectory of the wekan repo and an admin looking for "the
  # newest test logs" should find all of them in one directory. WEKAN_LOGDIR is
  # set when WeKan's build.sh is driving this, so one run shares one directory.
  local logdir="${WEKAN_LOGDIR:-}"
  if [ -z "$logdir" ]; then
    # $ROOT is wekan/FerretDB, so ../../log is the ../log/ WeKan writes to.
    logdir="$ROOT/../../log/$(date '+%Y-%m-%d_%H-%M-%S')"
  fi
  mkdir -p "$logdir" 2>/dev/null || logdir="$ROOT/tmp"
  logdir="$(cd "$logdir" && pwd)"

  local failed=""

  info "=== 1/3 unit tests ===   -> $logdir/ferretdb-unit.log"
  act_unit 2>&1 | tee "$logdir/ferretdb-unit.log"
  [ "${PIPESTATUS[0]}" -eq 0 ] || failed="$failed unit"

  info "=== 2/3 vet ===          -> $logdir/ferretdb-vet.log"
  act_lint 2>&1 | tee "$logdir/ferretdb-vet.log"
  [ "${PIPESTATUS[0]}" -eq 0 ] || failed="$failed vet"

  info "=== 3/3 integration tests (SQLite, sequential) === -> $logdir/ferretdb-integration.log"
  act_test seq 2>&1 | tee "$logdir/ferretdb-integration.log"
  [ "${PIPESTATUS[0]}" -eq 0 ] || failed="$failed integration"

  if [ -n "$failed" ]; then
    err "FerretDB tests FAILED:$failed  (logs: $logdir/)"
    return 1
  fi

  info "All FerretDB tests passed (unit, vet, integration). Logs: $logdir/"
}

act_no_lfs() {
  # Repository hygiene, not Go: this fork stores nothing in Git LFS, because an
  # LFS budget in a fork network is upstream's and one pointer breaks every
  # clone. The script explains the rest.
  "$ROOT/.github/scripts/no-lfs.sh"
}

act_lint() {
  act_no_lfs || return 1
  go_env
  # -composites=false, or this stage reports nothing usable.
  #
  # The `composites` analyzer flags a composite literal of an IMPORTED struct
  # type that sets its fields positionally. `bson.E{"key", value}` is exactly
  # that, and it is also the documented way to write a BSON element - the driver
  # named the fields Key and Value precisely so the positional form reads as an
  # ordered pair. FerretDB's tests are made of them, so the analyzer produced
  # 8449 identical lines in the last run, out of 8457 lines of output.
  #
  # That is not a lint result, it is a wall. A real finding - the one line about
  # a package outside the main module, say - is invisible in it, and a stage
  # nobody can read is a stage nobody reads. Every other vet analyzer stays on.
  # ./... walks tmp/ as well, which is this script's own scratch: GOTMPDIR is
  # tmp/go, and a Go module cache has ended up under tmp/gopath. vet then
  # reports on the Go toolchain's own sources and on a dependency's copy in the
  # module cache - "use of internal package internal/runtime/sys not allowed",
  # "directory tmp/gopath/... outside main module" - neither of which is about
  # FerretDB. tmp/ is gitignored scratch, so it is filtered out of the package
  # list rather than vetted.
  info "go vet -composites=false ./... ..."
  vet_pkgs() { go list ./... 2>/dev/null | grep -v "/tmp/" || true; }
  # shellcheck disable=SC2046
  go vet -composites=false $(vet_pkgs) || true
  ( cd integration && go vet -composites=false $(vet_pkgs) || true )
  info "vet done (install golangci-lint separately for full linting)."
}

act_docker() {
  if ! command -v docker >/dev/null 2>&1; then err "docker not found"; return 1; fi
  info "docker compose up --build  (FerretDB v1 SQLite + example app) ..."
  docker compose up --build
}

act_clean() {
  info "Removing bin/, state/, tmp/sqlite-tests/ ..."
  rm -rf bin/ferretdb "$STATE_DIR" "$SQLITE_TEST_DIR"
  if detect_go; then go clean -cache 2>/dev/null || true; fi
  info "Clean done. (Local Go toolchain in ./.goroot kept; delete it manually to remove.)"
}

# ---- run GitHub Actions workflows ----------------------------------------
# Push the current branch (so the remote has the workflow file + latest commit)
# and trigger a workflow via `gh`. Both FerretDB workflows take an optional
# `version` input, so this shared helper handles both.
#   trigger_workflow <workflow-file> [version]
#
# Requirements so this dispatches automatically (instead of you clicking
# "Run workflow" in the Actions tab):
#   1. `gh` installed and authenticated for github.com (`gh auth login`, or a
#      GH_TOKEN / GITHUB_TOKEN env var).
#   2. The token must be able to dispatch workflows. A classic PAT needs the
#      `workflow` scope (plus `repo`); a fine-grained PAT / GitHub App token needs
#      Actions: write. A classic token with only `repo` returns 403 "Resource not
#      accessible" — the usual silent cause; fix that one with
#      `gh auth refresh -h github.com -s workflow`. We do NOT pre-flag the scope,
#      because `gh auth status` does not report the capability for fine-grained /
#      app tokens (which dispatch fine) — so a pre-check gives false 403 warnings.
#      A real permission error surfaces in the final error message below instead.
#   3. The workflow must exist on the default branch with `on: workflow_dispatch`
#      (all FerretDB workflows do). A JUST-pushed new workflow is not dispatchable
#      until GitHub registers it (a few seconds) — handled by the retry loop below.
# This function pins the repo with `-R`, verifies auth, retries the registration
# race, and falls back to the REST dispatch API.
trigger_workflow() {
  command -v gh >/dev/null 2>&1 || {
    err "'gh' (GitHub CLI) is required and must be authenticated: run 'gh auth login'."
    return 1
  }
  local wf="$1" version="${2:-}" branch repo i ok

  # Resolve OWNER/REPO from the git remote so `gh` targets the right repo even when
  # run outside a detected checkout; fall back to the fork's canonical path.
  repo="$(git remote get-url origin 2>/dev/null \
    | sed -E 's#^(git@github.com:|https://github.com/)##; s#\.git$##')"
  [ -n "$repo" ] || repo="wekan/FerretDB"

  # Auth preflight — missing auth is fatal (the scope/permission is NOT pre-checked;
  # see note 2 above — a real 403 is reported by the final error handler).
  if ! gh auth status -h github.com >/dev/null 2>&1; then
    err "gh is not authenticated for github.com. Run: gh auth login  (or set GH_TOKEN)."
    return 1
  fi

  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo main-v1)"

  info "Pushing '$branch' so the workflow runs the latest committed version ..."
  git push origin "$branch" || warn "git push reported no changes / failed; continuing to trigger."

  info "Triggering $wf on '$repo' ('$branch')${version:+ (version $version)} ..."
  # `gh workflow run`, retried for the new-workflow registration race.
  ok=0
  for i in 1 2 3 4 5; do
    if [ -n "$version" ]; then
      gh workflow run "$wf" -R "$repo" --ref "$branch" -f version="$version" && { ok=1; break; }
    else
      gh workflow run "$wf" -R "$repo" --ref "$branch" && { ok=1; break; }
    fi
    warn "trigger attempt $i/5 failed (GitHub may still be registering '$wf'); retrying in 5s ..."
    sleep 5
  done

  # Fallback: the REST dispatch API (some gh versions resolve the workflow id more
  # reliably this way). Body is built as explicit JSON to avoid input-syntax issues.
  if [ "$ok" -ne 1 ]; then
    warn "Direct 'gh workflow run' did not succeed; trying the REST dispatch API ..."
    if [ -n "$version" ]; then
      printf '{"ref":"%s","inputs":{"version":"%s"}}' "$branch" "$version" \
        | gh api "repos/$repo/actions/workflows/$wf/dispatches" --input - && ok=1
    else
      printf '{"ref":"%s"}' "$branch" \
        | gh api "repos/$repo/actions/workflows/$wf/dispatches" --input - && ok=1
    fi
  fi

  if [ "$ok" -ne 1 ]; then
    err "Could not dispatch $wf on $repo. If this is a 403, the token cannot dispatch"
    err "workflows: a classic PAT needs the 'workflow' scope"
    err "(gh auth refresh -h github.com -s workflow); a fine-grained PAT / app token needs"
    err "Actions: write. Also check $wf exists on the default branch and you can push to $repo."
    err "You can start it manually at: https://github.com/$repo/actions"
    return 1
  fi

  info "Triggered. Track progress at: https://github.com/$repo/actions"
  info "Requires the DOCKERHUB_AUTH / QUAY_AUTH / GHCR_AUTH secrets in this repo."
}

# release-all.yml: build the per-arch binaries for all platforms + publish the
# GitHub Release (with notes from CHANGELOG.md). Nothing is built locally.
#   act_release [version]   (empty version => the workflow uses `git describe`)
act_release() {
  local version="${1:-}"
  if [ -z "$version" ] && [ -t 0 ]; then
    printf "Release tag (e.g. v1.25.0; empty = git describe on the runner): "
    read -r version
  fi
  trigger_workflow release-all.yml "$version"
}

# docker.yml: build the multi-arch FerretDB Docker image from the prebuilt
# per-arch binaries attached to a GitHub Release and push to Docker Hub, Quay.io
# and GHCR. Does NOT recompile — run this after a release exists.
#   act_release_docker [version]   (empty version => the newest release)
act_release_docker() {
  local version="${1:-}"
  if [ -z "$version" ] && [ -t 0 ]; then
    printf "Release tag to build the image from (empty = newest release): "
    read -r version
  fi
  trigger_workflow docker.yml "$version"
}

# "Release FerretDB": the one-command full release. Renames "## Upcoming FerretDB
# release" to the next version (auto, with the correct tag link), commits everything,
# tags vX.Y.Z and pushes the branch + tag, and THEN kicks off the GitHub Actions
# release — it runs "Release via GitHub Actions" (release-all.yml: build every per-arch
# binary + publish the GitHub Release), which in turn runs "Docker via GitHub Actions"
# (docker.yml: multi-arch image -> Docker Hub, Quay.io, GHCR). Exits when done (or if
# CHANGELOG isn't ready / the tag already exists).
act_release_ferretdb() {
  printf "Did you add your changes under '## Upcoming FerretDB release' in CHANGELOG.md (y/n) ? "
  read -r ans
  case "${ans:-}" in
    [yY]*) ;;
    *) printf '%s\n' "Please first update CHANGELOG.md . Thanks !"; exit 0 ;;
  esac

  # Determine the version to release — NO version number needed. The wekan-fork
  # releases are the "## [vX.Y.Z](https://github.com/wekan/FerretDB/releases/tag/vX.Y.Z)"
  # headings (upstream FerretDB's own pre-fork entries link to FerretDB/FerretDB and are
  # skipped). Newest first:
  local newest second
  newest="$(grep -oE '^## \[v[0-9]+\.[0-9]+\.[0-9]+\]\(https://github.com/wekan/FerretDB' CHANGELOG.md \
            | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sed -n 1p)"
  second="$(grep -oE '^## \[v[0-9]+\.[0-9]+\.[0-9]+\]\(https://github.com/wekan/FerretDB' CHANGELOG.md \
            | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sed -n 2p)"
  if [ -z "$newest" ]; then
    err "No released '## [vX.Y.Z](.../wekan/FerretDB/...)' heading found in CHANGELOG.md. Aborting."
    exit 1
  fi

  local version
  if grep -qE '^## Upcoming FerretDB release' CHANGELOG.md; then
    # Rename the Upcoming section to the next version — the same increment (of the
    # minor, patch stays .0) as the last release, default +1 — WITH the correct git-tag
    # link generated from the new version (so the link can never point at the previous
    # tag).
    local major nmin smin step
    major="$(printf '%s' "${newest#v}" | cut -d. -f1)"
    nmin="$(printf '%s' "${newest#v}"  | cut -d. -f2)"
    step=1
    if [ -n "$second" ]; then
      smin="$(printf '%s' "${second#v}" | cut -d. -f2)"
      step=$(( nmin - smin )); [ "$step" -le 0 ] && step=1
    fi
    version="v${major}.$(( nmin + step )).0"
    local date link
    date="$(date +%F)"
    link="https://github.com/wekan/FerretDB/releases/tag/${version}"
    info "Renaming '## Upcoming FerretDB release' -> ## [${version}](${link}) (${date})"
    local _tmp; _tmp="$(mktemp)"
    sed "s|^## Upcoming FerretDB release.*|## [${version}](${link}) (${date})|" CHANGELOG.md > "$_tmp" && mv "$_tmp" CHANGELOG.md
  else
    # No Upcoming section: the newest heading is already the prepared release. Sanity-
    # check it is the expected +1 increment of the previous release.
    version="$newest"
    if [ -n "$second" ]; then
      local exp_major exp_min
      exp_major="$(printf '%s' "${second#v}" | cut -d. -f1)"
      exp_min="$(( $(printf '%s' "${second#v}" | cut -d. -f2) + 1 ))"
      if [ "$version" != "v${exp_major}.${exp_min}.0" ]; then
        info "Note: newest CHANGELOG version $version is not the +1 increment (v${exp_major}.${exp_min}.0) of the previous $second; proceeding anyway."
      fi
    fi
    info "No Upcoming section; using newest CHANGELOG version $version."
  fi

  if git rev-parse -q --verify "refs/tags/$version" >/dev/null 2>&1; then
    err "Tag $version already exists — nothing to release. Aborting."
    exit 1
  fi

  info "Releasing $version"
  if ! git diff --quiet || ! git diff --cached --quiet; then
    git add --all
    git commit -m "$version"
    git push
  fi
  git tag -a "$version" -m "$version"
  git push origin "$version"
  git push

  # Then kick off the whole GitHub Actions release, so one command does everything:
  # this runs "Release via GitHub Actions" (release-all.yml — builds every per-arch
  # binary and publishes the GitHub Release), which in turn dispatches "Docker via
  # GitHub Actions" (docker.yml — builds and pushes the multi-arch image to Docker Hub,
  # Quay.io and GHCR). Nothing is built locally.
  info "Starting the GitHub Actions release (release-all.yml, which then triggers docker.yml) for $version ..."
  act_release "$version"
  exit 0
}

# ---- menu ----------------------------------------------------------------
menu() {
  while true; do
    printf "\n${B}FerretDB v1 (SQLite) — build.sh${N}\n"
    cat <<EOF
  1) Install dependencies         (go mod download: root + integration + tools)
  2) Build FerretDB               (-> bin/ferretdb, SQLite handler)
  3) Run FerretDB                 (SQLite backend on $LISTEN_ADDR)
  4) Run integration tests        — PARALLEL (all tests, $(cpu_count) workers)
  5) Run integration tests        — SEQUENTIAL (one at a time)
  6) Run ONE integration test     (enter a Test name / regex, pick par/seq)
  7) Run unit tests               (./internal/... ./cmd/...)
  8) Lint / vet
  9) Build & run with Docker      (docker compose up --build)
 16) Run all FerretDB tests      — SEQUENTIAL (unit + vet + integration, one at a
                                    time; logs to ../log/<datetime>/ with WeKan's,
                                    and the same thing WeKan's build.sh runs)
 10) Clean build artifacts
 11) Build per-arch binaries      — SEQUENTIAL (all platforms, one at a time)
 12) Build per-arch binaries      — PARALLEL (all platforms at once)
 13) Release via GitHub Actions   (trigger release-all.yml: build per-arch
                                    binaries + publish GitHub Release w/ notes)
 14) Docker via GitHub Actions    (trigger docker.yml: multi-arch image from the
                                    release binaries -> Docker Hub, Quay.io, GHCR)
 15) Release FerretDB             (rename Upcoming -> version, commit + tag + push,
                                    then run 13 Release + 14 Docker via GitHub Actions)
  g) Show / install Go toolchain
  0) Exit
EOF
    printf "Select: "
    read -r choice
    case "$choice" in
      1) act_deps ;;
      2) act_build ;;
      3) act_run ;;
      4) act_test par ;;
      5) act_test seq ;;
      6) printf "Test name / regex (e.g. TestSessions): "; read -r t
         printf "Mode - [P]arallel / [s]equential: "; read -r m
         case "$m" in [sS]*) act_test_one "$t" seq ;; *) act_test_one "$t" par ;; esac ;;
      7) act_unit ;;
      8) act_lint ;;
      16) act_test_all ;;
      9) act_docker ;;
      10) act_clean ;;
      11) act_dist seq ;;
      12) act_dist par ;;
      13) act_release ;;
      14) act_release_docker ;;
      15) act_release_ferretdb ;;
      g|G) act_goenv ;;
      0|q|Q) info "Bye."; exit 0 ;;
      *) warn "Unknown option: $choice" ;;
    esac
  done
}

# ---- dispatch ------------------------------------------------------------
case "${1:-}" in
  "")         menu ;;
  deps)       act_deps ;;
  build)      act_build ;;
  run)        act_run ;;
  dist)       act_dist seq ;;
  dist-seq)   act_dist seq ;;
  dist-par)   act_dist par ;;
  release)    shift; act_release "${1:-}" ;;
  docker-release) shift; act_release_docker "${1:-}" ;;
  release-ferretdb) act_release_ferretdb ;;
  test)       act_test par ;;
  test-seq)   act_test seq ;;
  test-all)   act_test_all ;;
  test-par)   shift; [ -n "${1:-}" ] && export TEST_PARALLEL="$1"; act_test par ;;
  test-one)   shift; act_test_one "${1:-}" "${2:-par}" ;;
  unit)       act_unit ;;
  lint)       act_lint ;;
  no-lfs)     act_no_lfs ;;
  docker)     act_docker ;;
  clean)      act_clean ;;
  goenv)      act_goenv ;;
  -h|--help|help)
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) err "Unknown command: $1"; err "Run '$0 --help' for usage."; exit 2 ;;
esac
