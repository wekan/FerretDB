# TODO

Work on more databases for FerretDB v1, investigated but not done yet, each
with what it needs and why it is not done. When an item is done, it moves to
`## Upcoming FerretDB release` in [CHANGELOG.md](CHANGELOG.md).

Checked on 2026-11-25: licenses from each driver's repository, upkeep from its
newest push, and the platforms each test image publishes from its registry.

## Licenses: compatible, and what copyfree means here

Every license here can be combined and shipped together: MIT, BSD, ISC,
Apache-2.0 and MPL-2.0 code goes into one binary, each part under its own
license (Apache's license text and NOTICE kept, MPL's only for changes to its
own files). FerretDB itself is Apache-2.0, as is the SAP HANA driver
`github.com/SAP/go-hdb`, and WeKan, MIT, depends on a hundred Apache-2.0 npm
packages (the AWS SDK among them). So a license is never what rules a driver
out below.

[Copyfree](https://copyfree.org/standard/licenses) is a stricter label for a
license itself, not about combining: it certifies MIT, the BSD licenses, ISC,
the PostgreSQL License, Blue Oak 1.0.0 and BSD+Patent, and
[rejects](https://copyfree.org/standard/rejected) Apache-2.0 (section 4:
notices required in modified files) and MPL. It has not certified Apache in
any archived version of its list (2014, 2016, 2019). Where two drivers do the
same, the copyfree one is preferred.

- The MySQL driver stays `github.com/go-sql-driver/mysql` (MPL-2.0). The MIT
  `github.com/go-mysql-org/go-mysql` (maintained, pushed 2026-11-24) was looked
  at and not adopted, for what it costs rather than its license: its `driver`
  package brings in `pingcap/log`, `pingcap/tidb/pkg/parser` and
  `coreos/go-semver` (Apache-2.0), TiDB's SQL parser makes an otherwise empty
  program 7 MB, and the DSN and error handling of the mysql backend would be
  rewritten. One small dependency would become four and a bigger binary.
  Revisit if its driver stops depending on them.

## Need a machine with Docker to run the tests

- **MariaDB and TiDB with the mysql backend** (`task env-up`, then
  `task test-integration-mariadb` and `task test-integration-tidb`). The
  services, their setup in `envtool` and the test tasks are there; the tests
  have not been run against them yet - this environment has no Docker. Fix
  what they find in `internal/backends/mysql`. Images: `mariadb` (amd64,
  arm64, ppc64le, s390x), `pingcap/tidb` (amd64, arm64).
- **PostgreSQL-compatible servers with the postgresql backend**, no new driver
  (`github.com/jackc/pgx/v5`, MIT): add services and test tasks like
  MariaDB's, and run the integration tests.
  - YugabyteDB, `yugabytedb/yugabyte` (amd64, arm64).
  - Citus: `build/deps/citus.Dockerfile` exists; a test task does not.
  - CockroachDB: PostgreSQL wire protocol, but its server is no longer open
    source (since 2024); test it only if WeKan users ask for it.

## New backends: a driver and an SQL dialect each

Each needs `internal/backends/<name>` with its metadata and pool, a build
tag `ferretdb_no_<name>` like the others, a service in
`docker-compose.dev.yml`, `envtool setup`, and a `test-integration-<name>`
task.

- **rqlite** (distributed SQLite over HTTP): `github.com/rqlite/gorqlite`
  (MIT, pushed 2026-10-20); image `rqlite/rqlite` (amd64, arm, arm64). SQLite's
  SQL, so most of `internal/backends/sqlite` can be shared.
- **libSQL server (sqld)**: `github.com/tursodatabase/libsql-client-go` (MIT,
  pure Go, pushed 2026-11-11); image `ghcr.io/tursodatabase/libsql-server`
  (amd64, arm64). SQLite's SQL, like rqlite.
- **Firebird**: `github.com/nakagami/firebirdsql` (MIT, pushed 2026-11-18);
  image `firebirdsql/firebird` (amd64, arm64). A dialect of its own.
- **Oracle Database Free**: `github.com/sijms/go-ora` (MIT, pure Go, pushed
  2026-10-07); image `gvenzl/oracle-free` (amd64, arm64). A dialect of its own.
- **Microsoft SQL Server**: `github.com/microsoft/go-mssqldb` (BSD-3-Clause,
  pushed 2026-11-24); image `mcr.microsoft.com/mssql/server` (the free
  Developer edition) is published for amd64 only, so its tests are run on an
  amd64 machine (an Ubuntu amd64 laptop), not on arm64. A dialect of its own.

## Not suitable

- ClickHouse (`ClickHouse/clickhouse-go`): an analytics column store, not a
  document store's backend (its Apache-2.0 driver would be fine to use).
- Spanner (`googleapis/go-sql-spanner`): a Google Cloud service; its emulator
  could be tested, but nobody runs WeKan on it.
- IBM Db2 (`ibmdb/go_ibm_db`): cgo and IBM's own CLI driver download.
- DuckDB (`duckdb/duckdb-go`): embedded, cgo, no server image to test against.
- `ziutek/mymysql`: not maintained (newest push 2024).
