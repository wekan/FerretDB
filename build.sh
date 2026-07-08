#!/usr/bin/env bash
#
# build.sh - interactive helper for FerretDB v1 (SQLite) + WeKan.
#
# Usage:
#   ./build.sh              # interactive menu
#   ./build.sh <command>    # run one action non-interactively, e.g.:
#   ./build.sh deps | build | run | goenv | unit | lint | docker | clean
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
  info "Running unit tests (./internal/...) ..."
  go test -count=1 -short ./internal/... ./cmd/...
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
    sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) err "Unknown command: $1"; err "Run '$0 --help' for usage."; exit 2 ;;
esac
