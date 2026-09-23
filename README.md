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

# Docker

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
attempts, the workflow reports how many registries succeeded and fails if any
failed; images already published remain available.

# Roadmap

[ROADMAP](ROADMAP.md)
