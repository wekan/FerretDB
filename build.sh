#!/usr/bin/env bash
#
# build.sh - interactive helper for FerretDB v1 (SQLite) + WeKan.
#
# Usage:
#   ./build.sh              # interactive menu
#   ./build.sh <command>    # run one action non-interactively, e.g.:
#   ./build.sh deps | build | run | goenv | unit | lint | docker | clean
#   ./build.sh zip                     # build ferretdb.zip, sequential (default)
#   ./build.sh zip-seq                 # build ferretdb.zip, one platform at a time
#   ./build.sh zip-par                 # build ferretdb.zip, all platforms in parallel
#   ./build.sh release [version]       # push + trigger release-all.yml on GitHub Actions
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

GO_VERSION="1.25.0"
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
  info "Building FerretDB (SQLite handler) -> bin/ferretdb ..."
  mkdir -p bin
  go build -o bin/ferretdb ./cmd/ferretdb || return 1
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

# ---- ferretdb.zip (cross-compile for every platform) ---------------------
# WeKan-style arch name + GOOS + GOARCH + GOARM. .exe suffix is added only for
# Windows; every other platform's binary has no extension. This is the same set
# the WeKan release workflow (release-all.yml build-ferretdb) produces, so the
# zip built here matches the released ferretdb.zip. Targets that a platform can't
# compile (e.g. an arch modernc.org/sqlite has no port for) are skipped.
FERRETDB_ZIP_TARGETS=(
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
# Builds one target; on success writes the (executable) binary into
# <out-dir>/<name>/ and records <name> in <report-dir>/built.list, else skips and
# records it in <report-dir>/failed.list. Safe to run concurrently.
build_ferretdb_target() {
  local name="$1" goos="$2" goarch="$3" goarm="$4" out="$5" rep="$6"
  local ext=""; [ "$goos" = windows ] && ext=".exe"
  mkdir -p "$out/$name"
  if CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
       go build -trimpath -o "$out/$name/ferretdb-$name$ext" ./cmd/ferretdb 2>"$rep/$name.log"; then
    chmod +x "$out/$name/ferretdb-$name$ext"
    printf '%s\n' "$name" >> "$rep/built.list"
    info "  built   $name"
  else
    printf '%s\n' "$name" >> "$rep/failed.list"
    warn "  skipped $name (does not compile) — see $rep/$name.log"
    tail -3 "$rep/$name.log" | sed 's/^/        /' >&2 || true
    rm -rf "$out/$name"
  fi
}

# act_zip <seq|par> — cross-compile FerretDB (SQLite) for all platforms into
# ./ferretdb.zip with the layout ferretdb/<arch>/ferretdb-<arch>[.exe] + README.md
act_zip() {
  local mode="${1:-seq}"
  go_env
  command -v zip >/dev/null 2>&1 || { err "'zip' is required (apt-get install zip)"; return 1; }

  info "Generating version info (build/version) ..."
  ( cd build/version && go run generate.go ) || { err "gen-version failed"; return 1; }
  local fver; fver="$(cat build/version/version.txt 2>/dev/null || echo unknown)"
  info "FerretDB version: $fver"

  local stage="$ROOT/tmp/ferretdb-zip"
  rm -rf "$stage"; mkdir -p "$stage/ferretdb"
  local out="$stage/ferretdb"          # becomes ferretdb/ inside the zip
  : > "$stage/built.list"; : > "$stage/failed.list"

  # Prime the module + build cache once (resolve deps, compile shared std/deps)
  # so the parallel builds below don't all race downloading/compiling at once.
  info "Priming build cache (this resolves modules on first run) ..."
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/ferretdb 2>/dev/null || true

  if [ "$mode" = par ]; then
    info "Cross-compiling FerretDB for all platforms — PARALLEL ..."
    local pids=()
    for t in "${FERRETDB_ZIP_TARGETS[@]}"; do
      # shellcheck disable=SC2086
      set -- $t
      build_ferretdb_target "$1" "$2" "$3" "${4:-}" "$out" "$stage" &
      pids+=($!)
    done
    wait "${pids[@]}" 2>/dev/null || true
  else
    info "Cross-compiling FerretDB for all platforms — SEQUENTIAL ..."
    for t in "${FERRETDB_ZIP_TARGETS[@]}"; do
      # shellcheck disable=SC2086
      set -- $t
      build_ferretdb_target "$1" "$2" "$3" "${4:-}" "$out" "$stage"
    done
  fi

  {
    echo "# FerretDB v1 (SQLite) for WeKan"
    echo
    echo "FerretDB v1 binaries compiled from the WeKan fork:"
    echo "https://github.com/wekan/FerretDB"
    echo
    echo "FerretDB is an open-source, MongoDB-compatible database. These builds use"
    echo "the embedded pure-Go SQLite backend, so each binary is a single"
    echo "self-contained MongoDB-compatible server that needs no external database."
    echo
    echo "Version: $fver"
    echo
    echo "## Layout"
    echo
    echo "    ferretdb/<arch>/ferretdb-<arch>        (Linux/macOS/BSD)"
    echo "    ferretdb/<arch>/ferretdb-<arch>.exe    (Windows)"
    echo
    echo "Included in this build: $(tr '\n' ' ' < "$stage/built.list")"
    echo
    echo "## Run"
    echo
    echo "    ferretdb-<arch> --handler=sqlite --sqlite-url=file:./ferretdb-sqlite/ \\"
    echo "      --listen-addr=127.0.0.1:27017 --telemetry=disable"
    echo
    echo "Then point WeKan at it: MONGO_URL=mongodb://127.0.0.1:27017/wekan"
    echo
    echo "Telemetry is disabled by the flag above (FerretDB also honors DO_NOT_TRACK=1)."
  } > "$out/README.md"

  rm -f "$ROOT/ferretdb.zip"
  ( cd "$stage" && zip -qr "$ROOT/ferretdb.zip" ferretdb )
  info "Created $ROOT/ferretdb.zip"
  info "Built:   $(tr '\n' ' ' < "$stage/built.list")"
  if [ -s "$stage/failed.list" ]; then
    warn "Skipped: $(tr '\n' ' ' < "$stage/failed.list")"
  fi
  echo
  unzip -l "$ROOT/ferretdb.zip"
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

act_lint() {
  go_env
  info "go vet ./... ..."
  go vet ./... || true
  ( cd integration && go vet ./... || true )
  info "vet done (install golangci-lint separately for full linting)."
}

act_docker() {
  if ! command -v docker >/dev/null 2>&1; then err "docker not found"; return 1; fi
  info "docker compose up --build  (FerretDB v1 SQLite + WeKan) ..."
  docker compose up --build
}

act_clean() {
  info "Removing bin/, state/, tmp/sqlite-tests/ ..."
  rm -rf bin/ferretdb "$STATE_DIR" "$SQLITE_TEST_DIR"
  if detect_go; then go clean -cache 2>/dev/null || true; fi
  info "Clean done. (Local Go toolchain in ./.goroot kept; delete it manually to remove.)"
}

# ---- release (trigger GitHub Actions) ------------------------------------
# Like wekan/wekan's releases/release-all.sh: nothing is built locally — this
# pushes the current branch and triggers .github/workflows/release-all.yml, which
# builds ferretdb.zip for all platforms, publishes the GitHub Release, and pushes
# the multi-arch FerretDB Docker image to all registries.
#   act_release [version]   (empty version => the workflow uses `git describe`)
act_release() {
  command -v gh >/dev/null 2>&1 || {
    err "'gh' (GitHub CLI) is required and must be authenticated: run 'gh auth login'."
    return 1
  }
  local branch version
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo main-v1)"
  version="${1:-}"
  if [ -z "$version" ] && [ -t 0 ]; then
    printf "Release tag (e.g. v1.24.2-wekan1; empty = git describe on the runner): "
    read -r version
  fi

  info "Pushing '$branch' so the workflow builds the latest commit ..."
  git push origin "$branch" || warn "git push reported no changes / failed; continuing to trigger."

  info "Triggering release-all.yml on '$branch'${version:+ (version $version)} ..."
  if [ -n "$version" ]; then
    gh workflow run release-all.yml --ref "$branch" -f version="$version" || return 1
  else
    gh workflow run release-all.yml --ref "$branch" || return 1
  fi
  info "Triggered. Track progress at: https://github.com/wekan/FerretDB/actions"
  info "Requires the DOCKERHUB_AUTH / QUAY_AUTH / GHCR_AUTH secrets in this repo."
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
 10) Clean build artifacts
 11) Build ferretdb.zip           — SEQUENTIAL (all platforms, one at a time)
 12) Build ferretdb.zip           — PARALLEL (all platforms at once)
 13) Release via GitHub Actions   (push + trigger release-all.yml: zip, GitHub
                                    Release, multi-arch Docker to all registries)
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
      9) act_docker ;;
      10) act_clean ;;
      11) act_zip seq ;;
      12) act_zip par ;;
      13) act_release ;;
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
  zip)        act_zip seq ;;
  zip-seq)    act_zip seq ;;
  zip-par)    act_zip par ;;
  release)    shift; act_release "${1:-}" ;;
  test)       act_test par ;;
  test-seq)   act_test seq ;;
  test-par)   shift; [ -n "${1:-}" ] && export TEST_PARALLEL="$1"; act_test par ;;
  test-one)   shift; act_test_one "${1:-}" "${2:-par}" ;;
  unit)       act_unit ;;
  lint)       act_lint ;;
  docker)     act_docker ;;
  clean)      act_clean ;;
  goenv)      act_goenv ;;
  -h|--help|help)
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) err "Unknown command: $1"; err "Run '$0 --help' for usage."; exit 2 ;;
esac
