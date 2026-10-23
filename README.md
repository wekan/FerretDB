# FerretDB v1 Fork by xet7

Tested to work with WeKan:

- SQLite
- PostgreSQL

Someone please test:

- MySQL
- MariaDB
- SAP Hana

# Download

https://github.com/wekan/FerretDB/releases

Every platform has two binaries, each with a `<name>.sha256sum` beside it:

| Asset | Handlers |
| --- | --- |
| `ferretdb-<arch>`, `ferretdb-<arch>.exe` | all: SQLite, PostgreSQL, MySQL/MariaDB, SAP HANA |
| `ferretdb-sqlite-<arch>`, `ferretdb-sqlite-<arch>.exe` | SQLite only: smaller, and `--handler` defaults to `sqlite` |

The SQLite-only binary is built with
`-tags ferretdb_no_postgresql,ferretdb_no_mysql` (and without `ferretdb_hana`);
`./build.sh build-sqlite` makes the same one locally as `bin/ferretdb-sqlite`.

# Docker

Two images, both multi-arch and both defaulting to SQLite in `/state`:
`ferretdb` (every handler) and `ferretdb-sqlite` (the SQLite handler only, made
of the `ferretdb-sqlite-<arch>` binaries). Use `ferretdb-sqlite` unless you need
PostgreSQL, MySQL, MariaDB or SAP HANA.

- [GitHub](https://ghcr.io/wekan/ferretdb-sqlite)
```
image: ghcr.io/wekan/ferretdb-sqlite:latest
```
- [Docker Hub](https://hub.docker.com/r/wekanteam/ferretdb-sqlite)
```
image: wekanteam/ferretdb-sqlite:latest
```
- [RedHat Quay.io](https://quay.io/wekan/ferretdb-sqlite)
```
image: quay.io/wekan/ferretdb-sqlite:latest
```

Every handler:

- [GitHub](https://ghcr.io/wekan/ferretdb)
```
image: ghcr.io/wekan/ferretdb:latest
```
- [Docker Hub](https://hub.docker.com/r/wekanteam/ferretdb)
```
image: wekanteam/ferretdb:latest
```
- [RedHat Quay.io](https://quay.io/wekan/ferretdb)
```
image: quay.io/wekan/ferretdb:latest
```

The Docker release workflow attempts Docker Hub, Quay.io and GHCR independently.
Each registry login has two attempts with a two-minute timeout per attempt. An
unavailable registry, invalid credentials or a failed push does not prevent the
remaining registries from receiving the version tag and `latest`. After all
attempts, the workflow reports how many images were published and fails if any
failed; images already published remain available. A release made before the
SQLite-only build has no `ferretdb-sqlite-<arch>` assets, so only `ferretdb` is
published for it. A repository's first push creates it with the registry's
default visibility (private on Quay.io and GHCR), so a new image name has to be
made public once after its first release.

# Roadmap

[ROADMAP](ROADMAP.md)
