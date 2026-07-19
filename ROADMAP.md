# WeKan on FerretDB — Compatibility Roadmap

This roadmap tracks what is needed to run [WeKan](https://github.com/wekan/wekan)
(Meteor 3 / MongoDB) on **FerretDB v1.24.2 with the SQLite backend** (this repo,
`main-v1` branch), and compares that against **FerretDB v2** (`main`, PostgreSQL +
DocumentDB).

Everything is consolidated into the single **[Compatibility matrix](#compatibility-matrix)**
below: every capability, by category, with whether WeKan uses it, the status in v1
and v2, what is missing, and which tests cover it. The v1 and v2 columns were
validated against the actual code on the `main-v1` and `main` branches (see
[Validation notes](#validation-notes)).

**Assumptions**

- WeKan **attachments are stored on the filesystem** (`WRITABLE_PATH`, storage
  strategy `fs`), not in the database — so **GridFS is out of scope**. GridFS is
  only exercised if an admin explicitly selects the `gridfs` backend or migrates
  legacy CollectionFS data. See WeKan `models/attachments.server.js`,
  `models/lib/fileStoreStrategy.js`, `server/lib/mongoStartup.js` (startup notice:
  *"Attachments and avatars are stored ON DISK under WRITABLE_PATH, NOT in MongoDB"*).
- Core CRUD (`insert`, `find`, `count`, `update` with `$set`, `findOneAndUpdate`)
  has already been **verified working** against this FerretDB v1.24.2 SQLite build
  with the MongoDB wire driver.

---

## Contents

1. [Introduction — what this fork does](#introduction--what-this-fork-does)
2. [Where FerretDB info is visible in WeKan](#where-ferretdb-info-is-visible-in-wekan)
3. [FerretDB CLI options](#ferretdb-cli-options)
4. [FerretDB settings (SQLite pragmas + environment variables)](#ferretdb-settings-sqlite-pragmas--environment-variables)
5. [Features & fixes added AFTER xet7 forked FerretDB](#features--fixes-added-after-xet7-forked-ferretdb)
6. [Features & fixes already present BEFORE the fork](#features--fixes-already-present-before-the-fork)
7. [Compatibility matrix](#compatibility-matrix) — the detailed capability-by-capability reference

---

## Introduction — what this fork does

**wekan/FerretDB** (this repo, `main-v1` branch) is a fork of **upstream FerretDB
v1.24.2** whose job is to let [WeKan](https://github.com/wekan/wekan) — a Meteor 3
app written against MongoDB — run on a single, embedded, **zero-dependency**
database. FerretDB is a Go server that speaks the **MongoDB wire protocol** and
translates it to a real storage backend; this fork ships and tests the **SQLite
backend**, so WeKan gets a "MongoDB" that is just one file on disk
(`WRITABLE_PATH/files/db/`), with no separate `mongod` process, replica set, or
tuning to run.

On top of upstream v1.24.2 the fork adds the pieces WeKan actually needs and the
performance/robustness fixes that surfaced running a real WeKan workload:
a completed **OpLog + single-node replica-set handshake** (so Meteor tails
changes instead of poll-and-diff), **SQLite performance pragmas + connection-pool
sizing + filter pushdown**, **slow-query logging**, crash/migration robustness,
a large **aggregation / query-operator build-out**, richer **index options**,
**session/transaction compatibility commands**, **telemetry lockdown**, and the
fork's own build/release/Docker tooling. Every WeKan platform that defaults to
FerretDB — the **Snap**, **Sandstorm**, the **Docker** image/compose, and the
prebuilt **bundle** (`releases/ferretdb/start-wekan.sh` / `.bat`) — launches this
binary with `--repl-set-name=rs0` and points WeKan at it, with polling kept as an
automatic fallback.

This document is both a **roadmap** (the compatibility matrix at the end tracks
v1-vs-v2 capability status) and a **reference**: where to see FerretDB info inside
WeKan, the CLI options and settings, and exactly what the fork changed.

---

## Where FerretDB info is visible in WeKan

**Admin Panel → Info (Version page)** — WeKan's `getStatistics()` method
([`server/statistics.js`](https://github.com/wekan/wekan/blob/main/server/statistics.js))
runs `buildInfo` / `serverStatus` against the database and Meteor internals and
renders this table
([`client/components/settings/informationBody.jade`](https://github.com/wekan/wekan/blob/main/client/components/settings/informationBody.jade)):

| Row | For FerretDB it shows |
|---|---|
| Database type | `FerretDB` (detected from `buildInfo.ferretdbVersion`/`ferretdb`) |
| MongoDB compatible version | the MongoDB wire version FerretDB reports (~5.0 / FCV 7.0) |
| Database commit | `buildInfo.gitVersion` |
| **FerretDB version** | e.g. `v1.24.2-<n>-g<sha>` (fork build), from `buildInfo.ferretdbVersion` |
| **FerretDB commit** | the fork's git commit |
| MongoDB storage engine | `SQLite` (the embedded backend) |
| **MongoDB Oplog enabled** | `true` when Meteor has a live oplog handle, else `false` |
| **Reactivity mode** | the driver actually LIVE now: `oplog` / `changeStreams` / `polling` |
| **Reactivity order** | the configured `METEOR_REACTIVITY_ORDER` (what was requested) |
| **DDP transport** | the configured `DDP_TRANSPORT` |

Read together, **Reactivity mode** (live) next to **Reactivity order** (requested)
tell you whether OpLog actually came up or fell back to polling.

**Admin Panel → Problems → Speed / Tests** — WeKan's event-log subsystem
([`models/eventLog.js`](https://github.com/wekan/wekan/blob/main/models/eventLog.js))
records performance and self-check problems (including slow HTTP requests and
database errors FerretDB reports back to WeKan) into the existing WeKan database
and surfaces per-area counts with an acknowledge button.

**FerretDB server logs** — FerretDB logs to its process output (Snap:
`journalctl` for the `wekan.ferretdb` service; Docker: `docker logs`; Sandstorm:
the grain log; bundle: the terminal). The fork defaults the log level to **error**
(quiet) but always emits a `slow query: <statement>` **WARN** line for any SQL
statement at or above `FERRETDB_SLOW_QUERY_THRESHOLD` (default 1s) — the single
most useful signal for "everything is slow" investigations.

---

## FerretDB CLI options

The `ferretdb` binary uses Kong with `kong.DefaultEnvars("FERRETDB")`, so almost
every flag `--foo-bar` also reads the environment variable `FERRETDB_FOO_BAR`.
Defined in [`cmd/ferretdb/main.go`](cmd/ferretdb/main.go). Commands: `run`
(default, runs the server) and `ping` (ping a running instance).

**General**

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `--handler` | `FERRETDB_HANDLER` | `postgresql` (WeKan uses `sqlite`) | Storage backend handler |
| `--mode` | `FERRETDB_MODE` | `normal` | Operation mode |
| `--state-dir` | `FERRETDB_STATE_DIR` | `.` | Directory holding `state.json` (instance UUID); `-` disables |
| `--repl-set-name` | `FERRETDB_REPL_SET_NAME` | `""` | **Replica-set name — enables the OpLog + replica-set handshake** (WeKan sets `rs0`) |
| `--version` | (n/a) | — | Print version and exit |

**Backend URL (one per handler)**

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `--sqlite-url` | `FERRETDB_SQLITE_URL` | `file:data/` | SQLite data **directory** URI (`file:`; must end with `/`) |
| `--postgresql-url` | `FERRETDB_POSTGRESQL_URL` | `postgres://127.0.0.1:5432/ferretdb` | PostgreSQL URL |
| `--mysql-url` | `FERRETDB_MYSQL_URL` | `mysql://127.0.0.1:3306/ferretdb` | MySQL URL (beta) |
| `--hana-url` | `FERRETDB_HANA_URL` | — | SAP HANA URL (experimental) |

**Listen / network**

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `--listen-addr` | `FERRETDB_LISTEN_ADDR` | `127.0.0.1:27017` | Listen TCP address |
| `--listen-unix` | `FERRETDB_LISTEN_UNIX` | `""` | Listen Unix-socket path |
| `--listen-tls` | `FERRETDB_LISTEN_TLS` | `""` | Listen TLS address |
| `--listen-tls-cert-file` / `--listen-tls-key-file` / `--listen-tls-ca-file` | `FERRETDB_LISTEN_TLS_*` | `""` | TLS cert / key / CA files |
| `--proxy-addr`, `--proxy-tls-*` | `FERRETDB_PROXY_*` | `""` | Proxy address + TLS (proxy/diff modes) |

**Logging / telemetry / debug**

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `--log-level` | `FERRETDB_LOG_LEVEL` | **`error`** (fork default; upstream `info`) | `DEBUG`/`INFO`/`WARN`/`ERROR` |
| `--log-format` | `FERRETDB_LOG_FORMAT` | `console` | `console`/`text`/`json` |
| `--log-uuid` / `--no-log-uuid` | `FERRETDB_LOG_UUID` | `false` | Add instance UUID to log lines |
| `--telemetry` | `FERRETDB_TELEMETRY` | **`disable`** (fork default; reporter never started) | Basic telemetry on/off |
| `--debug-addr` | `FERRETDB_DEBUG_ADDR` | `127.0.0.1:8088` | HTTP metrics/profiling/probes; `-` disables |
| `--metrics-uuid` / `--no-metrics-uuid` | `FERRETDB_METRICS_UUID` | `false` | Add instance UUID to metrics |
| `--otel-traces-url` | `FERRETDB_OTEL_TRACES_URL` | `""` | OpenTelemetry OTLP/HTTP traces endpoint |

**Setup / auth** (`--setup-database` requires `--test-enable-new-auth`, and
`--setup-database`+`--setup-username` must be used together):
`--setup-database`, `--setup-username`, `--setup-password`, `--setup-timeout`
(`30s`) — env `FERRETDB_SETUP_*`.

**Experimental / test** (`FERRETDB_TEST_*`): `--test-enable-new-auth`,
`--test-disable-pushdown`, `--test-enable-nested-pushdown`,
`--test-capped-cleanup-interval` (`1m`), `--test-capped-cleanup-percentage`
(`10`), `--test-batch-size` (`100`), `--test-max-bson-object-size-mi-b` (`16`),
`--test-records-dir`, and the `--test-telemetry-*` group.

---

## FerretDB settings (SQLite pragmas + environment variables)

**SQLite connection pragmas** — the fork applies these as DEFAULTS in
[`internal/backends/sqlite/metadata/pool/uri.go`](internal/backends/sqlite/metadata/pool/uri.go)
(`setDefaultValues`). Each is skipped if the operator already supplied a `_pragma`
of the same name in `--sqlite-url` — an operator setting always wins.

| Pragma | Value | Why |
|---|---|---|
| `busy_timeout` | `30000` (30 s) | Wait up to 30 s for a write lock instead of failing with `SQLITE_BUSY` (raised from upstream 10 s to survive heavy write load / big migrations) — #6480 |
| `journal_mode` | `wal` | Write-Ahead Logging → concurrent readers + one writer |
| `synchronous` | `normal` | Crash-safe under WAL; removes one `fsync` per commit (biggest write win) — #6480 |
| `cache_size` | `-65536` | 64 MiB page cache per connection (KiB, negative) — hot pages stay in RAM — #6480 |
| `mmap_size` | `268435456` | 256 MiB memory-mapped I/O — reads served from RAM — #6480 |
| `temp_store` | `memory` | Sorts / temp indexes in RAM |
| `auto_vacuum` | `none` | Disabled (upstream TODO #3612) |

**Environment variables specific to the fork / to how WeKan runs it**

| Env var | Default | Controls |
|---|---|---|
| `FERRETDB_REPL_SET_NAME` | `""` | Replica-set name; **set it (WeKan uses `rs0`) to auto-create `local.oplog.rs` and enable OpLog tailing** |
| `FERRETDB_SLOW_QUERY_THRESHOLD` | `1s` | Log any SQL statement at/above this at WARN (`500ms`, `2s`, …); `0`/negative disables — fork addition (#6480), read in `internal/util/fsql/slow.go` |
| `FERRETDB_TELEMETRY` / `DO_NOT_TRACK` | telemetry `disable` | Telemetry is off by default and the reporter loop is never started |
| `FERRETDB_STATE_DIR` | `.` | Where `state.json` lives (Sandstorm points it at writable `/var`) |

**On the WeKan side**, the OpLog is turned on per platform by
`WEKAN_FERRETDB_OPLOG` (default `true`; set `false` to force polling only) and
`WEKAN_FERRETDB_REPL_SET` (default `rs0`); WeKan then exports
`MONGO_OPLOG_URL=mongodb://<host>/local?replicaSet=rs0` and
`METEOR_REACTIVITY_ORDER=oplog,polling` (OpLog preferred, polling fallback).

---

## Features & fixes added AFTER xet7 forked FerretDB

Fork point: **upstream FerretDB v1.24.2** (2025-05-27). Every CHANGELOG section
from **v1.25.0** through the top **"Upcoming"** section is fork work by xet7 for
WeKan (entries reference `wekan/wekan#NNNN`); the module path stays
`github.com/FerretDB/FerretDB` and `wire` is pinned at v0.0.8 to match the
v1.24.2 BSON API.

**OpLog / replica set (so Meteor tails changes instead of polling)** — #6480, #6481
- `ensureOplog`: auto-create the capped `local.oplog.rs` (128 MiB) at startup and
  on `replSetInitiate` when `FERRETDB_REPL_SET_NAME` is set; makes "run with an
  OpLog" the default.
- `hello`/`isMaster` advertise the full single-node-primary identity (`me`,
  `primary`, `secondary:false`, `setVersion`) so the driver's SDAM accepts the
  server as PRIMARY.
- New `replSetGetStatus` and `replSetGetConfig` commands (valid single-member
  status/config); `replSetInitiate` compatibility no-op.

**SQLite performance / stability** — #6467, #6469, #6480
- Raise `busy_timeout` 10 s → 30 s (fixes `SQLITE_BUSY` on Sign In).
- Connection pragmas as defaults: `synchronous(normal)`, `cache_size(-65536)`,
  `mmap_size(256 MiB)`, `temp_store(memory)`.
- Stop capping `MaxOpenConns` (parked Meteor cursors each pin a connection);
  cap the per-DB pool at 2×GOMAXPROCS (min 4, max 16) instead of 100/100 to stop
  modernc-SQLite/GC thrashing; keep `MaxIdleConns` warm.
- `InsertAll` no longer takes the registry global write lock when the collection
  already exists.

**SQLite filter pushdown** — #6467, #6468
- Push top-level string/ObjectID equality (not just bare `{_id:X}`), `$in`, literal
  `$regex`→`LIKE` (superset-safe, ASCII-only gating), and numeric/date range
  `$gt`/`$gte`/`$lt`/`$lte` down into SQL `WHERE` (WeKan search + "Filter by date").

**Slow-query logging** — #6480
- `internal/util/fsql` emits a `slow query: <statement>` WARN with elapsed time +
  threshold; tunable via `FERRETDB_SLOW_QUERY_THRESHOLD` (`0` disables).

**Crash / migration robustness**
- `collectionCreate` ADOPTS an orphaned physical table (`CREATE ... IF NOT
  EXISTS`) instead of crashing/dropping data (fixes a systemd crash-loop) — #6476.
- Accept documents with literal dotted field names (`{"foo.bar":"baz"}`) like
  MongoDB 3.6+ so migration no longer silently drops such docs — #6473.

**Query operators**
- `$elemMatch` document/field form (fixed WeKan board-access returning no boards).
- `$where` (embedded pure-Go goja JS engine); `$text` (partial self-contained
  semantics: term/OR/phrase/negation, no stemming/scoring).

**Aggregation build-out** (v1.25.0)
- Stages: `$setWindowFields` (rank/position + window accumulators), `$lookup`
  (equality join), `$replaceRoot`/`$replaceWith`, `$sortByCount`, `$sample`,
  `$facet`, `$unionWith`, `$bucket`, `$bucketAuto`.
- Expression operators across comparison/boolean/conditional, arithmetic,
  trig/log, string, array, type-conversion, date, `$bsonSize`, and `$function`
  (server-side JS).

**Update operators** — `$push` modifiers `$slice`/`$sort`/`$position`; `$pullAll`
tests.

**Indexes** (v1.25.0, SQLite backend) — TTL (`expireAfterSeconds` + background
reaper); text index option storage (`weights`/`default_language`/… round-tripped,
no real inverted index); accept/store/round-trip `hidden`, `collation`,
`partialFilterExpression`, `2dsphere` (reported, not enforced).

**Sessions / transactions** (v1.25.0) — the session/transaction command family as
compatibility commands + a real server-side session registry (30-min timeout)
ported from v2. No real multi-document transactions (every SQLite write
auto-commits).

**Telemetry / logging lockdown** (v1.26.0) — `--telemetry` defaults to `disable`,
reporter loop never started (no phone-home); default log level lowered to `error`.

**Build / release / packaging** — Go toolchain pinned & patched (stdlib CVEs);
modernc SQLite bumped (embedded SQLite 3.46.0 → 3.53.2); interactive `build.sh`
menu (build/run/test/vet/docker/release); `release-all.yml` + split `docker.yml`
multi-arch publishing (Docker Hub/Quay/GHCR); per-arch `ferretdb-<arch>` binary
assets; cross-compile 9+ Linux arches without QEMU; a `docker-compose.yml` running
WeKan on FerretDB SQLite.

---

## Features & fixes already present BEFORE the fork

These upstream **FerretDB v1.24.2** capabilities WeKan relies on shipped before
the fork (see the [compatibility matrix](#compatibility-matrix) for per-feature
detail):

- **MongoDB wire-protocol compatibility layer in Go** — reports MongoDB ~5.0
  (FCV 7.0); `hello`/`isMaster`, `saslStart`/`saslContinue` auth, Stable API.
- **Pluggable storage backends** — SQLite (embedded, complete), PostgreSQL
  (vanilla, mature), MySQL (beta), SAP HANA (experimental).
- **Core CRUD + cursors** — `find`/`insert`/`update`/`delete`,
  `findAndModify`/`findOneAndUpdate` (`$inc`, `upsert`, `returnDocument`),
  `count`, `distinct`, `getMore`, `killCursors`, `rawCollection()`.
- **Update operators** — `$set`/`$unset`/`$inc`/`$push`/`$pull`/`$addToSet`/
  `$rename`/`$pop`/`$mul`/`$min`/`$max`/`$currentDate`/`$bit`/`$each`/
  `$setOnInsert`/`$pullAll`.
- **Query filter operators** — the full comparison/element/array/logical set incl.
  `$regex` with `$options:'i'` and `$not:{$regex}` (WeKan's entire search),
  `$expr`, all four `$bits*`.
- **Base aggregation pipeline** — `$match`/`$group`/`$project`/`$sort`/`$limit`/
  `$skip`/`$unwind`/`$addFields`/`$set`/`$unset`/`$count`/`$collStats`.
- **Indexes** — single/compound/unique (incl. compound-unique); `sparse` accepted.
- **Capped collections + tailable/`awaitData` cursors** on SQLite — the mechanism
  the fork's default-OpLog feature builds on.
- **Base OpLog tailing collection** — the `backends/decorators/oplog` layer wrote
  correctly-shaped `local.oplog.rs` entries pre-fork, but it was opt-in/manual and
  the replica-set handshake was incomplete (the fork completed & automated it).
- **Admin/diagnostic commands** — `serverStatus`, `buildInfo`, `ping`, `collStats`,
  `dbStats`, `listCollections`/`listDatabases`/`listIndexes`, `create`, `drop`,
  `compact`, `getParameter`.
- **Observability infra** — OpenTelemetry tracing, K8s liveness/readiness probes,
  `slog` logging.

---

## Legend

**WeKan** — does WeKan depend on this feature?
`✅` uses it · `⚙️` admin/metrics only (off the hot path) · `—` not used by WeKan.

**v1 (main-v1)** / **v2 (main)** — implementation status:

- `✅` full — implemented and verifiable in that branch's own source/tests.
- `⚠️` partial / compatibility-only — see the note in the cell.
- `❌` not implemented — confirmed absent from that branch.
- `✅ᴰ` (**v2 only**) — **delegated to DocumentDB.** v2 forwards the query/pipeline to
  the PostgreSQL DocumentDB extension untouched (`internal/handler/msg_aggregate.go`
  parses no stages), so the feature is **not present in FerretDB/main's own code** and
  its real status is determined by DocumentDB, not verifiable from this repo. Treated
  as "generally works via DocumentDB" but not source-confirmed here.
- `—` n/a.

Notes in the **v1** column name the covering integration test (in `integration/`)
where one exists. Unless stated otherwise, **v1 feature rows are exercised on the
SQLite backend** in this stack; the Go compatibility layer is backend-independent, but
only SQLite is run in CI here (PostgreSQL is untested here; MySQL/HANA are partial
backends). Source paths in `v1` cells are relative to this repo; WeKan sources link to
`github.com/wekan/wekan`.

---

## Compatibility matrix

| Capability | WeKan | v1 (`main-v1`) | v2 (`main`) |
|---|:--:|---|---|
| **— Storage backends / databases —** | | | |
| SQLite (embedded, single file) | ✅ (this stack) | ✅ **complete & the only tested target**: full CRUD, single/compound/unique + capped collections, and the **only** backend that persists & round-trips this fork's new index options (text `weights`/`default_language`, `hidden`, `collation`, `partialFilterExpression`, `2dsphere`) via `sqlite/metadata` + `sqlite/collection.go`. All `integration/` tests run here | ❌ no `internal/backends` |
| PostgreSQL (vanilla, no extension) | — | ✅ complete & mature (`internal/backends/postgresql`: full CRUD, indexes, capped) but **untested in this stack**; new index-option round-tripping **not** threaded (0 refs) | ❌ requires the DocumentDB extension |
| PostgreSQL + DocumentDB extension | — | ❌ | ✅ the only engine (`internal/documentdb/`) |
| MySQL | — | ⚠️ partial/experimental (`internal/backends/mysql`): implements the full `Collection` interface (CRUD/indexes/stats/compact) but is a beta backend; new index options not threaded | ❌ |
| SAP HANA | — | ⚠️ experimental (`internal/backends/hana`): CRUD as string-built SQL keyed on `_id`, `Compact` is a no-op, column-mode collections "not supported yet", no `metadata`/`insert.go`; new index options not threaded | ❌ |
| Embeddable / no external DB server | ✅ | ✅ (SQLite in-process) | ❌ (needs PostgreSQL) |
| MongoDB wire target | — | ~5.0 (reports FCV 7.0) | 5.0+ |
| **— CRUD & cursors —** | | | |
| `find` / `insert` / `update` / `delete` | ✅ | ✅ `msg_find.go` / `msg_insert.go` / `msg_update.go` / `msg_delete.go` | ✅ (compatibility.md) |
| `findAndModify`, `findOneAndUpdate` (`$inc` counters, `upsert`, `returnDocument`) | ✅ [`models/counters.js`](https://github.com/wekan/wekan/blob/main/models/counters.js) | ✅ `msg_findandmodify.go` — **verified** | ✅ |
| `updateOne` `$setOnInsert` + `upsert` (race-safe card numbering) | ✅ [`models/boards.js`](https://github.com/wekan/wekan/blob/main/models/boards.js) | ✅ | ✅ |
| `count`, `distinct`, `getMore`, `killCursors` | ✅ | ✅ | ✅ |
| `rawCollection()` (node-mongodb driver) | ✅ | ✅ | ✅ |
| `bulkWrite` | — | ⚠️ (per-op via driver) | ❌ not implemented yet |
| **— Update operators —** | | | |
| `$set` `$unset` `$inc` `$push` `$pull` `$addToSet` `$rename` `$pop` `$mul` `$min` `$max` `$currentDate` `$bit` | ✅ (`$set`/`$unset`/`$push`/`$pull`/`$addToSet`/`$inc`/`$rename`) | ✅ all present (`common/update.go`) | ✅ᴰ |
| `$each` + `$slice` / `$sort` / `$position` push-modifiers | ✅ | ✅ `update_array_operators.go` · `update_push_modifiers_test.go` | ✅ᴰ |
| `$setOnInsert` | ✅ | ✅ | ✅ᴰ |
| `$pullAll` | ✅ [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js) | ✅ `update_pullall_test.go` | ✅ᴰ |
| **— Query filter operators —** | | | |
| `$eq` `$ne` `$gt` `$gte` `$lt` `$lte` `$in` `$nin` `$exists` `$type` `$size` `$all` `$elemMatch` `$mod` `$bits*` | ✅ (`$in`/`$gte`/`$ne`/`$exists`/`$size`) | ✅ `filter.go` (all four `$bits*`) | ✅ᴰ |
| `$regex` incl. `$options:'i'`, `$not:{$regex}` (**WeKan's entire search**) | ✅ [`client/lib/filter.js`](https://github.com/wekan/wekan/blob/main/client/lib/filter.js) | ✅ `filterFieldRegex` | ✅ᴰ |
| `$and` `$or` `$nor` `$not` | ✅ (`$or`/`$not`) | ✅ | ✅ᴰ |
| `$expr` | — | ✅ `filterExprOperator` | ✅ᴰ |
| `$where` (server-side JavaScript) | — | ✅ via embedded goja engine; `this` bound to doc, expression or function form (`filterWhereOperator`) · `query_where_test.go` | ❌ no JS engine in DocumentDB; zero refs in v2 |
| `$text` | — | ⚠️ partial (`filterTextOperator`): matches `$search` terms against the doc's string fields (recurses into sub-docs/arrays); multi-term OR, case-insensitive whole-word, `$caseSensitive`, quoted phrases, leading `-` negation. No stemming, no scoring, no `$meta:"textScore"`, does not consult the text index · `query_text_test.go` | ✅ᴰ (full-text-search guides) |
| geospatial (`$near` / `$geoWithin` / `2dsphere` query) | — | ❌ | ✅ᴰ (DocumentDB; not sourced in FerretDB/main) |
| **— Aggregation stages —** | | | |
| `$match` `$group` `$project` `$sort` `$limit` `$skip` `$unwind` `$addFields` `$set` `$unset` `$count` `$collStats` | ⚙️ [`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js) | ✅ `stages/…` (map + `init()`-injected) | ✅ᴰ |
| `$lookup` | ⚙️ (Prometheus "top boards") | ⚠️ basic equality-join only; `pipeline`/`let` sub-form `ErrNotImplemented` · `aggregate_lookup_test.go` | ✅ᴰ (form coverage DocumentDB-determined) |
| `$replaceRoot` `$replaceWith` `$sortByCount` `$sample` `$facet` `$unionWith` | — | ✅ (`$facet`/`$bucket`/`$unionWith` `init()`-injected) · `aggregate_stages_extra_test.go`, `aggregate_facet_test.go`, `aggregate_unionwith_test.go` | ✅ᴰ |
| `$bucket` `$bucketAuto` | — | ⚠️ (`$bucketAuto` `granularity` `ErrNotImplemented`) · `aggregate_bucket_test.go` | ✅ᴰ |
| `$setWindowFields` | — | ⚠️ stage + window ops (see window rows) · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| `$graphLookup` `$merge` `$out` `$geoNear` | — | ❌ `ErrNotImplemented` (in `unsupportedStages`) | ✅ᴰ |
| `$changeStream` (stage) | — | ❌ `ErrNotImplemented` | ❌ only in `internal/mongoerrors`; no handler |
| **— Aggregation expression operators —** | | | |
| WeKan-used: `$map` `$objectToArray` `$ifNull` `$anyElementTrue` `$eq` `$ne` `$or` | ⚙️ [`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) | ✅ `aggregate_expr_operators_test.go` | ✅ᴰ |
| Comparison/boolean/conditional: `$cmp` `$gt…$lte` `$and` `$not` `$cond` `$switch` `$allElementsTrue` | — | ✅ `aggregate_expr_bool_test.go` | ✅ᴰ |
| Arithmetic: `$add` `$subtract` `$multiply` `$divide` `$mod` `$abs` `$ceil` `$floor` `$trunc` `$round` `$pow` `$sqrt` `$exp` `$ln` `$log` `$max` `$min` `$avg` | — | ✅ `aggregate_expr_arithmetic_test.go` | ✅ᴰ |
| String: `$concat` `$toUpper` `$toLower` `$strLen*` `$substr*` `$split` `$trim*` `$indexOf*` `$replaceOne` `$replaceAll` `$regexMatch` | — | ✅ `aggregate_expr_string_test.go` | ✅ᴰ |
| Array: `$size` `$arrayElemAt` `$concatArrays` `$isArray` `$in` `$reverseArray` `$slice` `$range` `$indexOfArray` `$arrayToObject` `$filter` `$reduce` `$sortArray` `$set*` `$zip` | — | ✅ `aggregate_expr_array_test.go` | ✅ᴰ |
| Type-conversion: `$toString` `$toInt` `$toLong` `$toDouble` `$toBool` `$toObjectId` `$toDate` `$convert` `$isNumber` `$literal` `$let` `$getField` `$setField` `$unsetField` `$binarySize` `$rand` | — | ✅ `aggregate_expr_convert_test.go` | ✅ᴰ |
| Date: `$year`…`$millisecond` `$isoWeek*` `$dateToString` `$dateFromString` `$dateTo/FromParts` `$dateAdd` `$dateSubtract` `$dateDiff` `$dateTrunc` | — | ✅ `aggregate_expr_date_test.go` | ✅ᴰ |
| Trig / hyperbolic / angle / `$log10`: `$sin` `$cos` `$tan` `$asin` `$acos` `$atan` `$atan2` `$sinh` `$cosh` `$tanh` `$asinh` `$acosh` `$atanh` `$degreesToRadians` `$radiansToDegrees` `$log10` | — | ✅ (return `double`) · `aggregate_expr_trig_test.go` | ✅ᴰ |
| `$bsonSize` | — | ✅ (BSON byte size as `int32`; `null`→`null`; type error otherwise) · `aggregate_expr_bsonsize_test.go` | ✅ᴰ |
| `$function` (server-side JavaScript, `{body,args,lang:"js"}`) | — | ✅ via embedded goja engine (`operators/function.go`) · `aggregate_expr_function_test.go` | ❌ no JS engine in DocumentDB; zero refs in v2 |
| `$toDecimal` | — | ❌ v1's `internal/types` has **no `Decimal128` type** | ✅ᴰ |
| `$meta` | — | ❌ needs per-query metadata plumbing v1 does not produce | ✅ᴰ |
| **— Window operators (inside `$setWindowFields`) —** | | | |
| `$rank` `$denseRank` `$documentNumber` `$shift` (require `sortBy`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| Window accumulators `$sum` `$avg` `$min` `$max` `$count` `$push` `$first` `$last` `$stdDevPop` `$stdDevSamp` (full-partition or `window:{documents:[l,u]}`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| `$derivative` `$integral` `$expMovingAvg` `$covariancePop` `$covarianceSamp` `$linearFill` `$locf` `$minN`/`$maxN`, `range` windows | — | ❌ deferred (return not-implemented) | ✅ᴰ |
| **— Indexes —** | | | |
| Single-field, compound, unique (incl. compound-unique) | ✅ [`server/lib/mongoStartup.js`](https://github.com/wekan/wekan/blob/main/server/lib/mongoStartup.js) | ✅ `msg_createindexes.go` | ✅ (`createIndexes` supported) |
| `sparse` | — | ⚠️ accepted, silently ignored (*"to make Meteor apps work"*) | ✅ᴰ |
| `expireAfterSeconds` (**TTL**) | — | ✅ createIndexes + listIndexes + background reaper (`handler.go runTTLCleanup`) | ✅ (`ttl-indexes` guide) |
| **text** (`"text"` key, `weights` / `default_language` / `language_override` / `textIndexVersion`) | — | ⚠️ **SQLite only**: accepted, stored & round-tripped via listIndexes; **no real inverted index** (see `$text`). On PostgreSQL/MySQL/HANA the option is accepted by the handler but not persisted/reported · `query_text_test.go` | ✅ᴰ (full-text-search guides) |
| `hidden` | — | ⚠️ **SQLite only**: accepted/stored/reported (not hidden from the planner); other backends accept but don't round-trip · `createindexes_options_test.go` | ✅ᴰ |
| `collation` | — | ⚠️ **SQLite only**: accepted/stored/reported (no locale-aware collation); other backends accept but don't round-trip · `createindexes_options_test.go` | ✅ᴰ |
| `partialFilterExpression` | — | ⚠️ **SQLite only**: accepted/stored/reported (index not restricted to matching docs); other backends accept but don't round-trip · `createindexes_options_test.go` | ✅ᴰ |
| `2dsphere` (`"2dsphere"` key + `2dsphereIndexVersion`) | — | ⚠️ **SQLite only**: accepted/stored/reported (no geospatial queries); other backends accept but don't round-trip · `createindexes_options_test.go` | ✅ᴰ |
| `storageEngine` `bits` `min` `max` `bucketSize` `wildcardProjection` | — | ❌ `ErrNotImplemented` | ✅ᴰ |
| **— Reactivity (Meteor pub/sub) —** | | | |
| `polling` (poll-and-diff) — primary supported path | ✅ [`start-wekan.sh`](https://github.com/wekan/wekan/blob/main/start-wekan.sh) | ✅ (~2000 ms latency; no oplog dependency) | ✅ |
| Capped collections + tailable / awaitData cursors | ⚙️ | ✅ SQLite backend (`msg_find.go`, `msg_getmore.go`) | ⚠️ᴰ (DocumentDB; not sourced here) |
| MongoDB oplog tailing (`local.oplog.rs` i/u/d) | ⚙️ (Admin Panel introspection only) | ⚠️ tailing-only via `backends/decorators/oplog/`; capped oplog created manually + `FERRETDB_REPL_SET_NAME` | ❌ no Mongo oplog (uses Postgres WAL) |
| Change streams (`$changeStream` / `watch`) | — | ❌ ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)) → use `METEOR_REACTIVITY_ORDER=polling` | ❌ no `watch`/change-stream handler in v2 |
| Real replication / elected primary | — | ❌ (`replSetInitiate` is a no-op) | ⚠️ PostgreSQL WAL streaming replication — **not** a MongoDB replica set / oplog |
| **— Sessions / transactions —** | | | |
| `startSession` | — | ✅ tracked in a session registry adapted from v2: returns a session record with a generated UUID and registers it server-side (`msg_startsession.go`, `internal/handler/session`) · `sessions_transactions_test.go`, `session_registry_test.go` | ✅ logical session record only (`msg_startsession.go`) |
| `commitTransaction` / `abortTransaction` | — | ⚠️ compat no-op `{ok:1}`; **no** atomicity/isolation · `sessions_transactions_test.go` | ❌ "Not implemented yet" (compatibility.md; #1548/#1547) |
| `endSessions` `refreshSessions` `killSessions` `killAllSessions` `killAllSessionsByPattern` | — | ✅ tracked in a session registry adapted from v2: actually end / refresh / remove sessions from the registry, still returning `{ok:1}` (`msg_endsessions.go`, `internal/handler/session`) · `session_registry_test.go` | ✅ᴰ (session-mgmt commands) |
| Retryable-write / session fields (`lsid` `txnNumber` `autocommit` `startTransaction` `stmtId` `stmtIds`) | — | ⚠️ accepted & ignored on insert/update/delete/findAndModify · `sessions_transactions_test.go` | ⚠️ not evidenced in v2 |
| Real multi-document transactions (atomicity/isolation) | — | ❌ every write auto-commits (SQLite) | ❌ commit/abort unimplemented (#1548/#1547) |
| **— Admin / diagnostic —** | | | |
| `serverStatus` `buildInfo` `hello`/`ismaster` `ping` `collStats` `dbStats` | ✅ | ✅ | ✅ |
| `listCollections` `listDatabases` `listIndexes` `createIndexes` `dropIndexes` `create` `drop` `compact` | ✅ | ✅ | ✅ |
| `getParameter` | ✅ [`meteorMongoIntegration.js`](https://github.com/wekan/wekan/blob/main/models/lib/meteorMongoIntegration.js) | ✅ `msg_getparameter.go` (verified, `TestCommandsAdministrationGetParameter`) | ✅ |
| `replSetInitiate` | — | ⚠️ compat no-op `{ok:1}` (echoes config `_id`); **no** real replica set (`msg_replsetinitiate.go`) · `replsetinitiate_test.go` | ❌ not registered; TODO in `msg_hello.go` (#3936) |
| `replSetGetStatus` | ⚙️ (GridFS admin tooling; skippable with `fs`) | ❌ not registered in `commands.go` | ❌ not registered |
| **— Not required by WeKan —** | | | |
| GridFS storage / commands | — (attachments on filesystem) | ⚠️ driver convention over `fs.files`/`fs.chunks` CRUD | ✅ (same driver convention) |
| `mapReduce` | — | ❌ not registered | ❌ not registered (no handler) |

**Backends note — what actually differs per v1 backend (SQLite / PostgreSQL / MySQL /
SAP HANA).** v1 splits into two layers, and only one of them is backend-specific:

- **Handler layer** (`internal/handler/…`, `internal/handler/common/…`) — *all* of the
  query filter operators, aggregation stages, aggregation expression operators, window
  operators, `$where`/`$function`/`$text`, the session/transaction/`replSetInitiate`
  compatibility commands, index-option **parsing/validation**, and the TTL reaper
  (`handler.go runTTLCleanup`, which deletes via the backend's `DeleteAll`). This layer
  runs in Go on documents *after* the backend returns them, so it is **backend-
  independent**: every non-storage matrix row above behaves identically on SQLite,
  PostgreSQL, MySQL and HANA. Only SQLite is *tested* here, but the code path is shared.
- **Storage layer** (`internal/backends/{sqlite,postgresql,mysql,hana}`) — raw CRUD,
  cursors, capped collections, and index **persistence**. This is where the backends
  differ:
  - **SQLite** — complete; the reference/tested target; the **only** backend wired to
    persist & round-trip this fork's new index options (text `weights`/`default_language`,
    `hidden`, `collation`, `partialFilterExpression`, `2dsphere`).
  - **PostgreSQL (vanilla)** — complete and mature, but untested here and **not** wired
    for the new index-option round-tripping.
  - **MySQL** — beta/partial: full interface, usable CRUD, but not production-hardened
    and not wired for the new index options.
  - **SAP HANA** — experimental: string-built SQL keyed on `_id`, `Compact` no-op,
    column-mode collections unimplemented, no `metadata`/`insert.go`; not wired for the
    new index options.

  So the index-option rows marked "**SQLite only**" above are the main per-backend
  divergence introduced by this fork; the `createIndexes` call still *succeeds* on the
  other backends (a plain index is created), it just doesn't store/report the extra
  option. Everything else in the matrix is either backend-independent (handler layer)
  or basic CRUD that all four backends provide (SQLite/PostgreSQL fully, MySQL beta,
  HANA experimental).

**v2 delegation note (why so many `✅ᴰ`).** v2 is a thin proxy: `msg_aggregate.go`
forwards the pipeline to DocumentDB without parsing stages, and query operators are
likewise executed by the DocumentDB PostgreSQL extension. So for aggregation stages,
expression operators, most query operators and the richer index options, **the real
support lives in DocumentDB and cannot be sourced from FerretDB/main** — hence `✅ᴰ`
("delegated; generally works via DocumentDB, not verified here") rather than a plain
`✅`. What *is* code-confirmed as **missing in v2** is listed as `❌`: server-side
JavaScript (`$where`, `$function`), change streams, real multi-document transactions
(`commitTransaction`/`abortTransaction`), `replSetInitiate`, `replSetGetStatus` and
`mapReduce`.

---

## Validation notes

Both feature columns were checked against the branches' real code (2026-07):

- **v1 (`main-v1`)** — verified from the registration maps and handlers: the
  aggregation `Stages` map plus `init()`-injected `$facet`/`$bucket`/`$bucketAuto`/
  `$unionWith`; the `Operators` / `unsupportedOperators` maps; `filter.go`'s operator
  cases (incl. `$where`, `$text`); the update-operator set; `msg_createindexes.go`
  option handling; and `commands.go`. Correction applied vs. an earlier draft:
  `replSetGetStatus` is **not** implemented in v1 (`❌`, not `⚠️`); `$where` is a
  genuine goja implementation (`✅`, matching `$function`).
- **v1 per-backend** — checked `internal/backends/{sqlite,postgresql,mysql,hana}`. All
  four implement the same `backends.Collection` interface, so the handler layer (every
  non-storage matrix row) works on any of them. The fork's new index options
  (`TextOptions`/`Hidden`/`Collation`/`PartialFilterExpression`/`Sphere2D` on
  `backends.IndexInfo`) are referenced **only** in `sqlite/` (2 files); PostgreSQL,
  MySQL and HANA have **0** references, so those options are SQLite-only for
  storage/round-trip. SQLite and PostgreSQL are complete backends; MySQL is beta; HANA
  is experimental (column-mode collections unsupported, `Compact` no-op, no
  `metadata`/`insert.go`).
- **v2 (`main`)** — verified that there is **no `internal/backends`** (only
  `internal/documentdb/`), that `msg_aggregate.go` forwards pipelines untouched, and
  from v2's own `website/docs/migration/compatibility.md`. Corrections applied vs. an
  earlier draft that over-credited v2: `$where`, `$function`, `$changeStream`, change
  streams, `replSetInitiate`, `replSetGetStatus` and `mapReduce` are **not implemented
  in v2** (`❌`); `commitTransaction`/`abortTransaction` are explicitly "Not
  implemented yet" so **real multi-document transactions are `❌`** (only `startSession`
  session-bookkeeping works); and v2's replication is **PostgreSQL WAL streaming, not a
  MongoDB replica set/oplog** (`⚠️`). Everything DocumentDB executes on v2's behalf is
  marked `✅ᴰ` because it is not verifiable from FerretDB/main's source.

---

## Bottom line

WeKan's **core functionality runs on FerretDB v1.24.2 SQLite** with
`METEOR_REACTIVITY_ORDER=polling` and filesystem attachments — no blocking gaps for
boards/cards/lists/CRUD/search. The admin-only aggregation gaps (Prometheus
`/metrics` "top boards" via `$lookup`; attachment-stats via `$map`/`$objectToArray`/
`$ifNull`/`$anyElementTrue`/`$eq`/`$ne`/`$or`) are closed and tested.

The one real gap for WeKan is **low-latency reactivity**: change streams are
unsupported in v1 ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)); use
`METEOR_REACTIVITY_ORDER=polling`, or set up basic oplog tailing manually. A
configuration choice, not a blocker.

Beyond WeKan's needs, this branch also adds broad MongoDB compatibility (trigonometry,
`$bsonSize`, `$where`/`$function` server-side JS, `$setWindowFields`, text/`hidden`/
`collation`/`partialFilterExpression`/`2dsphere` index options, session/transaction
compat commands, `replSetInitiate`) — see the matrix for exact full/partial status.
Notably, several of these (server-side JS, session/transaction *commands*,
`replSetInitiate`) exist in **v1 but not in v2** — as compatibility shims — while v2
leads on everything DocumentDB implements natively.

A ready-to-run stack is provided in `docker-compose.yml` (+ `Dockerfile`):
`docker compose up --build` builds FerretDB v1 (SQLite) and runs WeKan against it.
For local development, `./build.sh` offers a menu (and non-interactive commands) to
install dependencies, build/run FerretDB on SQLite, and run the integration tests
sequentially or in parallel.

---

## FerretDB v1 vs v2 — why the matrix differs

The two FerretDB lines have fundamentally different architectures, which is why they
support different databases:

- **v1** (`main-v1`, module `github.com/FerretDB/FerretDB`) implements the
  MongoDB-compatibility layer **itself, in Go** (`internal/handler/…` parses and
  executes commands; `internal/handler/common/aggregations/…` implements operators and
  stages), on top of a **pluggable storage layer** (`internal/backends/{sqlite,
  postgresql, mysql, hana}`). Compatibility is *partial* but portable — any backend a
  Go driver can talk to. This branch has greatly expanded that Go layer.
- **v2** (`main`, module `github.com/FerretDB/FerretDB/v2`) is a **thin proxy** that
  translates the MongoDB wire protocol to SQL and delegates all compatibility to
  **PostgreSQL + the [DocumentDB extension](https://github.com/documentdb/documentdb)**
  (`internal/documentdb/…`). There is **no `internal/backends`** — PostgreSQL (with the
  extension) is the only engine. Aggregation/operator coverage is *far broader* because
  DocumentDB does the heavy lifting, but a number of MongoDB features are still not
  implemented in the v2 proxy itself (server-side JavaScript, multi-document
  transactions, change streams, replica-set admin commands, `mapReduce`).

**Takeaway:** v1 is the only line that runs on **SQLite / MySQL / SAP HANA / vanilla
PostgreSQL** and is embeddable; v2 has **broader aggregation/query compatibility** via
DocumentDB (text/geo, the full operator set), but **multi-document transactions and
change streams are not yet implemented** in either line, and only v1 carries the
server-side-JS and session/replica-set *compatibility shims*.

---

## Merging FerretDB v1 and v2

Goal: one FerretDB that keeps v2's completeness **and** v1's reach (SQLite,
vanilla PostgreSQL, MySQL, SAP HANA, embeddable). The obstacle is that v2's
compatibility lives inside a **PostgreSQL C extension (DocumentDB)** that cannot
run inside SQLite or the other backends, while v1's compatibility lives in **Go**
and is portable but incomplete. So a merge cannot simply "use DocumentDB
everywhere"; it must make the compatibility engine **pluggable**.

### Proposal: a pluggable compatibility "engine" behind a shared frontend

Adopt v2 as the base module and reintroduce v1's portability as a selectable
engine, sharing everything above the engine boundary.

1. **Shared wire frontend + conformance suite.** Factor the parts that are
   engine-independent — `clientconn`, wire parsing, command dispatch, error
   mapping (v1 `internal/handler/*`, v2 `internal/mongoerrors`) — into a common
   frontend used by every engine. Make the existing `integration/` compat test
   suite the **single conformance suite** every engine must run in CI; each
   engine's failing tests become its published gap list.

2. **`Engine` strategy interface.** Define one interface (roughly `RunCommand` /
   CRUD / aggregate / index ops) with two implementations:
   - **`documentdb`** — v2's current path (PostgreSQL + DocumentDB): broad features.
   - **`go`** — v1's Go compatibility layer (`internal/handler/common/…`, now with
     this branch's expanded operators, stages, window functions, text/index options
     and session/transaction compat) over `internal/backends/{sqlite, postgresql,
     mysql, hana}`: portable, partial.

3. **Engine/back-end selection from the connection target.** Choose the engine
   from a scheme or env var, e.g. `FERRETDB_ENGINE` or the URL:
   `sqlite:file:/state/` and `mysql://…` → `go` engine; a plain
   `postgres://…` → `go` engine (vanilla Postgres, no extension); a
   `postgres://…?documentdb=on` → `documentdb` engine (full features). This lets a
   single binary serve an embedded SQLite dev instance *and* a
   DocumentDB-backed production instance.

4. **Capability negotiation.** Because engines differ, report per-engine
   capabilities through `buildInfo`/`hello`/`getParameter` (e.g. `transactions`,
   `changeStreams`, `textSearch`) so clients adapt — exactly how WeKan already
   chooses `METEOR_REACTIVITY_ORDER=polling` when change streams are absent. The
   `go` engine advertises the reduced set; `documentdb` advertises the broader set.

5. **Incremental convergence.** Close `go`-engine gaps (the `⚠️`/`❌` v1 cells in the
   matrix) against the shared conformance suite so SQLite/MySQL/Hana parity approaches
   DocumentDB. Features impractical to reimplement portably in Go — real multi-document
   **transactions**, **change streams**, **text/geo search** — are surfaced as
   available/unavailable per engine via capability flags rather than silently failing
   (note: multi-document transactions and change streams are currently missing in
   *both* lines). v1's partial **MySQL** and **SAP HANA** backends are carried forward
   under the `go` engine and completed as needed.

### Alternatives considered

- **Port DocumentDB to SQLite ("DocumentDB-lite").** Reimplement DocumentDB's BSON
  operators as SQLite loadable extension functions (or in Go over SQLite). This
  would give SQLite near-v2 fidelity from one implementation, but it is essentially
  rebuilding DocumentDB — very large, and duplicated maintenance. Not recommended
  short-term; revisit only if the `go` engine's gap list proves too costly to
  maintain.
- **Keep two separate products.** Simplest, but perpetuates divergence and means
  SQLite users never benefit from v2's work. The shared frontend + conformance
  suite (step 1) is worth doing even if full engine-pluggability is deferred.

**Recommended path:** steps 1–4 first (shared frontend, `Engine` interface with
`documentdb` + `go`, target-based selection, capability negotiation), then step 5
(convergence) as an ongoing effort. This yields a single FerretDB that supports
SQLite, vanilla PostgreSQL, MySQL and SAP HANA via the `go` engine, and broad
MongoDB compatibility via the `documentdb` engine — the WeKan-on-SQLite stack in
this branch becomes the `go`-engine reference deployment.
