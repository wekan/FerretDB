# syntax=docker/dockerfile:1

# Builds FerretDB v1 from this repository's source and runs it with the SQLite
# backend, for use together with WeKan (see docker-compose.yml in this directory).

# build stage

FROM golang:1.25 AS build

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

# Do not raise this without providing a separate v1 build,
# because v2+ is problematic for some virtualization platforms and older hardware.
export GOAMD64=v1
export GOARM=${TARGETVARIANT#v}
export CGO_ENABLED=0

go build -v -o=/bin/ferretdb ./cmd/ferretdb

/bin/ferretdb --version

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
