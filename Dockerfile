# syntax=docker/dockerfile:1

# Builds FerretDB v1 (SQLite backend) for use together with WeKan (see
# docker-compose.yml in this directory).
#
# The build stage runs NATIVELY on the build platform and cross-compiles to each
# target platform with the Go toolchain (CGO disabled => pure Go => any GOARCH),
# so buildx can emit every architecture the release ships — amd64, arm64,
# arm/v7 (armhf), arm/v5 (armel), 386 (i386), ppc64le, s390x, riscv64, loong64 —
# WITHOUT QEMU emulation of the compiler. The final image is FROM scratch, which
# is architecture-agnostic.

# build stage — native on $BUILDPLATFORM, cross-compiles for $TARGETPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.25.11 AS build

# Provided automatically by buildx for the target platform.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# use a single directory for all Go caches to simplify RUN --mount commands below
ENV GOPATH=/cache/gopath
ENV GOCACHE=/cache/gocache
ENV GOMODCACHE=/cache/gomodcache

# do not download a newer toolchain than the one in this image
ENV GOTOOLCHAIN=local

# see .dockerignore for what is included in the build context
WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/cache <<EOF
set -ex

# build/version/version.txt is gitignored, so make sure a valid version file
# exists; without it the compiled binary panics on startup.
if [ ! -s build/version/version.txt ]; then
  echo "v1.24.2" > build/version/version.txt
fi

# Cross-compile for the requested target platform. Because we build on the native
# build platform, we CANNOT run the (possibly foreign-arch) binary here, so there
# is no --version smoke test in this stage.
export GOOS=${TARGETOS}
export GOARCH=${TARGETARCH}
# GOARM only matters for 32-bit arm (v5 => armel, v7 => armhf); harmless otherwise.
export GOARM=${TARGETVARIANT#v}

# Do not raise GOAMD64 above v1 without providing a separate v1 build,
# because v2+ is problematic for some virtualization platforms and older hardware.
export GOAMD64=v1
export CGO_ENABLED=0

go build -v -o=/bin/ferretdb ./cmd/ferretdb

# create a state directory owned by the runtime user
mkdir /state
chown 1000:1000 /state
EOF


# final stage

FROM scratch AS final

COPY build/ferretdb/passwd /etc/passwd
COPY build/ferretdb/group  /etc/group
USER ferretdb:ferretdb

COPY --from=build /bin/ferretdb /ferretdb
COPY --from=build --chown=ferretdb:ferretdb /state /state

ENTRYPOINT ["/ferretdb"]

WORKDIR /
VOLUME /state
EXPOSE 27017 8088

ENV FERRETDB_LISTEN_ADDR=0.0.0.0:27017
ENV FERRETDB_STATE_DIR=/state
ENV FERRETDB_HANDLER=sqlite
ENV FERRETDB_SQLITE_URL=file:/state/
ENV FERRETDB_TELEMETRY=disable
